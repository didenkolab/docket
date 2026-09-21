package server

import (
	"fmt"
	"os"
	"sort"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/task"
	"github.com/didenkolab/docket/internal/vault"
)

// place decides where a card sits in its column and writes that down.
//
// The client says which card the moved one landed after — a key, or the empty
// string for the top of the column. A key rather than an index, because between
// drawing the board and letting go of the card the column may have changed, and
// "after ACME-4" survives that while "third from the top" does not.
//
// It returns the paths of any other tasks it had to rewrite. Normally there are
// none: the numbers are spaced a thousand apart, so a card usually lands in a
// gap. When a gap runs out, the whole column is renumbered, which is the one
// case where dragging one card writes several files.
//
// Runs with the write lock held, from inside an editTask mutation.
func (s *Server) place(c *project.Config, moved *task.Task, status, after string) ([]string, error) {
	column, err := s.column(c, status, moved.Key)
	if err != nil {
		return nil, err
	}

	at := 0
	if after != "" {
		at = -1
		for i, e := range column {
			if e.Key == after {
				at = i + 1
				break
			}
		}
		if at < 0 {
			return nil, fmt.Errorf("%s is not in %s, so there is nowhere to put %s after it",
				after, status, moved.Key)
		}
	}

	var above, below *int
	room := true
	if at > 0 {
		above = column[at-1].Task.Order
		// The card above has no number, and every card without one sorts at
		// the end together. There is no number that lands between them, so the
		// column has to be numbered before this card can sit inside it.
		room = above != nil
	}
	if at < len(column) {
		below = column[at].Task.Order
	}

	if room {
		if order, ok := task.Between(above, below); ok {
			moved.SetOrder(order)
			return nil, nil
		}
	}
	return s.renumber(column, moved, at)
}

// renumber gives every card in a column a fresh number, spaced out again, with
// the moved card at the position it was dropped.
func (s *Server) renumber(column []vault.Entry, moved *task.Task, at int) ([]string, error) {
	order := make([]*task.Task, 0, len(column)+1)
	for i := 0; i <= len(column); i++ {
		if i == at {
			order = append(order, moved)
		}
		if i < len(column) {
			order = append(order, column[i].Task)
		}
	}

	var written []string
	for i, t := range order {
		want := (i + 1) * task.Step
		if t.Order != nil && *t.Order == want {
			continue
		}
		t.SetOrder(want)
		if t == moved {
			continue // its own file is written by editTask
		}
		path, err := s.writeTask(t)
		if err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}

// column is the tasks in one status, in the order the board draws them, without
// the task being moved.
// column reads every task in a status, filtered by nothing.
//
// Ordering is a property of the whole column: the numbers have to stay
// consistent with cards the person dragging cannot see, or renumbering one
// column corrupts somebody else's order in it. Only somebody who may write to
// the project gets here.
func (s *Server) column(c *project.Config, status, without string) ([]vault.Entry, error) {
	entries, err := s.sp().Entries()
	if err != nil {
		return nil, err
	}

	var column []vault.Entry
	for _, e := range entries {
		if e.Task != nil && e.Task.Status == status && e.Key != without {
			column = append(column, e)
		}
	}
	sort.SliceStable(column, func(i, j int) bool {
		a, b := column[i].Task.Order, column[j].Task.Order
		switch {
		case a != nil && b != nil:
			return *a < *b
		case a != nil:
			return true
		default:
			return false
		}
	})
	return column, nil
}

// writeTask saves a task that is not the one editTask is editing, and returns
// its path for the commit. It does not stamp `updated`: being carried along by
// somebody else's reordering is not a change to the task.
func (s *Server) writeTask(t *task.Task) (string, error) {
	rel, full, err := s.locate(t.Key)
	if err != nil {
		return "", err
	}
	if err := t.Sync(); err != nil {
		return "", err
	}
	content, err := t.Bytes()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		return "", err
	}
	return rel, nil
}
