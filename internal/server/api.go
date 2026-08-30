package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// taskJSON is the wire shape of a task. It carries a version so that a client
// can write back only what it actually read.
type taskJSON struct {
	Key            string   `json:"key"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`
	Status         string   `json:"status"`
	StatusCategory string   `json:"status_category"`
	Priority       string   `json:"priority"`
	Assignee       string   `json:"assignee,omitempty"`
	Parent         string   `json:"parent,omitempty"`
	Labels         []string `json:"labels,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Created        string   `json:"created"`
	Updated        string   `json:"updated"`
	Aliases        []string `json:"aliases,omitempty"`
	Body           string   `json:"body,omitempty"`
	Order          *int     `json:"order,omitempty"`
	Version        string   `json:"version,omitempty"`
	// Reachable is where the workflow lets this task go from where it is. The
	// board redraws a card's constraint from it after a move, so a card dragged
	// twice is not checked against the workflow it used to be under.
	Reachable []string `json:"reachable,omitempty"`
}

func toJSON(c *project.Config, t *task.Task, version string, withBody bool) taskJSON {
	out := taskJSON{
		Key: t.Key, Title: t.Title, Type: t.Type,
		Status: t.Status, StatusCategory: t.StatusCategory,
		Priority: t.Priority, Assignee: t.Assignee, Parent: t.Parent,
		Labels: t.Labels, Tags: t.Tags, Created: t.Created, Updated: t.Updated,
		Aliases: t.Aliases, Order: t.Order, Version: version,
	}
	if c != nil {
		out.Reachable = names(c.Reachable(t.Status))
	}
	if withBody {
		out.Body = t.Body()
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func apiError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]string{"error": message})
}

func (s *Server) apiListTasks(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries, err := s.entries()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	wanted := r.URL.Query().Get("status")
	category := r.URL.Query().Get("category")
	wantedProject := r.URL.Query().Get("project")

	out := []taskJSON{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if wanted != "" && e.Task.Status != wanted {
			continue
		}
		if category != "" && e.Task.StatusCategory != category {
			continue
		}
		if wantedProject != "" && e.Project != wantedProject {
			continue
		}
		out = append(out, toJSON(c, e.Task, "", false))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) apiGetTask(w http.ResponseWriter, r *http.Request) {
	t, version, err := s.loadTask(keyOf(r))
	if err != nil {
		apiError(w, http.StatusNotFound, err.Error())
		return
	}
	c, _ := s.config()
	writeJSON(w, http.StatusOK, toJSON(c, t, version, true))
}

type createRequest struct {
	Project  string   `json:"project"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Priority string   `json:"priority"`
	Assignee string   `json:"assignee"`
	Parent   string   `json:"parent"`
	Labels   []string `json:"labels"`
	Tags     []string `json:"tags"`
}

func (s *Server) apiCreateTask(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, err := s.config()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}

	author := s.authorFor(r)
	s.writes.Lock()
	rel, t, err := s.create(vault.NewOptions{
		Project: req.Project,
		Title:   req.Title, Type: req.Type, Status: req.Status,
		Priority: req.Priority, Assignee: req.Assignee, Parent: req.Parent,
		Labels: req.Labels, Tags: req.Tags, Now: s.now(),
	})
	if err == nil {
		err = s.commit([]string{rel}, t.Key+": "+t.Title, author)
	}
	s.writes.Unlock()

	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, version, _ := s.loadTask(t.Key)
	writeJSON(w, http.StatusCreated, toJSON(c, t, version, true))
}

// patchRequest carries only what the client wants changed. A nil pointer means
// "leave it alone", which is the difference between clearing an assignee and
// not mentioning one.
type patchRequest struct {
	Title    *string   `json:"title"`
	Status   *string   `json:"status"`
	Priority *string   `json:"priority"`
	Assignee *string   `json:"assignee"`
	Labels   *[]string `json:"labels"`
	Tags     *[]string `json:"tags"`
	Comment  *string   `json:"comment"`
	Version  string    `json:"version"`
	// After places the task in its column, directly below the task with this
	// key. An empty string is the top of the column. Absent — nil — leaves the
	// order alone, which is what every client that does not draw a board wants.
	After *string `json:"after"`
}

func (s *Server) apiPatchTask(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)

	var req patchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	// What may happen to this task is its own project's business, not the
	// space's — see configFor.
	c, err := s.configFor(key)
	if err != nil {
		apiError(w, http.StatusNotFound, err.Error())
		return
	}

	author := s.authorFor(r)
	err = s.editTask(key, req.Version, author, func(t *task.Task) (string, []string, error) {
		var changed []string

		if req.Title != nil && *req.Title != t.Title {
			t.Set("title", *req.Title)
			changed = append(changed, "title")
		}
		if req.Status != nil && *req.Status != t.Status {
			category, known := c.CategoryOf(*req.Status)
			if !known {
				return "", nil, errors.New("status " + *req.Status + " is not one of " +
					strings.Join(c.StatusNames(), ", "))
			}
			// The board drags through this, so the workflow has to be enforced
			// here and not only on the form.
			if !c.CanMove(t.Status, *req.Status) {
				return "", nil, errors.New("the workflow does not allow " + t.Status + " → " +
					*req.Status + ". From " + t.Status + " a task can go to " +
					strings.Join(names(c.Reachable(t.Status)), ", "))
			}
			changed = append(changed, t.Status+" → "+*req.Status)
			t.SetStatus(*req.Status, category)
		}
		if req.Priority != nil && *req.Priority != t.Priority {
			if !c.HasPriority(*req.Priority) {
				return "", nil, errors.New("priority " + *req.Priority + " is not one of " +
					strings.Join(c.Priorities, ", "))
			}
			t.Set("priority", *req.Priority)
			changed = append(changed, "priority")
		}
		if req.Assignee != nil && *req.Assignee != t.Assignee {
			t.Set("assignee", *req.Assignee)
			changed = append(changed, "assignee")
		}
		if req.Labels != nil {
			t.SetLabels(*req.Labels)
			changed = append(changed, "labels")
		}
		if req.Tags != nil {
			t.SetTags(*req.Tags)
			changed = append(changed, "tags")
		}
		if req.Comment != nil && strings.TrimSpace(*req.Comment) != "" {
			t.AppendComment(author.Name, s.now(), *req.Comment)
			changed = append(changed, "comment")
		}

		// Placement comes last, because where a card belongs depends on which
		// column it is now in.
		var alsoCommit []string
		if req.After != nil {
			renumbered, err := s.place(c, t, t.Status, *req.After)
			if err != nil {
				return "", nil, err
			}
			alsoCommit = renumbered
			if len(changed) == 0 || len(renumbered) > 0 {
				changed = append(changed, "order")
			}
		}

		if len(changed) == 0 {
			return "", nil, nil
		}
		return key + ": " + strings.Join(changed, ", "), alsoCommit, nil
	})

	switch {
	case errors.Is(err, ErrStale):
		apiError(w, http.StatusConflict, ErrStale.Error()+
			" — reload the task, take its new version, and try again")
		return
	case os.IsNotExist(err):
		apiError(w, http.StatusNotFound, key+" is not in this vault")
		return
	case err != nil:
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}

	t, version, err := s.loadTask(key)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toJSON(c, t, version, true))
}
