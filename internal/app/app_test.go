package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// pack writes an app to a directory and returns the path.
func pack(t *testing.T, manifest string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		at := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// vaultAt scaffolds a vault to install into.
func vaultAt(t *testing.T) (string, *project.Config) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return root, c
}

const testsApp = `name: tests
version: "1"
description: Test management
vocabulary:
  types:
    - name: test
  fields:
    - name: steps
      kind: text
      types: [test]
  relations:
    - name: tests
      inverse: tested_by
`

// The whole point: four types, six fields and a verb arrive as a diff, and the
// vault understands them afterwards exactly as if somebody had typed them.
func TestInstallingAnAppAddsItsVocabularyAndFiles(t *testing.T) {
	root, c := vaultAt(t)
	source := pack(t, testsApp, map[string]string{
		"templates/test.md": "---\ntype: test\n---\n\n## Steps\n",
		"boards/cover.base": "filters: 'note.type == \"test\"'\n",
		"docs/testing.md":   "---\ntitle: Testing\ntype: page\n---\n\nHow it works.\n",
	})

	p, err := Read(source, source)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := Install(root, c, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 4 {
		t.Errorf("changed %v", changed)
	}

	after, err := project.Load(root)
	if err != nil {
		t.Fatalf("the app left the vault unreadable: %v", err)
	}
	if !after.IsRelation("tests") || !after.IsRelation("tested_by") {
		t.Error("the verb it brought is not a verb")
	}
	if !hasType(after, "test") {
		t.Error("the type it brought is not a type")
	}
	if !hasField(after, "steps") {
		t.Error("the field it brought is not a field")
	}
	if len(after.Apps) != 1 || after.Apps[0].Name != "tests" || after.Apps[0].Version != "1" {
		t.Errorf("the vault does not record what it installed: %+v", after.Apps)
	}
	for _, name := range []string{"templates/test.md", "boards/cover.base", "docs/testing.md"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	// Installing the same app twice says nothing new and writes nothing.
	again, err := Install(root, after, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Errorf("installing it twice wrote %v", again)
	}
}

// What it refuses matters more than what it does.
func TestAnAppIsRefusedRatherThanMerged(t *testing.T) {
	root, c := vaultAt(t)

	// The vault already means something else by two of these names.
	c.Fields = append(c.Fields, project.Field{Name: "steps", Kind: "number"})
	c.Declared = append(c.Declared, project.Relation{Name: "verifies", Inverse: "verified_by"})
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// And a file somebody has edited.
	at := filepath.Join(root, "templates", "test.md")
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	source := pack(t, `name: tests
description: Test management
vocabulary:
  fields:
    - name: steps
      kind: text
    - name: status
      kind: text
  relations:
    - name: verifies
      inverse: covered_by
`, map[string]string{"templates/test.md": "theirs\n"})

	p, err := Read(source, source)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := Check(root, c, p)
	if len(conflicts) < 4 {
		t.Fatalf("only %d conflicts found: %v", len(conflicts), conflicts)
	}

	said := strings.Join(reasons(conflicts), "\n")
	for _, want := range []string{
		"field steps", "field status", "relation verifies", "file templates/test.md",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("nothing said about %q:\n%s", want, said)
		}
	}

	// And nothing is written: an installer that got half way is worse than one
	// that refused.
	if _, err := Install(root, c, p); err == nil {
		t.Fatal("it installed anyway")
	}
	body, err := os.ReadFile(at)
	if err != nil || string(body) != "mine\n" {
		t.Errorf("somebody's file was written over: %q %v", body, err)
	}
}

// An app may only carry files, in three folders. A symlink is a path out of the
// pack, and following one turns installing an app into running one.
func TestAnAppCannotCarryASymlink(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("name: sneaky\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "templates", "hosts.md")); err != nil {
		t.Skipf("symlinks are not available here: %v", err)
	}

	if _, err := Read(dir, dir); err == nil {
		t.Fatal("a pack carrying a symlink was accepted")
	} else if !strings.Contains(err.Error(), "regular file") {
		t.Errorf("refused with %q", err)
	}
}

func reasons(conflicts []Conflict) []string {
	var out []string
	for _, c := range conflicts {
		out = append(out, c.Error())
	}
	return out
}

func hasType(c *project.Config, name string) bool {
	for _, t := range c.Types {
		if t.Name == name {
			return true
		}
	}
	return false
}

func hasField(c *project.Config, name string) bool {
	for _, f := range c.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
