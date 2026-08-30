package server

import (
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

type pageData struct {
	Config *project.Config
	Title  string
	Data   any
}

func (s *Server) render(w http.ResponseWriter, name string, c *project.Config, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, pageData{c, title, data}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) fail(w http.ResponseWriter, code int, title, message string) {
	c, _ := project.Load(s.root)
	w.WriteHeader(code)
	s.render(w, "error.html", c, title, message)
}

// ---- board ----

type column struct {
	Status project.Status
	Cards  []card
}

type card struct {
	Key      string
	Href     string
	Project  string
	Title    string
	Status   string
	Assignee string
	Priority string
	Labels   []string
	// Version is the fingerprint of the file this card was rendered from. The
	// board hands it back when a card is dragged, so a drop lands on the file
	// the person actually saw.
	Version string
}

type boardView struct {
	Columns  []column
	Broken   []vault.Entry
	Projects []projectTab
	Selected string
	Total    int
}

type projectTab struct {
	Key   string
	Name  string
	Count int
	Href  string
	On    bool
}

// handleBoard shows every project at once, or one of them.
//
// Work crosses projects constantly, so the default is all of them and the
// project is a chip on the card rather than a separate board to go and find.
func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := vault.List(s.root, c)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	selected := r.URL.Query().Get("project")
	if selected != "" && !c.HasProject(selected) {
		s.fail(w, http.StatusNotFound, "No such project",
			selected+" is not in this vault: it holds "+strings.Join(c.ProjectKeys(), ", "))
		return
	}

	view := boardView{Selected: selected}
	for _, status := range c.Statuses {
		view.Columns = append(view.Columns, column{Status: status})
	}

	counts := map[string]int{}
	for _, e := range entries {
		if e.Err != nil {
			view.Broken = append(view.Broken, e)
			continue
		}
		counts[e.Project]++
		if selected != "" && e.Project != selected {
			continue
		}
		for i := range view.Columns {
			if view.Columns[i].Status.Name == e.Task.Status {
				view.Columns[i].Cards = append(view.Columns[i].Cards, card{
					Key: e.Key, Href: "/task/" + e.Key, Project: e.Project,
					Title: e.Task.Title, Status: e.Task.Status,
					Assignee: e.Task.Assignee, Priority: e.Task.Priority,
					Labels: e.Task.Labels, Version: version(e.Raw),
				})
				view.Total++
			}
		}
	}

	view.Projects = append(view.Projects, projectTab{
		Key: "All", Name: "Every project", Count: len(entries), Href: "/", On: selected == "",
	})
	for _, p := range c.Projects {
		view.Projects = append(view.Projects, projectTab{
			Key: p.Key, Name: p.Name, Count: counts[p.Key],
			Href: "/?project=" + url.QueryEscape(p.Key), On: selected == p.Key,
		})
	}

	title := c.Name
	if selected != "" {
		title = c.ProjectName(selected)
	}
	s.render(w, "board.html", c, title, view)
}

// ---- one task ----

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	t, ver, err := s.loadTask(key)
	if err != nil {
		s.fail(w, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	ix, err := buildIndex(s.root, c)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	s.render(w, "task.html", c, t.Key+" "+t.Title, struct {
		Task    *task.Task
		Body    template.HTML
		Version string
		Path    string
	}{t, renderMarkdown(t.Body(), ix), ver, vault.TaskPath(key)})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	status := r.FormValue("status")
	category, known := c.CategoryOf(status)
	if !known {
		s.fail(w, http.StatusBadRequest, "Unknown status",
			status+" is not one of "+strings.Join(c.StatusNames(), ", "))
		return
	}

	author := s.authorFor(r)
	err = s.editTask(key, r.FormValue("version"), author, func(t *task.Task) (string, error) {
		if t.Status == status {
			return "", nil
		}
		was := t.Status
		t.SetStatus(status, category)
		return key + ": " + was + " → " + status, nil
	})
	s.afterEdit(w, r, key, err)
}

func (s *Server) handleComment(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	text := strings.TrimSpace(r.FormValue("text"))
	if text == "" {
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
		return
	}

	author := s.authorFor(r)
	err := s.editTask(key, r.FormValue("version"), author, func(t *task.Task) (string, error) {
		t.AppendComment(author.Name, s.now(), text)
		return key + ": comment from " + author.Name, nil
	})
	s.afterEdit(w, r, key, err)
}

// afterEdit turns the outcome of a write into a response.
func (s *Server) afterEdit(w http.ResponseWriter, r *http.Request, key string, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
	case errors.Is(err, ErrStale):
		s.fail(w, http.StatusConflict, "Someone got there first",
			"This task changed on disk after the page was loaded — most likely in Obsidian or "+
				"by an agent. Nothing was written. Reload "+key+" and make the change again.")
	case os.IsNotExist(err):
		s.fail(w, http.StatusNotFound, "No such task", key+" is not in this vault")
	default:
		s.fail(w, http.StatusInternalServerError, "The change was not saved", err.Error())
	}
}

// ---- creating a task ----

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	s.render(w, "new.html", c, "New task", r.URL.Query().Get("project"))
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	author := s.authorFor(r)
	s.writes.Lock()
	rel, t, err := vault.Create(s.root, c, vault.NewOptions{
		Project:  r.FormValue("project"),
		Title:    r.FormValue("title"),
		Type:     r.FormValue("type"),
		Priority: r.FormValue("priority"),
		Assignee: strings.TrimSpace(r.FormValue("assignee")),
		Now:      s.now(),
	})
	if err == nil {
		err = s.repo.Commit([]string{rel}, t.Key+": "+t.Title, author)
	}
	s.writes.Unlock()

	if err != nil {
		s.fail(w, http.StatusBadRequest, "The task was not created", err.Error())
		return
	}
	http.Redirect(w, r, "/task/"+t.Key, http.StatusSeeOther)
}

// ---- knowledge base ----

func (s *Server) handlePages(w http.ResponseWriter, r *http.Request) {
	c, _ := project.Load(s.root)

	var paths []string
	docs := filepath.Join(s.root, vault.DocsDir)
	_ = filepath.WalkDir(docs, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(full, ".md") {
			return nil
		}
		rel, err := filepath.Rel(s.root, full)
		if err != nil {
			return nil
		}
		paths = append(paths, strings.TrimSuffix(filepath.ToSlash(rel), ".md"))
		return nil
	})
	sort.Strings(paths)

	s.render(w, "pages.html", c, "Pages", paths)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	c, _ := project.Load(s.root)

	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if rel == "" || strings.HasPrefix(rel, "..") {
		s.fail(w, http.StatusBadRequest, "Not a page", "that path leads outside the vault")
		return
	}

	raw, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)+".md"))
	if err != nil {
		s.fail(w, http.StatusNotFound, "No such page", rel+" is not in this vault")
		return
	}
	ix, err := buildIndex(s.root, c)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	body := string(raw)
	title := path.Base(rel)
	if t, err := task.Parse(raw); err == nil {
		body = t.Body()
		if t.Title != "" {
			title = t.Title
		}
	}

	s.render(w, "page.html", c, title, struct {
		Path string
		Body template.HTML
	}{rel, renderMarkdown(body, ix)})
}

// ---- search ----

type hit struct {
	Href    string
	Label   string
	Context string
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	c, _ := project.Load(s.root)
	query := strings.TrimSpace(r.FormValue("q"))

	var hits []hit
	if query != "" {
		hits = s.search(c, query)
	}

	s.render(w, "search.html", c, "Search", struct {
		Query string
		Hits  []hit
	}{query, hits})
}

// search is a plain substring scan over the vault. An index would be faster and
// would be one more thing that can disagree with the files; at the size a
// vault reaches, reading them is fast enough.
func (s *Server) search(c *project.Config, query string) []hit {
	needle := strings.ToLower(query)
	var hits []hit

	entries, _ := vault.List(s.root, c)
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		text := e.Task.Title + "\n" + e.Task.Body()
		if strings.Contains(strings.ToLower(text), needle) {
			hits = append(hits, hit{
				Href:    "/task/" + e.Key,
				Label:   e.Key + " " + e.Task.Title,
				Context: excerpt(text, needle),
			})
		}
	}

	docs := filepath.Join(s.root, vault.DocsDir)
	_ = filepath.WalkDir(docs, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(full, ".md") {
			return nil
		}
		raw, err := os.ReadFile(full)
		if err != nil || !strings.Contains(strings.ToLower(string(raw)), needle) {
			return nil
		}
		rel, err := filepath.Rel(s.root, full)
		if err != nil {
			return nil
		}
		rel = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		hits = append(hits, hit{"/page/" + rel, rel, excerpt(string(raw), needle)})
		return nil
	})
	return hits
}

func excerpt(text, needle string) string {
	at := strings.Index(strings.ToLower(text), needle)
	if at < 0 {
		return ""
	}
	start := max(0, at-60)
	end := min(len(text), at+len(needle)+60)
	return strings.ReplaceAll(strings.TrimSpace(text[start:end]), "\n", " ")
}
