package server

import (
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
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
	// Buttons are what this page can be asked to do.
	Buttons []actionButton
	// Did is what the last action printed, when one has just run.
	Did     string
	DidName string
	Wrote   []string
	Failed  bool
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

	view := surfacePage{
		Title: surface.Called(), Name: surface.Name, Ran: surface.Run,
		Buttons: s.buttonsOn(surface.Name),
		Did:     r.URL.Query().Get("said"), DidName: r.URL.Query().Get("did"),
		Failed: r.URL.Query().Get("failed") == "1",
	}
	if !s.programs {
		view.Refused = true
		s.render(w, r, "surface.html", c, view.Title, view)
		return
	}

	said, err := reaction.Output(r.Context(), v.Root, surface.Run,
		map[string]string{"surface": surface.Name, "root": v.Root}, surfaceLife,
		s.whereEnv(v))
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
			}, surfaceLife, s.whereEnv(owner))
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

// ---- doing something ----

type actionButton struct {
	Name    string
	Title   string
	Confirm string
}

// buttonsOn is what this page can be asked to do.
func (s *Server) buttonsOn(page string) []actionButton {
	var out []actionButton
	for _, v := range s.sp().Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		for _, a := range c.Actions {
			if a.On != "" && !strings.EqualFold(a.On, page) {
				continue
			}
			out = append(out, actionButton{Name: a.Name, Title: a.Called(), Confirm: a.Confirm})
		}
	}
	return out
}

// actionNamed finds a declared action and the repository that declared it.
func (s *Server) actionNamed(name string) (*space.Vault, project.Action, bool) {
	for _, v := range s.sp().Vaults() {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		for _, a := range c.Actions {
			if strings.EqualFold(a.Name, name) {
				return v, a, true
			}
		}
	}
	return nil, project.Action{}, false
}

// handleAction runs one, and commits what it wrote.
//
// The one thing an app can do rather than draw, so it is held to everything a
// reaction is held to and one thing more: a person asked for it, at a moment,
// and the page says what came back. A button that runs something and then shows
// the page it was on, unchanged, is a button nobody trusts twice.
func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	page := strings.TrimSpace(r.FormValue("page"))
	back := func(said string, failed bool) {
		where := "/app/" + url.PathEscape(page) + "?did=" + url.QueryEscape(name) +
			"&said=" + url.QueryEscape(said)
		if failed {
			where += "&failed=1"
		}
		http.Redirect(w, r, where, http.StatusSeeOther)
	}

	v, action, ok := s.actionNamed(name)
	if !ok {
		s.fail(w, r, http.StatusNotFound, "No such action",
			"No project in this space declares an action called "+name+".")
		return
	}
	if !s.programs {
		back("This server was not started with --programs, so nothing ran.", true)
		return
	}
	if !s.mayWriteTo(r, v) {
		s.refuse(w, r, "Running an app's action can change the vault, so it needs write access.")
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	before := dirtyIn(v.Root)
	said, err := reaction.Output(r.Context(), v.Root, action.Run,
		map[string]string{"action": action.Name, "root": v.Root}, actionLife, s.whereEnv(v))
	after := dirtyIn(v.Root)

	var wrote []string
	for path := range after {
		if _, was := before[path]; !was {
			wrote = append(wrote, path)
		}
	}
	sort.Strings(wrote)

	if len(wrote) > 0 {
		if commitErr := v.Repo.Commit(wrote, action.Called()+": "+
			plural(len(wrote), "file", "files")+" written", s.authorFor(r)); commitErr != nil {
			said += "\n\nWritten but not committed: " + commitErr.Error()
		} else {
			s.after(r, v)
		}
	}
	if err != nil {
		back(strings.TrimSpace(said+"\n\n"+err.Error()), true)
		return
	}
	back(said, false)
}

// actionLife is how long a person will wait with a page open. Longer than a
// page that only draws, because this one is doing something they asked for —
// and still bounded, because a browser gives up on its own.
const actionLife = 3 * time.Minute

// whereEnv tells a program where its vault sits in the space.
//
// An app writing a picture into its own attachments folder has to link to it,
// and in a workspace that link carries the project in front of the path. The
// alternative is every app guessing, and every app guessing differently.
func (s *Server) whereEnv(v *space.Vault) string {
	prefix := ""
	if v != nil && v.Prefix != "" {
		prefix = v.Prefix + "/"
	}
	return "DOCKET_PREFIX=" + prefix
}
