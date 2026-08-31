package server

import (
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// A board over a clone that tracks a bare repository on disk. No network, and
// the remote can be looked at directly to see whether anything arrived.
func servedClone(t *testing.T) (http.Handler, string, string) {
	t.Helper()

	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	shell(t, root, "git", "init", "--bare", "-q", "-b", "main", remote)

	dir := filepath.Join(root, "work")
	if _, err := vault.Init(dir, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(dir, c, vault.NewOptions{Project: "ACME", Title: "A task"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"remote", "add", "origin", remote},
		{"add", "-A"},
		{"commit", "-q", "-m", "start"},
		{"push", "-q", "-u", "origin", "main"},
	} {
		shell(t, dir, "git", args...)
	}

	s, err := New(dir, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		SessionLife: time.Hour,
		// Nobody signs in, so the push uses whatever credential git would —
		// which for a path on disk is none at all.
		Unauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), dir, remote
}

func shell(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func remoteLog(t *testing.T, remote string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", remote, "log", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Every write is a commit, and every commit goes on to where everybody else
// reads it. Until this, the commits sat in whichever clone the server happened
// to be running over.
func TestAChangeOnTheBoardReachesTheRemote(t *testing.T) {
	h, _, remote := servedClone(t)

	w := as(t, h, nil, "POST", "/task/ACME-1/comment", url.Values{"text": {"said something"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("commenting: code = %d; body:\n%s", w.Code, w.Body)
	}

	// The push is in the background, so wait for it rather than assuming.
	settled := func() bool { return strings.Contains(remoteLog(t, remote), "ACME-1") }
	for i := 0; i < 100 && !settled(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !settled() {
		t.Errorf("the comment never reached the remote:\n%s", remoteLog(t, remote))
	}
}

// A background push that failed silently is the same bug as never pushing, so
// the interface says how many commits are waiting.
func TestWhatCannotBeSentIsSaidOutLoud(t *testing.T) {
	h, dir, _ := servedClone(t)

	// Point the remote at nothing, so the push cannot work.
	shell(t, dir, "git", "remote", "set-url", "origin", filepath.Join(dir, "nowhere.git"))

	if w := as(t, h, nil, "POST", "/task/ACME-1/comment",
		url.Values{"text": {"said something"}}); w.Code != http.StatusSeeOther {
		t.Fatalf("commenting: code = %d", w.Code)
	}

	// The board says so, on any page, once the attempt has finished.
	var body string
	for i := 0; i < 100; i++ {
		body = as(t, h, nil, "GET", "/", nil).Body.String()
		if strings.Contains(body, "not yet sent") || strings.Contains(body, "not sent") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("the board does not say anything is unsent:\n%s", body)
}

// A repository with nowhere to push is not a repository with a problem.
func TestWithNoRemoteThereIsNothingToSay(t *testing.T) {
	s, h, _, _ := guardedServer(t)
	_ = s

	if notes := s.pushNotes(); len(notes) != 0 {
		t.Errorf("a vault with no remote reports %+v", notes)
	}
	if body := as(t, h, signIn(t, h, "admin"), "GET", "/", nil).Body.String(); strings.Contains(body, "unsent") {
		t.Error("the board shows an unsent badge with nowhere to send to")
	}
}
