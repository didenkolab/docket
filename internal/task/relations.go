package task

import "strings"

// A relation is a link between two tasks that says how they are connected.
//
// Jira ships five of these — blocks, duplicates, clones, relates, causes — and
// keeps them in a table with an admin screen over it. Here the property name is
// the verb and the value is a link, so a relation is a line of frontmatter that
// Obsidian already draws in the graph and already counts as a backlink.
//
// They carry no structure. `parent` is hierarchy and decides what a board does;
// a relation is an annotation a person reads and acts on. Jira warns about
// exactly this confusion, because several of its marketplace apps ship a link
// type called "Parent-Child" that is not the parent field. The line is kept
// sharp here: one field is structure, these are not.

// Relation is one kind of connection, and the words for each direction.
type Relation struct {
	// Field is the property name, which is also the verb: `blocks`.
	Field string
	// Inverse is the property on the other task, when there is one. A relation
	// with no inverse is symmetric.
	Inverse string
	// Says is how it reads on the task that carries it.
	Says string
	// Said is how it reads on the task at the other end.
	Said string
}

// Relations are the ones a vault understands. Deliberately few: Jira's own set
// minus `clones`, which describes how a task came into existence rather than
// how it relates to the work, and which git records anyway.
var Relations = []Relation{
	{Field: "blocks", Inverse: "blocked_by", Says: "blocks", Said: "is blocked by"},
	{Field: "blocked_by", Inverse: "blocks", Says: "is blocked by", Said: "blocks"},
	{Field: "duplicates", Inverse: "duplicated_by", Says: "duplicates", Said: "is duplicated by"},
	{Field: "duplicated_by", Inverse: "duplicates", Says: "is duplicated by", Said: "duplicates"},
	{Field: "causes", Inverse: "caused_by", Says: "causes", Said: "is caused by"},
	{Field: "caused_by", Inverse: "causes", Says: "is caused by", Said: "causes"},
	{Field: "relates", Says: "relates to", Said: "relates to"},
}

// RelationOf finds a relation by its property name.
func RelationOf(field string) (Relation, bool) {
	for _, r := range Relations {
		if r.Field == field {
			return r, true
		}
	}
	return Relation{}, false
}

// IsRelation says whether a property name is one of these, so that `docket
// check` can tell a relation written as a string from somebody's own field.
func IsRelation(field string) bool {
	_, ok := RelationOf(field)
	return ok
}

// Related is the keys a task names under one relation.
//
// Both forms are read, as everywhere: a link gives its note name, from which
// the key is the first word, and a bare key is itself. Only the link form is
// written.
func (t *Task) Related(field string) []string {
	var keys []string
	for _, value := range t.rawRelated(field) {
		if key := KeyOf(NoteOf(value)); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

// RawRelated is what the file says, for `docket check` to report a relation
// still written as a string.
func (t *Task) RawRelated(field string) []string { return t.rawRelated(field) }

func (t *Task) rawRelated(field string) []string {
	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != field {
			continue
		}
		node := mapping.Content[i+1]
		var out []string
		for _, item := range node.Content {
			if v := strings.TrimSpace(item.Value); v != "" {
				out = append(out, v)
			}
		}
		// A single value written without a list is still one value.
		if len(out) == 0 && strings.TrimSpace(node.Value) != "" {
			out = append(out, strings.TrimSpace(node.Value))
		}
		return out
	}
	return nil
}

// SetRelated writes one relation as a list of links, by note name.
func (t *Task) SetRelated(field string, notes []string) {
	links := make([]string, 0, len(notes))
	for _, note := range notes {
		if note = strings.TrimSpace(note); note != "" {
			links = append(links, Link(note))
		}
	}
	if len(links) == 0 {
		t.Remove(field)
		return
	}
	t.setLinkList(field, links)
}

// AllRelations is every relation a task carries, in the order Relations lists
// them, so a task page reads the same way every time.
func (t *Task) AllRelations() map[string][]string {
	out := map[string][]string{}
	for _, r := range Relations {
		if keys := t.Related(r.Field); len(keys) > 0 {
			out[r.Field] = keys
		}
	}
	return out
}
