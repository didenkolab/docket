package vault

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/vault/vaulttest"

	"github.com/didenkolab/docket/internal/project"
)

func withTasks(t *testing.T) (root string, c *project.Config) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "v")
	if _, err := Init(root, Options{Key: "ACME", Name: "Acme", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, c
}

// A title lives in the file name, so retitling renames the file — and every
// link that pointed at the old name has to come with it, or the vault is full
// of dead links after every rewording.
func TestRetitleRepointsEveryLink(t *testing.T) {
	root, c := withTasks(t)

	if _, _, err := Create(root, c, NewOptions{Title: "Session model"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(root, c, NewOptions{
		Title: "Fix login", Description: "Depends on [[ACME-1 Session model]].",
	}); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(root, "docs", "notes.md")
	if err := os.WriteFile(page,
		[]byte("---\ntitle: Notes\n---\n\nSee [[ACME-1 Session model|the model]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	touched, err := Retitle(root,
		"ACME/ACME-1 Session model.md", "ACME/ACME-1 How a session is modelled.md")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"ACME/ACME-1 How a session is modelled.md",
		"ACME/ACME-1 Session model.md",
		"ACME/ACME-2 Fix login.md",
		"docs/notes.md",
	} {
		if !slices.Contains(touched, want) {
			t.Errorf("%s is not in the commit: %v", want, touched)
		}
	}

	body := read(t, root, "ACME/ACME-2 Fix login.md")
	if !strings.Contains(body, "[[ACME-1 How a session is modelled]]") {
		t.Errorf("a task's link was not repointed:\n%s", body)
	}
	note := read(t, root, "docs/notes.md")
	if !strings.Contains(note, "[[ACME-1 How a session is modelled|the model]]") {
		t.Errorf("a page's link lost its display text or its target:\n%s", note)
	}
}

// Renaming onto a name something else already has would lose that file.
func TestRetitleRefusesToLandOnAnExistingFile(t *testing.T) {
	root, c := withTasks(t)
	if _, _, err := Create(root, c, NewOptions{Title: "One"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Create(root, c, NewOptions{Title: "Two"}); err != nil {
		t.Fatal(err)
	}

	if _, err := Retitle(root, "ACME/ACME-1 One.md", "ACME/ACME-2 Two.md"); err == nil {
		t.Fatal("a rename onto an existing file was allowed")
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-2 Two.md")); err != nil {
		t.Error("the other file is gone")
	}
}

func TestRetitleToTheSameNameDoesNothing(t *testing.T) {
	root, _ := withTasks(t)
	touched, err := Retitle(root, "ACME/x.md", "ACME/x.md")
	if err != nil || touched != nil {
		t.Errorf("touched = %v, err = %v", touched, err)
	}
}
