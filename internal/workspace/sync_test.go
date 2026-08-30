package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGit records what it was asked to do and answers from a script, so sync
// can be exercised without a network or a real repository.
type fakeGit struct {
	calls  []string
	status map[string]string // directory -> git status --porcelain output
	pull   map[string]string // directory -> git pull output
	fail   map[string]error  // first argument -> error
	clone  func(dir, remote, path string) error
}

func (g *fakeGit) Run(dir string, args ...string) (string, error) {
	g.calls = append(g.calls, args[0]+" in "+filepath.Base(dir))

	if err, ok := g.fail[args[0]]; ok {
		return "", err
	}

	switch args[0] {
	case "clone":
		if g.clone != nil {
			return "", g.clone(dir, args[1], args[2])
		}
		return "", os.MkdirAll(filepath.Join(dir, args[2], ".git"), 0o755)
	case "status":
		return g.status[filepath.Base(dir)], nil
	case "pull":
		if out, ok := g.pull[filepath.Base(dir)]; ok {
			return out, nil
		}
		return "Already up to date.\n", nil
	}
	return "", nil
}

func manifest(keys ...string) *Manifest {
	m := &Manifest{}
	for _, k := range keys {
		m.Projects = append(m.Projects, Project{
			Key:    k,
			Path:   strings.ToLower(k),
			Remote: "git@example.com:" + strings.ToLower(k) + ".git",
		})
	}
	return m
}

func TestSyncClonesWhatIsMissing(t *testing.T) {
	root := t.TempDir()
	git := &fakeGit{}

	results := Sync(root, manifest("ACME"), git)
	if len(results) != 1 || results[0].Action != ActionCloned {
		t.Fatalf("results = %v, want one clone", results)
	}
	if _, err := os.Stat(filepath.Join(root, "acme", ".git")); err != nil {
		t.Errorf("nothing was cloned: %v", err)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	root := t.TempDir()
	git := &fakeGit{}

	Sync(root, manifest("ACME"), git)
	second := Sync(root, manifest("ACME"), git)

	if second[0].Action != ActionCurrent {
		t.Errorf("the second run reported %q, want %q", second[0].Action, ActionCurrent)
	}
}

func TestSyncReportsAnUpdate(t *testing.T) {
	root := t.TempDir()
	git := &fakeGit{pull: map[string]string{"acme": "Updating a1b2c3..d4e5f6\nFast-forward\n"}}

	Sync(root, manifest("ACME"), git)
	results := Sync(root, manifest("ACME"), git)

	if results[0].Action != ActionUpdated {
		t.Errorf("action = %q, want %q", results[0].Action, ActionUpdated)
	}
}

func TestSyncLeavesADirtyProjectAlone(t *testing.T) {
	// Stashing or resetting someone's work in progress to make a sync succeed
	// is not a service.
	root := t.TempDir()
	git := &fakeGit{status: map[string]string{"acme": " M tasks/ACME-1.md\n"}}

	Sync(root, manifest("ACME"), git)
	git.calls = nil
	results := Sync(root, manifest("ACME"), git)

	if results[0].Action != ActionSkipped {
		t.Fatalf("action = %q, want %q", results[0].Action, ActionSkipped)
	}
	for _, call := range git.calls {
		if strings.HasPrefix(call, "pull") {
			t.Errorf("a dirty project was pulled: %v", git.calls)
		}
	}
	if !strings.Contains(results[0].Detail, "uncommitted") {
		t.Errorf("detail = %q, want it to say why", results[0].Detail)
	}
}

func TestSyncReportsADirectoryThatIsNotARepository(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "acme"), 0o755); err != nil {
		t.Fatal(err)
	}

	results := Sync(root, manifest("ACME"), &fakeGit{})
	if results[0].Action != ActionFailed {
		t.Errorf("action = %q, want %q", results[0].Action, ActionFailed)
	}
	if !strings.Contains(results[0].Detail, "not a git repository") {
		t.Errorf("detail = %q", results[0].Detail)
	}
}

func TestOneFailureDoesNotStopTheOthers(t *testing.T) {
	root := t.TempDir()
	git := &fakeGit{clone: func(dir, remote, path string) error {
		if path == "acme" {
			return errors.New("repository not found")
		}
		return os.MkdirAll(filepath.Join(dir, path, ".git"), 0o755)
	}}

	results := Sync(root, manifest("ACME", "BETA"), git)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Action != ActionFailed {
		t.Errorf("the failing project reported %q", results[0].Action)
	}
	if results[1].Action != ActionCloned {
		t.Errorf("the healthy project reported %q", results[1].Action)
	}
	if !Failed(results) {
		t.Error("Failed() did not notice the failure")
	}
}

func TestFailedIsFalseWhenEverythingWorked(t *testing.T) {
	root := t.TempDir()
	if Failed(Sync(root, manifest("ACME"), &fakeGit{})) {
		t.Error("Failed() reported a failure on a clean sync")
	}
}
