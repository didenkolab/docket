package server

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/task"
	"github.com/didenkolab/docket/internal/vault"
)

type editView struct {
	Task      *task.Task
	Version   string
	Path      string
	Body      string
	Labels    string
	Tags      string
	Parents   []parentChoice
	Error     string
	Reachable []project.Status
	// Sizes says the vault sizes work, so the form offers an estimate.
	Sizes bool
	// Unit is the word for one, so the field can be labelled in the vault's
	// own terms rather than as "estimate".
	Unit string
	// Scale is the values the vault offers, which turns a free number into a
	// list of choices. Empty when the vault declared no scale.
	Scale []sizeChoice
	// Size is the estimate as written, for the free-number case.
	Size string
	// Sprints are the sprints this task could be put in, newest first, and
	// which one it is in now.
	Sprints []sprintChoice
	// HasChildren says a container's size is its children's, so the form says
	// so instead of offering a field rule 12 would refuse.
	HasChildren bool
	// Rollup is what its children add up to, for a container.
	Rollup string
	// Fields are the vault's own properties for this type, as controls.
	Fields []fieldControl
	// People are the handles already at work in this vault, offered beside the
	// assignee box.
	//
	// It was a bare text input: to move a task you had to know somebody's handle
	// by heart and type it exactly, and a typo made a person who does not
	// exist. On a project with a thousand tasks and thirteen people that is not
	// a field anybody uses twice.
	//
	// A list rather than a closed set, because the handles are whatever the
	// vault says — an import maps them, an agent has one, and somebody arriving
	// tomorrow is not in it yet. Whoever has rights on the repository is in it
	// from the moment they are given them, which is what somebody means by
	// "put it on them".
	People []candidate
}

// fieldControl is one declared property, ready to edit. The kind decides the
// control: a choice is a list, a flag is a checkbox, a date is a date picker.
// A text box for all of them would be a form that lets you write nonsense and
// then reports it.
type fieldControl struct {
	Name     string
	Label    string
	Kind     string
	Help     string
	Value    string
	Required bool
	Choices  []choiceOption
	// Type is the HTML input type for the kinds that are a plain box.
	Type string
	// On is a flag's state.
	On bool
}

type choiceOption struct {
	Value    string
	Selected bool
}

type parentChoice struct {
	Key      string
	Title    string
	Selected bool
}

type sizeChoice struct {
	Value    string
	Selected bool
}

type sprintChoice struct {
	Note     string
	Title    string
	When     string
	Running  bool
	Selected bool
}

// handleEditForm shows a task with every field editable.
//
// A tracker where a task cannot be edited is not a tracker. This is a plain
// form rather than an inline editor for the same reason the rest of the
// interface is: it has to work with JavaScript switched off.
func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	t, version, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	rel, _, err := s.locate(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}

	s.render(w, r, "edit.html", c, "Edit "+t.Key, s.editView(r, c, t, version, rel, ""))
}

func (s *Server) editView(r *http.Request, c *project.Config, t *task.Task, version, rel, message string) editView {
	view := editView{
		Task:      t,
		Version:   version,
		Path:      rel,
		Body:      t.Description(),
		Labels:    strings.Join(t.Labels, ", "),
		Tags:      strings.Join(t.Tags, ", "),
		Error:     message,
		Reachable: c.Reachable(t.Status),
	}

	// Any task but this one, and not one of its own descendants — a task
	// cannot be its own ancestor, and offering the choice invites the cycle.
	entries, _ := s.entries(r)
	descendants := descendantsOf(entries, t.Key)
	var rollup float64
	sized := false
	for _, e := range entries {
		if e.Task == nil || e.Key == t.Key || descendants[e.Key] {
			continue
		}
		view.Parents = append(view.Parents, parentChoice{
			Key: e.Key, Title: e.Task.Title, Selected: e.Key == t.Parent,
		})
	}
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == t.Key {
			view.HasChildren = true
			if e.Task.Sized() {
				rollup += e.Task.Size()
				sized = true
			}
		}
	}
	if sized {
		view.Rollup = project.Amount(rollup)
	}

	// An estimate, when the vault has said what one is. A container is not
	// offered the field: its size is what its children add up to, and rule 12
	// refuses a second answer.
	view.Sizes = c.Sizes() && !view.HasChildren
	view.Unit = c.Unit()
	if t.Sized() {
		view.Size = project.Amount(t.Size())
	}
	for _, v := range c.EstimateScale() {
		value := project.Amount(v)
		view.Scale = append(view.Scale, sizeChoice{Value: value, Selected: value == view.Size})
	}

	view.People = s.candidates(r)

	for _, f := range c.FieldsFor(t.Type) {
		control := fieldControl{
			Name: f.Name, Label: f.Shown(), Kind: f.Kind, Help: f.Help,
			Required: f.Required, Value: strings.TrimSpace(t.Property(f.Name)),
		}
		switch f.Kind {
		case project.FieldNumber:
			control.Type = "number"
		case project.FieldDate:
			control.Type = "date"
		case project.FieldMoment:
			control.Type = "text"
		case project.FieldLink:
			control.Type = "url"
		case project.FieldFlag:
			control.On = strings.EqualFold(control.Value, "true")
		case project.FieldChoice:
			// An empty first option, unless the field is required: a choice
			// somebody has not made is a real state, and a list that forces one
			// gets a wrong answer rather than no answer.
			if !f.Required {
				control.Choices = append(control.Choices, choiceOption{})
			}
			for _, value := range f.Choices {
				control.Choices = append(control.Choices,
					choiceOption{Value: value, Selected: value == control.Value})
			}
		default:
			control.Type = "text"
		}
		view.Fields = append(view.Fields, control)
	}

	today := s.now().UTC()
	for _, sp := range s.sp().Sprints() {
		view.Sprints = append(view.Sprints, sprintChoice{
			Note: sp.Note, Title: sp.Title, When: span(dayOf(sp.Starts), dayOf(sp.Ends)),
			Running: sp.On(today), Selected: strings.EqualFold(sp.Note, t.Sprint),
		})
	}
	return view
}

// dayOf writes a day, or nothing for a date that was never set.
func dayOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(vault.DateFormat)
}

// descendantsOf walks down the parent graph, so the parent list cannot offer a
// choice that would make a cycle.
func descendantsOf(entries []vault.Entry, key string) map[string]bool {
	children := map[string][]string{}
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent != "" {
			children[e.Task.Parent] = append(children[e.Task.Parent], e.Key)
		}
	}

	found := map[string]bool{}
	var walk func(string)
	walk = func(k string) {
		for _, child := range children[k] {
			if !found[child] {
				found[child] = true
				walk(child)
			}
		}
	}
	walk(key)
	return found
}

// handleEdit applies the form.
func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	// Its own project's vocabulary decides what it may become — see configFor.
	c, err := s.configFor(key)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cannot read the form", err.Error())
		return
	}

	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		s.rejectEdit(w, r, c, key, "A task needs a title.")
		return
	}

	author := s.authorFor(r)
	err = s.editTask(r, key, r.FormValue("version"), author, func(t *task.Task) (string, []string, error) {
		var changed []string

		if title != t.Title {
			t.Set("title", title)
			changed = append(changed, "title")
		}

		taskType := strings.TrimSpace(r.FormValue("type"))
		if taskType != t.Type {
			if !c.HasType(taskType) {
				return "", nil, fmt.Errorf("type %q is not one of %s", taskType, strings.Join(c.TypeNames(), ", "))
			}
			t.Set("type", taskType)
			changed = append(changed, "type")
		}

		priority := strings.TrimSpace(r.FormValue("priority"))
		if priority != t.Priority {
			if !c.HasPriority(priority) {
				return "", nil, fmt.Errorf("priority %q is not one of %s",
					priority, strings.Join(c.Priorities, ", "))
			}
			t.Set("priority", priority)
			changed = append(changed, "priority")
		}

		if status := strings.TrimSpace(r.FormValue("status")); status != "" && status != t.Status {
			category, known := c.CategoryOf(status)
			if !known {
				return "", nil, fmt.Errorf("status %q is not one of %s",
					status, strings.Join(c.StatusNames(), ", "))
			}
			if !c.CanMove(t.Status, status) {
				return "", nil, fmt.Errorf("the workflow does not allow %s → %s", t.Status, status)
			}
			changed = append(changed, t.Status+" → "+status)
			t.SetStatus(status, category)
		}

		if assignee := strings.TrimSpace(r.FormValue("assignee")); assignee != t.Assignee {
			t.Set("assignee", assignee)
			changed = append(changed, "assignee")
		}

		if said, err := s.applyEstimate(r, c, t, key); err != nil {
			return "", nil, err
		} else if said != "" {
			changed = append(changed, said)
		}

		if said, err := s.applySprint(r, t); err != nil {
			return "", nil, err
		} else if said != "" {
			changed = append(changed, said)
		}

		for _, said := range applyFields(r, c, t) {
			changed = append(changed, said)
		}

		parent := strings.TrimSpace(r.FormValue("parent"))
		if parent != t.Parent {
			switch {
			case parent == "":
				t.SetParent("")
			default:
				// A parent is a link, and a link resolves by note name.
				owner, inVault, _, err := s.sp().Locate(parent)
				if err != nil {
					return "", nil, fmt.Errorf("parent %s does not exist", parent)
				}
				note := strings.TrimSuffix(path.Base(inVault), ".md")
				_ = owner
				t.SetParent(note)
			}
			changed = append(changed, "parent")
		}

		labels := splitCommas(r.FormValue("labels"))
		if strings.Join(labels, ",") != strings.Join(t.Labels, ",") {
			t.SetLabels(labels)
			changed = append(changed, "labels")
		}

		tags := splitCommas(r.FormValue("tags"))
		if strings.Join(tags, ",") != strings.Join(t.Tags, ",") {
			t.SetTags(tags)
			changed = append(changed, "tags")
		}

		// The description is the body above the comments. Rewriting it must
		// not disturb the conversation underneath.
		body := normaliseNewlines(r.FormValue("body"))
		if body != t.Description() {
			t.SetDescription(body)
			changed = append(changed, "description")
		}

		if len(changed) == 0 {
			return "", nil, nil
		}
		return key + ": " + strings.Join(changed, ", "), nil, nil
	})

	switch {
	case err == nil:
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
	case errors.Is(err, ErrStale):
		s.fail(w, r, http.StatusConflict, "Someone got there first",
			"This task changed on disk after the form was loaded — most likely in Obsidian or "+
				"by an agent. Nothing was written. Reload "+key+" and make the change again.")
	default:
		s.rejectEdit(w, r, c, key, err.Error())
	}
}

func (s *Server) rejectEdit(w http.ResponseWriter, r *http.Request, c *project.Config, key, message string) {
	t, version, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	rel, _, _ := s.locate(key)

	w.WriteHeader(http.StatusBadRequest)
	s.render(w, r, "edit.html", c, "Edit "+key, s.editView(r, c, t, version, rel, message))
}

func normaliseNewlines(s string) string {
	return strings.TrimRight(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// applyEstimate reads the estimate off the form and says what changed.
//
// Refused rather than rounded when it is off the scale: a vault that declared
// 1, 2, 3, 5, 8, 13 meant it, and quietly storing 4 would make the validator
// disagree with the interface that wrote it.
func (s *Server) applyEstimate(r *http.Request, c *project.Config, t *task.Task, key string) (string, error) {
	// A form that never offered the field must not clear one that is set: the
	// absence of a value in a request is not a decision.
	raw, offered := r.Form["estimate"]
	if !offered {
		return "", nil
	}
	value := strings.TrimSpace(strings.Join(raw, ""))

	if value == "" {
		if !t.Sized() {
			return "", nil
		}
		t.ClearEstimate()
		return "estimate taken off", nil
	}
	if !c.Sizes() {
		return "", fmt.Errorf("this vault does not size work: %s has no estimates block",
			project.FileName)
	}

	size, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return "", fmt.Errorf("%q is not a number", value)
	}
	if size < 0 {
		return "", errors.New("work cannot be smaller than nothing")
	}
	if !c.OnScale(size) {
		return "", fmt.Errorf("%s is not on the scale %s",
			project.Amount(size), amounts(c.EstimateScale()))
	}
	// A container's size is what its children add to — see rule 12.
	if s.hasChildren(r, key) {
		return "", errors.New("this task has children, so its size is what they add up to")
	}
	if t.Sized() && t.Size() == size {
		return "", nil
	}
	t.SetEstimate(size)
	return "estimate " + project.Amount(size), nil
}

// applySprint puts the task in a sprint, or takes it out of every one.
//
// The sprint has to be a page that exists. A task pointing at a sprint nobody
// wrote is in a commitment with no goal and no dates, which rule 13 reports and
// this refuses to create.
func (s *Server) applySprint(r *http.Request, t *task.Task) (string, error) {
	raw, offered := r.Form["sprint"]
	if !offered {
		return "", nil
	}
	note := strings.TrimSpace(strings.Join(raw, ""))

	if note == "" {
		if t.Sprint == "" {
			return "", nil
		}
		was := t.Sprint
		t.SetSprint("")
		return "out of " + was, nil
	}
	if strings.EqualFold(note, t.Sprint) {
		return "", nil
	}

	for _, sp := range s.sp().Sprints() {
		if strings.EqualFold(sp.Note, note) {
			t.SetSprint(sp.Note)
			return "into " + sp.Note, nil
		}
	}
	return "", fmt.Errorf("%q is not a sprint page in this vault", note)
}

// hasChildren reports whether any task names this one as its parent.
func (s *Server) hasChildren(r *http.Request, key string) bool {
	entries, err := s.entries(r)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == key {
			return true
		}
	}
	return false
}

// amounts writes a scale the way the configuration says it.
func amounts(scale []float64) string {
	out := make([]string, 0, len(scale))
	for _, v := range scale {
		out = append(out, project.Amount(v))
	}
	return strings.Join(out, ", ")
}

// applyFields reads the vault's own properties off the form.
//
// Refused nowhere: a value that does not match its declaration is written and
// then reported by rule 15. That is the opposite of how a status is handled,
// and on purpose — a status the board cannot read breaks the board, and a field
// that says the wrong thing is a mistake in somebody's data. Refusing it would
// mean a form that will not save until every unrelated field is correct, on a
// vault imported from a system that had no such rule.
//
// A field only offered to some types is not read off a form that did not offer
// it, so retyping a task does not silently blank the properties of the type it
// used to be — rule 15 reports the leftovers instead, where a person can see
// them.
func applyFields(r *http.Request, c *project.Config, t *task.Task) []string {
	var said []string
	for _, f := range c.FieldsFor(t.Type) {
		name := "field_" + f.Name
		raw, offered := r.Form[name]
		if !offered && f.Kind != project.FieldFlag {
			continue
		}
		value := strings.TrimSpace(strings.Join(raw, ""))

		// A checkbox that is off sends nothing at all, which is how a flag says
		// no. Every other kind treats a missing field as "not asked".
		if f.Kind == project.FieldFlag {
			value = "false"
			if len(raw) > 0 {
				value = "true"
			}
		}

		was := strings.TrimSpace(t.Property(f.Name))
		if was == value {
			continue
		}
		// A number, a date, a moment and a flag are written plain, so Obsidian
		// shows them as what they are rather than as strings of them.
		plain := f.Kind == project.FieldNumber || f.Kind == project.FieldDate ||
			f.Kind == project.FieldMoment || f.Kind == project.FieldFlag
		t.SetProperty(f.Name, value, plain)

		switch {
		case value == "":
			said = append(said, f.Shown()+" cleared")
		default:
			said = append(said, f.Shown()+" "+value)
		}
	}
	return said
}

// handlesIn is who is already at work in this vault, most tasks first.
//
// Read from the tasks rather than from the host: the host knows who may push,
// and the vault knows who the work is actually on — an agent, or somebody an
// import mapped, is in the second list and not the first. Ordered by how much
