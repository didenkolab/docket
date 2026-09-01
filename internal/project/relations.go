package project

import (
	"fmt"
	"strings"
)

// What one task can say about another.
//
// These were five names in the code, and a vault could not add a sixth. That
// held for as long as a tracker was only a tracker: Jira ships the same five
// and keeps them in a table. It stops holding the moment anybody wants what the
// marketplace sells — a test that covers a requirement, a risk that threatens a
// release, a ticket that implements a decision. Every one of those is a verb
// between two pieces of work, and a format that owns the list of verbs is a
// format nobody can extend without a fork.
//
// So the list is vocabulary, like statuses and types: the vault says it, and
// what the vault does not say is the sensible default. A relation stays what it
// was — a property whose value is a link, so Obsidian draws it and counts it as
// a backlink — and it stays not structure: `parent` decides what a board does,
// and none of these do. Jira warns about the same confusion, because several of
// its apps ship a link type called "Parent-Child" that is not the parent field.

// Relation is one direction of a connection: the property name is the verb, and
// the words are how it reads at each end.
type Relation struct {
	// Name is the property, which is also the verb: `blocks`.
	Name string `yaml:"name"`
	// Inverse is the property on the task at the other end, when there is one.
	// A relation with no inverse is symmetric — `relates` reads the same both
	// ways.
	Inverse string `yaml:"inverse,omitempty"`
	// Says is how it reads on the task that carries it, and Said is how it
	// reads on the task at the other end. Both default to the names.
	Says string `yaml:"says,omitempty"`
	Said string `yaml:"said,omitempty"`
}

// DefaultRelations are what a vault understands when it says nothing.
//
// Jira's own set minus `clones`, which describes how a task came into existence
// rather than how it relates to the work, and which git records anyway.
func DefaultRelations() []Relation {
	return []Relation{
		{Name: "blocks", Inverse: "blocked_by", Says: "blocks", Said: "is blocked by"},
		{Name: "blocked_by", Inverse: "blocks", Says: "is blocked by", Said: "blocks"},
		{Name: "duplicates", Inverse: "duplicated_by", Says: "duplicates", Said: "is duplicated by"},
		{Name: "duplicated_by", Inverse: "duplicates", Says: "is duplicated by", Said: "duplicates"},
		{Name: "causes", Inverse: "caused_by", Says: "causes", Said: "is caused by"},
		{Name: "caused_by", Inverse: "causes", Says: "is caused by", Said: "causes"},
		{Name: "relates", Says: "relates to", Said: "relates to"},
	}
}

// Relations are every relation this vault understands, both directions of each,
// in the order a task page should read them.
//
// The defaults first and always: a vault that adds `tests` has not stopped
// believing in `blocks`, and a declared list that replaced them would break
// every task in it the moment somebody added one verb.
func (c *Config) Relations() []Relation {
	out := DefaultRelations()
	known := map[string]bool{}
	for _, r := range out {
		known[r.Name] = true
	}

	for _, declared := range c.Declared {
		for _, r := range bothWays(declared) {
			if known[r.Name] {
				continue
			}
			known[r.Name] = true
			out = append(out, r)
		}
	}
	return out
}

// bothWays is a declared relation as the one or two directions it stands for.
//
// A vault writes the pair once — `name: tests, inverse: tested_by` — because
// writing both halves by hand is how they end up disagreeing about which is the
// inverse of which.
func bothWays(r Relation) []Relation {
	forward := Relation{
		Name: strings.TrimSpace(r.Name), Inverse: strings.TrimSpace(r.Inverse),
		Says: firstSaid(r.Says, r.Name), Said: firstSaid(r.Said, r.Inverse, r.Name),
	}
	if forward.Inverse == "" {
		return []Relation{forward}
	}
	return []Relation{forward, {
		Name: forward.Inverse, Inverse: forward.Name,
		Says: forward.Said, Said: forward.Says,
	}}
}

// firstSaid is the first of these that says anything, with underscores read as
// spaces — `tested_by` reads as "tested by" without anybody writing it twice.
func firstSaid(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return strings.ReplaceAll(v, "_", " ")
		}
	}
	return ""
}

// RelationOf finds a relation by its property name.
func (c *Config) RelationOf(name string) (Relation, bool) {
	for _, r := range c.Relations() {
		if r.Name == name {
			return r, true
		}
	}
	return Relation{}, false
}

// IsRelation says whether a property name is one, so that `docket check` can
// tell a relation written as a string from somebody's own field.
func (c *Config) IsRelation(name string) bool {
	_, ok := c.RelationOf(name)
	return ok
}

// validateRelations checks what the vault declared.
func (c *Config) validateRelations() error {
	builtin := map[string]bool{}
	for _, r := range DefaultRelations() {
		builtin[r.Name] = true
	}

	seen := map[string]bool{}
	for _, declared := range c.Declared {
		// Asked before the pair is expanded: a relation that is its own inverse
		// expands to the same name twice, and "declared twice" is a true
		// sentence about the wrong mistake.
		if inverse := strings.TrimSpace(declared.Inverse); inverse != "" &&
			strings.EqualFold(declared.Name, inverse) {
			return fmt.Errorf("relation %q is its own inverse: leave inverse out to say "+
				"it reads the same both ways", declared.Name)
		}

		for _, r := range bothWays(declared) {
			switch {
			case r.Name == "":
				return fmt.Errorf("a relation with no name")
			case !FieldName.MatchString(r.Name):
				return fmt.Errorf("relation %q: a relation is a property name — "+
					"a letter, then letters, digits or underscores", r.Name)
			case builtin[r.Name]:
				return fmt.Errorf("relation %q is one the format already has, "+
					"and redefining it would make two vaults mean different things "+
					"by the same word", r.Name)
			case OwnedProperties[r.Name]:
				return fmt.Errorf("relation %q is a property the format owns", r.Name)
			case seen[r.Name]:
				return fmt.Errorf("relation %q is declared twice", r.Name)
			}
			seen[r.Name] = true

			for _, f := range c.Fields {
				if f.Name == r.Name {
					return fmt.Errorf("relation %q is also declared as a field: "+
						"one property cannot be both a link to a task and a value", r.Name)
				}
			}
		}
	}
	return nil
}
