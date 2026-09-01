package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// A verb the vault declared is a relation everywhere: written as a string it is
// reported as one, and written as a link it passes. Before the list came from
// the configuration this could not be said at all — a vault could not add a
// sixth verb, which is what every test-management app is built on.
func TestADeclaredRelationIsCheckedLikeABuiltInOne(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}

	config := filepath.Join(root, "docket.yaml")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, append(raw,
		[]byte("relations:\n  - name: tests\n    inverse: tested_by\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatalf("the declared relation was refused: %v", err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{Title: "The thing"}); err != nil {
		t.Fatal(err)
	}
	rel, _, err := vault.Create(root, c, vault.NewOptions{Title: "Its test"})
	if err != nil {
		t.Fatal(err)
	}

	// As a string: nothing in Obsidian connects, and the rule says so.
	at := filepath.Join(root, filepath.FromSlash(rel))
	body, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, append(body[:0:0], strings.Replace(string(body),
		"tags: []", "tags: []\ntests: [ACME-1]", 1)...), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	var said string
	for _, f := range findings {
		if f.Rule == RuleRelations && strings.Contains(f.Message, "tests") {
			said = f.Message
		}
	}
	if said == "" {
		t.Fatalf("a declared relation written as a string was not reported:\n%v", findings)
	}
	if !strings.Contains(said, "not a link") {
		t.Errorf("reported as %q", said)
	}
}
