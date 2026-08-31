package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// A workspace whose repositories are on different hosts is what turned this
// up. Before, the server asked one host — whichever repository happened to be
// first — and checked permissions by URL path, so write access to one
// repository was write access to all of them.

// twoHosts is a host that vouches for one repository and knows one token.
type twoHosts struct {
	hostName string
	repo     string
	// grants maps a token to the role it buys here. A token that is not in it
	// is a token this host will not vouch for, which is what a person with
	// access to the other repository looks like.
	grants map[string]string
}

func (h *twoHosts) Name() string        { return h.hostName }
func (h *twoHosts) HostName() string    { return h.hostName }
func (h *twoHosts) Repository() string  { return h.repo }
func (h *twoHosts) SettingsURL() string { return "https://" + h.hostName + "/" + h.repo }

func (h *twoHosts) Identify(_ context.Context, token string) (access.Identity, error) {
	role, ok := h.grants[token]
	if !ok {
		return access.Identity{}, fmt.Errorf("%s: %s is not yours", h.hostName, h.repo)
	}
	return access.Identity{
		Login: token + "@" + h.hostName, Name: strings.ToUpper(token),
		Email: token + "@example.com", Role: role,
	}, nil
}

func (h *twoHosts) Collaborators(context.Context, string) ([]access.Collaborator, error) {
	return nil, fmt.Errorf("only an administrator may ask")
}

// workspaceOnTwoHosts builds ONE and TWO in separate repositories, hands each a
// host of its own, and returns a server over both.
func workspaceOnTwoHosts(t *testing.T) (*Server, http.Handler, *twoHosts, *twoHosts) {
	t.Helper()

	root := t.TempDir()
	template := vaulttest.Template(t)
	for _, key := range []string{"ONE", "TWO"} {
		dir := filepath.Join(root, strings.ToLower(key))
		if _, err := vault.Init(dir, vault.Options{Key: key, Name: key + " Platform",
			Template: template}); err != nil {
			t.Fatalf("init %s: %v", key, err)
		}
		c, err := project.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := vault.Create(dir, c, vault.NewOptions{
			Project: key, Title: "A task in " + key, Assignee: "somebody",
		}); err != nil {
			t.Fatalf("create in %s: %v", key, err)
		}
		gitInit(t, dir, remotes[key])
	}
	writeManifest(t, root)

	sp, err := space.Open(root)
	if err != nil {
		t.Fatalf("open workspace: %v", err)
	}
	if len(sp.Vaults()) != 2 {
		t.Fatalf("workspace holds %d vaults", len(sp.Vaults()))
	}

	// dana works on ONE; sam works on TWO. Neither host has heard of the other's
	// person, which is exactly the situation this is about.
	first := &twoHosts{hostName: "first.example.com", repo: "acme/one",
		grants: map[string]string{"dana": access.RoleAdmin}}
	second := &twoHosts{hostName: "second.example.com", repo: "acme/two",
		grants: map[string]string{"sam": access.RoleMember}}

	s, err := New(root, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Recheck:     time.Nanosecond,
		SessionLife: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The hosts are assigned per repository, standing in for what a remote and
	// a docket.yaml would have resolved to.
	if len(s.auth.repos) != 2 {
		t.Fatalf("server built %d repositories", len(s.auth.repos))
	}
	for _, repo := range s.auth.repos {
		host := first
		if repo.projects[0] == "TWO" {
			host = second
		}
		repo.host = host
		repo.hostKey = host.HostName()
		repo.checker = access.NewChecker(host, time.Nanosecond)
	}
	return s, s.Handler(), first, second
}

// remotes are where each project pretends to live. The host is resolved from
// the repository's own remote — not from the manifest — because the repository
// is what a project is.
var remotes = map[string]string{
	"ONE": "https://github.com/acme/one.git",
	"TWO": "https://gitlab.com/acme/two.git",
}

func gitInit(t *testing.T, dir, remote string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"remote", "add", "origin", remote},
		{"add", "-A"},
		{"commit", "-q", "-m", "start"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func writeManifest(t *testing.T, root string) {
	t.Helper()
	// Remotes on two hosts a hostname is enough to recognise, so the server
	// resolves a host for each project the way it would in life. The test then
	// swaps in stubs, because what is being tested is the model rather than
	// either host's API.
	body := "projects:\n" +
		"  - key: ONE\n    path: one\n    remote: " + remotes["ONE"] + "\n" +
		"  - key: TWO\n    path: two\n    remote: " + remotes["TWO"] + "\n"
	if err := os.WriteFile(filepath.Join(root, workspace.FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// signInTo signs into one host by name, which is what a workspace requires.
func signInTo(t *testing.T, h http.Handler, cookie *http.Cookie, hostKey, token string) *http.Cookie {
	t.Helper()
	w := as(t, h, cookie, "POST", "/sign-in", url.Values{"host": {hostKey}, "token": {token}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("sign into %s as %s: code = %d; body:\n%s", hostKey, token, w.Code, w.Body)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	if cookie != nil {
		return cookie
	}
	t.Fatalf("sign into %s set no session cookie", hostKey)
	return nil
}

// The hole, stated as a test: write access to one repository must not be write
// access to a repository on another host.
func TestAccessToOneRepositoryIsNotAccessToAnother(t *testing.T) {
	_, h, first, _ := workspaceOnTwoHosts(t)
	dana := signInTo(t, h, nil, first.HostName(), "dana")

	// dana administers ONE, so ONE is hers to change.
	if w := as(t, h, dana, "GET", "/task/ONE-1", nil); w.Code != http.StatusOK {
		t.Errorf("reading her own project: code = %d", w.Code)
	}
	if w := as(t, h, dana, "POST", "/task/ONE-1/comment", url.Values{"body": {"mine"}}); w.Code != http.StatusSeeOther {
		t.Errorf("commenting on her own project: code = %d; body:\n%s", w.Code, w.Body)
	}

	// TWO is on a host that has never heard of her.
	if w := as(t, h, dana, "POST", "/task/TWO-1/comment", url.Values{"body": {"not mine"}}); w.Code != http.StatusForbidden {
		t.Errorf("commenting on somebody else's project: code = %d, want it refused", w.Code)
	}
	if w := as(t, h, dana, "GET", "/task/TWO-1", nil); w.Code != http.StatusForbidden {
		t.Errorf("reading somebody else's project: code = %d, want it refused", w.Code)
	}
}

// And the board shows what you may see rather than everything on the disk.
func TestTheBoardShowsOnlyWhatYouMaySee(t *testing.T) {
	_, h, first, second := workspaceOnTwoHosts(t)

	dana := signInTo(t, h, nil, first.HostName(), "dana")
	board := as(t, h, dana, "GET", "/", nil).Body.String()
	if !strings.Contains(board, "ONE-1") {
		t.Error("her own project is missing from the board")
	}
	if strings.Contains(board, "TWO-1") {
		t.Error("the board shows a task from a repository she has no access to")
	}

	// Signing into the second host as well adds to the same session.
	both := signInTo(t, h, dana, second.HostName(), "sam")
	board = as(t, h, both, "GET", "/", nil).Body.String()
	if !strings.Contains(board, "ONE-1") || !strings.Contains(board, "TWO-1") {
		t.Error("with both hosts signed into, the board should show both projects")
	}
}

// A search is a read like any other, and used to be the way past a filter.
func TestSearchDoesNotReachIntoARepositoryYouCannotSee(t *testing.T) {
	_, h, first, _ := workspaceOnTwoHosts(t)
	dana := signInTo(t, h, nil, first.HostName(), "dana")

	body := as(t, h, dana, "GET", "/search?q=task", nil).Body.String()
	if strings.Contains(body, "TWO-1") {
		t.Errorf("search found a task in a repository she cannot see:\n%s", body)
	}
	if !strings.Contains(body, "ONE-1") {
		t.Error("search did not find her own task")
	}
}

// The API is the same server, and an agent must not get more than a browser.
func TestTheApiFiltersTheSameWay(t *testing.T) {
	_, h, first, _ := workspaceOnTwoHosts(t)
	dana := signInTo(t, h, nil, first.HostName(), "dana")

	body := as(t, h, dana, "GET", "/api/tasks", nil).Body.String()
	if strings.Contains(body, "TWO-1") {
		t.Errorf("the API listed a task she cannot see:\n%s", body)
	}
	if w := as(t, h, dana, "PATCH", "/api/tasks/TWO-1", nil); w.Code != http.StatusForbidden {
		t.Errorf("the API let her change it: code = %d", w.Code)
	}
}

// Signing into a second host must not sign you out of the first.
func TestSigningIntoASecondHostKeepsTheFirst(t *testing.T) {
	s, h, first, second := workspaceOnTwoHosts(t)

	dana := signInTo(t, h, nil, first.HostName(), "dana")
	both := signInTo(t, h, dana, second.HostName(), "sam")

	if both.Value != dana.Value {
		t.Error("the second sign-in started a new session instead of adding to it")
	}
	current, ok := s.auth.lookup(both.Value)
	if !ok {
		t.Fatal("no session")
	}
	if len(current.tokens) != 2 {
		t.Errorf("the session holds %d tokens, want one per host", len(current.tokens))
	}
}

// A repository nobody can vouch for is readable and never writable. Treating
// "cannot ask" as "anyone may" is how one repository ends up unguarded.
func TestARepositoryNobodyVouchesForIsReadOnly(t *testing.T) {
	_, h, first, _ := workspaceOnTwoHosts(t)
	dana := signInTo(t, h, nil, first.HostName(), "dana")

	// Take the host away from TWO, as though it had no remote at all.
	server, _, _, _ := workspaceOnTwoHosts(t)
	for _, repo := range server.auth.repos {
		if repo.projects[0] == "TWO" {
			repo.host, repo.checker, repo.why = nil, nil, "no origin remote"
		}
	}
	h = server.Handler()
	dana = signInTo(t, h, nil, first.HostName(), "dana")

	if w := as(t, h, dana, "GET", "/task/TWO-1", nil); w.Code != http.StatusOK {
		t.Errorf("reading it: code = %d, want it readable", w.Code)
	}
	if w := as(t, h, dana, "POST", "/task/TWO-1/comment", url.Values{"body": {"no"}}); w.Code != http.StatusForbidden {
		t.Errorf("writing to it: code = %d, want it refused", w.Code)
	}
}

// The message has to say which of the two problems it is, because the next step
// differs: sign into another host, or ask somebody for access.
func TestARefusalSaysWhichProblemItIs(t *testing.T) {
	_, h, first, _ := workspaceOnTwoHosts(t)
	dana := signInTo(t, h, nil, first.HostName(), "dana")

	body := as(t, h, dana, "POST", "/task/TWO-1/comment", url.Values{"body": {"x"}}).Body.String()
	if !strings.Contains(body, "second.example.com") {
		t.Errorf("the refusal does not say where to sign in:\n%s", body)
	}
}
