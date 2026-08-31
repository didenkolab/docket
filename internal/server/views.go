package server

import (
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/base"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Saved views — the boards/*.base files, drawn.
//
// The question a board answers is written in the vault, in the language
// Obsidian already evaluates, in a file that is in git and reviewed in a
// proposal like anything else. Drawing those files here is what makes the two
// views of the vault one thing: change the filter in Obsidian and the web board
// changes; change it here and Obsidian's does.
//
// What this will not do is guess. Bases is larger than the reader in
// internal/base, and a view whose filter cannot be evaluated is listed saying
// so rather than drawn from the half that parsed.

type viewCard struct {
	Key   string
	Href  string
	Title string
	// Cells are the properties the view asked for, in its order.
	Cells []cell
}

type cell struct {
	Label string
	Value string
}

type viewGroup struct {
	Name  string
	Cards []viewCard
}

type viewPage struct {
	Note    string
	Path    string
	Project string
	Filter  string
	// Views are the shapes the file offers; Showing is the one being drawn.
	Views   []string
	Showing string
	// GroupBy is the property the columns are, empty for a table.
	GroupBy string
	Groups  []viewGroup
	Rows    []viewCard
	Columns []string
	Total   int
	// Formulas names the columns Obsidian computes and this does not, so a
	// blank column is explained rather than merely blank.
	Formulas []string
}

type viewListing struct {
	Note     string
	Href     string
	EditHref string
	Project  string
	Filter   string
	Views    string
	Trouble  string
}

// handleViews lists the saved views in the space.
func (s *Server) handleViews(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	var out []viewListing
	for _, v := range s.sp().Views() {
		listed := viewListing{
			Note:     v.Note,
			Href:     "/view/" + escapePath(v.Path),
			EditHref: "/views/edit/" + escapePath(v.Path),
			Project:  repositoryOf(v.Vault),
			Trouble:  v.Trouble,
		}
		if v.Trouble == "" {
			if v.Filter != nil {
				listed.Filter = v.Filter.String()
			}
			var names []string
			for _, view := range v.Views {
				names = append(names, view.Name)
			}
			listed.Views = strings.Join(names, " · ")
		}
		out = append(out, listed)
	}

	s.render(w, r, "views.html", c, "Views", out)
}

// handleView draws one.
func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	at := strings.TrimPrefix(r.PathValue("path"), "/")
	saved, ok := s.sp().ViewAt(at)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "No such view",
			at+" is not a saved view in this space. The views are the .base files "+
				"in each project's boards/ folder.")
		return
	}
	if saved.Trouble != "" {
		s.fail(w, r, http.StatusUnprocessableEntity, "This view cannot be drawn here",
			capitalise(saved.Trouble)+". Obsidian may still draw it: it evaluates the "+
				"whole of Bases, and this reads the part a board uses.")
		return
	}

	view, ok := pick(saved.Base, r.URL.Query().Get("view"))
	if !ok {
		s.fail(w, r, http.StatusNotFound, "No such view in that file",
			"The file offers "+strings.Join(viewNames(saved.Base), ", ")+".")
		return
	}

	entries, err := s.entries(r)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the tasks", err.Error())
		return
	}

	page := viewPage{
		Note: saved.Note, Path: saved.Path, Project: repositoryOf(saved.Vault),
		Showing: view.Name, GroupBy: saved.Displayed(view.GroupBy),
		Views: viewNames(saved.Base),
	}
	if saved.Filter != nil {
		page.Filter = saved.Filter.String()
	}

	// A view file belongs to its own repository, so it selects that
	// repository's tasks. A filter naming a folder means the folder in the
	// project it was written in, not one of the same name next door.
	prefix := saved.Vault.Prefix

	// What a view asks to show, read for this note. A property may be written
	// with its note. prefix or without — Bases takes both, and the boards docket
	// generates use both in one file.
	valueOf := func(n base.Note, property string) string {
		if strings.HasPrefix(property, "formula.") {
			computed, _ := saved.Compute(property, n)
			return computed
		}
		switch property {
		case "file.name":
			return strings.TrimSuffix(path.Base(n.Path), ".md")
		case "file.folder":
			return path.Dir(n.Path)
		}
		return n.Values[base.Property(property)]
	}

	var wanted []string
	for _, property := range view.Order {
		if strings.HasPrefix(property, "formula.") {
			// A formula the reader could not parse is named rather than drawn
			// as a column of blanks.
			if _, ok := saved.Formulas[strings.TrimPrefix(property, "formula.")]; !ok {
				page.Formulas = append(page.Formulas, saved.Displayed(property))
				continue
			}
		}
		// The key and the title are the row itself — the key is what it links
		// by and the title is what it says. A view that lists them among its
		// properties, as every generated one does, would otherwise draw each of
		// them twice.
		if name := base.Property(property); name == "key" || name == "title" || name == "file.name" {
			continue
		}
		wanted = append(wanted, property)
		page.Columns = append(page.Columns, saved.Displayed(property))
	}

	grouped := map[string][]viewCard{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		note, ok := noteOf(e, prefix)
		if !ok {
			continue
		}
		if !saved.Matches(note) || (view.Filter != nil && !view.Filter.Match(note)) {
			continue
		}

		drawn := viewCard{Key: e.Key, Href: "/task/" + e.Key, Title: e.Task.Title}
		for i, property := range wanted {
			drawn.Cells = append(drawn.Cells, cell{
				Label: page.Columns[i], Value: valueOf(note, property),
			})
		}
		page.Total++

		if view.GroupBy == "" {
			page.Rows = append(page.Rows, drawn)
			continue
		}
		grouped[unlinked(valueOf(note, view.GroupBy))] = append(
			grouped[unlinked(valueOf(note, view.GroupBy))], drawn)
	}

	if view.GroupBy != "" {
		var names []string
		for name := range grouped {
			names = append(names, name)
		}
		sort.Strings(names)
		if strings.EqualFold(view.Direction, "DESC") {
			sort.Sort(sort.Reverse(sort.StringSlice(names)))
		}
		for _, name := range names {
			shown := name
			if shown == "" {
				shown = "—"
			}
			page.Groups = append(page.Groups, viewGroup{Name: shown, Cards: grouped[name]})
		}
	}

	s.render(w, r, "view.html", c, saved.Note, page)
}

// pick is the view being asked for, or the first one the file offers.
func pick(b *base.Base, named string) (base.View, bool) {
	if len(b.Views) == 0 {
		return base.View{}, false
	}
	if named == "" {
		return b.Views[0], true
	}
	for _, v := range b.Views {
		if strings.EqualFold(v.Name, named) {
			return v, true
		}
	}
	return base.View{}, false
}

func viewNames(b *base.Base) []string {
	var out []string
	for _, v := range b.Views {
		out = append(out, v.Name)
	}
	return out
}

// noteOf is one task as a filter sees it, with the path made relative to the
// repository the view lives in — and skipped when it is in another one.
func noteOf(e vault.Entry, prefix string) (base.Note, bool) {
	at := e.Path
	if prefix != "" {
		if !strings.HasPrefix(at, prefix+"/") {
			return base.Note{}, false
		}
		at = strings.TrimPrefix(at, prefix+"/")
	}
	values, lists := e.Task.Frontmatter()
	return base.Note{Path: at, Values: values, Lists: lists}, true
}

// unlinked is a wikilink read as what it points at, so a column of sprints is
// one column per sprint rather than one per spelling.
func unlinked(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[[") || !strings.HasSuffix(value, "]]") {
		return value
	}
	inside := value[2 : len(value)-2]
	if bar := strings.Index(inside, "|"); bar >= 0 {
		inside = inside[:bar]
	}
	return strings.TrimSpace(inside)
}

// projectOf names the repository a view belongs to. Empty for a space of one,
// where saying which repository would be saying the only thing there is.
func repositoryOf(v *space.Vault) string {
	if v == nil {
		return ""
	}
	return v.Prefix
}

// escapePath escapes each segment, so a folder name with a space in it stays
// one path.
func escapePath(at string) string {
	parts := strings.Split(at, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
