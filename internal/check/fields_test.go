package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A value has to mean what the vault said the field would hold.
//
// The kinds are the ones a real import produced: a number that turned out to be
// a LexoRank string, a date that turned out to be a timestamp, and fifteen
// others nobody had declared at all.
func TestFields(t *testing.T) {
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
	write("docket.yaml", `name: Пирс
projects:
  - key: PIER
statuses:
  - {name: Backlog, category: todo}
types:
  - {name: Баг}
  - {name: Задача}
priorities: [normal]
fields:
  - {name: points, label: Story points, kind: number}
  - {name: due, kind: date}
  - {name: seen_at, kind: datetime}
  - {name: рейтинг, kind: text}
  - {name: risk, kind: choice, choices: [low, high]}
  - {name: клиентский, kind: flag}
  - {name: spec, kind: link}
  - {name: found_in, kind: text, types: [Баг], required: true}
`)
	for _, b := range []string{"board", "backlog", "my-tasks"} {
		write("boards/"+b+".base", "mine\n")
	}
	task := func(key, kind, extra string) {
		write("PIER/"+key+" A task.md",
			"---\nkey: "+key+"\ntitle: A task\ntype: "+kind+"\nstatus: Backlog\n"+
				"status_category: todo\npriority: normal\nassignee:\n"+extra+
				"created: 2026-08-01T00:00:00Z\nupdated: 2026-08-01T00:00:00Z\naliases: []\n---\n")
	}

	// Everything right, including a Cyrillic property name — the importer
	// writes those, and an ASCII-only rule would refuse a whole vault.
	task("PIER-1", "Задача", "points: 5\ndue: 2026-09-01\nseen_at: 2026-08-31T14:05:00Z\n"+
		"рейтинг: \"2|i02v2v:\"\nrisk: high\nклиентский: true\nspec: https://example.com/spec\n")
	task("PIER-2", "Баг", "points: five\nfound_in: 2.0\n")
	task("PIER-3", "Баг", "due: 2026-08-31T21:10:47.089+0300\nfound_in: 2.0\n")
	task("PIER-4", "Баг", "seen_at: yesterday\nfound_in: 2.0\n")
	task("PIER-5", "Баг", "risk: medium\nfound_in: 2.0\n")
	task("PIER-6", "Баг", "клиентский: maybe\nfound_in: 2.0\n")
	task("PIER-7", "Баг", "spec: see the wiki\nfound_in: 2.0\n")
	task("PIER-8", "Баг", "")                   // required field missing
	task("PIER-9", "Задача", "found_in: 2.0\n") // a bug's field on a task

	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, c := range []struct{ file, says string }{
		{"PIER-2", `Story points is a number and this says "five"`},
		{"PIER-3", "due is a date and this says"},
		{"PIER-4", "seen_at is a moment in time"},
		{"PIER-5", `risk is "medium", which is not one of low, high`},
		{"PIER-6", "клиентский is yes or no"},
		{"PIER-7", "spec is a link somewhere else"},
		{"PIER-8", "found_in is required of every Баг"},
		{"PIER-9", "found_in is a field of Баг, and this is a Задача"},
	} {
		if !said(findings, c.file, c.says) {
			t.Errorf("nothing about %s says %q\ngot:\n%s", c.file, c.says, list(findings))
		}
	}

	// The one that is right must be silent, or the rule is noise — and it holds
	// a Cyrillic property name and a LexoRank string, both of which a stricter
	// rule would have refused.
	for _, f := range findings {
		if f.Rule == RuleFields && strings.Contains(f.Path, "PIER-1") {
			t.Errorf("a task with nothing wrong with it was reported: %s", f)
		}
	}
}
