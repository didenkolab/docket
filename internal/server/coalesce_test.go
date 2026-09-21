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
	// -b main, and every read below names the branch: a bare repository's HEAD
	// follows the machine's init.defaultBranch, which is main here and master
	// on the build. The push goes to main either way, so `git log` following
	// HEAD found an empty master and the test passed at home and failed there.
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", remote)
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
	cmd := exec.Command("git", "log", "main", "--format=%s", "-6")
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

// A write that lands while a push is in the air is sent by the push after it.
//
// The version before this one asked whether the unpushed count was falling and
// stopped when it was not. That reads a write arriving mid-push as a push making
// no progress: one commit goes, one arrives, the count is unchanged, and the
// loop gives up with the new commit still in the folder and the board reporting
// nothing wrong (DKT-62).
//
// The race is made deterministic with a pre-push hook that sleeps, so the window
// is half a second wide rather than however long the machine happens to take.
// Hoping the scheduler cooperates is what made the first version of this fail
// about one run in ten, and only on the build.
func TestAWriteDuringASlowPushIsNotStranded(t *testing.T) {
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
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", remote)
	run(root, "remote", "add", "origin", remote)
	run(root, "push", "-q", "-u", "origin", "HEAD")

	// The tasks first, and settled, so that the timed part below is only the
	// three moves and not whatever their creation was still pushing.
	for _, title := range []string{"One", "Two", "Three"} {
		w := as(t, handler, nil, http.MethodPost, "/new",
			url.Values{"title": {title}, "type": {"task"}, "priority": {"normal"}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("could not make %s: %d %s", title, w.Code, w.Body)
		}
	}
	settle := func(within time.Duration) int {
		t.Helper()
		deadline, n := time.Now().Add(within), -1
		for time.Now().Before(deadline) {
			if got, err := s.sp().Vaults()[0].Repo.Unpushed(); err == nil {
				if n = got; n == 0 {
					return 0
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		return n
	}
	if n := settle(20 * time.Second); n != 0 {
		t.Fatalf("setup never settled: %d commits still waiting", n)
	}

	// Every push from here takes half a second, on the client side, which is
	// what a real one over a real network does.
	hook := filepath.Join(root, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nsleep 0.5\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// ACME-2 starts a push that runs until t+500ms.
	// ACME-3 lands inside it, and is sent by the round that follows.
	// ACME-4 lands inside *that* round — the case the count could not see,
	// because one commit left and one arrived, so the count did not move.
	move := func(key string) {
		t.Helper()
		w := as(t, handler, nil, http.MethodPost, "/task/"+key+"/status",
			url.Values{"status": {"In review"}})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
			t.Fatalf("%s was not moved: %d %s", key, w.Code, w.Body)
		}
	}
	move("ACME-2")
	time.Sleep(100 * time.Millisecond)
	move("ACME-3")
	time.Sleep(600 * time.Millisecond)
	move("ACME-4")

	if n := settle(30 * time.Second); n != 0 {
		t.Errorf("%d commits never left the folder — a write that arrived during a push "+
			"was dropped rather than sent after it", n)
	}

	cmd := exec.Command("git", "log", "main", "--format=%s", "-8")
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
	// -b main, and every read below names the branch: a bare repository's HEAD
	// follows the machine's init.defaultBranch, which is main here and master
	// on the build. The push goes to main either way, so `git log` following
	// HEAD found an empty master and the test passed at home and failed there.
	run(t.TempDir(), "init", "-q", "--bare", "-b", "main", remote)
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

	cmd := exec.Command("git", "log", "main", "--format=%s", "-4")
	cmd.Dir = remote
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reading the remote: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "a commit the board never saw") {
		t.Errorf("a commit made in the folder never reached the remote:\n%s", out)
	}
}
