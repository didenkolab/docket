package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const good = `name: Acme
projects:
  - {key: ACME, name: Acme Platform}
  - {key: BETA, name: Beta}
statuses:
  - {name: Backlog, category: todo}
  - {name: In progress, category: doing}
  - {name: Done, category: done}
  - {name: Dropped, category: done}
types: [task, bug]
priorities: [low, normal, high]
`

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoad(t *testing.T) {
	c, err := Load(write(t, good))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Name != "Acme" {
		t.Errorf("name = %q", c.Name)
	}
	if got := c.ProjectKeys(); strings.Join(got, ",") != "ACME,BETA" {
		t.Errorf("ProjectKeys = %v", got)
	}
	if !c.HasProject("BETA") || c.HasProject("NOPE") {
		t.Error("HasProject is wrong")
	}
	if got := c.ProjectName("ACME"); got != "Acme Platform" {
		t.Errorf("ProjectName = %q", got)
	}
	if got, ok := c.CategoryOf("Dropped"); !ok || got != CategoryDone {
		t.Errorf("CategoryOf(Dropped) = %q, %v; want done, true", got, ok)
	}
	if c.FirstStatus().Name != "Backlog" {
		t.Errorf("FirstStatus = %q", c.FirstStatus().Name)
	}
	if c.DefaultPriority() != "normal" {
		t.Errorf("DefaultPriority = %q", c.DefaultPriority())
	}
}

func TestLoadRejectsBrokenConfigs(t *testing.T) {
	cases := map[string]string{
		"no name":           strings.Replace(good, "name: Acme\n", "", 1),
		"no projects":       "name: Acme\nprojects: []\nstatuses:\n  - {name: A, category: todo}\ntypes: [task]\npriorities: [normal]\n",
		"lower-case key":    strings.Replace(good, "key: ACME", "key: acme", 1),
		"reserved key":      strings.Replace(good, "key: ACME", "key: DOCS", 1),
		"duplicate project": strings.Replace(good, "key: BETA", "key: ACME", 1),
		"unknown category":  strings.Replace(good, "category: doing", "category: pending", 1),
		"duplicate status":  strings.Replace(good, "{name: Done, category: done}", "{name: Backlog, category: done}", 1),
		"no types":          strings.Replace(good, "types: [task, bug]", "types: []", 1),
		"no priorities":     strings.Replace(good, "priorities: [low, normal, high]", "priorities: []", 1),
		"not even yaml":     "name: [unclosed\n",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestReservedKeysCannotBeProjects(t *testing.T) {
	// docs/ and boards/ are already something else at the vault root.
	for key := range Reserved {
		if err := ValidKey(key); err == nil {
			t.Errorf("%s was accepted as a project key", key)
		}
	}
}

func TestSaveAndAddProject(t *testing.T) {
	dir := write(t, good)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.AddProject("GAMMA", "Gamma"); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := c.AddProject("GAMMA", "again"); err == nil {
		t.Error("the same project was added twice")
	}
	if err := c.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.HasProject("GAMMA") {
		t.Error("the added project did not survive a round trip")
	}
}

func TestSplitKey(t *testing.T) {
	projectKey, number, err := SplitKey("ACME/12")
	if err != nil {
		t.Fatalf("SplitKey: %v", err)
	}
	if projectKey != "ACME" || number != 12 {
		t.Errorf("got %q/%d", projectKey, number)
	}
	if got := Key("ACME", 12); got != "ACME/12" {
		t.Errorf("Key = %q", got)
	}
}

func TestSplitKeyRejectsWhatIsNotAKey(t *testing.T) {
	for _, key := range []string{"", "ACME", "ACME-12", "/12", "ACME/", "acme/12", "ACME/0", "ACME/x"} {
		if _, _, err := SplitKey(key); err == nil {
			t.Errorf("SplitKey(%q) was accepted", key)
		}
	}
}

func TestLoadOnADirectoryWithoutAVault(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrNotAVault) {
		t.Error("Load did not report a missing config")
	}
}

func TestFindRootWalksUp(t *testing.T) {
	root := write(t, good)
	deep := filepath.Join(root, "docs", "spec")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(deep)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	// t.TempDir can sit behind a symlink, so compare resolved paths.
	wantResolved, _ := filepath.EvalSymlinks(root)
	gotResolved, _ := filepath.EvalSymlinks(got)
	if gotResolved != wantResolved {
		t.Errorf("FindRoot = %q, want %q", gotResolved, wantResolved)
	}
}

func TestFindRootOutsideAnyVault(t *testing.T) {
	if _, err := FindRoot(t.TempDir()); !errors.Is(err, ErrNotAVault) {
		t.Errorf("err = %v, want ErrNotAVault", err)
	}
}
