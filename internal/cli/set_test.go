package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/space"
	"github.com/didenkolab/docket/internal/task"
	"github.com/didenkolab/docket/internal/vault"
	"github.com/didenkolab/docket/internal/vault/vaulttest"
)

// A vault with a declared field, a relation, and two tasks.
func settable(t *testing.T) (*space.Space, *project.Config, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "docket.yaml")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	more := "fields:\n  - name: result\n    kind: choice\n    choices: [passed, failed]\n" +
		"relations:\n  - name: runs\n    inverse: run_by\n"
	if err := os.WriteFile(config, append(raw, []byte(more)...), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"The test", "The run"} {
		if _, _, err := vault.Create(root, c, vault.NewOptions{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	sp, err := space.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return sp, c, root
}

func read(t *testing.T, root, key string) *task.Task {
	t.Helper()
	sp, err := space.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	owner, rel, _, err := sp.Locate(key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(owner.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := task.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

// The way a script writes to the vault, and the reason it is a command rather
// than sed: everything it sets is held to what the vault declared.
func TestSetWritesWhatTheVaultDeclared(t *testing.T) {
	sp, c, root := settable(t)
	parsed := read(t, root, "ACME-2")

	for _, pair := range [][2]string{
		{"result", "failed"},
		{"runs", "ACME-1"},
		{"status", "In review"},
		{"assignee", "marina"},
		{"estimate", "3"},
	} {
		if _, err := setOne(sp, c, parsed, pair[0], pair[1]); err != nil {
			t.Fatalf("%s=%s: %v", pair[0], pair[1], err)
		}
	}

	body, err := parsed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	written := string(body)
	for _, want := range []string{
		"result: failed",
		`runs: ["[[ACME-1 The test]]"]`, // a key, written as the link Obsidian resolves
		"status: In review",
		"estimate: 3",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("does not say %q:\n%s", want, written)
		}
	}
}

// A script must be refused what a person would be refused. A validator that
// only runs at check time finds out after the fact.
func TestSetRefusesWhatCheckWouldReport(t *testing.T) {
	sp, c, root := settable(t)
	parsed := read(t, root, "ACME-2")

	for _, c2 := range []struct {
		name, value, mentions string
	}{
		{"result", "maybe", "not one of"},
		{"status", "Nowhere", "not a status"},
		{"estimate", "big", "not a number"},
		{"runs", "ACME-99", "no such task"},
		{"invented", "x", "not a property this vault declared"},
	} {
		_, err := setOne(sp, c, parsed, c2.name, c2.value)
		if err == nil {
			t.Errorf("%s=%s was accepted", c2.name, c2.value)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), c2.mentions) {
			t.Errorf("%s=%s: refused with %q, which does not say %q",
				c2.name, c2.value, err, c2.mentions)
		}
	}
}
