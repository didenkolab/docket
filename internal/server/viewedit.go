package server

import (
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/base"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
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

	// Conditions is the filter as a form: the folders it selects, how the rows
	// are joined, and the rows.
	//
	// Absent when the filter is not that shape — a nested or, a not around a
	// comparison. Then the text is the only way to edit it, and the page says
	// so rather than flattening a filter into a form that would change what the
	// board selects.
	Conditions *base.Filters
	// Rows is Conditions plus a few empty ones to fill in, which is how a row
	// is added without a line of script.
	Rows []base.Condition
	// Operators and Suggestions are what the form offers: every operator, and
	// the property names this vault actually uses.
	Operators   []string
	Suggestions []string
	// Folders is the projects in the vault, for the "in" clause.
	Folders []string
}

// spareRows is how many empty condition rows a form shows below the filled
// ones. Three, because adding a condition without a script means the row has to
// be there already, and a form of twenty blank rows is a form nobody reads.
const spareRows = 3

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
	form := viewForm{
		Repositories: s.viewFolders(),
		Operators:    base.Operators,
		Suggestions:  s.propertyNames(c),
		Folders:      c.ProjectKeys(),
	}

	if at == "" {
		form.New, form.Body = true, starter
		form.Conditions = &base.Filters{Join: "and"}
		form.Rows = make([]base.Condition, spareRows)
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
	if shown, ok := base.Read(saved.Filter); ok {
		form.Conditions = &shown
		form.Rows = append(append([]base.Condition{}, shown.Conditions...),
			make([]base.Condition, spareRows)...)
	}
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
		Operators:    base.Operators,
		Suggestions:  s.propertyNames(c),
		Folders:      c.ProjectKeys(),
	}

	// The conditions come back whichever way the form was submitted, so a
	// rejection redraws the rows the person filled in rather than the ones the
	// file still holds.
	asked := conditionsIn(r)
	form.Conditions = &asked
	form.Rows = append(append([]base.Condition{}, asked.Conditions...),
		make([]base.Condition, spareRows)...)

	reject := func(message string) {
		form.Error = message
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "view-edit.html", c, "View", form)
	}

	// Two ways in, one file out. The form builds a `filters:` block and puts it
	// into the file that is already there — through the document, so the views,
	// formulas and display names it does not model survive being edited.
	if r.FormValue("from") == "conditions" {
		filters, err := asked.Write()
		if err != nil {
			reject(capitalise(err.Error()))
			return
		}
		if filters == "" {
			reject("A view with no conditions selects every task in the project. " +
				"Say at least one thing about what belongs on it.")
			return
		}
		rewritten, err := base.Rewrite([]byte(form.Body), filters)
		if err != nil {
			reject(capitalise(err.Error()))
			return
		}
		form.Body = string(rewritten)
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

// conditionsIn reads the rows a form sent. Rows are three parallel lists, which
// is how a browser sends repeated fields; an empty property is a row nobody
// filled in.
func conditionsIn(r *http.Request) base.Filters {
	out := base.Filters{Join: r.FormValue("join")}
	if out.Join == "" {
		out.Join = "and"
	}
	for _, folder := range r.Form["folder"] {
		if strings.TrimSpace(folder) != "" {
			out.Folders = append(out.Folders, folder)
		}
	}

	properties, operators, values := r.Form["property"], r.Form["operator"], r.Form["value"]
	for i, property := range properties {
		if strings.TrimSpace(property) == "" {
			continue
		}
		c := base.Condition{Property: property}
		if i < len(operators) {
			c.Op = operators[i]
		}
		if i < len(values) {
			c.Value = values[i]
		}
		out.Conditions = append(out.Conditions, c)
	}
	return out
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

// propertyNames is what a condition can be written about in this vault: the
// properties the format owns, and the fields the vault declared for itself.
//
// A suggestion list rather than a closed choice. A vault imported from Jira
// carries properties nobody declared — x_спринт and its like — and a form that
// refused to filter on them would be a form that cannot ask the question the
// data supports.
func (s *Server) propertyNames(c *project.Config) []string {
	seen := map[string]bool{}
	var out []string
	for name := range project.OwnedProperties {
		if !seen[name] {
			seen[name], out = true, append(out, name)
		}
	}
	for _, f := range c.Fields {
		if !seen[f.Name] {
			seen[f.Name], out = true, append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}
