package server

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
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
}

type parentChoice struct {
	Key      string
	Title    string
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
	for _, e := range entries {
		if e.Task == nil || e.Key == t.Key || descendants[e.Key] {
			continue
		}
		view.Parents = append(view.Parents, parentChoice{
			Key: e.Key, Title: e.Task.Title, Selected: e.Key == t.Parent,
		})
	}
	return view
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
