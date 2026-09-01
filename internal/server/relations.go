package server

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// relationGroup is one kind of connection and what it points at, ready to
// render. The order follows task.Relations, so a task page reads the same way
// every time rather than in whatever order the frontmatter happens to be in.
type relationGroup struct {
	Says string
	To   []relatedTask
}

type relatedTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	// Missing is a key nothing in the space has. Reported rather than hidden:
	// a relation pointing at nothing is worth seeing on the page, not only in
	// `docket check`.
	Missing bool
}

// relationsOf reads what a task says it is connected to, and looks each one up
// so the page can show what state that work is in.
//
// Only what the task itself says. The other end of a relation is found by the
// backlinks, which already show everything pointing here — so a task blocked by
// another shows it under "is blocked by" if it says so, and under "Referenced
// by" either way.
func (s *Server) relationsOf(r *http.Request, t *task.Task) []relationGroup {
	relations := project.DefaultRelations()
	if c, err := s.config(); err == nil {
		relations = c.Relations()
	}

	entries, err := s.entries(r)
	if err != nil {
		return nil
	}
	known := map[string]vault.Entry{}
	for _, e := range entries {
		if e.Task != nil {
			known[e.Key] = e
		}
	}

	var groups []relationGroup
	for _, r := range relations {
		keys := t.Related(r.Name)
		if len(keys) == 0 {
			continue
		}
		group := relationGroup{Says: r.Says}
		for _, key := range keys {
			e, ok := known[key]
			if !ok {
				group.To = append(group.To, relatedTask{Key: key, Missing: true})
				continue
			}
			group.To = append(group.To, relatedTask{
				Key: key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
			})
		}
		groups = append(groups, group)
	}
	return groups
}

// blocked says whether anything this task waits on is unfinished.
//
// It is the one relation that changes what somebody does next, so a board says
// it on the card rather than making people open the task to find out. Blocked
// by something already done is not blocked.
func blocked(t *task.Task, known map[string]vault.Entry) bool {
	for _, key := range t.Related("blocked_by") {
		e, ok := known[key]
		if !ok {
			// A task waiting on something that does not exist is stuck in a
			// way worth showing, not a reason to say it is free to start.
			return true
		}
		if e.Task != nil && e.Task.StatusCategory != "done" {
			return true
		}
	}
	return false
}

// notesFor turns task keys into the note names a link resolves by.
//
// A relation is written by key — that is what a person or an agent knows — and
// stored as a link, which resolves by note name. Refusing a key nothing has is
// better than writing a link to nothing and reporting it later.
func (s *Server) notesFor(keys []string) ([]string, error) {
	notes := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		_, inVault, _, err := s.sp().Locate(key)
		if err != nil {
			return nil, fmt.Errorf("%s is not in this space", key)
		}
		notes = append(notes, strings.TrimSuffix(path.Base(inVault), ".md"))
	}
	return notes, nil
}

// relationNames is the list to offer when somebody names one that does not
// exist.
func relationNames(relations []project.Relation) string {
	var names []string
	for _, r := range relations {
		names = append(names, r.Name)
	}
	return "one of " + strings.Join(names, ", ")
}
