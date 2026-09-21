package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
	"github.com/didenkolab/docket/internal/workspace"
)

// Making a project that does not exist yet.
//
// Connecting a repository assumes there is one. A new project has nowhere to
// live, and the useful thing is to make that somewhere rather than to send
// somebody to a host's web form, come back, and paste a URL.
//
// Four steps, in this order, and the order is the whole design:
//
//  1. Ask the host for an empty repository. If this fails nothing local has
//     happened, which is the cheapest place to fail.
//  2. Scaffold into the workspace from the template — the same docket init does,
//     so a project made here and a project made from a terminal are the same
//     thing.
//  3. Commit and push. The scaffold is the first commit, so the repository's
//     history starts with what the project is.
//  4. Add it to the manifest.
//
// Nothing is half-done on the way: a failure after the host has made the
// repository leaves the repository — which is why the message says so and says
// where it is, rather than pretending the whole thing did not happen. Deleting
// somebody's new repository to tidy up after ourselves would be worse than
// telling them it is there.

// createView is the part of the Projects page that makes one.
type createView struct {
	// Hosts are the hosts a project can be made on: those that can be asked,
	// and that somebody is signed into or the machine has a credential for.
	Hosts []createHost
}

type createHost struct {
	Key   string
	Name  string
	Scope string
}

// creatableHosts is where a new project could go.
func (s *Server) creatableHosts() []createHost {
	var out []createHost
	seen := map[string]bool{}
	for _, repo := range s.repositories() {
		if repo.host == nil || seen[repo.hostKey] {
			continue
		}
		creator, ok := repo.host.(access.Creator)
		if !ok {
			continue
		}
		seen[repo.hostKey] = true
		out = append(out, createHost{
			Key: repo.hostKey, Name: repo.host.Name(), Scope: creator.CreateScope(),
		})
	}
	return out
}

// handleCreateProject makes a repository, scaffolds a vault into it, pushes it
// and adds it to the workspace.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
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
		s.refuse(w, r, "Making a project changes the workspace for everybody, so it needs "+
			"administrator access to a repository already in it.")
		return
	}

	// Not upper-cased for whoever typed it. A key names a folder, heads every
	// task key in the project and appears in every link to one, so it is taken
	// as given or refused — quietly changing `acme` into `ACME` is how a vault
	// ends up with two spellings of the same thing.
	key := strings.TrimSpace(r.FormValue("key"))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = key
	}
	if err := project.ValidKey(key); err != nil {
		fail(err.Error())
		return
	}

	host, creator, ok := s.creator(r.FormValue("host"))
	if !ok {
		fail("Say which host to make it on.")
		return
	}
	token, ok := s.tokenFor(r, host.HostName())
	if !ok {
		fail("Nothing here can prove to " + host.Name() + " who is asking. Sign into " +
			host.Name() + " first, or make the repository there and add it with its URL.")
		return
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	made, err := creator.Create(r.Context(), token, access.NewRepository{
		Name:        strings.TrimSpace(r.FormValue("repository")),
		Owner:       strings.TrimSpace(r.FormValue("owner")),
		Description: strings.TrimSpace(r.FormValue("description")),
		Private:     r.FormValue("visibility") != "public",
	})
	if err != nil {
		fail(err.Error() + ". A token needs the " + creator.CreateScope() + " scope on " +
			host.Name() + " to make a repository; the one signing you in may be narrower.")
		return
	}

	if err := s.scaffold(r, sp.Root, key, name, made, host); err != nil {
		fail(err.Error() + " The repository exists on " + host.Name() + " — " + made.Web +
			" — and is empty. Push to it by hand, or add it here once it is a vault.")
		return
	}
	if err := s.reload(); err != nil {
		fail("Made it, but the workspace cannot be reopened: " + err.Error())
		return
	}

	http.Redirect(w, r, "/projects?saved="+urlEscape(
		key+" is a new repository on "+host.Name()+" at "+made.FullName+
			", scaffolded and pushed."), http.StatusSeeOther)
}

// scaffold puts a vault in the workspace, points it at the new repository and
// pushes the first commit.
func (s *Server) scaffold(r *http.Request, root, key, name string,
	made access.Created, host access.Host) error {

	dir := directoryFor(made.Remote)
	target := filepath.Join(root, dir)
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("%s already exists in the workspace.", dir)
	}

	// The same scaffold docket init writes, from the same template, so a project
	// made here is not a second kind of project.
	if _, err := vault.Init(target, vault.Options{
		Key: key, Name: name, Template: s.template,
	}); err != nil {
		return fmt.Errorf("cannot scaffold it: %w.", err)
	}

	repo, err := s.startRepository(target, made.Remote)
	if err != nil {
		_ = os.RemoveAll(target)
		return err
	}

	author := s.authorFor(r)
	message := key + ": a vault for tasks and pages, kept as files in git\n\n" +
		"Made by docket from " + s.template + ". AGENTS.md is how an agent works in here;\n" +
		"docket.yaml is the projects this vault holds and the vocabulary they share."
	if err := repo.Commit([]string{"."}, message, author); err != nil {
		return fmt.Errorf("scaffolded it but cannot commit: %w.", err)
	}

	cred := gitvcs.Credential{}
	if token, ok := s.tokenFor(r, host.HostName()); ok {
		cred = gitvcs.Credential{User: host.GitUser(), Token: token}
	}
	if err := repo.PushNew(cred); err != nil {
		return fmt.Errorf("scaffolded and committed it but cannot push: %w.", err)
	}

	m, err := workspace.Load(root)
	if err != nil {
		return err
	}
	if err := m.Add(workspace.Project{Key: key, Path: dir, Remote: made.Remote}); err != nil {
		return err
	}
	return m.Save(root)
}

// startRepository makes the directory a repository pointed at its remote.
func (s *Server) startRepository(dir, remote string) (*gitvcs.Repo, error) {
	if err := gitvcs.Start(dir, remote); err != nil {
		return nil, fmt.Errorf("cannot start a repository in it: %w.", err)
	}
	repo, err := gitvcs.Open(dir)
	if err != nil {
		return nil, err
	}
	return repo, nil
}

// creator is the host named, if it can be asked for a repository.
func (s *Server) creator(named string) (access.Host, access.Creator, bool) {
	named = strings.ToLower(strings.TrimSpace(named))
	hosts := s.creatableHosts()
	if named == "" && len(hosts) == 1 {
		named = hosts[0].Key
	}
	if s.auth == nil {
		return nil, nil, false
	}
	host, _, ok := s.auth.hostFor(named)
	if !ok {
		return nil, nil, false
	}
	creator, ok := host.(access.Creator)
	return host, creator, ok
}

// tokenFor is the token to speak to a host with: the signed-in person's, or the
// one on this machine.
//
// The machine's is a real answer rather than a fallback: a board run over
// somebody's own clone is run by somebody whose git already talks to that host,
// and a token that can push is usually a token that can create.
func (s *Server) tokenFor(r *http.Request, hostName string) (string, bool) {
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			if current, ok := s.auth.lookup(cookie.Value); ok {
				if token, held := current.tokenFor(strings.ToLower(hostName)); held {
					return token, true
				}
			}
		}
	}
	if !s.onLoopback {
		return "", false
	}
	token, err := access.LocalToken(r.Context(), hostName)
	if err != nil || token == "" {
		return "", false
	}
	return token, true
}
