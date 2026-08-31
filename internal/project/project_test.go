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
	projectKey, number, err := SplitKey("ACME-12")
	if err != nil {
		t.Fatalf("SplitKey: %v", err)
	}
	if projectKey != "ACME" || number != 12 {
		t.Errorf("got %q/%d", projectKey, number)
	}
	if got := Key("ACME", 12); got != "ACME-12" {
		t.Errorf("Key = %q", got)
	}
}

func TestSplitKeyRejectsWhatIsNotAKey(t *testing.T) {
	for _, key := range []string{"", "ACME", "ACME/12", "-12", "ACME-", "acme-12", "ACME-0", "ACME-x"} {
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

func TestAVaultWithNoWorkflowAllowsEverything(t *testing.T) {
	// A workflow nobody asked for is a workflow that gets in the way.
	c, err := Load(write(t, good))
	if err != nil {
		t.Fatal(err)
	}
	for _, from := range c.StatusNames() {
		for _, to := range c.StatusNames() {
			if !c.CanMove(from, to) {
				t.Errorf("%s → %s was refused with no workflow set", from, to)
			}
		}
	}
	if len(c.Reachable("Backlog")) != len(c.Statuses) {
		t.Error("Reachable did not offer every status")
	}
}

const withWorkflow = good + `transitions:
  Backlog: [In progress]
  In progress: [Done, Dropped]
  Done: []
  Dropped: []
`

func TestAWorkflowLimitsMoves(t *testing.T) {
	c, err := Load(write(t, withWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	if !c.CanMove("Backlog", "In progress") {
		t.Error("an allowed move was refused")
	}
	if c.CanMove("Backlog", "Done") {
		t.Error("a move nobody allowed was permitted")
	}
	// Staying put is not a move.
	if !c.CanMove("Done", "Done") {
		t.Error("a task was not allowed to stay where it is")
	}

	reachable := c.Reachable("In progress")
	if len(reachable) != 3 {
		t.Errorf("Reachable(In progress) = %d statuses, want 3 including itself", len(reachable))
	}
}

func TestATransitionToAStatusThatDoesNotExistIsRejected(t *testing.T) {
	body := good + "transitions:\n  Backlog: [Nowhere]\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("a transition to an unknown status was accepted")
	}
	body = good + "transitions:\n  Nowhere: [Done]\n"
	if _, err := Load(write(t, body)); err == nil {
		t.Error("a transition from an unknown status was accepted")
	}
}

func TestRenamingAStatusKeepsItsMoves(t *testing.T) {
	c, err := Load(write(t, withWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	c.RenameInTransitions("In progress", "Doing")

	if targets, ok := c.Transitions["Doing"]; !ok || len(targets) != 2 {
		t.Errorf("the renamed status lost its moves: %v", c.Transitions)
	}
	if targets := c.Transitions["Backlog"]; len(targets) != 1 || targets[0] != "Doing" {
		t.Errorf("a move pointing at the renamed status was not followed: %v", targets)
	}
}

func TestRemovingAStatusRemovesItsMoves(t *testing.T) {
	c, err := Load(write(t, withWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	c.DropFromTransitions("Dropped")

	if _, ok := c.Transitions["Dropped"]; ok {
		t.Error("the removed status still has moves of its own")
	}
	for from, targets := range c.Transitions {
		for _, to := range targets {
			if to == "Dropped" {
				t.Errorf("%s still moves to the removed status", from)
			}
		}
	}
}

// A vault whose words are its own gets a sensible default, which is what the
// testbed vault caught: its priorities are низкий, обычный, высокий, критичный,
// and taking the first would have made every task it created низкий.
func TestTheDefaultPriorityIsTheMiddleOfTheList(t *testing.T) {
	for _, tc := range []struct {
		name       string
		priorities []string
		want       string
	}{
		{"four of the vault's own", []string{"низкий", "обычный", "высокий", "критичный"}, "обычный"},
		{"five, Jira's shape", []string{"lowest", "low", "medium", "high", "highest"}, "medium"},
		{"one, so there is no choice", []string{"same"}, "same"},
		{"the word normal wins wherever it is", []string{"a", "b", "normal", "c", "d", "e"}, "normal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Priorities: tc.priorities}
			if got := c.DefaultPriority(); got != tc.want {
				t.Errorf("DefaultPriority() = %q, want %q", got, tc.want)
			}
		})
	}
}
