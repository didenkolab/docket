package report

import (
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/gitvcs"
)

func at(day int) time.Time {
	return time.Date(2026, 3, day, 12, 0, 0, 0, time.UTC)
}

func moved(path, status string, day int) gitvcs.StatusChange {
	return gitvcs.StatusChange{Path: path, Status: status, When: at(day)}
}

// The question a board cannot answer by looking at today: how long did this sit
// where, and how long has it been sitting where it is now.
func TestALifeIsTheStretchesBetweenMoves(t *testing.T) {
	now := at(20)
	lives := Read(map[string][]gitvcs.StatusChange{
		"ACME/ACME-1 a.md": {
			moved("ACME/ACME-1 a.md", "Backlog", 1),
			moved("ACME/ACME-1 a.md", "In progress", 4),
			moved("ACME/ACME-1 a.md", "In review", 5),
		},
	}, now)

	life := lives["ACME/ACME-1 a.md"]
	if len(life.Spans) != 3 {
		t.Fatalf("%d spans", len(life.Spans))
	}
	if got := life.Spans[0].Took(); got != 3*24*time.Hour {
		t.Errorf("the first stretch took %s", got)
	}
	if !life.Spans[2].Open {
		t.Error("the last stretch is not open, and the task has not moved since")
	}
	if life.Now != "In review" || life.Waiting != 15*24*time.Hour {
		t.Errorf("it is in %s for %s", life.Now, life.Waiting)
	}
	if life.Age != 19*24*time.Hour {
		t.Errorf("it is %s old", life.Age)
	}
}

// A median, not a mean: one task somebody abandoned moves a mean and says
// nothing true about the column.
func TestAColumnIsSummarisedByItsMiddle(t *testing.T) {
	now := at(100)
	changes := map[string][]gitvcs.StatusChange{}
	// Three that took a day, and one that took sixty.
	for i, days := range []int{1, 1, 1, 60} {
		path := "ACME/task" + string(rune('a'+i)) + ".md"
		changes[path] = []gitvcs.StatusChange{
			{Path: path, Status: "In review", When: at(1)},
			{Path: path, Status: "Done", When: at(1 + days)},
		}
	}

	columns := Columns(Read(changes, now), []string{"In review", "Done"},
		func(path string) string { return path })

	if len(columns) != 2 || columns[0].Status != "In review" {
		t.Fatalf("columns came out as %+v", columns)
	}
	review := columns[0]
	if review.Entered != 4 {
		t.Errorf("entered %d times", review.Entered)
	}
	if review.Median != 24*time.Hour {
		t.Errorf("the middle is %s — a mean would have said about 16 days", review.Median)
	}
	if review.Longest != 60*24*time.Hour {
		t.Errorf("the longest is %s", review.Longest)
	}
	// Everything ended in Done and is still there.
	if columns[1].Open != 4 {
		t.Errorf("%d are open in Done", columns[1].Open)
	}
}

// The list a standing meeting is actually about.
func TestStuckIsWhatHasWaitedLongest(t *testing.T) {
	now := at(30)
	changes := map[string][]gitvcs.StatusChange{
		"a.md": {{Path: "a.md", Status: "In review", When: at(1)}},
		"b.md": {{Path: "b.md", Status: "In review", When: at(29)}},
		"c.md": {{Path: "c.md", Status: "Backlog", When: at(15)}},
	}
	stuck := Stuck(Read(changes, now), 2)
	if len(stuck) != 2 {
		t.Fatalf("%d listed", len(stuck))
	}
	if stuck[0].Path != "a.md" || stuck[1].Path != "c.md" {
		t.Errorf("worst first came out as %s, %s", stuck[0].Path, stuck[1].Path)
	}
}

// A duration as somebody would say it out loud.
func TestADurationIsSaidTheWayPeopleSayIt(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "—"},
		{30 * time.Minute, "30 minutes"},
		{time.Hour, "1 hour"},
		{25 * time.Hour, "25 hours"},
		{72 * time.Hour, "3 days"},
		{90 * 24 * time.Hour, "3 months"},
	} {
		if got := Said(c.d); got != c.want {
			t.Errorf("%s said as %q, want %q", c.d, got, c.want)
		}
	}
}
