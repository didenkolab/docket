package vault

import (
	"strings"
	"testing"

	"github.com/didenkolab/docket/internal/project"
)

func pipeline() *project.Config {
	return &project.Config{
		Name:     "Пирс",
		Projects: []project.Project{{Key: "PIER", Name: "Пирс"}},
		Statuses: []project.Status{
			{Name: "Discovery", Category: project.CategoryTodo},
			{Name: "В работе", Category: project.CategoryDoing},
			{Name: "QA Stage", Category: project.CategoryDoing},
			{Name: "Готово", Category: project.CategoryDone},
		},
		Types:      []project.Type{{Name: "Задача"}},
		Priorities: []string{"обычный"},
	}
}

// Bases sorts the groups of a board and offers no way to hand it an order, so
// the column order has to be in the value it groups by. Without this an
// eight-stage pipeline came out alphabetically: Business Review first,
// Discovery second.
func TestTheBoardGroupsByStageSoTheColumnsAreInOrder(t *testing.T) {
	board := boardBase(pipeline())

	if !strings.Contains(board, "property: formula.stage") {
		t.Error("the board does not group by the stage formula")
	}
	for i, want := range []string{
		`"1. Discovery"`, `"2. В работе"`, `"3. QA Stage"`, `"4. Готово"`,
	} {
		if !strings.Contains(board, want) {
			t.Errorf("stage %d is not numbered in the formula: want %s", i+1, want)
		}
	}
}

// A board is generated, so a stale one has a right answer rather than two
// things somebody meant — which is what lets check --fix rewrite it.
func TestAGeneratedBoardIsRecognisedByItsFirstLine(t *testing.T) {
	for rel, body := range Generated(pipeline()) {
		if !strings.HasPrefix(body, Marker) {
			t.Errorf("%s does not start with the marker that says docket wrote it", rel)
		}
	}
}
