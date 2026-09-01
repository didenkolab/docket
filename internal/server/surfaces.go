package server

import (
	"html/template"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/reaction"
	"github.com/vadymdidenkolab/docket/internal/space"
)

// Pages and panels drawn from what a program prints.
//
// Twenty two of the Jira marketplace's top hundred are a query and a drawing.
// The query is already a command here — `docket report … --json` — so what was
// missing was somewhere to put the drawing. This is that: a program in the
// repository prints Markdown, and it becomes a page in the navigation or a
// block on a task.
//
// Markdown rather than HTML on purpose. A program that could return HTML could
// put anything at all on a page people trust, and the vault is written in
// Markdown anyway, so its renderer is the one that already handles a wikilink.
//
// The same consent as a reaction, and for the same reason: the declaration is
// in the repository, and only a server started with --programs runs it.

// surfaceLife is how long a page may take. Shorter than a reaction's: somebody
// is looking at a blank screen, and a report that needs twenty seconds should
// be written to a file by a reaction and read from there.
const surfaceLife = 8 * time.Second

type surfacePage struct {
	Title string
	Name  string
	Body  template.HTML
	// Ran is the program, so a page that says something surprising can be
	// traced to what produced it.
	Ran string
	// Trouble is what went wrong, when something did.
	Trouble string
	// Refused says this server does not run programs, so nothing was tried.
	Refused bool
}

// handleSurface draws one declared page.
func (s *Server) handleSurface(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))

	v, surface, ok := s.surfaceNamed(name)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "No such page",
			"No project in this space declares a page called "+name+".")
		return
	}

	view := surfacePage{Title: surface.Called(), Name: surface.Name, Ran: surface.Run}
	if !s.programs {
		view.Refused = true
		s.render(w, r, "surface.html", c, view.Title, view)
		return
	}

	said, err := reaction.Output(r.Context(), v.Root, surface.Run,
		map[string]string{"surface": surface.Name, "root": v.Root}, surfaceLife)
	if err != nil {
		view.Trouble = err.Error()
		if said != "" {
			view.Trouble += "\n" + said
		}
		s.render(w, r, "surface.html", c, view.Title, view)
		return
	}
	if ix, err := s.index(); err == nil {
		view.Body = renderMarkdown(said, ix)
	}
	s.render(w, r, "surface.html", c, view.Title, view)
}

// surfaceNamed finds a declared page and the repository that declared it.
func (s *Server) surfaceNamed(name string) (*space.Vault, project.Surface, bool) {
	for _, v := range s.sp().Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		for _, surface := range c.Pages {
			if strings.EqualFold(surface.Name, name) {
				return v, surface, true
			}
		}
	}
	return nil, project.Surface{}, false
}

// surfaces is every declared page, for the navigation.
func (s *Server) surfaces() []project.Surface {
	var out []project.Surface
	for _, v := range s.sp().Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		out = append(out, c.Pages...)
	}
	return out
}

// panel is a block on a task page, drawn by a program.
type panel struct {
	Title   string
	Body    template.HTML
	Trouble string
}

// panelsFor runs the panels declared for the repository holding this task.
func (s *Server) panelsFor(r *http.Request, key string) []panel {
	if !s.programs {
		return nil
	}
	owner, at, _, err := s.sp().Locate(key)
	if err != nil || owner == nil {
		return nil
	}
	c, err := project.Load(owner.Root)
	if err != nil || len(c.Panels) == 0 {
		return nil
	}

	var out []panel
	for _, declared := range c.Panels {
		shown := panel{Title: declared.Called()}
		said, err := reaction.Output(r.Context(), owner.Root, declared.Run,
			map[string]string{
				"panel": declared.Name, "key": key,
				"path": filepath.ToSlash(at), "root": owner.Root,
			}, surfaceLife)
		switch {
		case err != nil:
			shown.Trouble = err.Error()
		case strings.TrimSpace(said) == "":
			// A panel with nothing to say says nothing. A block reading "no
			// data" on every task is a block people stop seeing.
			continue
		default:
			if ix, err := s.index(); err == nil {
				shown.Body = renderMarkdown(said, ix)
			}
		}
		out = append(out, shown)
	}
	return out
}
