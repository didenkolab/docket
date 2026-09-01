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

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
)

// reactingServer is a vault that asks for a program to be run when a card
// reaches Done, and a server that may or may not agree to run it.
func reactingServer(t *testing.T, allowed bool) (http.Handler, string) {
	t.Helper()
	_, _, root := newServer(t)

	script := filepath.Join(root, "hooks", "on-done.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(`#!/bin/sh
cat > "$DOCKET_ROOT/docs/last-done.json"
`), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c.Reactions = []project.Reaction{
		{On: "task.moved", Run: "hooks/on-done.sh", Name: "note-the-finish", Status: "Done"},
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "a reaction")

	s, err := New(root, Options{
		Author:   gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Programs: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return noon.Add(time.Hour) }
	return s.Handler(), root
}

// The point: a card reaches a column, a program runs, and what it wrote is a
// commit of its own — named after the reaction, so history says why the file
// changed.
func TestAReactionRunsOnAMoveAndItsWriteIsCommitted(t *testing.T) {
	h, root := reactingServer(t, true)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{"status": {"Done"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("moving: %d — %s", w.Code, w.Body.String())
	}

	written, err := os.ReadFile(filepath.Join(root, "docs", "last-done.json"))
	if err != nil {
		t.Fatalf("the reaction did not run: %v", err)
	}
	for _, want := range []string{`"event":"task.moved"`, `"key":"ACME-1"`, `"to":"Done"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("it was not told %s:\n%s", want, written)
		}
	}
	if got := lastCommit(t, root); !strings.Contains(got, "reaction note-the-finish") {
		t.Errorf("what it wrote was committed as %q", got)
	}
	if dirty := gitOutput(t, root, "status", "--porcelain"); strings.TrimSpace(dirty) != "" {
		t.Errorf("it left the tree dirty:\n%s", dirty)
	}
}

// A repository can declare a program. Only whoever starts the server can agree
// to run it — the same reason git does not put hooks in the repository.
func TestAReactionDoesNotRunUnlessThisServerAgreed(t *testing.T) {
	h, root := reactingServer(t, false)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{"status": {"Done"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("moving: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "last-done.json")); err == nil {
		t.Fatal("a program in the repository ran on a server that never agreed to run one")
	}
	// And the move itself still happened: refusing to run somebody's automation
	// is not refusing their work.
	if body := get(t, h, "/task/ACME-1").Body.String(); !strings.Contains(body, "Done") {
		t.Error("the move did not happen")
	}
}

// A reaction narrowed to one column does not fire for another.
func TestAReactionIsNotRunForAnotherColumn(t *testing.T) {
	h, root := reactingServer(t, true)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{"status": {"In review"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("moving: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "last-done.json")); err == nil {
		t.Error("a reaction asked for Done ran for In review")
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
