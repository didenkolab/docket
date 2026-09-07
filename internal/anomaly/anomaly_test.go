package anomaly

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

func made(t *testing.T, body string) vault.Entry {
	t.Helper()
	parsed, err := task.Parse([]byte("---\n" + body + "---\n"))
	if err != nil {
		t.Fatalf("%v\nfrom:\n%s", err, body)
	}
	return vault.Entry{Key: parsed.Key, Task: parsed, Path: "ACME/" + parsed.Key + ".md"}
}

func vocabulary() *project.Config {
	return &project.Config{
		Name: "x", Projects: []project.Project{{Key: "ACME"}},
		Statuses: []project.Status{
			{Name: "Backlog", Category: project.CategoryTodo},
			{Name: "In progress", Category: project.CategoryDoing},
			{Name: "Done", Category: project.CategoryDone},
		},
		Types: []project.Type{{Name: "epic", Level: 1}, {Name: "task"}},
	}
}

func kinds(found []Finding) string {
	var out []string
	for _, f := range found {
		out = append(out, f.Kind+":"+f.About)
	}
	return strings.Join(out, " ")
}

// A task with no parent, no label, no relation and nothing linking to it will
// not appear under any epic or in any search for the thing it belongs to. It
// can only be found by scrolling the column it is in.
func TestWorkThatFellOutOfThePlanIsFound(t *testing.T) {
	entries := []vault.Entry{
		made(t, "key: ACME-1\ntitle: Adrift\nstatus: Backlog\nstatus_category: todo\n"),
		made(t, "key: ACME-2\ntitle: Held\nstatus: Backlog\nstatus_category: todo\nlabels: [\"[[auth]]\"]\n"),
		made(t, "key: ACME-3\ntitle: Finished\nstatus: Done\nstatus_category: done\n"),
		// Carries nothing itself, and something else points at it. Reachable,
		// so not adrift — the backlink is the half a board never shows.
		made(t, "key: ACME-4\ntitle: Spoken of\nstatus: Backlog\nstatus_category: todo\n"),
		made(t, "key: ACME-5\ntitle: Speaks\nstatus: Backlog\nstatus_category: todo\n---\nSee [[ACME-4 Spoken of]].\n"),
	}

	found := kinds(Look(entries, vault.Shape{}, vocabulary()))
	if !strings.Contains(found, "adrift:ACME-1") {
		t.Errorf("the adrift task was not found: %s", found)
	}
	if strings.Contains(found, "adrift:ACME-2") {
		t.Error("a task carrying a label was called adrift")
	}
	if strings.Contains(found, "adrift:ACME-3") {
		t.Error("finished work was called adrift — it is not waiting for anybody")
	}
	if strings.Contains(found, "adrift:ACME-4") {
		t.Error("a task another task links to was called adrift")
	}
}

// The bus factor, asked of the graph: an epic every open child of which is on
// one person. Often correct, and worth knowing on the day they are ill.
func TestAnEpicOnePersonIsTheWholeOfIsFound(t *testing.T) {
	entries := []vault.Entry{
		made(t, "key: ACME-1\ntitle: Epic\ntype: epic\nstatus: Backlog\nstatus_category: todo\n"),
		made(t, "key: ACME-2\ntitle: a\nstatus: Backlog\nstatus_category: todo\nassignee: marina\nparent: \"[[ACME-1 Epic]]\"\n"),
		made(t, "key: ACME-3\ntitle: b\nstatus: Backlog\nstatus_category: todo\nassignee: marina\nparent: \"[[ACME-1 Epic]]\"\n"),
		made(t, "key: ACME-4\ntitle: c\nstatus: Backlog\nstatus_category: todo\nassignee: marina\nparent: \"[[ACME-1 Epic]]\"\n"),
	}
	if found := kinds(Look(entries, vault.Shape{}, vocabulary())); !strings.Contains(found, "only-owner:ACME-1") {
		t.Errorf("not found: %s", found)
	}

	// One task on somebody else and it is not the same finding.
	entries[3] = made(t, "key: ACME-4\ntitle: c\nstatus: Backlog\nstatus_category: todo\nassignee: timur\nparent: \"[[ACME-1 Epic]]\"\n")
	if found := kinds(Look(entries, vault.Shape{}, vocabulary())); strings.Contains(found, "only-owner") {
		t.Errorf("two people were called one: %s", found)
	}
}

// A closed epic with open children: the work is still there and it is no longer
// on anybody's board.
func TestAFinishedContainerWithUnfinishedWorkIsFound(t *testing.T) {
	entries := []vault.Entry{
		made(t, "key: ACME-1\ntitle: Epic\ntype: epic\nstatus: Done\nstatus_category: done\n"),
		made(t, "key: ACME-2\ntitle: a\nstatus: Backlog\nstatus_category: todo\nparent: \"[[ACME-1 Epic]]\"\n"),
	}
	if found := kinds(Look(entries, vault.Shape{}, vocabulary())); !strings.Contains(found, "contained:ACME-1") {
		t.Errorf("not found: %s", found)
	}
}

// The status says somebody is doing it and the file says nobody is.
func TestWorkInProgressWithNobodyOnItIsFound(t *testing.T) {
	entries := []vault.Entry{
		made(t, "key: ACME-1\ntitle: a\nstatus: In progress\nstatus_category: doing\n"),
		made(t, "key: ACME-2\ntitle: b\nstatus: In progress\nstatus_category: doing\nassignee: marina\n"),
	}
	found := kinds(Look(entries, vault.Shape{}, vocabulary()))
	if !strings.Contains(found, "unattended:ACME-1") {
		t.Errorf("not found: %s", found)
	}
	if strings.Contains(found, "unattended:ACME-2") {
		t.Error("work somebody is on was called unattended")
	}
}

// Two tasks disagreeing about their own relationship: A blocks B, and B does
// not say it is blocked.
func TestARelationshipOnlyOneSideNamesIsFound(t *testing.T) {
	entries := []vault.Entry{
		made(t, "key: ACME-1\ntitle: a\nstatus: Backlog\nstatus_category: todo\nblocks: [\"[[ACME-2 b]]\"]\n"),
		made(t, "key: ACME-2\ntitle: b\nstatus: Backlog\nstatus_category: todo\n"),
	}
	if found := kinds(Look(entries, vault.Shape{}, vocabulary())); !strings.Contains(found, "one-sided:ACME-1") {
		t.Errorf("not found: %s", found)
	}

	// Said from both ends, and there is nothing to report.
	entries[1] = made(t, "key: ACME-2\ntitle: b\nstatus: Backlog\nstatus_category: todo\nblocked_by: [\"[[ACME-1 a]]\"]\n")
	if found := kinds(Look(entries, vault.Shape{}, vocabulary())); strings.Contains(found, "one-sided") {
		t.Errorf("a relationship both sides name was reported: %s", found)
	}
}

// A note holding a large share of every link is doing what an index does. The
// measurement that caught two real mistakes in this project.
func TestANoteHoldingMostOfTheLinksIsFound(t *testing.T) {
	shape := vault.Shape{Edges: 100, Hubs: []vault.Hub{
		{Note: "roadmap", Edges: 30, Share: 30},
		{Note: "ACME-1", Edges: 4, Share: 4},
	}}
	found := kinds(Look(nil, shape, vocabulary()))
	if !strings.Contains(found, "concentrated:roadmap") {
		t.Errorf("not found: %s", found)
	}
	if strings.Contains(found, "concentrated:ACME-1") {
		t.Error("an ordinary note was called a hub")
	}
}

// A relation lives in the frontmatter, and `Links()` only reads the Markdown
// under it. An app that writes tasks whose one connection is a relation — a
// test that says `tests:`, a run that says `runs:` — had every one of them
// reported adrift, while the finding's own reason said "no relation".
func TestARelationIsNotAdriftAtEitherEnd(t *testing.T) {
	c := vocabulary()
	c.Declared = []project.Relation{{Name: "tests", Inverse: "tested_by"}}
	entries := []vault.Entry{
		// No parent, no label, no sprint, nothing in the body — and a relation.
		made(t, "key: ACME-1\ntitle: The test\nstatus: Backlog\nstatus_category: todo\n"+
			"tests: [\"[[ACME-2 The work]]\"]\n"),
		// Named by that relation and carrying nothing itself.
		made(t, "key: ACME-2\ntitle: The work\nstatus: Backlog\nstatus_category: todo\n"),
	}

	found := kinds(Look(entries, vault.Shape{}, c))
	if strings.Contains(found, "adrift:ACME-1") {
		t.Errorf("a task with a relation was called adrift: %s", found)
	}
	if strings.Contains(found, "adrift:ACME-2") {
		t.Errorf("a task another task's relation names was called adrift: %s", found)
	}
}
