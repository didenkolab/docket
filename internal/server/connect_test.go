package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
	"github.com/didenkolab/docket/internal/vault/vaulttest"
	"github.com/didenkolab/docket/internal/workspace"
)

// A workspace of one project, and a second repository sitting on disk waiting
// to be connected. Local paths throughout, so nothing reaches a network.
func workspaceToGrow(t *testing.T) (*Server, http.Handler, string, string) {
	t.Helper()

	template := vaulttest.Template(t)
	root := t.TempDir()

	// The project already in it.
	first := filepath.Join(root, "one")
	if _, err := vault.Init(first, vault.Options{Key: "ONE", Template: template}); err != nil {
		t.Fatal(err)
	}
	shell(t, first, "git", "init", "-q", "-b", "main")
	shell(t, first, "git", "config", "user.email", "t@example.com")
	shell(t, first, "git", "config", "user.name", "T")
	shell(t, first, "git", "add", "-A")
	shell(t, first, "git", "commit", "-q", "-m", "start")

	// The manifest wants a remote for every project, because that is what a
	// workspace clones from. Nothing here fetches it.
	m := &workspace.Manifest{Projects: []workspace.Project{
		{Key: "ONE", Path: "one", Remote: "https://git.example.com/team/one.git"},
	}}
	if err := m.Save(root); err != nil {
		t.Fatal(err)
	}

	// And one to add, somewhere else entirely.
	outside := t.TempDir()
	second := filepath.Join(outside, "two")
	if _, err := vault.Init(second, vault.Options{Key: "TWO", Template: template}); err != nil {
		t.Fatal(err)
	}
	c, err := project.Load(second)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(second, c, vault.NewOptions{Project: "TWO", Title: "Their task"}); err != nil {
		t.Fatal(err)
	}
	shell(t, second, "git", "init", "-q", "-b", "main")
	shell(t, second, "git", "config", "user.email", "t@example.com")
	shell(t, second, "git", "config", "user.name", "T")
	shell(t, second, "git", "add", "-A")
	shell(t, second, "git", "commit", "-q", "-m", "start")

	s, err := New(root, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		SessionLife: time.Hour,
		// A local template: making a project scaffolds from one, and a test
		// must not reach the network.
		Template:        template,
		Unauthenticated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, s.Handler(), root, second
}

// The point of the whole thing: paste a URL, and the project is there — without
// restarting the server, because a project nobody can see yet is not added.
func TestConnectingARepositoryAddsAProject(t *testing.T) {
	s, h, root, second := workspaceToGrow(t)

	if len(s.sp().Vaults()) != 1 {
		t.Fatalf("the workspace starts with %d projects", len(s.sp().Vaults()))
	}

	// Through the handler, a path on the server is refused on purpose.
	w := as(t, h, nil, "POST", "/projects", url.Values{"remote": {"file://" + second}})
	if w.Code != http.StatusBadRequest {
		t.Errorf("a file:// remote was accepted: %d", w.Code)
	}
	if len(s.sp().Vaults()) != 1 {
		t.Fatal("a refused remote changed the workspace")
	}

	// The operation itself, with a remote git can actually reach here.
	if err := s.connectDirect(second); err != nil {
		t.Fatalf("connect: %v", err)
	}

	if len(s.sp().Vaults()) != 2 {
		t.Fatalf("after adding, the workspace holds %d projects", len(s.sp().Vaults()))
	}
	m, err := workspace.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Projects) != 2 || m.Projects[1].Key != "TWO" {
		t.Errorf("the manifest says %+v", m.Projects)
	}
	// And its tasks are readable at once.
	if body := as(t, h, nil, "GET", "/", nil).Body.String(); !strings.Contains(body, "TWO-1") {
		t.Error("the new project's task is not on the board")
	}
}

// A repository that is not a vault is refused, and nothing is left behind.
func TestARepositoryThatIsNotAVaultIsRefused(t *testing.T) {
	s, _, root, _ := workspaceToGrow(t)

	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plain, "README.md"), []byte("# not a vault\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	shell(t, plain, "git", "init", "-q", "-b", "main")
	shell(t, plain, "git", "config", "user.email", "t@example.com")
	shell(t, plain, "git", "config", "user.name", "T")
	shell(t, plain, "git", "add", "-A")
	shell(t, plain, "git", "commit", "-q", "-m", "start")

	err := s.connectDirect(plain)
	if err == nil {
		t.Fatal("a repository with no docket.yaml was accepted")
	}
	if !strings.Contains(err.Error(), "docket init") {
		t.Errorf("the refusal does not say what would make it one: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "plain")); err == nil {
		t.Error("the clone was left behind after being refused")
	}
	m, _ := workspace.Load(root)
	if len(m.Projects) != 1 {
		t.Errorf("the manifest was changed: %+v", m.Projects)
	}
}

// Two projects cannot share a key: it is a folder name and the head of every
// task key in it.
func TestAKeyAlreadyInTheWorkspaceIsRefused(t *testing.T) {
	s, _, root, _ := workspaceToGrow(t)

	// A second repository claiming ONE.
	clash := filepath.Join(t.TempDir(), "clash")
	if _, err := vault.Init(clash, vault.Options{Key: "ONE", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	shell(t, clash, "git", "init", "-q", "-b", "main")
	shell(t, clash, "git", "config", "user.email", "t@example.com")
	shell(t, clash, "git", "config", "user.name", "T")
	shell(t, clash, "git", "add", "-A")
	shell(t, clash, "git", "commit", "-q", "-m", "start")

	err := s.connectDirect(clash)
	if err == nil || !strings.Contains(err.Error(), "ONE") {
		t.Fatalf("a clashing key was accepted or the reason is unclear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "clash")); err == nil {
		t.Error("the clone was left behind")
	}
}

// A path on the server is a legitimate git remote and a bad idea here: it would
// let whoever can reach the page copy any directory the server can read.
func TestAPathOnTheServerIsRefused(t *testing.T) {
	for _, remote := range []string{
		"/etc", "./secrets", "file:///etc/passwd", "-upload-pack=evil",
	} {
		if err := usableRemote(remote); err == nil {
			t.Errorf("%q was accepted as a remote", remote)
		}
	}
	for _, remote := range []string{
		"https://github.com/acme/one.git",
		"git@git.example.com:team/two.git",
		"ssh://git@git.example.com:2222/team/two.git",
	} {
		if err := usableRemote(remote); err != nil {
			t.Errorf("%q was refused: %v", remote, err)
		}
	}
}

// A single vault has no manifest to add to, and says which command makes one.
func TestASingleVaultSaysHowToMakeAWorkspace(t *testing.T) {
	_, h, _, _ := guardedServer(t)
	admin := signIn(t, h, "admin")

	body := as(t, h, admin, "GET", "/projects", nil).Body.String()
	if !strings.Contains(body, "docket workspace") {
		t.Errorf("the page does not say how to make a workspace:\n%s", body)
	}
	if w := as(t, h, admin, "POST", "/projects",
		url.Values{"remote": {"https://example.com/x.git"}}); w.Code != http.StatusBadRequest {
		t.Errorf("adding to a single vault: code = %d", w.Code)
	}
}

func TestWhereACloneGoes(t *testing.T) {
	cases := map[string]string{
		"https://github.com/acme/one.git":         "one",
		"https://git.example.com/team/two":        "two",
		"git@git.example.com:team/their-repo.git": "their-repo",
		"https://git.example.com/a/b/c.git/":      "c",
	}
	for remote, want := range cases {
		if got := directoryFor(remote); got != want {
			t.Errorf("%s → %q, want %q", remote, got, want)
		}
	}
}
