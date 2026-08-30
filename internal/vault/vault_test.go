package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initVault(t *testing.T, opts Options) (dir string, written []string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "vault")
	written, err := Init(dir, opts)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dir, written
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	return string(b)
}

func TestInitWritesTheWholeVault(t *testing.T) {
	dir, written := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	want := []string{
		".gitignore",
		".obsidian/app.json",
		".obsidian/core-plugins.json",
		"AGENTS.md",
		"README.md",
		"attachments/.gitkeep",
		"boards/backlog.base",
		"boards/board.base",
		"boards/my-tasks.base",
		"docs/index.md",
		"project.yaml",
		"tasks/.gitkeep",
		"templates/page.md",
		"templates/task.md",
	}

	got := map[string]bool{}
	for _, p := range written {
		got[p] = true
	}
	for _, p := range want {
		if !got[p] {
			t.Errorf("Init did not report %s\ngot: %v", p, written)
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s is missing on disk: %v", p, err)
		}
	}
	if len(written) != len(want) {
		t.Errorf("wrote %d files, want %d: %v", len(written), len(want), written)
	}
}

func TestInitStampsKeyAndName(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	project := read(t, dir, "project.yaml")
	if !strings.Contains(project, "key: ACME") {
		t.Errorf("project.yaml has no key:\n%s", project)
	}
	if !strings.Contains(project, "name: Acme Platform") {
		t.Errorf("project.yaml has no name:\n%s", project)
	}
	if agents := read(t, dir, "AGENTS.md"); !strings.Contains(agents, "ACME-12") {
		t.Error("AGENTS.md does not use the project key in its examples")
	}
	if readme := read(t, dir, "README.md"); !strings.Contains(readme, "Acme Platform") {
		t.Error("README.md does not use the project name")
	}
}

func TestInitLeavesNoUnrenderedPlaceholders(t *testing.T) {
	dir, written := initVault(t, Options{Key: "ACME", Name: "Acme Platform"})

	for _, rel := range written {
		if strings.Contains(read(t, dir, rel), "{{") {
			t.Errorf("%s still contains a template placeholder", rel)
		}
	}
}

func TestNameDefaultsToKey(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME"})
	if project := read(t, dir, "project.yaml"); !strings.Contains(project, "name: ACME") {
		t.Errorf("name did not default to the key:\n%s", project)
	}
}

func TestInitCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "nested", "vault")
	if _, err := Init(dir, Options{Key: "ACME"}); err != nil {
		t.Fatalf("Init into a missing directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "project.yaml")); err != nil {
		t.Errorf("vault not created: %v", err)
	}
}

func TestInitRefusesANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(dir, Options{Key: "ACME"}); err == nil {
		t.Fatal("Init overwrote a non-empty directory")
	} else if !strings.Contains(err.Error(), "notes.md") {
		t.Errorf("the error does not name what was in the way: %v", err)
	}

	if got := read(t, dir, "notes.md"); got != "mine" {
		t.Errorf("the existing file was modified: %q", got)
	}
}

func TestInitRunsInsideAFreshClone(t *testing.T) {
	// `git clone` of an empty repository leaves a .git directory and nothing
	// else. That is the common case and must not be treated as occupied.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Init(dir, Options{Key: "ACME"}); err != nil {
		t.Fatalf("Init refused a directory holding only .git: %v", err)
	}
}

func TestInitRefusesToRunTwice(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "ACME"})
	if _, err := Init(dir, Options{Key: "ACME"}); err == nil {
		t.Error("Init ran twice over the same directory")
	}
}

func TestKeysThatAreRejected(t *testing.T) {
	for _, key := range []string{"", "  ", "a", "AC ME", "acme", "1ACME", "ACME-1", "TOOLONGAKEY"} {
		dir := filepath.Join(t.TempDir(), "vault")
		if _, err := Init(dir, Options{Key: key}); err == nil {
			t.Errorf("key %q was accepted", key)
		}
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("key %q was rejected but the directory was created anyway", key)
		}
	}
}

func TestKeysThatAreAccepted(t *testing.T) {
	for _, key := range []string{"AC", "ACME", "A1", "PROJECT123"} {
		dir := filepath.Join(t.TempDir(), "vault")
		if _, err := Init(dir, Options{Key: key}); err != nil {
			t.Errorf("key %q was rejected: %v", key, err)
		}
	}
}

func TestKeyAndNameAreTrimmed(t *testing.T) {
	dir, _ := initVault(t, Options{Key: "  ACME  ", Name: "  Acme Platform  "})
	project := read(t, dir, "project.yaml")
	if !strings.Contains(project, "key: ACME\n") {
		t.Errorf("the key was not trimmed:\n%s", project)
	}
	if !strings.Contains(project, "name: Acme Platform\n") {
		t.Errorf("the name was not trimmed:\n%s", project)
	}
}
