package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/workspace"
)

// Taking a project out is a manifest edit. The repository is not touched, and
// the board stops showing it without anybody restarting anything.
func TestAProjectCanBeTakenOutOfTheWorkspace(t *testing.T) {
	s, h, root, second := workspaceToGrow(t)

	// Two projects, so there is one to remove. Added directly: a file:// remote
	// is refused through the handler on purpose, and this test is about taking
	// one out rather than about putting one in.
	if err := s.connectDirect(second); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if len(s.sp().Vaults()) != 2 {
		t.Fatalf("the workspace has %d projects", len(s.sp().Vaults()))
	}

	w := as(t, h, nil, "POST", "/projects/remove", url.Values{"key": {"TWO"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("removing: %d — %s", w.Code, w.Body.String())
	}

	if len(s.sp().Vaults()) != 1 {
		t.Errorf("the board still serves %d projects", len(s.sp().Vaults()))
	}
	m, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range m.Projects {
		if p.Key == "TWO" {
			t.Error("the manifest still names it")
		}
	}
	// The clone is left alone: "I do not want this here" is not "destroy it".
	if _, err := os.Stat(filepath.Join(root, "two")); err != nil {
		t.Errorf("the folder was deleted when nobody asked: %v", err)
	}
	// And the page says what happened, which it never used to.
	page := as(t, h, nil, "GET", w.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "out of the workspace") {
		t.Error("the page does not say what it just did")
	}
}

// Deleting the clone is refused while it holds anything that exists nowhere
// else. A folder with commits nobody has sent is the definition of that.
func TestDeletingAFolderIsRefusedWhileItHoldsTheOnlyCopy(t *testing.T) {
	s, h, root, second := workspaceToGrow(t)
	if err := s.connectDirect(second); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// A commit that has gone nowhere: the clone is from a local path with no
	// upstream to send to.
	at := filepath.Join(root, "two")
	if err := os.WriteFile(filepath.Join(at, "docs", "note.md"), []byte("---\ntitle: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell(t, at, "git", "add", "-A")
	shell(t, at, "git", "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "unsent")

	w := as(t, h, nil, "POST", "/projects/remove",
		url.Values{"key": {"TWO"}, "delete_folder": {"1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d", w.Code)
	}
	if _, err := os.Stat(at); err != nil {
		t.Fatalf("the folder was deleted despite holding the only copy: %v", err)
	}
	// And it is still in the workspace: a project is never half removed.
	if len(s.sp().Vaults()) != 2 {
		t.Errorf("the manifest was edited anyway: %d projects", len(s.sp().Vaults()))
	}
	page := as(t, h, nil, "GET", w.Header().Get("Location"), nil).Body.String()
	if !strings.Contains(page, "never been sent") {
		t.Errorf("the page does not say why it refused:\n%s", page)
	}
}
