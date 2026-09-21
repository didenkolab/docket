package server

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/task"
	"github.com/didenkolab/docket/internal/vault"
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
	Fields     []fieldRow
	Kinds      []string
	AllTypes   []string
	Statuses   []statusRow
	Types      string
	Priorities string
	Projects   []projectUsage
	Categories []string
	Workflow   bool
	Matrix     []transitionRow
	Error      string
	Saved      string
}

// fieldRow is one of the vault's own properties, on the form.
//
// Editable here because a team adds a field when it turns out it needs one, and
// a tracker where that means editing YAML on somebody's laptop is a tracker
// where it does not happen. What is on the form is what a person can decide;
// the property name is not, because renaming it would leave the value behind on
// every task that carries it — that is a migration, not a setting.
type fieldRow struct {
	Name     string
	Label    string
	Kind     string
	Choices  string
	Types    string
	Required bool
	Help     string
	// Carried is how many tasks hold a value for it, so removing one says what
	// it would leave behind.
	Carried int
	// New is a blank row, which is how a field is added without any script.
	New bool
}

// transitionRow is one line of the workflow matrix: from this status, to which.
type transitionRow struct {
	From string
	To   []transitionCell
}

type transitionCell struct {
	Status  string
	Allowed bool
	Self    bool
}

type projectUsage struct {
	Key   string
	Name  string
	Tasks int
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	s.render(w, r, "settings.html", c, "Settings", s.settingsView(c, "", r.URL.Query().Get("saved")))
}

func (s *Server) settingsView(c *project.Config, message, saved string) settingsView {
	inUse, perProject := s.usage(c)

	view := settingsView{
		Name:       c.Name,
		Types:      strings.Join(c.TypeNames(), ", "),
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
	view.Kinds = project.FieldKinds
	view.AllTypes = c.TypeNames()
	carried := s.fieldUsage(c)
	for _, f := range c.Fields {
		view.Fields = append(view.Fields, fieldRow{
			Name: f.Name, Label: f.Label, Kind: f.Kind,
			Choices: strings.Join(f.Choices, ", "), Types: strings.Join(f.Types, ", "),
			Required: f.Required, Help: f.Help, Carried: carried[f.Name],
		})
	}
	view.Fields = append(view.Fields, fieldRow{New: true}, fieldRow{New: true})

	for _, p := range c.Projects {
		view.Projects = append(view.Projects, projectUsage{p.Key, p.Name, perProject[p.Key]})
	}

	view.Workflow = len(c.Transitions) > 0
	for _, from := range c.Statuses {
		row := transitionRow{From: from.Name}
		for _, to := range c.Statuses {
			row.To = append(row.To, transitionCell{
				Status:  to.Name,
				Allowed: from.Name != to.Name && c.CanMove(from.Name, to.Name),
				Self:    from.Name == to.Name,
			})
		}
		view.Matrix = append(view.Matrix, row)
	}
	return view
}

// usage counts how many tasks hold each status, and how many each project has.
// Both are there to make the consequences of an edit visible before it is made.
// usage counts every task in the space, filtered by nothing.
//
// It answers "would removing this status orphan work", and the answer has to
// cover tasks the person asking cannot see: a status removed because it looked
// unused is a status that takes somebody else's tasks with it. Only an
// administrator of the repository gets this far.
func (s *Server) usage(c *project.Config) (byStatus, byProject map[string]int) {
	byStatus, byProject = map[string]int{}, map[string]int{}

	entries, err := s.sp().Entries()
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
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cannot read the form", err.Error())
		return
	}

	updated := *c
	updated.Name = strings.TrimSpace(r.FormValue("name"))
	// A type's level is not on this form. Editing the vocabulary here must not
	// silently flatten a hierarchy somebody wrote in docket.yaml, so a type that
	// survives keeps the level it had and a new one is standard.
	updated.Types = retype(c, splitCommas(r.FormValue("types")))
	updated.Priorities = splitCommas(r.FormValue("priorities"))
	updated.Fields = readFields(r, c)

	statuses, renames, err := readStatuses(r)
	if err != nil {
		s.rejectSettings(w, r, c, err.Error())
		return
	}
	updated.Statuses = statuses
	updated.Transitions = readTransitions(r, c, statuses, renames)

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
			s.rejectSettings(w, r, c, fmt.Sprintf(
				"%q still holds %d task(s). Rename it instead of removing it, or move those "+
					"tasks first — a status no task can name is work no board can show.",
				was, count))
			return
		}
	}

	author := s.authorFor(r)
	s.writes.Lock()
	defer s.writes.Unlock()

	moved, err := s.applyRenames(r, c, renames, author)
	if err != nil {
		s.rejectSettings(w, r, c, err.Error())
		return
	}
	// The vocabulary belongs to a repository, and in a workspace there is one
	// per project. Editing them together here would write a merged vocabulary
	// into every repository and make each of them wrong about itself.
	home := s.sp().Single()
	if home == nil {
		s.rejectSettings(w, r, c, "This is a workspace, and a vocabulary belongs to a "+
			"repository. Open the project on its own to change its statuses, types and "+
			"priorities.")
		return
	}
	if err := updated.Save(home.Root); err != nil {
		s.rejectSettings(w, r, c, err.Error())
		return
	}
	boards, err := vault.WriteBoards(home.Root, &updated)
	if err != nil {
		s.rejectSettings(w, r, c, err.Error())
		return
	}

	changed := append([]string{project.FileName}, boards...)
	if err := s.commit(r, changed, "Settings: the vault's vocabulary", author); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Saved, but not committed", err.Error())
		return
	}

	saved := "Saved."
	if moved > 0 {
		saved = fmt.Sprintf("Saved, and %d task(s) followed the renamed status.", moved)
	}
	http.Redirect(w, r, "/settings?saved="+saved, http.StatusSeeOther)
}

func (s *Server) rejectSettings(w http.ResponseWriter, r *http.Request, c *project.Config, message string) {
	w.WriteHeader(http.StatusBadRequest)
	s.render(w, r, "settings.html", c, "Settings", s.settingsView(c, message, ""))
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

// readTransitions reads the workflow matrix.
//
// The matrix is keyed on the names the page was rendered with, because a rename
// can happen in the same submission. So it is read in the old names and then
// put through the same rename and removal the statuses went through — one
// mechanism, rather than a second one that has to be kept in step.
func readTransitions(r *http.Request, before *project.Config,
	after []project.Status, renames map[string]project.Status) map[string][]string {

	if r.FormValue("workflow") == "" {
		return nil // no workflow: anything to anything
	}

	asRendered := map[string][]string{}
	for _, from := range before.Statuses {
		asRendered[from.Name] = r.Form["transition_"+from.Name]
	}

	workflow := &project.Config{Transitions: asRendered}
	for was, to := range renames {
		workflow.RenameInTransitions(was, to.Name)
	}
	for _, gone := range removedStatuses(before, after, renames) {
		workflow.DropFromTransitions(gone)
	}

	// A status is always allowed to stay where it is, so listing itself is
	// noise in the file.
	for from, targets := range workflow.Transitions {
		kept := targets[:0]
		for _, to := range targets {
			if to != from {
				kept = append(kept, to)
			}
		}
		workflow.Transitions[from] = kept
	}
	return workflow.Transitions
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
func (s *Server) applyRenames(r *http.Request, c *project.Config, renames map[string]project.Status, author gitvcs.Author) (int, error) {
	if len(renames) == 0 {
		return 0, nil
	}

	entries, err := s.sp().Entries()
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
		full, err := s.abs(e.Path)
		if err != nil {
			return moved, err
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return moved, err
		}
		if err := s.commit(r, []string{e.Path},
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

// retype rebuilds the type list from names, keeping the level of every type
// that was already there.
func retype(c *project.Config, names []string) []project.Type {
	was := map[string]int{}
	for _, t := range c.Types {
		was[t.Name] = t.Level
	}
	types := make([]project.Type, 0, len(names))
	for _, name := range names {
		types = append(types, project.Type{Name: name, Level: was[name]})
	}
	return types
}

// fieldUsage is how many tasks hold a value for each declared field, so the
// form can say what removing one would leave behind.
func (s *Server) fieldUsage(c *project.Config) map[string]int {
	carried := map[string]int{}
	entries, err := s.sp().Entries()
	if err != nil {
		return carried
	}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		for _, f := range c.Fields {
			if strings.TrimSpace(e.Task.Property(f.Name)) != "" {
				carried[f.Name]++
			}
		}
	}
	return carried
}

// readFields reads the declared fields off the form.
//
// A row whose name is cleared is a field removed from the vocabulary. The
// values stay in the task files: deleting a property from a thousand tasks
// because somebody edited a settings page is not something a form should do
// behind them, and rule 15 goes quiet about a field nobody declares.
func readFields(r *http.Request, c *project.Config) []project.Field {
	var out []project.Field
	for i := 0; ; i++ {
		at := strconv.Itoa(i)
		name, present := r.Form["field_name_"+at]
		if !present {
			break
		}
		clean := strings.TrimSpace(strings.Join(name, ""))
		if clean == "" {
			continue
		}

		f := project.Field{
			Name:     clean,
			Label:    strings.TrimSpace(r.FormValue("field_label_" + at)),
			Kind:     strings.TrimSpace(r.FormValue("field_kind_" + at)),
			Help:     strings.TrimSpace(r.FormValue("field_help_" + at)),
			Required: r.FormValue("field_required_"+at) != "",
			Choices:  splitCommas(r.FormValue("field_choices_" + at)),
			Types:    splitCommas(r.FormValue("field_types_" + at)),
		}
		// A label the same as the name says nothing twice.
		if f.Label == f.Name {
			f.Label = ""
		}
		out = append(out, f)
	}
	return out
}
