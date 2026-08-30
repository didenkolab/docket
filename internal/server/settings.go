package server

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// statusRow is one editable line of the status table. `Was` carries the name
// the row was rendered with, which is what turns an edited name into a rename
// rather than a delete and an add — and a rename can take its tasks with it.
type statusRow struct {
	Was      string
	Name     string
	Category string
	Position int
	InUse    int
}

type settingsView struct {
	Name       string
	Statuses   []statusRow
	Types      string
	Priorities string
	Projects   []projectUsage
	Categories []string
	Error      string
	Saved      string
}

type projectUsage struct {
	Key   string
	Name  string
	Tasks int
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	s.render(w, "settings.html", c, "Settings", s.settingsView(c, "", r.URL.Query().Get("saved")))
}

func (s *Server) settingsView(c *project.Config, message, saved string) settingsView {
	inUse, perProject := s.usage(c)

	view := settingsView{
		Name:       c.Name,
		Types:      strings.Join(c.Types, ", "),
		Priorities: strings.Join(c.Priorities, ", "),
		Categories: []string{project.CategoryTodo, project.CategoryDoing, project.CategoryDone},
		Error:      message,
		Saved:      saved,
	}
	for i, status := range c.Statuses {
		view.Statuses = append(view.Statuses, statusRow{
			Was: status.Name, Name: status.Name, Category: status.Category,
			Position: i + 1, InUse: inUse[status.Name],
		})
	}
	// Blank rows, so a status can be added without any JavaScript.
	for i := range 2 {
		view.Statuses = append(view.Statuses, statusRow{Position: len(c.Statuses) + i + 1})
	}
	for _, p := range c.Projects {
		view.Projects = append(view.Projects, projectUsage{p.Key, p.Name, perProject[p.Key]})
	}
	return view
}

// usage counts how many tasks hold each status, and how many each project has.
// Both are there to make the consequences of an edit visible before it is made.
func (s *Server) usage(c *project.Config) (byStatus, byProject map[string]int) {
	byStatus, byProject = map[string]int{}, map[string]int{}

	entries, err := vault.List(s.root, c)
	if err != nil {
		return byStatus, byProject
	}
	for _, e := range entries {
		byProject[e.Project]++
		if e.Task != nil {
			byStatus[e.Task.Status]++
		}
	}
	return byStatus, byProject
}

// handleSaveSettings rewrites docket.yaml, moves the tasks that sit on a renamed
// status, regenerates the boards and commits the lot.
func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, http.StatusBadRequest, "Cannot read the form", err.Error())
		return
	}

	updated := *c
	updated.Name = strings.TrimSpace(r.FormValue("name"))
	updated.Types = splitCommas(r.FormValue("types"))
	updated.Priorities = splitCommas(r.FormValue("priorities"))

	statuses, renames, err := readStatuses(r)
	if err != nil {
		s.rejectSettings(w, c, err.Error())
		return
	}
	updated.Statuses = statuses

	// A project's display name is safe to change here. Its key is its folder
	// and cannot move without moving every task in it, so it is not editable.
	updated.Projects = append([]project.Project(nil), c.Projects...)
	for i, p := range updated.Projects {
		if name := strings.TrimSpace(r.FormValue("project_name_" + p.Key)); name != "" {
			updated.Projects[i].Name = name
		}
	}

	inUse, _ := s.usage(c)
	for _, was := range removedStatuses(c, statuses, renames) {
		if count := inUse[was]; count > 0 {
			s.rejectSettings(w, c, fmt.Sprintf(
				"%q still holds %d task(s). Rename it instead of removing it, or move those "+
					"tasks first — a status no task can name is work no board can show.",
				was, count))
			return
		}
	}

	author := s.authorFor(r)
	s.writes.Lock()
	defer s.writes.Unlock()

	moved, err := s.applyRenames(c, renames, author)
	if err != nil {
		s.rejectSettings(w, c, err.Error())
		return
	}
	if err := updated.Save(s.root); err != nil {
		s.rejectSettings(w, c, err.Error())
		return
	}
	boards, err := vault.WriteBoards(s.root, &updated)
	if err != nil {
		s.rejectSettings(w, c, err.Error())
		return
	}

	changed := append([]string{project.FileName}, boards...)
	if err := s.repo.Commit(changed, "Settings: the vault's vocabulary", author); err != nil {
		s.fail(w, http.StatusInternalServerError, "Saved, but not committed", err.Error())
		return
	}

	saved := "Saved."
	if moved > 0 {
		saved = fmt.Sprintf("Saved, and %d task(s) followed the renamed status.", moved)
	}
	http.Redirect(w, r, "/settings?saved="+saved, http.StatusSeeOther)
}

func (s *Server) rejectSettings(w http.ResponseWriter, c *project.Config, message string) {
	w.WriteHeader(http.StatusBadRequest)
	s.render(w, "settings.html", c, "Settings", s.settingsView(c, message, ""))
}

// readStatuses reads the status table, ordered by its position column, and
// reports which names changed.
func readStatuses(r *http.Request) (statuses []project.Status, renames map[string]project.Status, err error) {
	was := r.Form["status_was"]
	names := r.Form["status_name"]
	categories := r.Form["status_category"]
	positions := r.Form["status_position"]

	if len(names) != len(categories) || len(names) != len(was) {
		return nil, nil, fmt.Errorf("the status table came back malformed")
	}

	type row struct {
		status   project.Status
		was      string
		position int
	}
	var rows []row

	for i, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue // a cleared row removes the status
		}
		position := i
		if i < len(positions) {
			if n, convErr := strconv.Atoi(strings.TrimSpace(positions[i])); convErr == nil {
				position = n
			}
		}
		rows = append(rows, row{
			status:   project.Status{Name: name, Category: strings.TrimSpace(categories[i])},
			was:      strings.TrimSpace(was[i]),
			position: position,
		})
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].position < rows[j].position })

	renames = map[string]project.Status{}
	for _, row := range rows {
		statuses = append(statuses, row.status)
		if row.was != "" && row.was != row.status.Name {
			renames[row.was] = row.status
		}
	}
	return statuses, renames, nil
}

// removedStatuses lists names the vault used to have and no longer does, not
// counting the ones that were renamed.
func removedStatuses(before *project.Config, after []project.Status, renames map[string]project.Status) []string {
	kept := map[string]bool{}
	for _, s := range after {
		kept[s.Name] = true
	}

	var gone []string
	for _, s := range before.Statuses {
		if kept[s.Name] {
			continue
		}
		if _, renamed := renames[s.Name]; renamed {
			continue
		}
		gone = append(gone, s.Name)
	}
	return gone
}

// applyRenames moves every task off a renamed status onto its new name, so that
// a rename does not leave the vault failing its own validation.
func (s *Server) applyRenames(c *project.Config, renames map[string]project.Status, author gitvcs.Author) (int, error) {
	if len(renames) == 0 {
		return 0, nil
	}

	entries, err := vault.List(s.root, c)
	if err != nil {
		return 0, err
	}

	moved := 0
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		to, renamed := renames[e.Task.Status]
		if !renamed {
			continue
		}

		t, err := task.Parse(e.Raw)
		if err != nil {
			return moved, err
		}
		from := t.Status
		t.SetStatus(to.Name, to.Category)
		t.Touch(s.now())

		content, err := t.Bytes()
		if err != nil {
			return moved, err
		}
		if err := os.WriteFile(s.taskPath(e.Key), content, 0o644); err != nil {
			return moved, err
		}
		if err := s.repo.Commit([]string{e.Path},
			fmt.Sprintf("%s: %s → %s", e.Key, from, to.Name), author); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

func splitCommas(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
