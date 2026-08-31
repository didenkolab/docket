package server

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Three cards dragged quickly, and all three commits reach the remote.
//
// A write that arrived while a push was in the air used to be dropped. Because
// `git push` sends every commit on the branch, that was usually covered by the
// next write — and "usually" is the failure: the last change of a quick run sat
// in the folder, with the board reporting nothing wrong because as far as it
// knew its push had worked.
func TestQuickChangesAllReachTheRemote(t *testing.T) {
	s, handler, root := newServer(t)

	// A remote to push at: a bare repository next door, which is what a real
	// one is from git's side.
	remote := filepath.Join(t.TempDir(), "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(t.TempDir(), "init", "-q", "--bare", remote)
	run(root, "remote", "add", "origin", remote)
	run(root, "push", "-q", "-u", "origin", "HEAD")

	// Enough tasks to move, made through the server so each is its own commit.
	for _, title := range []string{"One", "Two", "Three"} {
		w := as(t, handler, nil, http.MethodPost, "/new",
			url.Values{"title": {title}, "type": {"task"}, "priority": {"normal"}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("could not make %s: %d %s", title, w.Code, w.Body)
		}
	}

	// Move them one after another with no pause, which is what dragging is.
	for _, key := range []string{"ACME-2", "ACME-3", "ACME-4"} {
		w := as(t, handler, nil, http.MethodPost, "/task/"+key+"/status",
			url.Values{"status": {"In review"}})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
			t.Fatalf("%s was not moved: %d %s", key, w.Code, w.Body)
		}
	}

	// The pushes are in the background, so wait for them to settle rather than
	// for a fixed time.
	deadline := time.Now().Add(20 * time.Second)
	var unpushed int
	for time.Now().Before(deadline) {
		v := s.sp().Vaults()[0]
		n, err := v.Repo.Unpushed()
		if err == nil {
			unpushed = n
			if n == 0 {
				break
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	if unpushed != 0 {
		t.Errorf("%d commits never left the folder — a write that arrived during a push "+
			"was dropped rather than sent after it", unpushed)
	}

	// And the remote really has them, rather than the count merely agreeing.
	cmd := exec.Command("git", "log", "--format=%s", "-6")
	cmd.Dir = remote
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reading the remote: %v: %s", err, out)
	}
	for _, key := range []string{"ACME-2", "ACME-3", "ACME-4"} {
		if !strings.Contains(string(out), key) {
			t.Errorf("%s never reached the remote:\n%s", key, out)
		}
	}
}

// A commit the board did not make is sent too.
//
// This is what the count knows and a flag could not. The first version of the
// retry remembered that the board had written something and went round again
// for it — so a commit made in the folder by an agent, or by `docket new` on the
// command line, was invisible to it and sat there. Asking git what is unpushed
// covers every way a commit can appear, which is the point of keeping no record
// beside the repository.
func TestACommitTheBoardDidNotMakeIsSentToo(t *testing.T) {
	s, handler, root := newServer(t)

	remote := filepath.Join(t.TempDir(), "remote.git")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run(t.TempDir(), "init", "-q", "--bare", remote)
	run(root, "remote", "add", "origin", remote)
	run(root, "push", "-q", "-u", "origin", "HEAD")

	// Somebody else writes in the folder and commits, the way an agent does.
	if err := os.WriteFile(filepath.Join(root, "docs", "by-hand.md"),
		[]byte("---\ntitle: by-hand\ntype: page\n---\n\nWritten in the folder.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(root, "add", "-A")
	run(root, "-c", "user.email=agent@example.com", "-c", "user.name=Agent",
		"commit", "-q", "-m", "a commit the board never saw")

	// Then the board makes one of its own, which is what starts a push.
	w := as(t, handler, nil, http.MethodPost, "/task/ACME-1/status",
		url.Values{"status": {"In review"}})
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("the move was refused: %d %s", w.Code, w.Body)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if n, err := s.sp().Vaults()[0].Repo.Unpushed(); err == nil && n == 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}

	cmd := exec.Command("git", "log", "--format=%s", "-4")
	cmd.Dir = remote
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reading the remote: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "a commit the board never saw") {
		t.Errorf("a commit made in the folder never reached the remote:\n%s", out)
	}
}
