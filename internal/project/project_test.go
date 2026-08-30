package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const good = `key: ACME
name: Acme Platform
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
	p, err := Load(write(t, good))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Key != "ACME" || p.Name != "Acme Platform" {
		t.Errorf("key/name = %q/%q", p.Key, p.Name)
	}
	if got, ok := p.CategoryOf("Dropped"); !ok || got != CategoryDone {
		t.Errorf("CategoryOf(Dropped) = %q, %v; want done, true", got, ok)
	}
	if _, ok := p.CategoryOf("Nope"); ok {
		t.Error("an unknown status was recognised")
	}
	if p.FirstStatus().Name != "Backlog" {
		t.Errorf("FirstStatus = %q, want Backlog", p.FirstStatus().Name)
	}
	if p.DefaultPriority() != "normal" {
		t.Errorf("DefaultPriority = %q", p.DefaultPriority())
	}
	if !p.HasType("bug") || p.HasType("saga") {
		t.Error("HasType is wrong")
	}
}

func TestDefaultPriorityFallsBackToTheFirst(t *testing.T) {
	p, err := Load(write(t, strings.Replace(good, "[low, normal, high]", "[p1, p2]", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if got := p.DefaultPriority(); got != "p1" {
		t.Errorf("DefaultPriority = %q, want p1", got)
	}
}

func TestLoadRejectsBrokenProjects(t *testing.T) {
	cases := map[string]string{
		"lower-case key":    strings.Replace(good, "key: ACME", "key: acme", 1),
		"no name":           strings.Replace(good, "name: Acme Platform", "name: \"\"", 1),
		"no statuses":       "key: ACME\nname: Acme\nstatuses: []\ntypes: [task]\npriorities: [normal]\n",
		"unknown category":  strings.Replace(good, "category: doing", "category: pending", 1),
		"duplicated status": strings.Replace(good, "{name: Done, category: done}", "{name: Backlog, category: done}", 1),
		"no types":          strings.Replace(good, "types: [task, bug]", "types: []", 1),
		"no priorities":     strings.Replace(good, "priorities: [low, normal, high]", "priorities: []", 1),
		"not even yaml":     "key: [unclosed\n",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: Load accepted it", name)
		}
	}
}

func TestLoadOnADirectoryWithoutAProject(t *testing.T) {
	_, err := Load(t.TempDir())
	if !errors.Is(err, ErrNotAVault) {
		t.Errorf("err = %v, want ErrNotAVault", err)
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
