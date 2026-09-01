package server

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// Connecting a repository that already exists.
//
// A team's projects are not all in one place: one on GitHub, a private one on
// their own GitLab. Adding the second to a board meant editing workspace.yaml
// by hand and cloning in a terminal, which is a strange thing to ask of an
// interface that otherwise writes git for you.
//
// So: paste the remote, and the server clones it, checks it is a vault, and adds
// it to the manifest. What it deliberately does not do is create anything — a
// repository that is not a docket vault is refused with the command that would
// make it one, rather than being scaffolded over. Cloning is enough of a side
// effect for one button.
//
// Only in a workspace. A vault opened on its own has no manifest to add to, and
// inventing one would move somebody's files around underneath them; the page
// says so and says which command makes a workspace.

// connectView is the page that adds a project.
type connectView struct {
	// Workspace says this space can hold more projects at all.
	Workspace bool
	Root      string
	Projects  []connectRow
	Error     string
	Saved     string
	// Create is where a new project could be made, empty when nowhere can be
	// asked for one.
	Create createView
}

// connectRow is a project already here, so the page shows what it is adding to.
type connectRow struct {
	Key    string
	Path   string
	Remote string
	Host   string
	// Manifest is the key the manifest calls this project, which is what taking
	// it out asks for. It is not always the key inside the vault: a repository
	// may hold several projects, and the workspace knows it by one name.
	Manifest string
	// Unsent is how many commits here have never left the folder, so the page
	// can say what deleting the clone would cost before anybody clicks.
	Unsent int
	Dirty  bool
}

func (s *Server) handleConnectForm(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()
	// What the last change said. Every write here redirects rather than
	// rendering, so that a reload does not repeat it — and until this read the
	// message it redirected with was thrown away, which meant adding a project
	// looked exactly like doing nothing.
	s.render(w, r, "connect.html", c, "Add a project", s.connectPage(r,
		r.URL.Query().Get("trouble"), r.URL.Query().Get("saved")))
}

func (s *Server) connectPage(r *http.Request, problem, saved string) connectView {
	sp := s.sp()
	view := connectView{
		Workspace: sp.Workspace,
		Root:      sp.Root,
		Error:     problem,
		Saved:     saved,
	}
	if !sp.Workspace {
		return view
	}
	view.Create.Hosts = s.creatableHosts()

	byPath := map[string]string{}
	if m, err := workspace.Load(sp.Root); err == nil {
		for _, p := range m.Projects {
			byPath[strings.Trim(p.Path, "/")] = p.Key
		}
	}

	for _, v := range sp.Vaults() {
		row := connectRow{Path: v.Prefix, Manifest: byPath[strings.Trim(v.Prefix, "/")]}
		if c, err := project.Load(v.Root); err == nil {
			row.Key = strings.Join(c.ProjectKeys(), ", ")
		}
		if v.Repo != nil {
			row.Remote, _ = remoteOf(v.Root)
			// What would be lost. Asked here rather than at the moment of
			// deleting, so the page can say it before the click rather than
			// refuse after it.
			row.Unsent, _ = v.Repo.Unpushed()
			row.Dirty, _ = v.Repo.Dirty()
		}
		for _, repo := range s.repositories() {
			if repo.prefix == v.Prefix && repo.host != nil {
				row.Host = repo.host.Name()
			}
		}
		view.Projects = append(view.Projects, row)
	}
	return view
}

// repositories is the authority's list, or nothing when nobody signs in.
func (s *Server) repositories() []*repository {
	if s.auth == nil {
		return nil
	}
	return s.auth.repositories()
}

// handleConnect clones a repository into the workspace and adds it.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	fail := func(message string) {
		c, _ := s.config()
		w.WriteHeader(http.StatusBadRequest)
		s.render(w, r, "connect.html", c, "Add a project", s.connectPage(r, message, ""))
	}

	sp := s.sp()
	if !sp.Workspace {
		fail("This is a single vault, which has no manifest to add a project to. " +
			"Make a workspace with `docket workspace` and serve that instead.")
		return
	}
	if !s.mayConfigureAnything(r) {
		s.refuse(w, r, "Adding a project changes the workspace for everybody, so it needs "+
			"administrator access to a repository already in it.")
		return
	}

	remote := strings.TrimSpace(r.FormValue("remote"))
	if remote == "" {
		fail("Paste the repository's URL — whatever you would give `git clone`.")
		return
	}
	if err := usableRemote(remote); err != nil {
		fail(err.Error())
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	added, err := s.clone(r, sp, remote)
	if err != nil {
		fail(err.Error())
		return
	}
	if err := s.reload(); err != nil {
		fail("Cloned it, but the workspace cannot be reopened: " + err.Error())
		return
	}

	http.Redirect(w, r, "/projects?saved="+urlEscape(
		added.Key+" is in the workspace, cloned into "+added.Path+"."), http.StatusSeeOther)
}

// connectDirect is the whole operation without a request: clone, check, record,
// reload. The handler is this plus who is asking and what to say back.
func (s *Server) connectDirect(remote string) error {
	s.writes.Lock()
	defer s.writes.Unlock()

	if _, err := s.clone(nil, s.sp(), remote); err != nil {
		return err
	}
	return s.reload()
}

// clone brings the repository in and records it, or changes nothing.
//
// The order matters: clone first, look at what arrived, and only then touch the
// manifest. A manifest naming a project that failed to clone is a workspace
// that reports itself broken every time it is opened.
func (s *Server) clone(r *http.Request, sp *space.Space, remote string) (workspace.Project, error) {
	m, err := workspace.Load(sp.Root)
	if err != nil {
		return workspace.Project{}, err
	}

	dir := directoryFor(remote)
	target := filepath.Join(sp.Root, dir)
	if _, err := os.Stat(target); err == nil {
		return workspace.Project{}, fmt.Errorf("%s already exists in the workspace — "+
			"if that is this repository, it is already here; if not, rename one of them", dir)
	}

	if err := s.gitClone(r, sp.Root, remote, dir); err != nil {
		return workspace.Project{}, err
	}

	// What arrived has to be a vault. Nothing is scaffolded over it: a
	// repository that is not one is somebody else's repository, and the
	// interface says how to make it one rather than doing it.
	c, err := project.Load(target)
	if err != nil {
		_ = os.RemoveAll(target)
		return workspace.Project{}, fmt.Errorf("%s is not a docket vault: %w. "+
			"Run `docket init --key SOMEKEY` in it first, then add it here", remote, err)
	}
	keys := c.ProjectKeys()
	if len(keys) == 0 {
		_ = os.RemoveAll(target)
		return workspace.Project{}, fmt.Errorf("%s holds no projects", remote)
	}

	// A key is a folder name and the head of every task key in it, so two
	// projects cannot share one.
	taken := map[string]bool{}
	for _, v := range sp.Vaults() {
		if existing, err := project.Load(v.Root); err == nil {
			for _, key := range existing.ProjectKeys() {
				taken[key] = true
			}
		}
	}
	for _, key := range keys {
		if taken[key] {
			_ = os.RemoveAll(target)
			return workspace.Project{}, fmt.Errorf("this workspace already holds a project "+
				"called %s, and a key cannot be in it twice — it is a folder name and the "+
				"head of every task key in it", key)
		}
	}

	entry := workspace.Project{Key: keys[0], Path: dir, Remote: remote}
	if err := m.Add(entry); err != nil {
		_ = os.RemoveAll(target)
		return workspace.Project{}, err
	}
	if err := m.Save(sp.Root); err != nil {
		_ = os.RemoveAll(target)
		return workspace.Project{}, err
	}
	return entry, nil
}

// gitClone clones with whatever credential is available.
//
// The signed-in person's token for that host when there is one, and otherwise
// the machine's own credential helper — which is what makes this work for a
// private repository on a host nobody has signed into yet, since a host that is
// not in the workspace cannot have been signed into.
func (s *Server) gitClone(r *http.Request, root, remote, dir string) error {
	cred := s.cloneCredential(r, remote)

	args := append(gitvcs.PushArgs(cred), "clone", "--quiet", remote, dir)
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(cmd.Environ(), gitvcs.PushEnv(cred)...)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("cannot clone %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

// cloneCredential is the token for the remote's host, when somebody signed in
// there. A remote on a host not in this workspace has none, and git falls back
// to the machine's own helper.
func (s *Server) cloneCredential(r *http.Request, remote string) gitvcs.Credential {
	if s.auth == nil || r == nil {
		return gitvcs.Credential{}
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return gitvcs.Credential{}
	}
	current, ok := s.auth.lookup(cookie.Value)
	if !ok {
		return gitvcs.Credential{}
	}
	host := hostOfRemote(remote)
	for _, repo := range s.repositories() {
		if repo.host == nil || !strings.EqualFold(repo.hostKey, host) {
			continue
		}
		if token, held := current.tokenFor(repo.hostKey); held {
			return gitvcs.Credential{User: repo.host.GitUser(), Token: token}
		}
	}
	return gitvcs.Credential{}
}

// reload reopens the space and rebuilds who vouches for what.
//
// Both, because a project nobody has been asked about is a project nobody may
// see: standing is worked out per repository, and one that is not in the list
// has no role in it. Sessions and cached answers survive — see authority.adopt.
func (s *Server) reload() error {
	sp, err := space.Open(s.sp().Root)
	if err != nil {
		return err
	}
	if s.auth != nil {
		repos, err := newRepositories(sp, s.namedHost, s.recheck)
		if err != nil {
			return err
		}
		s.auth.adopt(repos)
	}
	s.space.Store(sp)
	return nil
}

// mayConfigureAnything reports whether the person asking administers something
// here, which is what adding a project takes.
func (s *Server) mayConfigureAnything(r *http.Request) bool {
	st := standingIn(r)
	return st == nil || st.canConfigureAnything()
}

/* ---------- reading a remote ---------- */

// usableRemote refuses what git would not clone, and what a server should not
// be asked to fetch.
func usableRemote(remote string) error {
	if strings.HasPrefix(remote, "-") {
		return errors.New("that is not a URL")
	}
	// A local path is a legitimate remote for git and a bad idea here: it would
	// let whoever can reach this page copy any directory the server can read
	// into the workspace.
	if strings.HasPrefix(remote, "/") || strings.HasPrefix(remote, ".") ||
		strings.HasPrefix(remote, "file://") {
		return errors.New("a path on the server is not something to clone from here — " +
			"give an https or ssh URL")
	}
	if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return fmt.Errorf("that URL cannot be read: %w", err)
		}
		switch u.Scheme {
		case "https", "ssh", "git":
		default:
			return fmt.Errorf("%s is not a scheme to clone over — use https or ssh", u.Scheme)
		}
		if u.Host == "" {
			return errors.New("that URL names no host")
		}
		return nil
	}
	// scp-like: git@host:owner/name.git
	if strings.Contains(remote, "@") && strings.Contains(remote, ":") {
		return nil
	}
	return errors.New("that does not look like a repository URL")
}

// hostOfRemote is the host a remote is on, in either of git's two spellings.
func hostOfRemote(remote string) string {
	if strings.Contains(remote, "://") {
		if u, err := url.Parse(remote); err == nil {
			return strings.ToLower(u.Hostname())
		}
		return ""
	}
	if at := strings.Index(remote, "@"); at >= 0 {
		rest := remote[at+1:]
		if colon := strings.Index(rest, ":"); colon >= 0 {
			return strings.ToLower(rest[:colon])
		}
		return strings.ToLower(rest)
	}
	return ""
}

// directoryFor is where to put a clone: the repository's own name, which is
// what `git clone` would have chosen.
func directoryFor(remote string) string {
	trimmed := strings.TrimSuffix(strings.TrimRight(remote, "/"), ".git")
	if slash := strings.LastIndexAny(trimmed, "/:"); slash >= 0 {
		trimmed = trimmed[slash+1:]
	}
	// Nothing that could climb out of the workspace or hide.
	trimmed = strings.TrimLeft(filepath.Base(trimmed), ".")
	if trimmed == "" {
		return "project"
	}
	return trimmed
}

// remoteOf is a repository's origin, for showing on the page.
func remoteOf(dir string) (string, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}
