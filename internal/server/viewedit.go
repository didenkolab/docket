package server

import (
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/base"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Editing a saved view.
//
// A view is a file, so editing one is editing a file: the same text Obsidian
// reads, saved through the same commit-and-send path as a task or a page. What
// is added here is the refusal — the filter is parsed before anything is
// written, and a file that would not draw is not saved with an apology
// afterwards.

type viewForm struct {
	// Path is where the file is, relative to the space root.
	Path string
	// In is the repository a new view is being written into, and Name is what
	// it will be called. Both empty when an existing view is being edited.
	In, Name string
	Body     string
	Version  string
	New      bool
	Error    string
	// Repositories are the projects a new view may go into.
	Repositories []string
}

// starter is what a new view begins as: the smallest thing that draws.
const starter = `filters:
  and:
    - file.inFolder("PROJECT")
    - 'note.status_category != "done"'
properties:
  note.key:
    displayName: Key
  note.title:
    displayName: Title
  note.assignee:
    displayName: Assignee
views:
  - type: table
    name: Everything open
    order:
      - note.key
      - note.title
      - note.assignee
`

func (s *Server) handleViewEditForm(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	at := strings.TrimPrefix(r.PathValue("path"), "/")
	form := viewForm{Repositories: s.viewFolders()}

	if at == "" {
		form.New, form.Body = true, starter
		s.render(w, r, "view-edit.html", c, "New view", form)
		return
	}

	saved, ok := s.sp().ViewAt(at)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "No such view",
			at+" is not a saved view in this space.")
		return
	}
	full, err := s.abs(at)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such view", err.Error())
		return
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such view", err.Error())
		return
	}

	form.Path, form.Body, form.Version = at, string(raw), version(raw)
	s.render(w, r, "view-edit.html", c, saved.Note, form)
}

func (s *Server) handleViewSave(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, http.StatusBadRequest, "Cannot read the form", err.Error())
		return
	}

	form := viewForm{
		Path:         strings.TrimSpace(r.FormValue("path")),
		In:           strings.Trim(strings.TrimSpace(r.FormValue("in")), "/"),
		Name:         strings.Trim(strings.TrimSpace(r.FormValue("name")), "/"),
		Body:         normaliseNewlines(r.FormValue("body")),
		Version:      r.FormValue("version"),
		New:          r.FormValue("new") == "1",
		Repositories: s.viewFolders(),
	}

	reject := func(message string) {
		form.Error = message
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "view-edit.html", c, "View", form)
	}

	if form.New {
		if form.Name == "" {
			reject("A view needs a name — it is the file it will be saved as.")
			return
		}
		name := strings.TrimSuffix(form.Name, ".base")
		if strings.ContainsAny(name, `/\`) {
			reject("A view's name is a file name, without folders in it.")
			return
		}
		form.Path = path.Join(form.In, vault.BoardsDir, name+".base")
	}
	if form.Path == "" {
		reject("Nothing said which view this is.")
		return
	}
	if !strings.HasSuffix(form.Path, ".base") || path.Base(path.Dir(form.Path)) != vault.BoardsDir {
		reject("A saved view is a .base file in a project's boards/ folder.")
		return
	}

	// Parsed before it is written. A file saved and then found unreadable is a
	// view somebody has to go and fix in a text editor, which is exactly what
	// this page exists to avoid.
	if _, err := base.Parse([]byte(form.Body)); err != nil {
		reject(capitalise(err.Error()))
		return
	}

	full, err := s.abs(form.Path)
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
		reject(form.Path + " already exists.")
		return
	case !form.New && readErr != nil:
		reject(form.Path + " is not in this space any more.")
		return
	case !form.New && form.Version != "" && version(existing) != form.Version:
		s.fail(w, r, http.StatusConflict, "Someone got there first",
			"This view changed on disk after the form was loaded. Nothing was written. "+
				"Reload it and make the change again.")
		return
	}

	if err := os.MkdirAll(path.Dir(full), 0o755); err != nil {
		reject(err.Error())
		return
	}
	if err := os.WriteFile(full, []byte(ending(form.Body)), 0o644); err != nil {
		reject(err.Error())
		return
	}

	verb := "edited"
	if form.New {
		verb = "wrote"
	}
	if err := s.commit(r, []string{form.Path}, verb+" the view "+form.Path, author); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Saved, but not committed", err.Error())
		return
	}

	http.Redirect(w, r, "/view/"+escapePath(form.Path), http.StatusSeeOther)
}

// viewFolders are the prefixes a new view may be written into. A space of one
// has a single empty prefix, which is the vault itself.
func (s *Server) viewFolders() []string {
	var out []string
	for _, v := range s.sp().Vaults() {
		out = append(out, v.Prefix)
	}
	return out
}

// ending gives the file the newline every text file has.
func ending(body string) string {
	if strings.HasSuffix(body, "\n") {
		return body
	}
	return body + "\n"
}
