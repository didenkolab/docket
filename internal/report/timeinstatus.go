// Package report computes what a board cannot show by looking at today.
//
// A board is the present tense. How long work sits in each column, where it
// stalls, what is waiting longest — those are questions about the past, and in
// Jira they are questions you buy an app to answer, because the changelog is
// behind an API and aggregating it is somebody's product.
//
// Here the past is the repository. Every move was a commit, so the answer is a
// read of `git log` and some arithmetic, and it is part of the tool rather than
// a purchase.
package report

import (
	"sort"
	"strconv"
	"time"

	"github.com/didenkolab/docket/internal/gitvcs"
)

// Span is a stretch of time a task spent in one status.
type Span struct {
	Status string
	From   time.Time
	To     time.Time
	// Open says the task is still in this status, so To is now rather than a
	// move that happened.
	Open bool
}

// Took is how long it lasted.
func (s Span) Took() time.Duration { return s.To.Sub(s.From) }

// Life is one task's passage through the workflow.
type Life struct {
	Path  string
	Spans []Span
	// Now is the status it is in, and Waiting is how long it has been there.
	Now     string
	Waiting time.Duration
	// Since is when it first appeared, and Age is how long ago that was.
	Since time.Time
	Age   time.Duration
}

// Column is what happened in one status, across every task.
type Column struct {
	Status string
	// Entered is how many times a task arrived here — not how many tasks, since
	// work comes back.
	Entered int
	// Median is the middle of the finished stretches: the number to quote,
	// because one task abandoned for a year moves an average and not a median.
	Median time.Duration
	// Longest is the longest finished stretch, and Waiting is the longest one
	// still running.
	Longest    time.Duration
	LongestIn  string
	Waiting    time.Duration
	WaitingFor string
	// Open is how many are sitting here now.
	Open int
}

// Read turns the history into a life per task.
//
// A task's last status runs to now: it is still there, and how long it has been
// there is usually the question being asked.
func Read(changes map[string][]gitvcs.StatusChange, now time.Time) map[string]Life {
	out := make(map[string]Life, len(changes))
	for path, moves := range changes {
		if len(moves) == 0 {
			continue
		}
		sort.SliceStable(moves, func(a, b int) bool { return moves[a].When.Before(moves[b].When) })

		life := Life{Path: path, Since: moves[0].When}
		for i, move := range moves {
			span := Span{Status: move.Status, From: move.When}
			if i+1 < len(moves) {
				span.To = moves[i+1].When
			} else {
				span.To, span.Open = now, true
			}
			life.Spans = append(life.Spans, span)
		}
		last := life.Spans[len(life.Spans)-1]
		life.Now, life.Waiting = last.Status, last.Took()
		life.Age = now.Sub(life.Since)
		out[path] = life
	}
	return out
}

// Columns aggregates the lives by status, in the order given — which is the
// vault's workflow, so a report reads the way the board does.
//
// A status the vault no longer has still appears if work went through it:
// deleting a column does not unhappen the fortnight something spent in it.
func Columns(lives map[string]Life, order []string, name func(path string) string) []Column {
	type gathered struct {
		finished []time.Duration
		open     int
		waiting  time.Duration
		waitFor  string
		longest  time.Duration
		longIn   string
		entered  int
	}
	by := map[string]*gathered{}
	at := func(status string) *gathered {
		if known, ok := by[status]; ok {
			return known
		}
		by[status] = &gathered{}
		return by[status]
	}

	for path, life := range lives {
		for _, span := range life.Spans {
			g := at(span.Status)
			g.entered++
			if span.Open {
				g.open++
				if span.Took() > g.waiting {
					g.waiting, g.waitFor = span.Took(), name(path)
				}
				continue
			}
			g.finished = append(g.finished, span.Took())
			if span.Took() > g.longest {
				g.longest, g.longIn = span.Took(), name(path)
			}
		}
	}

	seen := map[string]bool{}
	var out []Column
	add := func(status string) {
		g, ok := by[status]
		if !ok || seen[status] {
			return
		}
		seen[status] = true
		out = append(out, Column{
			Status: status, Entered: g.entered, Median: median(g.finished),
			Longest: g.longest, LongestIn: g.longIn,
			Waiting: g.waiting, WaitingFor: g.waitFor, Open: g.open,
		})
	}
	for _, status := range order {
		add(status)
	}
	// Whatever the workflow no longer names, after the ones it does.
	var rest []string
	for status := range by {
		if !seen[status] {
			rest = append(rest, status)
		}
	}
	sort.Strings(rest)
	for _, status := range rest {
		add(status)
	}
	return out
}

// median is the middle value, or zero for nothing.
//
// The middle rather than the mean, because one task somebody abandoned for a
// year moves a mean and says nothing true about the column.
func median(values []time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	middle := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[middle]
	}
	return (sorted[middle-1] + sorted[middle]) / 2
}

// Stuck is the work waiting longest where it is now, worst first.
//
// The one list a standing meeting is actually about, and the reason a median
// per column is not enough: the column is fine and one card in it is three
// weeks old.
func Stuck(lives map[string]Life, limit int) []Life {
	var out []Life
	for _, life := range lives {
		out = append(out, life)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Waiting != out[b].Waiting {
			return out[a].Waiting > out[b].Waiting
		}
		return out[a].Path < out[b].Path
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Said is a duration as a person would say it.
//
// Rounded to what the number is worth: nobody cares that something sat in
// review for three days and four hours, and "3 days" is the sentence they would
// repeat.
func Said(d time.Duration) string {
	switch {
	case d <= 0:
		return "—"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 60*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	default:
		return plural(int(d.Hours()/24/30), "month")
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
