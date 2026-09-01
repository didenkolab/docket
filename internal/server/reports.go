package server

import (
	"net/http"
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/report"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Reports: what the board cannot say by looking at today.
//
// Four of the Jira marketplace's top hundred sell one report — how long work
// sits in each column — and a dozen more sell dashboards over the same
// changelog. They exist because Jira's history is behind an API and aggregating
// it is somebody's product. Here every move was a commit, so this is a read of
// git log and some arithmetic.

type reportColumn struct {
	Status     string
	Entered    int
	Open       int
	Median     string
	Longest    string
	LongestIn  string
	Waiting    string
	WaitingFor string
}

type reportRow struct {
	Key     string
	Title   string
	Href    string
	Status  string
	Waiting string
	Age     string
}

type timeInStatus struct {
	Columns []reportColumn
	Stuck   []reportRow
	// Tasks is how many the history could be read for, and Moves how many
	// changes it holds — so a report over a vault that was imported in one
	// commit says so by the numbers rather than pretending.
	Tasks int
	Moves int
	// Trouble is why a repository could not be read, when one could not.
	Trouble string
}

// handleTimeInStatus draws how long work spends where.
func (s *Server) handleTimeInStatus(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	entries, err := s.entries(r)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}
	// A path in the space back to the key it belongs to, so a report can name
	// tasks the way everything else does.
	keyOf := map[string]vault.Entry{}
	for _, e := range entries {
		keyOf[e.Path] = e
	}

	view := timeInStatus{}
	lives := map[string]report.Life{}

	for _, v := range s.sp().Vaults() {
		if v.Repo == nil {
			continue
		}
		changes, err := v.Repo.StatusChanges(".")
		if err != nil {
			view.Trouble = "git could not be read for " + v.Prefix + ": " + err.Error()
			continue
		}
		for at, moves := range changes {
			view.Moves += len(moves)
			// Paths come back relative to the repository; everything else in
			// the interface speaks in paths relative to the space.
			changes[v.PathIn(at)] = moves
			if v.Prefix != "" {
				delete(changes, at)
			}
		}
		for at, life := range report.Read(changes, s.now().UTC()) {
			// Only tasks. Every document carries a status too — a decision is
			// `accepted`, a specification is `normative` — and counting those
			// as columns put "accepted" on a report about a board.
			if e, ok := keyOf[at]; ok && e.Task != nil {
				lives[at] = life
			}
		}
	}
	view.Tasks = len(lives)

	named := func(at string) string {
		if e, ok := keyOf[at]; ok {
			return e.Key
		}
		return strings.TrimSuffix(path.Base(at), ".md")
	}

	for _, column := range report.Columns(lives, c.StatusNames(), named) {
		view.Columns = append(view.Columns, reportColumn{
			Status: column.Status, Entered: column.Entered, Open: column.Open,
			Median:  report.Said(column.Median),
			Longest: report.Said(column.Longest), LongestIn: column.LongestIn,
			Waiting: report.Said(column.Waiting), WaitingFor: column.WaitingFor,
		})
	}

	for _, life := range report.Stuck(lives, 20) {
		e, known := keyOf[life.Path]
		if !known || e.Task == nil {
			// A file the history knows and the vault no longer has: deleted,
			// or on a branch. It is not stuck, it is gone.
			continue
		}
		view.Stuck = append(view.Stuck, reportRow{
			Key: e.Key, Title: e.Task.Title, Href: "/task/" + e.Key,
			Status: life.Now, Waiting: report.Said(life.Waiting), Age: report.Said(life.Age),
		})
	}

	s.render(w, r, "time-in-status.html", c, "Time in status", view)
}
