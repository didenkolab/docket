package base

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

func note(path string, pairs ...string) Note {
	n := Note{Path: path, Values: map[string]string{}, Lists: map[string][]string{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		n.Values[pairs[i]] = pairs[i+1]
	}
	return n
}

// The boards the vault ships with are the first thing this has to read. A
// reader that could not evaluate docket's own generated files would be a reader
// of a language nobody writes.
func TestTheShippedBoardsAreReadable(t *testing.T) {
	c := &project.Config{
		Projects: []project.Project{{Key: "ACME", Name: "Acme"}, {Key: "ACME", Name: "Acme"}},
		Statuses: []project.Status{
			{Name: "Backlog", Category: project.CategoryTodo},
			{Name: "In progress", Category: project.CategoryDoing},
			{Name: "Done", Category: project.CategoryDone},
		},
		Types: []project.Type{{Name: "epic", Level: 1}, {Name: "task"}, {Name: "subtask", Level: -1}},
	}

	for file, body := range vault.GeneratedIn(c, true) {
		b, err := Parse([]byte(body))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		if b.Filter == nil {
			t.Errorf("%s: read with no filter at all", file)
		}
		if len(b.Views) == 0 {
			t.Errorf("%s: no views", file)
		}
	}
}

// The board is "tasks in one of my projects that are not done". Which is two
// mechanisms at once — a folder and a property — and both have to hold.
func TestTheBoardSelectsUnfinishedWorkInItsProjects(t *testing.T) {
	b, err := Parse([]byte(`
filters:
  and:
    - or:
      - file.inFolder("ACME")
      - file.inFolder("ACME")
    - 'note.status_category != "done"'
`))
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		what string
		n    Note
		want bool
	}{
		{"unfinished, in a project", note("ACME/ACME-1 A.md", "status_category", "doing"), true},
		{"unfinished, in the other", note("ACME/ACME-9 B.md", "status_category", "todo"), true},
		{"finished", note("ACME/ACME-2 C.md", "status_category", "done"), false},
		{"a document, not a task", note("docs/design/why.md", "status_category", "todo"), false},
		{"a task in a folder below the project", note("ACME/old/ACME-3 D.md", "status_category", "todo"), true},
		{"no status at all", note("ACME/ACME-4 E.md"), true},
	} {
		if got := b.Matches(c.n); got != c.want {
			t.Errorf("%s: %v, want %v", c.what, got, c.want)
		}
	}
}

// A sprint is written as a wikilink, because that is what Obsidian resolves.
// A filter naming the sprint should not have to know that.
func TestALinkIsComparedByWhatItPointsAt(t *testing.T) {
	b, err := Parse([]byte(`filters: 'note.sprint == "Sprint 12"'`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		written string
		want    bool
	}{
		{"[[Sprint 12]]", true},
		{"Sprint 12", true},
		{"[[Sprint 12|the one we are in]]", true},
		{"[[Sprint 3]]", false},
		{"", false},
	} {
		if got := b.Matches(note("A/A-1 x.md", "sprint", c.written)); got != c.want {
			t.Errorf("sprint: %q matched %v, want %v", c.written, got, c.want)
		}
	}
}

func TestTheRestOfTheLanguage(t *testing.T) {
	tagged := Note{
		Path:   "ACME/ACME-1 x.md",
		Values: map[string]string{"assignee": "agent/claude", "estimate": "3"},
		Lists:  map[string][]string{"tags": {"#urgent", "wave-2"}, "labels": {"[[server]]", "[[docs]]"}},
	}

	for _, c := range []struct {
		filter string
		want   bool
	}{
		{`file.hasTag("urgent")`, true},
		{`file.hasTag("wave-2")`, true},
		{`file.hasTag("calm")`, false},
		{`note.assignee`, true},
		{`note.sprint`, false},
		{`!note.sprint`, true},
		{`note.labels.contains("server")`, true},
		{`note.labels.contains("design")`, false},
		{`note.estimate > 2`, true},
		{`note.estimate > 5`, false},
		{`note.estimate <= 3`, true},
	} {
		b, err := Parse([]byte("filters: '" + c.filter + "'"))
		if err != nil {
			t.Errorf("%s: %v", c.filter, err)
			continue
		}
		if got := b.Matches(tagged); got != c.want {
			t.Errorf("%s: %v, want %v", c.filter, got, c.want)
		}
	}
}

// Bases is larger than this reader. What it cannot evaluate it has to refuse by
// name: a view drawn from the half of a filter it understood is a wrong list
// that looks like a right one.
func TestAnUnreadableFilterIsRefusedByName(t *testing.T) {
	_, err := Parse([]byte(`
filters:
  and:
    - file.inFolder("ACME")
    - 'note.due.isBefore(date("today"))'
`))
	if err == nil {
		t.Fatal("a filter this cannot evaluate was accepted")
	}
	if !strings.Contains(err.Error(), "isBefore") {
		t.Errorf("the refusal does not say what it could not read: %v", err)
	}
}

// A view says how it wants to be drawn, and a grouped view is what a board is.
func TestViewsAreReadWithTheirShape(t *testing.T) {
	b, err := Parse([]byte(`
filters: 'note.sprint'
properties:
  note.title:
    displayName: Title
  note.estimate:
    displayName: Size
views:
  - type: cards
    name: Sprint
    groupBy:
      property: note.sprint
      direction: DESC
    order:
      - note.title
      - note.estimate
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Views) != 1 {
		t.Fatalf("read %d views", len(b.Views))
	}
	v := b.Views[0]
	if v.Name != "Sprint" || v.Type != "cards" || v.GroupBy != "note.sprint" || v.Direction != "DESC" {
		t.Errorf("read the view as %+v", v)
	}
	if len(v.Order) != 2 {
		t.Errorf("read %d properties to show", len(v.Order))
	}
	if got := b.Displayed("note.estimate"); got != "Size" {
		t.Errorf("the label is %q", got)
	}
	if got := b.Displayed("note.assignee"); got != "assignee" {
		t.Errorf("a property with no label came out as %q", got)
	}
}
