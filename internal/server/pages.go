package server

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

type pageData struct {
	Config *project.Config
	Title  string
	Data   any
	// You is who is asking, so the interface can offer only what they may do.
	You      access.Identity
	SignedIn bool
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string,
	c *project.Config, title string, data any) {

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := pageData{
		Config: c, Title: title, Data: data,
		You:      identityOf(r),
		SignedIn: s.auth != nil,
	}
	if err := s.tmpl.ExecuteTemplate(w, name, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, title, message string) {
	c, _ := project.Load(s.root)
	w.WriteHeader(code)
	s.render(w, r, "error.html", c, title, message)
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
	// Reachable is where the workflow lets this card go, so a drag can refuse
	// a column before the drop rather than after it.
	Reachable string
}

type boardView struct {
	Columns  []column
	Broken   []vault.Entry
	Projects []projectTab
	Selected string
	Total    int
}

// reachableList is the workflow, flattened for an attribute.
func reachableList(c *project.Config, from string) string {
	var names []string
	for _, s := range c.Reachable(from) {
		names = append(names, s.Name)
	}
	return strings.Join(names, "\n")
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
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	entries, err := vault.List(s.root, c)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	selected := r.URL.Query().Get("project")
	if selected != "" && !c.HasProject(selected) {
		s.fail(w, r, http.StatusNotFound, "No such project",
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
					Reachable: reachableList(c, e.Task.Status),
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
	s.render(w, r, "board.html", c, title, view)
}

// ---- one task ----

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	t, ver, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	rel, _, err := s.locate(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	projectKey, _, _ := project.SplitKey(key)
	ix, err := buildIndex(s.root, c)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	s.render(w, r, "task.html", c, t.Key+" "+t.Title, taskView{
		Task:        t,
		Reachable:   c.Reachable(t.Status),
		Project:     projectKey,
		ProjectName: c.ProjectName(projectKey),
		Description: renderMarkdown(t.Description(), ix),
		Comments:    renderComments(t.Comments(), ix),
		Children:    s.childrenOf(c, key),
		Version:     ver,
		Path:        rel,
	})
}

type taskView struct {
	Task        *task.Task
	Reachable   []project.Status
	Project     string
	ProjectName string
	Description template.HTML
	Comments    []renderedComment
	Children    []childTask
	Version     string
	Path        string
}

type renderedComment struct {
	Author  string
	When    string
	Text    template.HTML
	Initial string
}

type childTask struct {
	Key      string
	Title    string
	Status   string
	Category string
}

func renderComments(comments []task.Comment, ix *index) []renderedComment {
	out := make([]renderedComment, 0, len(comments))
	for _, c := range comments {
		initial := "?"
		if c.Author != "" {
			initial = strings.ToUpper(c.Author[:1])
		}
		out = append(out, renderedComment{
			Author: c.Author, When: c.When,
			Text: renderMarkdown(c.Text, ix), Initial: initial,
		})
	}
	return out
}

// childrenOf lists the tasks that name this one as their parent. A hierarchy
// written only downwards is a hierarchy you can only read from the wrong end.
func (s *Server) childrenOf(c *project.Config, key string) []childTask {
	entries, err := vault.List(s.root, c)
	if err != nil {
		return nil
	}
	var children []childTask
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == key {
			children = append(children, childTask{
				Key: e.Key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
			})
		}
	}
	return children
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	status := r.FormValue("status")
	category, known := c.CategoryOf(status)
	if !known {
		s.fail(w, r, http.StatusBadRequest, "Unknown status",
			status+" is not one of "+strings.Join(c.StatusNames(), ", "))
		return
	}

	author := s.authorFor(r)
	err = s.editTask(key, r.FormValue("version"), author, func(t *task.Task) (string, error) {
		if t.Status == status {
			return "", nil
		}
		if !c.CanMove(t.Status, status) {
			return "", fmt.Errorf("the workflow does not allow %s → %s. From %s a task can go to %s",
				t.Status, status, t.Status, strings.Join(names(c.Reachable(t.Status)), ", "))
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

// names flattens statuses for a message.
func names(statuses []project.Status) []string {
	out := make([]string, len(statuses))
	for i, s := range statuses {
		out[i] = s.Name
	}
	return out
}

// afterEdit turns the outcome of a write into a response.
func (s *Server) afterEdit(w http.ResponseWriter, r *http.Request, key string, err error) {
	switch {
	case err == nil:
		http.Redirect(w, r, "/task/"+key, http.StatusSeeOther)
	case errors.Is(err, ErrStale):
		s.fail(w, r, http.StatusConflict, "Someone got there first",
			"This task changed on disk after the page was loaded — most likely in Obsidian or "+
				"by an agent. Nothing was written. Reload "+key+" and make the change again.")
	case os.IsNotExist(err):
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
	case strings.Contains(err.Error(), "workflow does not allow"):
		s.fail(w, r, http.StatusBadRequest, "The workflow says no", err.Error())
	default:
		s.fail(w, r, http.StatusInternalServerError, "The change was not saved", err.Error())
	}
}

// ---- creating a task ----

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	parent := r.URL.Query().Get("parent")
	selected := r.URL.Query().Get("project")
	if selected == "" && parent != "" {
		// A child belongs where its parent does, unless told otherwise.
		if key, _, err := project.SplitKey(parent); err == nil {
			selected = key
		}
	}
	s.render(w, r, "new.html", c, "New task", struct {
		Project string
		Parent  string
	}{selected, parent})
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	author := s.authorFor(r)
	s.writes.Lock()
	rel, t, err := vault.Create(s.root, c, vault.NewOptions{
		Project:     r.FormValue("project"),
		Title:       r.FormValue("title"),
		Type:        r.FormValue("type"),
		Priority:    r.FormValue("priority"),
		Assignee:    strings.TrimSpace(r.FormValue("assignee")),
		Parent:      strings.TrimSpace(r.FormValue("parent")),
		Description: normaliseNewlines(r.FormValue("body")),
		Now:         s.now(),
	})
	if err == nil {
		err = s.repo.Commit([]string{rel}, t.Key+": "+t.Title, author)
	}
	s.writes.Unlock()

	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "The task was not created", err.Error())
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

	s.render(w, r, "pages.html", c, "Pages", paths)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	c, _ := project.Load(s.root)

	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if rel == "" || strings.HasPrefix(rel, "..") {
		s.fail(w, r, http.StatusBadRequest, "Not a page", "that path leads outside the vault")
		return
	}

	raw, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)+".md"))
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", rel+" is not in this vault")
		return
	}
	ix, err := buildIndex(s.root, c)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot index the vault", err.Error())
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

	s.render(w, r, "page.html", c, title, struct {
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

	s.render(w, r, "search.html", c, "Search", struct {
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
