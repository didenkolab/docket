package server

import (
	"net/http"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/app"
	"github.com/vadymdidenkolab/docket/internal/project"
)

// Installing an app from the browser.
//
// An app is a git repository, and installing one is pasting its URL. Doing that
// only from a terminal would mean the people who choose the tools are the
// people who have a terminal open, which is not the same set as the people who
// know what the team needs.
//
// What it does not do is make installing quieter. The page says what the app
// would change before it changes anything, and an app that brings a program
// says so in a sentence that cannot be skipped past — because the file lands
// whether or not this server ever runs it.

type appsView struct {
	Installed []project.App
	// Offered is what a pack would do, when one has been looked at but not yet
	// installed.
	Offered  *appOffer
	Saved    string
	Trouble  string
	CanWrite bool
	// Programs says this server runs what a vault declares, so a page or a
	// panel an app brings will actually draw.
	Programs bool
}

type appOffer struct {
	Source      string
	Name        string
	Version     string
	Description string
	Types       []string
	Fields      []string
	Relations   []string
	Draws       []string
	Files       []string
	Brings      bool
	Conflicts   []string
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	view := appsView{
		Saved:    r.URL.Query().Get("saved"),
		Trouble:  r.URL.Query().Get("trouble"),
		CanWrite: s.mayConfigureAnything(r),
		Programs: s.programs,
	}
	if home := s.sp().Single(); home != nil {
		if own, err := project.Load(home.Root); err == nil {
			view.Installed = own.Apps
		}
	} else {
		view.Trouble = "An app is installed into a repository, and this is a workspace of " +
			"several. Open a project on its own to give it one."
	}

	if source := strings.TrimSpace(r.FormValue("source")); source != "" && view.Installed != nil ||
		source != "" && view.Trouble == "" {
		view.Offered = s.lookAt(r, source, c)
	}
	s.render(w, r, "apps.html", c, "Apps", view)
}

// lookAt reads a pack and says what installing it would do, without doing it.
func (s *Server) lookAt(r *http.Request, source string, c *project.Config) *appOffer {
	home := s.sp().Single()
	if home == nil {
		return nil
	}

	pack, cleanup, err := app.Fetch(source)
	if err != nil {
		return &appOffer{Source: source, Conflicts: []string{err.Error()}}
	}
	defer cleanup()

	offer := &appOffer{
		Source: source, Name: pack.Name, Version: pack.Version,
		Description: pack.Description, Files: pack.Files, Brings: pack.BringsPrograms(),
	}
	for _, t := range pack.Vocabulary.Types {
		offer.Types = append(offer.Types, t.Name)
	}
	for _, f := range pack.Vocabulary.Fields {
		offer.Fields = append(offer.Fields, f.Name+" ("+f.Kind+")")
	}
	for _, rel := range pack.Vocabulary.Relations {
		if rel.Inverse != "" {
			offer.Relations = append(offer.Relations, rel.Name+" / "+rel.Inverse)
			continue
		}
		offer.Relations = append(offer.Relations, rel.Name)
	}
	for _, page := range pack.Surfaces.Pages {
		offer.Draws = append(offer.Draws, page.Called()+" (page)")
	}
	for _, panel := range pack.Surfaces.Panels {
		offer.Draws = append(offer.Draws, panel.Called()+" (panel)")
	}
	for _, conflict := range app.Check(home.Root, c, pack) {
		offer.Conflicts = append(offer.Conflicts, conflict.Error())
	}
	return offer
}

// handleAppInstall installs one, after the page has said what it would do.
func (s *Server) handleAppInstall(w http.ResponseWriter, r *http.Request) {
	back := func(key, message string) {
		http.Redirect(w, r, "/apps?"+key+"="+urlEscape(message), http.StatusSeeOther)
	}
	if !s.mayConfigureAnything(r) {
		s.refuse(w, r, "Installing an app changes the vocabulary for everybody, so it needs "+
			"administrator access.")
		return
	}
	home := s.sp().Single()
	if home == nil {
		back("trouble", "An app is installed into a repository, and this is a workspace.")
		return
	}
	source := strings.TrimSpace(r.FormValue("source"))
	if source == "" {
		back("trouble", "Paste the app's URL.")
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	c, err := project.Load(home.Root)
	if err != nil {
		back("trouble", err.Error())
		return
	}
	pack, cleanup, err := app.Fetch(source)
	if err != nil {
		back("trouble", err.Error())
		return
	}
	defer cleanup()

	changed, err := app.Install(home.Root, c, pack)
	if err != nil {
		back("trouble", err.Error())
		return
	}
	if len(changed) == 0 {
		back("saved", pack.Name+" was already installed, unchanged.")
		return
	}

	// Committed here, unlike the command, which prints what it changed and
	// leaves it: a browser has nowhere to show a diff, and an uncommitted
	// change nobody can see is a change that gets pushed by the next write
	// under somebody else's message.
	if err := s.commit(r, changed, "Installed the app "+pack.Name, s.authorFor(r)); err != nil {
		back("trouble", "Installed, but not committed: "+err.Error())
		return
	}
	if err := s.reload(); err != nil {
		back("trouble", "Installed, but the vault cannot be reopened: "+err.Error())
		return
	}

	said := pack.Name + " is installed — " + plural(len(changed), "file", "files") + " changed."
	if pack.BringsPrograms() && !s.programs {
		said += " It draws with a program, and this server was not started with --programs, " +
			"so its pages will say so rather than run it."
	}
	back("saved", said)
}
