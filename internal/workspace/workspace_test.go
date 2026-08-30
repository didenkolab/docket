package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func initWorkspace(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "space")
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return dir
}

func TestInitWritesAWorkspace(t *testing.T) {
	dir := initWorkspace(t)

	for _, rel := range []string{FileName, ".gitignore", "README.md",
		".obsidian/app.json", ".obsidian/core-plugins.json"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s is missing: %v", rel, err)
		}
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m.Projects) != 0 {
		t.Errorf("a fresh workspace has %d projects, want 0", len(m.Projects))
	}
}

func TestInitRefusesANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(dir); err == nil {
		t.Fatal("Init wrote into a non-empty directory")
	}
}

func TestAddAndSave(t *testing.T) {
	dir := initWorkspace(t)
	m, _ := Load(dir)

	if err := m.Add(Project{Key: "ACME", Remote: "git@example.com:acme.git"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := m.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reloaded.Projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(reloaded.Projects))
	}
	if got := reloaded.Projects[0].Path; got != "acme" {
		t.Errorf("path = %q, want the lower-case key", got)
	}
}

func TestAddRejectsADuplicate(t *testing.T) {
	m := &Manifest{}
	if err := m.Add(Project{Key: "ACME", Remote: "r"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Add(Project{Key: "ACME", Remote: "other"}); err == nil {
		t.Error("the same project was added twice")
	}
}

func TestGitignoreListsEveryProject(t *testing.T) {
	dir := initWorkspace(t)
	m, _ := Load(dir)
	_ = m.Add(Project{Key: "ACME", Remote: "r1"})
	_ = m.Add(Project{Key: "BETA", Remote: "r2"})
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/acme/", "/beta/", ignoreBegin, ignoreEnd} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in:\n%s", want, raw)
		}
	}
}

func TestGitignoreKeepsHandWrittenLines(t *testing.T) {
	dir := initWorkspace(t)
	path := filepath.Join(dir, ".gitignore")

	raw, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append([]byte("# mine\nscratch/\n\n"), raw...), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := Load(dir)
	_ = m.Add(Project{Key: "ACME", Remote: "r"})
	if err := m.Save(dir); err != nil {
		t.Fatal(err)
	}

	updated, _ := os.ReadFile(path)
	if !strings.Contains(string(updated), "scratch/") {
		t.Errorf("a hand-written line was lost:\n%s", updated)
	}
	if !strings.Contains(string(updated), "/acme/") {
		t.Errorf("the generated block was not written:\n%s", updated)
	}
	if strings.Count(string(updated), ignoreBegin) != 1 {
		t.Errorf("the generated block was duplicated:\n%s", updated)
	}
}

func TestLoadRejectsBrokenManifests(t *testing.T) {
	cases := map[string]string{
		"bad key":        "projects:\n  - {key: acme, remote: r}\n",
		"no remote":      "projects:\n  - {key: ACME, remote: \"\"}\n",
		"escaping path":  "projects:\n  - {key: ACME, path: ../outside, remote: r}\n",
		"absolute path":  "projects:\n  - {key: ACME, path: /etc, remote: r}\n",
		"root path":      "projects:\n  - {key: ACME, path: /, remote: r}\n",
		"sneaky escape":  "projects:\n  - {key: ACME, path: a/../.., remote: r}\n",
		"the workspace":  "projects:\n  - {key: ACME, path: ., remote: r}\n",
		"duplicate key":  "projects:\n  - {key: ACME, remote: r}\n  - {key: ACME, path: other, remote: r}\n",
		"duplicate path": "projects:\n  - {key: ACME, path: p, remote: r}\n  - {key: BETA, path: p, remote: r}\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestLoadOutsideAWorkspace(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNotAWorkspace) {
		t.Error("Load did not report a missing manifest")
	}
}

func TestFindRootWalksUp(t *testing.T) {
	root := initWorkspace(t)
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(deep)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("FindRoot = %q, want %q", gotResolved, wantResolved)
	}
}
