package vault

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
)

// The numbers that caught two mistakes on this project, as a test.
func TestMeasure(t *testing.T) {
	root := t.TempDir()
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

	// A cluster of three around a label, a pair on their own, and an island.
	write("ACME/ACME-1 One.md", "---\nkey: ACME-1\n---\nAbout [[auth]].\n")
	write("ACME/ACME-2 Two.md", "---\nkey: ACME-2\n---\nAlso [[auth]], and [[ACME-1 One]].\n")
	write("docs/auth.md", "---\ntitle: auth\n---\nWhat auth means here.\n")
	write("ACME/ACME-3 Three.md", "---\nkey: ACME-3\n---\nSee [[ACME-4 Four]].\n")
	write("ACME/ACME-4 Four.md", "---\nkey: ACME-4\n---\nNothing else.\n")
	write("ACME/ACME-5 Alone.md", "---\nkey: ACME-5\n---\nNobody links here.\n")
	// A link said twice is one line on the canvas, and a note linking itself is
	// not an edge at all.
	write("ACME/ACME-6 Repeats.md",
		"---\nkey: ACME-6\n---\n[[auth]] and [[auth]] again, and [[ACME-6 Repeats]].\n")
	// Written for whoever opens the repository, not for the vault.
	write("README.md", "# A vault\n")
	// Templates are not notes.
	write("templates/task.md", "---\nkey:\n---\n[[auth]]\n")
	// A directory under a dot is somebody's working state — an agent's scratch,
	// an editor's cache — and its files are not notes either, however many
	// wikilinks they hold.
	write(".superpowers/sdd/task-1-brief.md", "[[auth]] and [[ACME-1 One]] forty times over.\n")
	write(".obsidian/plugins/x/notes.md", "[[auth]]\n")

	s, err := Measure(root)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}

	if s.Notes != 8 {
		t.Errorf("notes = %d, want 8 (the template is not a note)", s.Notes)
	}
	// auth–1, auth–2, auth–6, 1–2, 3–4.
	if s.Edges != 5 {
		t.Errorf("edges = %d, want 5: a repeated link is one edge and a self-link is none", s.Edges)
	}

	if len(s.Clusters) != 2 {
		t.Fatalf("clusters = %+v, want two", s.Clusters)
	}
	if s.Clusters[0].Size != 4 || s.Clusters[0].Named != "auth" {
		t.Errorf("largest cluster is %+v, want 4 around auth", s.Clusters[0])
	}
	if s.Clusters[1].Size != 2 {
		t.Errorf("second cluster is %+v, want 2", s.Clusters[1])
	}

	if len(s.Hubs) == 0 || s.Hubs[0].Note != "auth" || s.Hubs[0].Edges != 3 {
		t.Errorf("most connected is %+v, want auth with 3", s.Hubs)
	}
	if got := s.Concentration(); got < 59 || got > 61 {
		t.Errorf("concentration = %.1f%%, want 60 (3 of 5)", got)
	}

	// README is unlinked and must not be reported; ACME-5 is and must be.
	if len(s.Islands) != 1 || s.Islands[0] != "ACME-5 Alone" {
		t.Errorf("islands = %v, want only ACME-5", s.Islands)
	}
}

// The colours are written in the vault's own words, because the type names
// belong to the vault and nothing central has a list of them.
func TestGraphConfigSpeaksTheVaultsLanguage(t *testing.T) {
	c := configWithTypes(t)
	got := GraphConfig(c)

	var queries []string
	for _, g := range got.ColorGroups {
		queries = append(queries, g.Query)
	}
	for _, want := range []string{
		`["type":"sprint"]`,
		`["type":"Эпик"]`,
		`["status_category":"doing"]`,
		`["status_category":"done"]`,
		`["status_category":"todo"]`,
		"path:docs",
	} {
		found := false
		for _, q := range queries {
			if q == want {
				found = true
			}
		}
		if !found {
			t.Errorf("no group queries %s; got %v", want, queries)
		}
	}
	// The exact query comes first, or a broad one would colour a sprint.
	if queries[0] != `["type":"sprint"]` {
		t.Errorf("the first group is %q, and the first match is what colours a note", queries[0])
	}
	// A tag is not a node — that is the whole reason tags are cheap.
	if got.ShowTags {
		t.Error("tags are drawn as nodes, which undoes the reason they cost nothing")
	}
	// An island is the fourth question the graph is for.
	if !got.ShowOrphans {
		t.Error("orphans are hidden, which hides what nobody is looking after")
	}
}

// WriteGraph keeps what belongs to the person at this machine.
func TestWriteGraphKeepsTheView(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"scale":0.42,"search":"-path:docs","colorGroups":[],"close":false}`
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(GraphFile)),
		[]byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteGraph(root, configWithTypes(t)); err != nil {
		t.Fatalf("WriteGraph: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(GraphFile)))
	if err != nil {
		t.Fatal(err)
	}
	// Read back the way Obsidian will, rather than as text: the queries are
	// JSON strings holding quotes, and matching the escaped form would be a
	// test of the encoder.
	var got GraphSettings
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("what was written is not JSON Obsidian could read: %v\n%s", err, raw)
	}

	if got.Scale != 0.42 {
		t.Errorf("scale is %v — the zoom belongs to whoever set it", got.Scale)
	}
	if got.Search != "-path:docs" {
		t.Errorf("search is %q — the filter belongs to whoever typed it", got.Search)
	}
	if got.Close {
		t.Error("a folded panel was unfolded")
	}
	if len(got.ColorGroups) == 0 || got.ColorGroups[0].Query != `["type":"sprint"]` {
		t.Errorf("the colours were not written: %+v", got.ColorGroups)
	}
}

func configWithTypes(t *testing.T) *project.Config {
	t.Helper()
	c, err := project.Parse([]byte(`name: Пирс
projects:
  - key: PIER
statuses:
  - {name: Discovery, category: todo}
  - {name: В работе, category: doing}
  - {name: Готово, category: done}
types:
  - {name: Эпик, level: 1}
  - {name: Задача, level: 0}
priorities: [низкий, обычный, высокий]
`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
