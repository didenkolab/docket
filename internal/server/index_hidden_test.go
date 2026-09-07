package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// A directory under a dot is somebody's working state, not a page: the server
// must neither serve it nor let a wikilink resolve into it. The same rule the
// graph, the pages and the check apply — vault.Hidden.
func TestTheIndexSkipsHiddenDirectories(t *testing.T) {
	root := t.TempDir()
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/visible.md", "---\ntitle: visible\ntype: page\nupdated: 2026-09-07\n---\n\nA page.\n")
	write(".scratch/brief.md", "# A brief left by a tool\n")
	write(".scratch/deeper/notes.md", "# More of the same\n")

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := &index{targets: map[string]string{}}
	if err := buildIndex(ix, root, "", c); err != nil {
		t.Fatal(err)
	}
	if _, ok := ix.resolve("visible"); !ok {
		t.Error("a page under docs/ did not index")
	}
	for _, target := range []string{"brief", "notes", ".scratch/brief", ".scratch/deeper/notes"} {
		if href, ok := ix.resolve(target); ok {
			t.Errorf("%q resolved to %q: a hidden directory was indexed", target, href)
		}
	}
}
