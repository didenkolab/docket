package project

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A type may be written as a word or as a name and a level, because a vault
// that never cared about levels must keep working.
func TestTypesReadBothForms(t *testing.T) {
	var c Config
	body := `
name: v
projects: [{key: ACME}]
statuses: [{name: Backlog, category: todo}]
priorities: [normal]
types:
  - name: epic
    level: 1
  - story
  - name: subtask
    level: -1
`
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	if got := c.TypeNames(); strings.Join(got, ",") != "epic,story,subtask" {
		t.Fatalf("types = %v", got)
	}
	for name, want := range map[string]int{"epic": 1, "story": 0, "subtask": -1} {
		if got := c.LevelOf(name); got != want {
			t.Errorf("LevelOf(%q) = %d, want %d", name, got, want)
		}
	}
}

// A flat list means what it always meant: nothing is enforced.
func TestAFlatVocabularyEnforcesNothing(t *testing.T) {
	c := Config{Types: []Type{{Name: "task"}, {Name: "bug"}, {Name: "epic"}}}
	if c.Layered() {
		t.Fatal("a flat list should not turn the rule on")
	}
	if !c.CanParent("bug", "epic") {
		t.Error("a vault that said nothing about levels had a parent refused")
	}
}

func TestAParentSitsAboveItsChild(t *testing.T) {
	c := Config{Types: []Type{
		{Name: "epic", Level: LevelEpic},
		{Name: "story"},
		{Name: "subtask", Level: LevelSubtask},
	}}
	if !c.Layered() {
		t.Fatal("levels were declared and the rule is off")
	}

	cases := []struct {
		parent, child string
		want          bool
	}{
		{"epic", "story", true},
		{"story", "subtask", true},
		{"epic", "subtask", true}, // above, not exactly one above
		{"story", "epic", false},
		{"story", "story", false}, // a task cannot own a task of its own level
		{"subtask", "story", false},
	}
	for _, tc := range cases {
		if got := c.CanParent(tc.parent, tc.child); got != tc.want {
			t.Errorf("a %s holding a %s: %v, want %v", tc.parent, tc.child, got, tc.want)
		}
	}
}

// A sub-task is work inside a task. A backlog listing it beside the task it
// belongs to counts the same work twice.
func TestOnlyStandardWorkAndAboveIsSchedulable(t *testing.T) {
	c := Config{Types: []Type{
		{Name: "epic", Level: LevelEpic},
		{Name: "task"},
		{Name: "subtask", Level: LevelSubtask},
	}}
	if !c.Schedulable("task") || !c.Schedulable("epic") {
		t.Error("real work is not schedulable")
	}
	if c.Schedulable("subtask") {
		t.Error("a sub-task belongs in a backlog on its own")
	}
}

// The default is the first standard type: an epic is a container and a sub-task
// belongs to something, so neither is what somebody means by "a task".
func TestTheDefaultTypeIsOrdinaryWork(t *testing.T) {
	c := Config{Types: []Type{
		{Name: "epic", Level: LevelEpic},
		{Name: "story"},
		{Name: "bug"},
	}}
	if got := c.DefaultType(); got != "story" {
		t.Errorf("DefaultType = %q", got)
	}
}

// A vault that declared no levels keeps a configuration file it recognises.
func TestAStandardTypeWritesBackAsAWord(t *testing.T) {
	out, err := yaml.Marshal([]Type{{Name: "task"}, {Name: "epic", Level: LevelEpic}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "- task") {
		t.Errorf("a plain type was written the long way:\n%s", out)
	}
	if !strings.Contains(string(out), "level: 1") {
		t.Errorf("a level was lost:\n%s", out)
	}
}
