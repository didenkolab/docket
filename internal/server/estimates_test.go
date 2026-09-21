package server

import (
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/project"
)

// A column head is where "how much is in progress" is asked, and counting cards
// answers it wrongly: three cards of thirteen is not three cards of one.
//
// The total adds up what each card says about itself. A container carries no
// estimate — rule 12 refuses one — so it contributes nothing and nothing is
// counted twice when a container and its child are in the same column.
func TestColumnTotals(t *testing.T) {
	sizes := func(values ...string) []card {
		out := make([]card, 0, len(values))
		for _, v := range values {
			out = append(out, card{Size: v})
		}
		return out
	}
	points := &project.Config{Estimates: &project.Estimates{Unit: "points"}}
	unsized := &project.Config{}

	for _, c := range []struct {
		what        string
		cards       []card
		config      *project.Config
		total       string
		unaccounted int
	}{
		{"a sized column", sizes("3", "5", "1"), points, "9", 0},
		{"a container among them", sizes("3", "", "5"), points, "8", 1},
		{"nothing sized", sizes("", ""), points, "", 2},
		{"halves, for a vault counting days", sizes("0.5", "1.5"), points, "2", 0},
		{"a vault that does not size work", sizes("3", "5"), unsized, "", 0},
		{"an empty column", nil, points, "", 0},
	} {
		total, unaccounted := totalOf(c.cards, c.config)
		if total != c.total || unaccounted != c.unaccounted {
			t.Errorf("%s: got %q/%d, want %q/%d",
				c.what, total, unaccounted, c.total, c.unaccounted)
		}
	}
}

// A sprint says itself in a line, and the line has to be honest about what it
// does not know: a total shown without the number of unsized tasks reads as the
// whole of the work.
func TestSprintLine(t *testing.T) {
	points := &project.Config{Estimates: &project.Estimates{Unit: "points"}}

	for _, c := range []struct {
		what  string
		tasks []sprintTask
		want  string
	}{
		{
			what: "a finished sprint",
			tasks: []sprintTask{
				{Size: "3", Category: project.CategoryDone},
				{Size: "5", Category: project.CategoryDone},
			},
			want: "2 tasks · 2 closed · 8 of 8 points",
		},
		{
			what: "one in flight, with something nobody sized",
			tasks: []sprintTask{
				{Size: "3", Category: project.CategoryDone},
				{Size: "5", Category: project.CategoryDoing},
				{Size: "", Category: project.CategoryTodo},
			},
			want: "3 tasks · 1 closed · 3 of 8 points · 1 unsized",
		},
		{
			what:  "a sprint nobody has sized at all",
			tasks: []sprintTask{{Category: project.CategoryTodo}},
			want:  "1 task · 0 closed · 1 unsized",
		},
		{what: "an empty sprint", tasks: nil, want: ""},
	} {
		got := strings.Join(describeSprintWork(c.tasks, points), " · ")
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.what, got, c.want)
		}
	}
}

// The page around a sprint already says its title, so the body's own heading
// would be the same words twice. Anything else in the body is left alone.
func TestWithoutItsOwnTitle(t *testing.T) {
	for _, c := range []struct{ what, body, title, want string }{
		{"its own heading", "# Sprint 13\n\nClose refunds.\n", "Sprint 13", "Close refunds.\n"},
		{"a leading blank line first", "\n# Sprint 13\nGoal.\n", "Sprint 13", "Goal.\n"},
		{"case differs", "# sprint 13\nGoal.\n", "Sprint 13", "Goal.\n"},
		{"a different heading", "# The plan\nGoal.\n", "Sprint 13", "# The plan\nGoal.\n"},
		{"a deeper heading", "## Sprint 13\nGoal.\n", "Sprint 13", "## Sprint 13\nGoal.\n"},
		{"no heading", "Goal.\n", "Sprint 13", "Goal.\n"},
		{"nothing at all", "", "Sprint 13", ""},
		{"a heading and nothing else", "# Sprint 13", "Sprint 13", ""},
	} {
		if got := withoutItsOwnTitle(c.body, c.title); got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, got, c.want)
		}
	}
}
