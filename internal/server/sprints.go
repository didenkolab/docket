package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/space"
	"github.com/didenkolab/docket/internal/vault"
)

// What is in the sprint.
//
// The page is shaped like the releases page for the same reason: the running
// sprint is what is being asked about, and the ones before it are context that
// should not be in the way. A finished sprint says itself in a line and waits.
//
// Two things this page has to be careful about, both found by putting real data
// in a vault and looking at it.
//
// A total that adds up every task in a sprint double-counts as soon as a
// container and one of its children are both in it — the container's size is
// its children, so the child would be counted twice. So a total adds up what
// each task says about itself and nothing else: a container carries no estimate
// (rule 12 refuses one), and therefore contributes nothing. That is exact and
// it is never a guess. The cost is that a sprint holding a container whose
// children are elsewhere totals less than the work in it, so the page says how
// many of its tasks nobody has sized — a total is never shown as though it were
// the whole.
//
// And "how much is done" is reported by status category rather than by a word,
// because a vault whose closing statuses are Готово and Отменено has decided
// that both are closed, and it is not this page's business to disagree.

type sprintsView struct {
	// Running is the sprint on today, spelled out. Nil when none is.
	Running *sprintView
	// Gap says there is no sprint on today, which is a real state — between
	// two of them, or before the first.
	Gap bool
	// Others are the rest, newest first.
	Others []sprintView
	// Loose is how many tasks are in no sprint at all. Not a fault: work that
	// is not committed to a fortnight is most of a backlog.
	Loose int
	// Unit is what estimates are counted in, empty when the vault does not
	// size work.
	Unit string
	None bool
}

type sprintView struct {
	Note  string
	Title string
	Href  string
	// Path is the page in the vault, so the prose can be opened.
	PagePath string
	Starts   string
	Ends     string
	Days     int
	// When is the span said in one phrase, for a summary line.
	When string
	// Says is the sprint in a line: how much work, how much closed, how much
	// nobody sized.
	Says []string
	// State is "running", "over" or "ahead" — computed from the dates, because
	// a sprint has no state of its own to fall out of step.
	State string
	// Left is how many days the running sprint has, counting today.
	Left int
	// Tasks are what is in it, in board order.
	Tasks []sprintTask
	// Repo is which repository it came from, on a board reading several.
	Repo string
	// Trouble is why the dates could not be read.
	Trouble string
	// Goal is the page's own prose, rendered — the part Jira has no room for.
	Goal template.HTML
}

type sprintTask struct {
	Key      string
	Title    string
	Status   string
	Category string
	// Size is the estimate as written, or empty when nobody has said.
	Size string
	// Carried says this task was in an earlier sprint too.
	//
	// Read out of git rather than out of a field: a task carries one sprint,
	// and where it has been is what the history is for. See docs/design/
	// git-as-the-database.md.
	Carried bool
}

func (s *Server) handleSprints(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := s.sp().Entries()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	sprints := s.sp().Sprints()
	today := s.now().UTC()
	st := standingIn(r)
	entries = visible(st, entries)

	view := sprintsView{Unit: c.Unit(), None: len(sprints) == 0}
	several := len(s.sp().Vaults()) > 1

	for _, e := range entries {
		if e.Task != nil && e.Task.Sprint == "" {
			view.Loose++
		}
	}

	for _, sp := range sprints {
		row := s.describeSprint(sp, entries, c, today)
		if several {
			row.Repo = s.nameOf(sp.Vault)
		}
		if row.State == "running" && view.Running == nil {
			shown := row
			view.Running = &shown
			continue
		}
		view.Others = append(view.Others, row)
	}
	view.Gap = view.Running == nil && !view.None

	s.render(w, r, "sprints.html", c, "Sprints", view)
}

// describeSprint is one sprint and what is in it.
func (s *Server) describeSprint(sp space.SprintIn, entries []vault.Entry,
	c *project.Config, today time.Time) sprintView {

	row := sprintView{
		Note: sp.Note, Title: sp.Title, Trouble: sp.Trouble,
		Href:     "/sprint/" + url.PathEscape(sp.Note),
		PagePath: sp.Vault.PathIn(sp.Path),
		Days:     sp.Days(),
	}
	if !sp.Starts.IsZero() {
		row.Starts = sp.Starts.Format(vault.DateFormat)
	}
	if !sp.Ends.IsZero() {
		row.Ends = sp.Ends.Format(vault.DateFormat)
	}
	row.When = span(row.Starts, row.Ends)

	switch {
	case sp.On(today):
		row.State = "running"
		row.Left = int(sp.Ends.Sub(day(today)).Hours()/24) + 1
	case sp.Over(today):
		row.State = "over"
	case sp.Ahead(today):
		row.State = "ahead"
	}

	for _, e := range entries {
		if e.Task == nil || !strings.EqualFold(e.Task.Sprint, sp.Note) {
			continue
		}
		t := sprintTask{
			Key: e.Key, Title: e.Task.Title,
			Status: e.Task.Status, Category: e.Task.StatusCategory,
		}
		if t.Size = ""; e.Task.Sized() {
			t.Size = project.Amount(e.Task.Size())
		}
		row.Tasks = append(row.Tasks, t)
	}
	row.Says = describeSprintWork(row.Tasks, c)
	return row
}

// describeSprintWork is the sprint in a line.
//
// The total adds up what each task says about itself. A container carries no
// estimate, so it contributes nothing and nothing is counted twice — see the
// note at the top of this file. How many are unsized is said in the same breath
// as the total, because a total on its own reads as the whole.
func describeSprintWork(tasks []sprintTask, c *project.Config) []string {
	if len(tasks) == 0 {
		return nil
	}

	closed, unsized := 0, 0
	var total, done float64
	for _, t := range tasks {
		if t.Size == "" {
			unsized++
		} else {
			size := amount(t.Size)
			total += size
			if t.Category == project.CategoryDone {
				done += size
			}
		}
		if t.Category == project.CategoryDone {
			closed++
		}
	}

	says := []string{plural(len(tasks), "task", "tasks")}
	says = append(says, fmt.Sprintf("%d closed", closed))
	if c.Sizes() && total > 0 {
		says = append(says, fmt.Sprintf("%s of %s %s",
			project.Amount(done), project.Amount(total), c.Unit()))
	}
	if unsized > 0 {
		says = append(says, fmt.Sprintf("%d unsized", unsized))
	}
	return says
}

// span is two days said as one phrase.
func span(from, to string) string {
	switch {
	case from == "" && to == "":
		return ""
	case to == "":
		return "from " + from
	case from == "":
		return "to " + to
	}
	return from + " to " + to
}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// amount reads back a written estimate. It was written by project.Amount, so it
// parses; a value that somehow does not contributes nothing rather than
// stopping the page.
func amount(s string) float64 {
	var v float64
	if _, err := fmt.Sscanf(s, "%g", &v); err != nil {
		return 0
	}
	return v
}

// handleSprint is one sprint spelled out: its prose, what is in it, and which
// of it was carried in from an earlier one.
func (s *Server) handleSprint(w http.ResponseWriter, r *http.Request) {
	note := strings.TrimSpace(r.PathValue("note"))
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := s.sp().Entries()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	var found *space.SprintIn
	for _, sp := range s.sp().Sprints() {
		if strings.EqualFold(sp.Note, note) {
			one := sp
			found = &one
			break
		}
	}
	if found == nil {
		s.fail(w, r, http.StatusNotFound, "No such sprint",
			note+" is not a sprint page in this vault.")
		return
	}

	row := s.describeSprint(*found, visible(standingIn(r), entries), c, s.now().UTC())
	if ix, err := s.index(); err == nil {
		row.Goal = renderMarkdown(withoutItsOwnTitle(found.Body, row.Title), ix)
	}
	s.carriedIn(&row, entries)

	title := row.Title
	if row.When != "" {
		title += ", " + row.When
	}
	s.render(w, r, "sprint.html", c, title, sprintsView{Running: &row, Unit: c.Unit()})
}

// carriedIn marks the tasks that were in an earlier sprint before this one.
//
// One `git log -p` per task in the sprint, which is a handful. Done here and
// not on the list page: the list is a list, and this is the page where "what
// did we drag in from last time" is the question.
func (s *Server) carriedIn(row *sprintView, entries []vault.Entry) {
	byKey := map[string]vault.Entry{}
	for _, e := range entries {
		byKey[e.Key] = e
	}

	for i := range row.Tasks {
		e, ok := byKey[row.Tasks[i].Key]
		if !ok {
			continue
		}
		owner, inVault, _, err := s.sp().Locate(e.Key)
		if err != nil || owner.Repo == nil {
			continue
		}
		values, err := owner.Repo.PropertyHistory(inVault, "sprint")
		if err != nil {
			continue
		}
		for _, was := range values {
			if !strings.Contains(strings.ToLower(was), strings.ToLower(row.Note)) {
				row.Tasks[i].Carried = true
				break
			}
		}
	}
}

// withoutItsOwnTitle drops a leading `# Title` from a page's body when the page
// around it already says the title.
//
// A vault page carries its own heading, and the wiki shows the body alone — so
// nothing is repeated there. This page has metadata that has to come before the
// prose, so it needs a heading of its own, and the body's would then be the
// same words twice.
func withoutItsOwnTitle(body, title string) string {
	trimmed := strings.TrimLeft(body, "\n")
	line, rest, found := strings.Cut(trimmed, "\n")
	if !found {
		line, rest = trimmed, ""
	}

	heading := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	if !strings.HasPrefix(line, "# ") || !strings.EqualFold(heading, strings.TrimSpace(title)) {
		return body
	}
	return strings.TrimLeft(rest, "\n")
}
