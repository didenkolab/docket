package base

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// The round trip is the whole of it: a filter read into a form and written back
// unchanged has to select the same tasks. A form that quietly rewrote what a
// board asks would change the board while looking like it only displayed it.
func TestAFilterSurvivesBeingReadAsAFormAndWrittenBack(t *testing.T) {
	c := &project.Config{
		Projects: []project.Project{{Key: "ACME", Name: "Acme"}, {Key: "ACME", Name: "Acme"}},
		Statuses: []project.Status{
			{Name: "Backlog", Category: project.CategoryTodo},
			{Name: "In progress", Category: project.CategoryDoing},
			{Name: "Done", Category: project.CategoryDone},
		},
		Types: []project.Type{{Name: "epic", Level: 1}, {Name: "task"}, {Name: "subtask", Level: -1}},
	}

	notes := []Note{
		note("ACME/ACME-1 a.md", "status_category", "todo", "type", "task", "assignee", "agent/claude"),
		note("ACME/ACME-2 b.md", "status_category", "done", "type", "task"),
		note("ACME/ACME-3 c.md", "status_category", "todo", "type", "subtask"),
		note("docs/why.md", "status_category", "todo"),
	}

	for file, body := range vault.GeneratedIn(c, true) {
		before, err := Parse([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		form, ok := Read(before.Filter)
		if !ok {
			t.Errorf("%s: its filter cannot be shown as a form", file)
			continue
		}

		filters, err := form.Write()
		if err != nil {
			t.Errorf("%s: writing it back: %v", file, err)
			continue
		}
		rewritten, err := Rewrite([]byte(body), filters)
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		after, err := Parse(rewritten)
		if err != nil {
			t.Errorf("%s: what was written back does not parse: %v\n%s", file, err, rewritten)
			continue
		}

		for _, n := range notes {
			if before.Matches(n) != after.Matches(n) {
				t.Errorf("%s: %s selected %v before and %v after\nbefore: %s\nafter:  %s",
					file, n.Path, before.Matches(n), after.Matches(n),
					before.Filter, after.Filter)
			}
		}
	}
}

// Everything else in the file has to survive the filter being edited: a base
// holds views, formulas and display names this reader does not model.
func TestRewritingTheFilterKeepsTheRestOfTheFile(t *testing.T) {
	body := []byte(`filters: 'note.key'
formulas:
  stage: 'if(note.status == "Done", "done", "")'
properties:
  note.title:
    displayName: Title
views:
  - type: cards
    name: Board
    groupBy:
      property: formula.stage
    order:
      - title
`)

	out, err := Rewrite(body, "filters:\n  and:\n    - 'note.status_category != \"done\"'\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(b.Views) != 1 || b.Views[0].Name != "Board" || b.Views[0].GroupBy != "formula.stage" {
		t.Errorf("the views did not survive: %+v", b.Views)
	}
	if _, ok := b.Formulas["stage"]; !ok {
		t.Error("the formula did not survive")
	}
	if b.Displayed("note.title") != "Title" {
		t.Error("the display names did not survive")
	}
	if b.Matches(note("A/A-1 x.md", "status_category", "done")) {
		t.Error("the new filter did not take")
	}
}

// A filter that is not a flat list of conditions is left to the text. Flattening
// it into a form would change what the board selects.
func TestAFilterTooBigForTheFormIsRefusedAsOne(t *testing.T) {
	b, err := Parse([]byte(`
filters:
  and:
    - or:
      - 'note.status == "Blocked"'
      - 'note.priority == "urgent"'
    - 'note.assignee'
`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Read(b.Filter); ok {
		t.Error("a nested filter was offered as a flat form")
	}
}

// What the form writes has to be what the reader reads back — including the
// spellings that need quoting.
func TestEveryOperatorWritesSomethingReadable(t *testing.T) {
	for _, c := range []struct {
		cond Condition
		on   Note
		want bool
	}{
		{Condition{"status", OpIs, "In review"}, note("A/A-1 x.md", "status", "In review"), true},
		{Condition{"status", OpIsNot, "Done"}, note("A/A-1 x.md", "status", "In review"), true},
		{Condition{"labels", OpContains, "server"}, note("A/A-1 x.md", "labels", "[[server]]"), true},
		{Condition{"assignee", OpHasValue, ""}, note("A/A-1 x.md", "assignee", "marina"), true},
		{Condition{"assignee", OpIsEmpty, ""}, note("A/A-1 x.md"), true},
		{Condition{"estimate", OpAtLeast, "3"}, note("A/A-1 x.md", "estimate", "5"), true},
		{Condition{"estimate", OpLessThan, "3"}, note("A/A-1 x.md", "estimate", "5"), false},
	} {
		filters, err := Filters{Join: "and", Conditions: []Condition{c.cond}}.Write()
		if err != nil {
			t.Errorf("%+v: %v", c.cond, err)
			continue
		}
		b, err := Parse([]byte(filters))
		if err != nil {
			t.Errorf("%+v wrote %q, which does not read back: %v", c.cond, filters, err)
			continue
		}
		if got := b.Matches(c.on); got != c.want {
			t.Errorf("%+v wrote %q, which matched %v, want %v", c.cond, strings.TrimSpace(filters), got, c.want)
		}
	}
}

// A row nobody filled in is not a condition. A form always shows a few empty
// ones, and they must not become `note. == ""`.
func TestEmptyRowsAreNotConditions(t *testing.T) {
	written, err := Filters{
		Join:       "and",
		Folders:    []string{"ACME"},
		Conditions: []Condition{{}, {Property: "status", Op: OpIs, Value: "Done"}, {}},
	}.Write()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(written, "note.") != 1 {
		t.Errorf("the empty rows were written too:\n%s", written)
	}
}
