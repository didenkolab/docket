package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A page is half the product — the knowledge base — and until now it could only
// be read. These handlers make it writable from the same interface, on the same
// terms as a task: a plain form, a version check, and a commit.

type pageForm struct {
	Path    string // vault-relative, without .md
	Title   string
	Body    string
	Version string
	New     bool
	Error   string
	// In is the folder a new page is being made in, so the form asks for a name
	// rather than for a path.
	//
	// The path box was the only way in, and it meant knowing and typing
	// docs/design before writing anything — for a page you were making because
	// you were looking at docs/design. The folder you are in is the folder you
	// meant; the form says so and asks for the one part it cannot know.
	In string
	// Name is the file name inside In, without .md.
	Name string
}

func (s *Server) handlePageNewForm(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()
	in := strings.Trim(strings.TrimSpace(r.URL.Query().Get("in")), "/")
	s.render(w, r, "page-edit.html", c, "New page", pageForm{In: in, New: true})
}

func (s *Server) handlePageEditForm(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()

	rel, err := pagePath(r.PathValue("path"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Not a page", err.Error())
		return
	}
	full, err := s.abs(rel)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", err.Error())
		return
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", rel+" is not in this vault")
		return
	}

	form := pageForm{
		Path:    strings.TrimSuffix(rel, ".md"),
		Body:    string(raw),
		Version: version(raw),
	}
	if t, err := task.Parse(raw); err == nil {
		form.Title = t.Title
		form.Body = strings.TrimLeft(t.Body(), "\n")
	}
	s.render(w, r, "page-edit.html", c, "Edit "+form.Path, form)
}

// handlePageSave writes a page, new or existing.
func (s *Server) handlePageSave(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cannot read the form", err.Error())
		return
	}

	form := pageForm{
		Path:    strings.TrimSpace(r.FormValue("path")),
		In:      strings.Trim(strings.TrimSpace(r.FormValue("in")), "/"),
		Name:    strings.Trim(strings.TrimSpace(r.FormValue("name")), "/"),
		Title:   strings.TrimSpace(r.FormValue("title")),
		Body:    normaliseNewlines(r.FormValue("body")),
		Version: r.FormValue("version"),
		New:     r.FormValue("new") == "1",
	}

	reject := func(message string) {
		form.Error = message
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "page-edit.html", c, "Page", form)
	}

	// A name inside a folder is the ordinary way in; a whole path is still
	// accepted, because somebody who knows where a page goes should not have to
	// go and find the folder first.
	if form.Path == "" && form.Name != "" {
		if form.In != "" {
			form.Path = form.In + "/" + form.Name
		} else {
			form.Path = form.Name
		}
	}
	if form.Path == "" {
		reject("A page needs a name.")
		return
	}

	rel, err := pagePath(form.Path)
	if err != nil {
		reject(err.Error())
		return
	}
	if !strings.HasPrefix(rel, vault.DocsDir+"/") {
		reject("Pages live under docs/. That keeps them out of the project folders, where a " +
			"file is a task.")
		return
	}
	if form.Title == "" {
		reject("A page needs a title.")
		return
	}

	full, err := s.abs(rel)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Nowhere to write that", err.Error())
		return
	}
	author := s.authorFor(r)

	s.writes.Lock()
	defer s.writes.Unlock()

	existing, readErr := os.ReadFile(full)
	switch {
	case form.New && readErr == nil:
		reject(rel + " already exists.")
		return
	case !form.New && readErr != nil:
		reject(rel + " is not in this vault any more.")
		return
	case !form.New && form.Version != "" && version(existing) != form.Version:
		s.fail(w, r, http.StatusConflict, "Someone got there first",
			"This page changed on disk after the form was loaded. Nothing was written. "+
				"Reload it and make the change again.")
		return
	}

	content := fmt.Sprintf("---\ntitle: %s\ntype: page\nupdated: %s\n---\n\n%s\n",
		yamlScalar(form.Title), s.now().UTC().Format("2006-01-02"), form.Body)

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		reject(err.Error())
		return
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		reject(err.Error())
		return
	}

	verb := "edited"
	if form.New {
		verb = "wrote"
	}
	if err := s.commit(r, []string{rel}, verb+" "+strings.TrimSuffix(rel, ".md"), author); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Saved, but not committed", err.Error())
		return
	}

	http.Redirect(w, r, "/page/"+strings.TrimSuffix(rel, ".md"), http.StatusSeeOther)
}

func (s *Server) handlePageDelete(w http.ResponseWriter, r *http.Request) {
	// The path travels in the form rather than the URL: net/http only allows a
	// {path...} wildcard as the last segment, so there is no room for /delete
	// after it.
	rel, err := pagePath(r.FormValue("path"))
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Not a page", err.Error())
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	full, err := s.abs(rel)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", err.Error())
		return
	}
	if err := os.Remove(full); err != nil {
		s.fail(w, r, http.StatusNotFound, "No such page", rel+" is not in this vault")
		return
	}
	if err := s.commit(r, []string{rel}, "deleted "+strings.TrimSuffix(rel, ".md"),
		s.authorFor(r)); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Deleted, but not committed", err.Error())
		return
	}
	http.Redirect(w, r, "/pages", http.StatusSeeOther)
}

// pagePath turns what a form or a URL says into a vault-relative .md path, and
// refuses anything that would leave the vault.
func pagePath(raw string) (string, error) {
	raw = strings.TrimSpace(strings.Trim(raw, "/"))
	if raw == "" {
		return "", errors.New("a page needs a path")
	}
	raw = strings.TrimSuffix(raw, ".md")

	cleaned := path.Clean(raw)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("that path leads outside the vault")
	}
	return cleaned + ".md", nil
}

// yamlScalar quotes a title when leaving it bare would change its meaning.
func yamlScalar(s string) string {
	if strings.ContainsAny(s, `:#{}[]&*!|>'"%@`+"`") || strings.TrimSpace(s) != s {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// handlePreview renders a body the way the page will render it.
//
// It exists so the editor's preview cannot disagree with the real thing: there
// is one Markdown implementation, on the server, and the preview asks it.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	ix, err := s.index()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, string(renderMarkdown(normaliseNewlines(r.FormValue("body")), ix)))
}
