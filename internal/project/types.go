package project

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Levels are what makes an epic a container rather than a word.
//
// A type on its own is a label — `bug`, `story`, `Эпик`. A level says how the
// types stack: an epic holds stories, a story holds sub-tasks, and a parent has
// to sit above its child. That is the rule Jira encodes as a hierarchy level,
// and it is the reason its epic panel, its roll-ups and its backlog behave as
// they do.
//
// The level cannot be guessed from the name. A real project's types are the
// team's own words in the team's own language, and `Эпик` is an epic only
// because somebody says so.
const (
	// LevelSubtask is work inside one task. It never appears in a backlog on
	// its own, because it is not a thing to schedule.
	LevelSubtask = -1
	// LevelStandard is the ordinary unit of work, and the default: a type that
	// says nothing about its level is one of these.
	LevelStandard = 0
	// LevelEpic is a container for standard work. Higher levels are allowed and
	// unnamed — a vault that wants an initiative above its epics says 2.
	LevelEpic = 1
)

// Type is one work type: a name, and where it sits in the hierarchy.
//
// It reads from either form, so a vault that never cared about levels is
// unchanged:
//
//	types: [task, bug, story, epic]
//
//	types:
//	  - name: epic
//	    level: 1
//	  - task
//	  - name: subtask
//	    level: -1
type Type struct {
	Name  string `yaml:"name"`
	Level int    `yaml:"level"`
}

// UnmarshalYAML accepts a plain name or a mapping. A plain name is a standard
// type, which is what almost every type is.
func (t *Type) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		t.Name, t.Level = node.Value, LevelStandard
		return nil
	}
	type plain Type // a distinct type, so this does not recurse
	var value plain
	if err := node.Decode(&value); err != nil {
		return err
	}
	*t = Type(value)
	if t.Name == "" {
		return fmt.Errorf("a type needs a name")
	}
	return nil
}

// MarshalYAML writes back the short form for a standard type, so a vault that
// declared no levels keeps a configuration file it recognises.
func (t Type) MarshalYAML() (any, error) {
	if t.Level == LevelStandard {
		return t.Name, nil
	}
	type plain Type
	return plain(t), nil
}

// TypeNames is the vocabulary as a plain list, for the places that only need
// the words.
func (c *Config) TypeNames() []string {
	names := make([]string, 0, len(c.Types))
	for _, t := range c.Types {
		names = append(names, t.Name)
	}
	return names
}

// HasType reports whether the vault defines a type.
func (c *Config) HasType(name string) bool { return contains(c.TypeNames(), name) }

// LevelOf is where a type sits. An unknown type is standard: a task carrying a
// type the vault does not define is reported by `docket check` as a vocabulary
// problem, and guessing a level for it here would turn one finding into two.
func (c *Config) LevelOf(name string) int {
	for _, t := range c.Types {
		if t.Name == name {
			return t.Level
		}
	}
	return LevelStandard
}

// Layered reports whether the vault has said anything about levels.
//
// Nothing is enforced until it has. A vault whose types are a flat list means
// what it has always meant — any task may parent any other — and only a vault
// that describes its hierarchy gets it checked. Turning a rule on for everybody
// would make existing vaults wrong about themselves overnight.
func (c *Config) Layered() bool {
	for _, t := range c.Types {
		if t.Level != LevelStandard {
			return true
		}
	}
	return false
}

// CanParent reports whether a task of one type may hold a task of another.
//
// A parent sits above its child. Jira says exactly one level above; that is
// right for its fixed three-level model and too strict here, where a vault may
// name levels 0, 1 and 3 and mean it. Above is the rule, and it is the one that
// stops a bug from owning an epic.
func (c *Config) CanParent(parentType, childType string) bool {
	if !c.Layered() {
		return true
	}
	return c.LevelOf(parentType) > c.LevelOf(childType)
}

// Schedulable reports whether a type belongs in a backlog on its own.
//
// Sub-tasks do not: they are work inside a task, and a backlog listing them
// beside the tasks they belong to is a backlog counting the same work twice.
// This is Jira's rule, and it is the visible consequence of having levels.
func (c *Config) Schedulable(name string) bool { return c.LevelOf(name) >= LevelStandard }

// DefaultType is what a task gets when nobody says. The first standard type,
// because an epic is a container and a sub-task belongs to something — neither
// is what somebody means by "a task".
func (c *Config) DefaultType() string {
	for _, t := range c.Types {
		if t.Level == LevelStandard {
			return t.Name
		}
	}
	if len(c.Types) > 0 {
		return c.Types[0].Name
	}
	return ""
}
