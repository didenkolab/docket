package server

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// atRef builds one task as it would be read out of a ref.
func atRef(t *testing.T, key, body string) vault.Entry {
	t.Helper()
	parsed, err := task.Parse([]byte(body))
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return vault.Entry{Key: key, Task: parsed, Path: "ACME/" + key + ".md"}
}

func taskFile(key, title, kind, status, category, priority, extra, body string) string {
	// The default labels line is left out when extra supplies one: two of the
	// same property is a different finding from the one under test.
	labels := "labels: []\n"
	if strings.Contains(extra, "labels:") {
		labels = ""
	}
	return "---\nkey: " + key + "\ntitle: " + title + "\ntype: " + kind +
		"\nstatus: " + status + "\nstatus_category: " + category +
		"\npriority: " + priority + "\nassignee:\n" + labels +
		"created: 2026-08-30T12:00:00Z\nupdated: 2026-08-30T12:00:00Z\naliases: []\n" +
		extra + "---\n\n" + body + "\n"
}

func planVocabulary() *project.Config {
	return &project.Config{
		Name:     "Acme",
		Projects: []project.Project{{Key: "ACME", Name: "Acme"}},
		Statuses: []project.Status{
			{Name: "Backlog", Category: project.CategoryTodo},
			{Name: "In review", Category: project.CategoryDoing},
			{Name: "Done", Category: project.CategoryDone},
		},
		Types:      []project.Type{{Name: "task"}, {Name: "bug"}},
		Priorities: []string{"low", "normal", "high"},
	}
}

// A proposal is summarised in the vault's words, not as a diff: what arrived,
// what was dropped, and one sentence per fact about what changed.
func TestAProposalIsSaidInTheVaultsWords(t *testing.T) {
	before := []vault.Entry{
		atRef(t, "ACME-1", taskFile("ACME-1", "Keep this", "task", "Backlog", "todo", "normal", "", "Body.")),
		atRef(t, "ACME-2", taskFile("ACME-2", "Move this", "task", "Backlog", "todo", "normal", "", "Body.")),
		atRef(t, "ACME-3", taskFile("ACME-3", "Drop this", "task", "Backlog", "todo", "low", "", "Body.")),
	}
	after := []vault.Entry{
		atRef(t, "ACME-1", taskFile("ACME-1", "Keep this", "task", "Backlog", "todo", "normal", "", "Body.")),
		atRef(t, "ACME-2", taskFile("ACME-2", "Move this", "task", "In review", "doing", "high", "", "Body.")),
		atRef(t, "ACME-4", taskFile("ACME-4", "Arrive here", "bug", "Backlog", "todo", "normal", "", "New.")),
	}

	change := comparePlans(before, after, planVocabulary(), planVocabulary())

	if len(change.Arrived) != 1 || change.Arrived[0].Key != "ACME-4" {
		t.Errorf("arrived = %+v", change.Arrived)
	}
	if len(change.Dropped) != 1 || change.Dropped[0].Key != "ACME-3" {
		t.Errorf("dropped = %+v", change.Dropped)
	}
	if len(change.Changed) != 1 || change.Changed[0].Key != "ACME-2" {
		t.Fatalf("changed = %+v", change.Changed)
	}
	if change.Untouched != 1 {
		t.Errorf("untouched = %d, want the one task nobody touched", change.Untouched)
	}

	said := strings.Join(change.Changed[0].Says, " | ")
	for _, want := range []string{"moved from Backlog to In review", "priority normal → high"} {
		if !strings.Contains(said, want) {
			t.Errorf("it does not say %q:\n%s", want, said)
		}
	}
}

// Criteria are what a body change is counted in. "Eleven lines changed" is the
// diff again; "two criteria added, one ticked" is what a reviewer is deciding
// about.
func TestABodyChangeIsCountedInCriteria(t *testing.T) {
	const was = "Do the thing.\n\n- [ ] one\n- [ ] two\n"
	const now = "Do the thing.\n\n- [x] one\n- [ ] two\n- [ ] three\n"

	before := []vault.Entry{atRef(t, "ACME-1",
		taskFile("ACME-1", "A task", "task", "Backlog", "todo", "normal", "", was))}
	after := []vault.Entry{atRef(t, "ACME-1",
		taskFile("ACME-1", "A task", "task", "Backlog", "todo", "normal", "", now))}

	change := comparePlans(before, after, planVocabulary(), planVocabulary())
	if len(change.Changed) != 1 {
		t.Fatalf("changed = %+v", change.Changed)
	}
	said := strings.Join(change.Changed[0].Says, " | ")
	if !strings.Contains(said, "1 criterion added") {
		t.Errorf("it does not count what was added:\n%s", said)
	}
	if !strings.Contains(said, "1 criterion ticked") {
		t.Errorf("it does not count what was ticked:\n%s", said)
	}
}

// A body that changed without touching criteria still has to be reported: the
// wording of a task is the scope of it.
func TestARewrittenDescriptionIsSaid(t *testing.T) {
	before := []vault.Entry{atRef(t, "ACME-1",
		taskFile("ACME-1", "A task", "task", "Backlog", "todo", "normal", "", "One sentence."))}
	after := []vault.Entry{atRef(t, "ACME-1",
		taskFile("ACME-1", "A task", "task", "Backlog", "todo", "normal", "", "Quite another."))}

	change := comparePlans(before, after, planVocabulary(), planVocabulary())
	if len(change.Changed) != 1 ||
		!strings.Contains(strings.Join(change.Changed[0].Says, " "), "rewritten") {
		t.Errorf("a rewritten description was not reported: %+v", change.Changed)
	}
}

// A proposal that changes the vocabulary is a proposal about how the team works,
// and it is reported apart from the cards it moves.
func TestAChangeToTheVocabularyIsReportedApart(t *testing.T) {
	was := planVocabulary()
	now := planVocabulary()
	now.Statuses = append(now.Statuses, project.Status{Name: "Blocked", Category: project.CategoryDoing})
	now.Transitions = map[string][]string{"Backlog": {"Blocked"}}

	change := comparePlans(nil, nil, was, now)
	said := strings.Join(change.Vocabulary, " | ")
	if !strings.Contains(said, "status Blocked added") {
		t.Errorf("the new status is not named:\n%s", said)
	}
	if !strings.Contains(said, "workflow") {
		t.Errorf("the workflow change is not mentioned:\n%s", said)
	}
	if change.Nothing() {
		t.Error("a proposal that changes the vocabulary is not nothing")
	}
}

// A branch that touches no tasks is a normal thing to have, and the page has to
// say so rather than showing three empty headings.
func TestABranchThatChangesNoPlanSaysSo(t *testing.T) {
	same := []vault.Entry{atRef(t, "ACME-1",
		taskFile("ACME-1", "A task", "task", "Backlog", "todo", "normal", "", "Body."))}
	change := comparePlans(same, same, planVocabulary(), planVocabulary())

	if !change.Nothing() {
		t.Errorf("it thinks something changed: %+v", change)
	}
}

// Naming what came and went beats counting it: which label was added is the
// fact, and how many is not.
func TestLabelsAndRelationsAreNamed(t *testing.T) {
	before := []vault.Entry{atRef(t, "ACME-1", taskFile("ACME-1", "A task", "task",
		"Backlog", "todo", "normal", `labels: ["[[auth]]"]
`, "Body."))}
	after := []vault.Entry{atRef(t, "ACME-1", taskFile("ACME-1", "A task", "task",
		"Backlog", "todo", "normal", `labels: ["[[payments]]"]
blocked_by: ["[[ACME-9 Something else]]"]
`, "Body."))}

	change := comparePlans(before, after, planVocabulary(), planVocabulary())
	said := strings.Join(change.Changed[0].Says, " | ")
	for _, want := range []string{"payments added", "auth taken off", "blocked_by"} {
		if !strings.Contains(said, want) {
			t.Errorf("it does not say %q:\n%s", want, said)
		}
	}
}
