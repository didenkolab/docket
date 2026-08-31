package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A vault whose documents have no shape is a vault whose documents drift into
// several. This project's own five decisions had three shapes before anybody
// noticed, and each was written by looking at whichever other one was open.
func TestDocuments(t *testing.T) {
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
	write("docket.yaml", "name: Acme\nprojects:\n  - key: ACME\nstatuses:\n"+
		"  - {name: Backlog, category: todo}\ntypes:\n  - {name: story}\npriorities: [normal]\n")
	for _, b := range []string{"board", "backlog", "my-tasks"} {
		write("boards/"+b+".base", "mine\n")
	}

	decision := func(name, front, sections string) {
		write("docs/decisions/"+name, "---\ntitle: A choice\ntype: decision\n"+front+"---\n\n"+sections)
	}
	const whole = "## Context\n\nWhy.\n\n## Decision\n\nWhat.\n\n" +
		"## What this costs\n\nThis.\n\n## Alternatives considered\n\nThose.\n"

	decision("0001-a-good-one.md", "status: accepted\ndate: 2026-08-01\n", whole)
	decision("0002-missing-a-section.md", "status: accepted\ndate: 2026-08-01\n",
		"## Context\n\nWhy.\n\n## Decision\n\nWhat.\n\n## Alternatives considered\n\nThose.\n")
	decision("0003-status-said-twice.md", "status: accepted\ndate: 2026-08-01\n",
		whole+"\n## Status\n\nAccepted.\n")
	decision("0004-no-date.md", "status: accepted\n", whole)
	decision("0005-odd-status.md", "status: agreed\ndate: 2026-08-01\n", whole)
	decision("0006-gone-nowhere.md", "status: superseded\ndate: 2026-08-01\n", whole)
	decision("0006-same-number.md", "status: accepted\ndate: 2026-08-01\n", whole)
	decision("unnumbered.md", "status: accepted\ndate: 2026-08-01\n", whole)
	// Every section present, in the wrong sequence — the way the drift began.
	decision("0007-out-of-order.md", "status: accepted\ndate: 2026-08-01\n",
		"## Context\n\nWhy.\n\n## Alternatives considered\n\nThose.\n\n"+
			"## Decision\n\nWhat.\n\n## What this costs\n\nThis.\n")
	write("docs/decisions/0008-a-string.md",
		"---\ntitle: A choice\ntype: decision\nstatus: superseded\ndate: 2026-08-01\n"+
			"supersedes: 0001-a-good-one\n---\n\n"+whole)

	// A design page is an argument, and a required shape would flatten it.
	write("docs/design/how-it-works.md", "---\ntitle: how-it-works\ntype: design\n---\n\nProse.\n")
	// A page that never said what it is.
	write("docs/notes.md", "---\ntitle: notes\n---\n\nWords.\n")
	// A specification full of examples must not be read as having the headings
	// its examples show.
	write("docs/spec/format.md", "---\ntitle: format\ntype: spec\nstatus: normative\n---\n\n"+
		"A decision looks like this:\n\n```markdown\n## Context\n## Decision\n```\n")

	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, c := range []struct{ file, says string }{
		{"0002-missing-a-section", "needs a ## What this costs section"},
		{"0003-status-said-twice", "Two records of one fact"},
		{"0004-no-date", "needs the date it was taken"},
		{"0005-odd-status", `status is one of proposed, accepted, superseded, and this one says "agreed"`},
		{"0006-gone-nowhere", "does not say by what"},
		{"0006-same-number", "is already"},
		{"unnumbered", "named NNNN-kebab-title.md"},
		{"0007-out-of-order", "## Alternatives considered is above ## What this costs and belongs below it"},
		{"0008-a-string", "is a string, so the two decisions are not joined"},
		{"notes", "does not say what kind of document it is"},
	} {
		if !said(findings, c.file, c.says) {
			t.Errorf("nothing about %s says %q\ngot:\n%s", c.file, c.says, list(findings))
		}
	}

	// The ones that are right must be silent, or the rule is noise.
	for _, f := range findings {
		if f.Rule != RuleDocuments {
			continue
		}
		for _, quiet := range []string{"0001-a-good-one", "how-it-works", "spec/format"} {
			if strings.Contains(f.Path, quiet) {
				t.Errorf("a document with nothing wrong with it was reported: %s", f)
			}
		}
	}
}
