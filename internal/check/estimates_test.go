package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two things an estimate can be wrong about, and the three a sprint can.
// Written against a vault on disk rather than against the functions, because
// both rules are about the other files: whether a task has children, and
// whether a sprint page exists, cannot be answered from the file being judged.
func TestEstimatesAndSprints(t *testing.T) {
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

	write("docket.yaml", `name: Acme
projects:
  - key: ACME
    name: Acme
statuses:
  - {name: Backlog, category: todo}
  - {name: Done, category: done}
types:
  - {name: epic, level: 1}
  - {name: story, level: 0}
priorities: [low, normal, high]
estimates:
  unit: points
  scale: [1, 2, 3, 5, 8, 13]
`)
	write("boards/board.base", "mine\n")
	write("boards/backlog.base", "mine\n")
	write("boards/my-tasks.base", "mine\n")

	task := func(key, title, extra string) string {
		return "---\nkey: " + key + "\ntitle: " + title + "\ntype: story\n" +
			"status: Backlog\nstatus_category: todo\npriority: normal\nassignee:\n" +
			extra + "created: 2026-08-01T00:00:00Z\nupdated: 2026-08-01T00:00:00Z\naliases: []\n---\n"
	}

	write("docs/sprints/Sprint 1.md", "---\ntitle: Sprint 1\ntype: sprint\nstarts: 2026-08-03\nends: 2026-08-14\n---\n")
	// Overlaps Sprint 1 by a day, which a board asked "which one is running"
	// would have to choose between.
	write("docs/sprints/Sprint 2.md", "---\ntitle: Sprint 2\ntype: sprint\nstarts: 2026-08-14\nends: 2026-08-28\n---\n")
	write("docs/sprints/Sprint 3.md", "---\ntitle: Sprint 3\ntype: sprint\nstarts: 2026-09-11\nends: 2026-08-31\n---\n")

	write("ACME/ACME-1 Fine.md", task("ACME-1", "Fine", "estimate: 5\nsprint: \"[[Sprint 1]]\"\n"))
	write("ACME/ACME-2 Off the scale.md", task("ACME-2", "Off the scale", "estimate: 4\n"))
	write("ACME/ACME-3 A container.md",
		"---\nkey: ACME-3\ntitle: A container\ntype: epic\nstatus: Backlog\n"+
			"status_category: todo\npriority: normal\nassignee:\nestimate: 8\n"+
			"created: 2026-08-01T00:00:00Z\nupdated: 2026-08-01T00:00:00Z\naliases: []\n---\n")
	write("ACME/ACME-4 A child.md", task("ACME-4", "A child",
		"estimate: 3\nparent: \"[[ACME-3 A container]]\"\n"))
	write("ACME/ACME-5 Sprint as a string.md", task("ACME-5", "Sprint as a string", "sprint: Sprint 1\n"))
	write("ACME/ACME-6 Sprint that is not a page.md",
		task("ACME-6", "Sprint that is not a page", "sprint: \"[[Sprint 9]]\"\n"))

	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []struct{ file, says string }{
		{"ACME-2", "is not on the scale 1, 2, 3, 5, 8, 13"},
		{"ACME-3", "a container's size is what its children add up to"},
		{"ACME-5", "is a string, so it is not an edge in the graph"},
		{"ACME-6", "is not a sprint page"},
		{"Sprint 3", "ends before it starts"},
		{"Sprint 2", "overlaps"},
	}
	for _, w := range want {
		if !said(findings, w.file, w.says) {
			t.Errorf("nothing about %s says %q\ngot:\n%s", w.file, w.says, list(findings))
		}
	}
	// The one that is right must not be reported, or the rule is noise.
	for _, f := range findings {
		if strings.Contains(f.Path, "ACME-1") || strings.Contains(f.Path, "ACME-4") {
			t.Errorf("a task with nothing wrong with it was reported: %s", f)
		}
	}
}

// A vault that says nothing about estimates should not have tasks carrying a
// number in no unit anybody can name.
func TestEstimateWithoutAUnit(t *testing.T) {
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
	write("docket.yaml", `name: Acme
projects:
  - key: ACME
statuses:
  - {name: Backlog, category: todo}
types:
  - {name: story}
priorities: [normal]
`)
	for _, b := range []string{"board", "backlog", "my-tasks"} {
		write("boards/"+b+".base", "mine\n")
	}
	write("ACME/ACME-1 Sized anyway.md", "---\nkey: ACME-1\ntitle: Sized anyway\ntype: story\n"+
		"status: Backlog\nstatus_category: todo\npriority: normal\nassignee:\nestimate: 3\n"+
		"created: 2026-08-01T00:00:00Z\nupdated: 2026-08-01T00:00:00Z\naliases: []\n---\n")

	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !said(findings, "ACME-1", "says nothing about estimates") {
		t.Errorf("got:\n%s", list(findings))
	}
}

func said(findings []Finding, file, says string) bool {
	for _, f := range findings {
		if strings.Contains(f.Path, file) && strings.Contains(f.Message, says) {
			return true
		}
	}
	return false
}

func list(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString("  " + f.String() + "\n")
	}
	if b.Len() == 0 {
		return "  (no findings)\n"
	}
	return b.String()
}

// A sprint page that links its tasks. The mechanism costs one edge per task,
// which is what a label costs; the prose cost twenty-eight per cent of a real
// vault's edges and collapsed its graph into one blob — the same failure the
// project already removed index pages for. See internal/check/sprints.go.
func TestSprintPageThatListsItsTasks(t *testing.T) {
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
	write("docket.yaml", `name: Acme
projects:
  - key: ACME
statuses:
  - {name: Backlog, category: todo}
types:
  - {name: story}
priorities: [normal]
`)
	for _, b := range []string{"board", "backlog", "my-tasks"} {
		write("boards/"+b+".base", "mine\n")
	}
	task := func(key, title, extra string) {
		write("ACME/"+key+" "+title+".md",
			"---\nkey: "+key+"\ntitle: "+title+"\ntype: story\nstatus: Backlog\n"+
				"status_category: todo\npriority: normal\nassignee:\n"+extra+
				"created: 2026-08-01T00:00:00Z\nupdated: 2026-08-01T00:00:00Z\naliases: []\n---\n")
	}
	task("ACME-1", "In the sprint", "sprint: \"[[Sprint 1]]\"\n")
	task("ACME-2", "Also in it", "sprint: \"[[Sprint 1]]\"\n")
	task("ACME-11", "Not in it", "")
	write("docs/decisions/why-fortnights.md", "---\ntitle: why-fortnights\ntype: page\n---\nBecause.\n")

	// Two of its own tasks, one that is not in it, and a page — which is the
	// one link that teaches something and must be left alone.
	write("docs/sprints/Sprint 1.md", `---
title: Sprint 1
type: sprint
starts: 2026-08-03
ends: 2026-08-14
---

# Sprint 1

Close [[ACME-1 In the sprint]] and [[ACME-2 Also in it]]. Blocked behind
[[ACME-11 Not in it]], which is not ours this fortnight. Why a fortnight at all:
[[why-fortnights]].
`)

	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !said(findings, "Sprint 1", "links 3 tasks, 1 of them not in this sprint") {
		t.Errorf("got:\n%s", list(findings))
	}
	// By key the way a board orders them, not by string: ACME-2 before ACME-11.
	if !said(findings, "Sprint 1", "ACME-1, ACME-2, ACME-11") {
		t.Errorf("the keys are not in board order:\n%s", list(findings))
	}
	// The link to a page is not a fault, and saying it were would teach people
	// that a sprint may cite nothing at all.
	for _, f := range findings {
		if strings.Contains(f.Message, "why-fortnights") {
			t.Errorf("the link to a page was reported: %s", f)
		}
	}

	// A sprint that names its work in backticks is silent.
	write("docs/sprints/Sprint 1.md", `---
title: Sprint 1
type: sprint
starts: 2026-08-03
ends: 2026-08-14
---

# Sprint 1

Close `+"`ACME-1`"+` and `+"`ACME-2`"+`. Blocked behind `+"`ACME-11`"+`, which is not ours
this fortnight. Why a fortnight at all: [[why-fortnights]].
`)
	findings, err = Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if f.Rule == RuleSprints {
			t.Errorf("a sprint naming its work in backticks was reported: %s", f)
		}
	}
}
