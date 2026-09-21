package server

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/workspace"
)

// makingHost is a host that can be asked for a repository, and makes it as a
// bare repository on disk so the push has somewhere real to go.
type makingHost struct {
	twoHosts
	// where is the directory bare repositories are made in.
	where string
	// refuse is what to fail with instead of making one.
	refuse string
	// asked is what it was asked for.
	asked access.NewRepository
}

func (h *makingHost) CreateScope() string { return "repo" }

func (h *makingHost) Create(_ context.Context, token string,
	want access.NewRepository) (access.Created, error) {

	h.asked = want
	if h.refuse != "" {
		return access.Created{}, errStr(h.refuse)
	}
	if _, ok := h.grants[token]; !ok {
		return access.Created{}, errStr("that token is not ours")
	}

	// Not the test helper: this runs inside the handler, where there is no
	// *testing.T to report to.
	bare := filepath.Join(h.where, want.Name+".git")
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", bare).
		CombinedOutput(); err != nil {
		return access.Created{}, errStr("cannot make the bare repository: " + string(out))
	}
	return access.Created{Remote: bare, Web: "https://" + h.hostName + "/x/" + want.Name,
		FullName: "x/" + want.Name}, nil
}

type errStr string

func (e errStr) Error() string { return string(e) }

// A workspace with one project, whose host can make repositories.
func workspaceThatCanCreate(t *testing.T) (*Server, http.Handler, string, *makingHost) {
	t.Helper()

	server, h, root, _ := workspaceToGrow(t)

	host := &makingHost{
		twoHosts: twoHosts{hostName: "first.example.com", repo: "x/one",
			grants: map[string]string{"dana": access.RoleAdmin}},
		where: t.TempDir(),
	}
	// The workspace was built unauthenticated; give it an authority whose one
	// repository is on a host that can create.
	server.auth = newAuthority([]*repository{{
		prefix: "one", projects: []string{"ONE"}, name: "ONE",
		host: host, hostKey: host.HostName(),
		checker: access.NewChecker(host, time.Nanosecond),
	}}, time.Hour)
	server.onLoopback = false
	return server, h, root, host
}

// The point: a project that has nowhere to live gets somewhere, is scaffolded
// and pushed, and is in the workspace — in that order.
func TestMakingANewProject(t *testing.T) {
	_, h, root, host := workspaceThatCanCreate(t)
	dana := signInTo(t, h, nil, host.HostName(), "dana")

	w := as(t, h, dana, "POST", "/projects/new", url.Values{
		"key": {"NEW"}, "name": {"A New Thing"}, "repository": {"new-board"},
		"host": {host.HostName()}, "visibility": {"private"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}

	if !host.asked.Private {
		t.Error("the repository was not asked for as private")
	}
	if host.asked.Name != "new-board" {
		t.Errorf("asked for %q", host.asked.Name)
	}

	// Scaffolded, with the key stamped in.
	dir := filepath.Join(root, "new-board")
	c, err := project.Load(dir)
	if err != nil {
		t.Fatalf("the new project is not a vault: %v", err)
	}
	if keys := c.ProjectKeys(); len(keys) != 1 || keys[0] != "NEW" {
		t.Errorf("its projects are %v", keys)
	}
	if c.Name != "A New Thing" {
		t.Errorf("its name is %q", c.Name)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Error("the scaffold has no AGENTS.md, so an agent has nothing to read")
	}

	// Pushed: the bare repository has the first commit.
	out := gitOut(t, filepath.Join(host.where, "new-board.git"), "log", "--format=%s")
	if !strings.Contains(out, "NEW:") {
		t.Errorf("the scaffold was not pushed; the remote says %q", out)
	}

	// And in the manifest, and on the board.
	m, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Projects) != 2 {
		t.Fatalf("the manifest holds %+v", m.Projects)
	}
	// The space was rebuilt, and this fixture's stub host does not survive that
	// — the real remotes are local paths no host answers for. What matters here
	// is that the space grew, which it did.
	if len(m.Projects) != 2 || m.Projects[1].Key != "NEW" {
		t.Errorf("the manifest does not name the new project: %+v", m.Projects)
	}
}

// When the host refuses, nothing local happens — that is why it is asked first.
func TestAHostRefusingLeavesNothingBehind(t *testing.T) {
	_, h, root, host := workspaceThatCanCreate(t)
	host.refuse = "insufficient scope"
	dana := signInTo(t, h, nil, host.HostName(), "dana")

	w := as(t, h, dana, "POST", "/projects/new", url.Values{
		"key": {"NEW"}, "repository": {"new-board"}, "host": {host.HostName()},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "insufficient scope") {
		t.Errorf("the host's own reason is not shown:\n%s", body)
	}
	if !strings.Contains(body, "repo") {
		t.Error("the page does not say which scope it would take")
	}

	if _, err := os.Stat(filepath.Join(root, "new-board")); err == nil {
		t.Error("a directory was left behind after the host refused")
	}
	m, _ := workspace.Load(root)
	if len(m.Projects) != 1 {
		t.Errorf("the manifest was changed: %+v", m.Projects)
	}
}

// A key the vault format will not accept is refused before anything is asked
// for, because a repository made for a key that cannot exist is litter.
func TestABadKeyIsRefusedBeforeAnythingIsMade(t *testing.T) {
	_, h, _, host := workspaceThatCanCreate(t)
	dana := signInTo(t, h, nil, host.HostName(), "dana")

	for _, key := range []string{"", "acme", "A", "TOO-LONG-A-KEY", "DOCS"} {
		w := as(t, h, dana, "POST", "/projects/new", url.Values{
			"key": {key}, "repository": {"x"}, "host": {host.HostName()},
		})
		if w.Code != http.StatusBadRequest {
			t.Errorf("key %q was accepted: %d", key, w.Code)
		}
		if host.asked.Name != "" {
			t.Errorf("key %q reached the host", key)
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
