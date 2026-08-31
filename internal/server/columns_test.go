package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// A workspace of two projects that run different workflows — which is the
// ordinary case, not the odd one: a team that imported its board from Jira and
// a team that wrote its own agree about almost nothing.
func disagreeingProjects(t *testing.T) http.Handler {
	t.Helper()

	template := vaulttest.Template(t)
	root := t.TempDir()

	for _, p := range []struct{ key, dir, status string }{
		{"ONE", "one", "In review"},
		{"TWO", "two", "RFT Stage"},
	} {
		at := filepath.Join(root, p.dir)
		if _, err := vault.Init(at, vault.Options{Key: p.key, Template: template}); err != nil {
			t.Fatal(err)
		}
		// One status nobody else has, so a column drawn from the wrong project
		// is visible as itself rather than as a count.
		config := filepath.Join(at, "docket.yaml")
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		if p.status != "In review" {
			body := strings.Replace(string(raw), "  - name: In review\n",
				"  - name: "+p.status+"\n", 1)
			if body == string(raw) {
				t.Fatalf("the template no longer has the status this test rewrites")
			}
			if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		shell(t, at, "git", "init", "-q", "-b", "main")
		shell(t, at, "git", "config", "user.email", "t@example.com")
		shell(t, at, "git", "config", "user.name", "T")
		shell(t, at, "git", "add", "-A")
		shell(t, at, "git", "commit", "-q", "-m", "start")
	}

	m := &workspace.Manifest{Projects: []workspace.Project{
		{Key: "ONE", Path: "one", Remote: "https://git.example.com/team/one.git"},
		{Key: "TWO", Path: "two", Remote: "https://git.example.com/team/two.git"},
	}}
	if err := m.Save(root); err != nil {
		t.Fatal(err)
	}

	s, err := New(root, Options{
		Author:          gitvcs.Author{Name: "Server", Email: "server@example.com"},
		SessionLife:     time.Hour,
		Template:        template,
		Unauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler()
}

// A tab draws its own project's workflow. A real workspace board came out with
// twenty one columns on every tab, fifteen of them belonging to another team
// and permanently empty — a column nothing here can be dropped into is a column
// in the way.
func TestAProjectTabDrawsItsOwnColumns(t *testing.T) {
	h := disagreeingProjects(t)

	one := get(t, h, "/?project=ONE").Body.String()
	if !strings.Contains(one, `data-status="In review"`) {
		t.Error("the tab is missing its own column")
	}
	if strings.Contains(one, `data-status="RFT Stage"`) {
		t.Error("the tab draws the other project's column")
	}

	two := get(t, h, "/?project=TWO").Body.String()
	if !strings.Contains(two, `data-status="RFT Stage"`) {
		t.Error("the other tab is missing its own column")
	}
	if strings.Contains(two, `data-status="In review"`) {
		t.Error("the other tab draws the first project's column")
	}
}

// Every project at once is the one view where the union is the honest answer:
// the board is being read across teams, and a status only one of them uses is
// still where its work is.
func TestEveryProjectDrawsEveryColumn(t *testing.T) {
	h := disagreeingProjects(t)

	all := get(t, h, "/").Body.String()
	for _, want := range []string{`data-status="In review"`, `data-status="RFT Stage"`} {
		if !strings.Contains(all, want) {
			t.Errorf("the whole workspace does not draw %s", want)
		}
	}
}
