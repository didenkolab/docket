package server

import (
	"errors"
	"html/template"
	"io/fs"
	"net/http"
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
	Project *project.Project
	Title   string
	Data    any
}

func (s *Server) render(w http.ResponseWriter, name string, p *project.Project, title string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, pageData{p, title, data}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) fail(w http.ResponseWriter, code int, title, message string) {
	p, _ := project.Load(s.root)
	w.WriteHeader(code)
	s.render(w, "error.html", p, title, message)
}

// ---- board ----

type column struct {
	Status project.Status
	Cards  []card
}

type card struct {
	Key      string
	Title    string
	Assignee string
	Priority string
	Labels   []string
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the project", err.Error())
		return
	}
	entries, err := vault.List(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	columns := make([]column, len(p.Statuses))
	for i, status := range p.Statuses {
		columns[i] = column{Status: status}
	}
	var broken []vault.Entry

	for _, e := range entries {
		if e.Err != nil {
			broken = append(broken, e)
			continue
		}
		for i := range columns {
			if columns[i].Status.Name == e.Task.Status {
				columns[i].Cards = append(columns[i].Cards, card{
					Key: e.Key, Title: e.Task.Title, Assignee: e.Task.Assignee,
					Priority: e.Task.Priority, Labels: e.Task.Labels,
				})
			}
		}
	}

	s.render(w, "board.html", p, p.Name, struct {
		Columns []column
		Broken  []vault.Entry
	}{columns, broken})
}

// ---- one task ----

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	p, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the project", err.Error())
		return
	}

	t, ver, err := s.loadTask(key)
	if err != nil {
		s.fail(w, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	ix, err := buildIndex(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot index the vault", err.Error())
		return
	}

	s.render(w, "task.html", p, t.Key+" "+t.Title, struct {
		Task    *task.Task
		Body    template.HTML
		Version string
	}{t, renderMarkdown(t.Body(), ix), ver})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	p, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the project", err.Error())
		return
	}

	status := r.FormValue("status")
	category, known := p.CategoryOf(status)
	if !known {
		s.fail(w, http.StatusBadRequest, "Unknown status",
			status+" is not one of "+strings.Join(p.StatusNames(), ", "))
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
	key := r.PathValue("key")
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
	p, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the project", err.Error())
		return
	}
	s.render(w, "new.html", p, "New task", nil)
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	p, err := project.Load(s.root)
	if err != nil {
		s.fail(w, http.StatusInternalServerError, "Cannot read the project", err.Error())
		return
	}

	author := s.authorFor(r)
	s.writes.Lock()
	rel, t, err := vault.Create(s.root, p, vault.NewOptions{
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
	p, _ := project.Load(s.root)

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

	s.render(w, "pages.html", p, "Pages", paths)
}

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	p, _ := project.Load(s.root)

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
	ix, err := buildIndex(s.root)
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

	s.render(w, "page.html", p, title, struct {
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
	p, _ := project.Load(s.root)
	query := strings.TrimSpace(r.FormValue("q"))

	var hits []hit
	if query != "" {
		hits = s.search(query)
	}

	s.render(w, "search.html", p, "Search", struct {
		Query string
		Hits  []hit
	}{query, hits})
}

// search is a plain substring scan over the vault. An index would be faster and
// would be one more thing that can disagree with the files; at the size a
// single project reaches, reading them is fast enough.
func (s *Server) search(query string) []hit {
	needle := strings.ToLower(query)
	var hits []hit

	entries, _ := vault.List(s.root)
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		haystack := strings.ToLower(e.Task.Title + "\n" + e.Task.Body())
		if strings.Contains(haystack, needle) {
			hits = append(hits, hit{
				Href:    "/task/" + e.Key,
				Label:   e.Key + " " + e.Task.Title,
				Context: excerpt(e.Task.Title+"\n"+e.Task.Body(), needle),
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
