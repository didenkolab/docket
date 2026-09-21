package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
	"github.com/didenkolab/docket/internal/vault/vaulttest"
)

// Jira's sprint field holds every sprint the work passed through, and the
// import flattens it to one string. The task is in the last of them.
func TestASprintFieldCanNameSeveral(t *testing.T) {
	for _, c := range []struct {
		value string
		want  []string
	}{
		{"ACME Sprint 39", []string{"ACME Sprint 39"}},
		{"KPN Sprint 14, KPN Sprint 15, old SP", []string{"KPN Sprint 14", "KPN Sprint 15", "old SP"}},
		{"  spaced  ,  out ", []string{"spaced", "out"}},
		{"", nil},
		{" , ", nil},
	} {
		got := sprintList(c.value)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q read as %v, want %v", c.value, got, c.want)
		}
	}
}

// The whole of it, on a vault that came out of an import: a property nobody
// declared becomes the sprint the board knows, with a page per sprint and a
// link from every task.
func TestAdoptingASprintFromAnImportedProperty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	// Two tasks in one sprint, one that passed through two, and one in none.
	for _, made := range []struct{ title, sprint, created, updated string }{
		{"first", "Sprint 9", "2026-03-02T09:00:00Z", "2026-03-10T09:00:00Z"},
		{"second", "Sprint 9", "2026-03-04T09:00:00Z", "2026-03-14T09:00:00Z"},
		{"carried", "Sprint 8, Sprint 9", "2026-02-20T09:00:00Z", "2026-03-12T09:00:00Z"},
		{"loose", "", "2026-03-01T09:00:00Z", "2026-03-01T09:00:00Z"},
	} {
		rel, _, err := vault.Create(root, c, vault.NewOptions{Title: made.title})
		if err != nil {
			t.Fatal(err)
		}
		at := filepath.Join(root, filepath.FromSlash(rel))
		raw, err := os.ReadFile(at)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.Replace(string(raw), "tags: []",
			"tags: []\nx_sprint: "+made.sprint+"\ncreated_at: "+made.created, 1)
		if err := os.WriteFile(at, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := vault.List(root, c)
	if err != nil {
		t.Fatal(err)
	}
	pages, touched, err := adoptSprints(root, entries, "x_sprint", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 3 {
		t.Errorf("pointed %d tasks at a sprint, want 3", len(touched))
	}
	// One page, not two: the sprint a task only passed through has nothing in
	// it, and a page nothing points at would sit on the sprints page looking
	// like a fortnight that is running.
	if len(pages) != 1 || !strings.Contains(pages[0], "Sprint 9") {
		t.Fatalf("wrote %v", pages)
	}

	sprints, err := vault.Sprints(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sprints) != 1 {
		t.Fatalf("the vault has %d sprints", len(sprints))
	}
	s := sprints[0]
	if !s.Inferred {
		t.Error("the page does not say its dates were inferred")
	}
	if s.Trouble != "" {
		t.Errorf("its dates cannot be read: %s", s.Trouble)
	}
	if !strings.Contains(s.Body, "guess") {
		t.Error("the page does not warn that the dates are a guess")
	}

	// And the tasks link to it, which is what a board reads.
	after, err := vault.List(root, c)
	if err != nil {
		t.Fatal(err)
	}
	linked := 0
	for _, e := range after {
		if e.Task != nil && e.Task.Sprint == "Sprint 9" {
			linked++
		}
	}
	if linked != 3 {
		t.Errorf("%d tasks are in the sprint, want 3", linked)
	}
}
