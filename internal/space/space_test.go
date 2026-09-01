package space

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// A workspace merges its projects' vocabularies. It merged statuses, types and
// priorities and dropped everything else on the floor — so a field a repository
// declared was invisible on its own tasks, and a verb it declared was not a
// verb, because the page asks the space rather than the project.
func TestAWorkspaceKeepsWhatItsProjectsDeclared(t *testing.T) {
	template := vaulttest.Template(t)
	root := t.TempDir()

	for _, p := range []struct{ key, dir, field, relation string }{
		{"ONE", "one", "risk", "tests"},
		{"TWO", "two", "customer", "threatens"},
	} {
		at := filepath.Join(root, p.dir)
		if _, err := vault.Init(at, vault.Options{Key: p.key, Template: template}); err != nil {
			t.Fatal(err)
		}
		config := filepath.Join(at, "docket.yaml")
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		more := "fields:\n  - name: " + p.field + "\n    kind: text\n" +
			"relations:\n  - name: " + p.relation + "\n    inverse: " + p.relation + "_by\n"
		if err := os.WriteFile(config, append(raw, []byte(more)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := &workspace.Manifest{Projects: []workspace.Project{
		{Key: "ONE", Path: "one", Remote: "https://git.example.com/team/one.git"},
		{Key: "TWO", Path: "two", Remote: "https://git.example.com/team/two.git"},
	}}
	if err := m.Save(root); err != nil {
		t.Fatal(err)
	}

	sp, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sp.Config()
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"risk", "customer"} {
		if !hasField(c, name) {
			t.Errorf("the merged vocabulary lost the field %q", name)
		}
	}
	for _, name := range []string{"tests", "tests_by", "threatens", "threatens_by"} {
		if !c.IsRelation(name) {
			t.Errorf("the merged vocabulary lost the relation %q", name)
		}
	}
	// And the ones the format ships are still there.
	if !c.IsRelation("blocks") {
		t.Error("merging dropped the built-in relations")
	}
}

func hasField(c *project.Config, name string) bool {
	for _, f := range c.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
