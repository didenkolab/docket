package server

import (
	"fmt"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/task"
	"github.com/didenkolab/docket/internal/vault"
)

// A branch, read as a change to the plan.
//
// A branch can already be drawn as the board it would produce — that answers
// "what would this be like". A reviewer is asking something else: "what exactly
// is being decided". The two are not the same, and the second is the one a
// person has to answer before merging.
//
// The alternative is what every host offers: a diff. A diff is the mechanism,
// not the meaning. Reading `-status: Backlog` / `+status: In review` across
// nine files and holding the result in your head is how a plan change gets
// approved without being understood — and the whole argument for keeping a
// tracker in git is that the plan becomes reviewable, which it only is if
// somebody can actually read the review.
//
// So this says it in the vault's own words: what arrived, what was dropped,
// what moved and where, whose criteria changed and by how many. One sentence
// per fact, in the vocabulary the vault uses about itself.

// planChange is what a proposal does.
type planChange struct {
	// Arrived and Dropped are tasks the proposal adds and takes away.
	Arrived []taskChange
	Dropped []taskChange
	// Changed are tasks that exist on both sides and are not the same.
	Changed []taskChange
	// Vocabulary is what the proposal does to docket.yaml — a proposal that adds
	// a status is a proposal about the workflow, and that is a bigger thing
	// than moving a card.
	Vocabulary []string
	// Untouched is how many tasks the proposal leaves alone, so the size of
	// what it does is visible against the size of what there is.
	Untouched int
}

// Nothing reports whether the proposal changes the plan at all. A branch that
// only touches code is a normal thing to have.
func (p planChange) Nothing() bool {
	return len(p.Arrived) == 0 && len(p.Dropped) == 0 && len(p.Changed) == 0 &&
		len(p.Vocabulary) == 0
}

// taskChange is one task and what the proposal does to it.
type taskChange struct {
	Key   string
	Title string
	// Status is where it is on the proposal's side, for a card-like reading.
	Status   string
	Category string
	// Says is what changed, one sentence per fact.
	Says []string
}

// comparePlans is the whole of it: two sets of tasks and two configurations,
// before and after.
func comparePlans(before, after []vault.Entry, was, now *project.Config) planChange {
	change := planChange{}

	beforeByKey := byKey(before)
	afterByKey := byKey(after)

	keys := make([]string, 0, len(afterByKey)+len(beforeByKey))
	for key := range afterByKey {
		keys = append(keys, key)
	}
	for key := range beforeByKey {
		if _, both := afterByKey[key]; !both {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	for _, key := range keys {
		old, existed := beforeByKey[key]
		fresh, exists := afterByKey[key]

		switch {
		case exists && !existed:
			change.Arrived = append(change.Arrived, taskChange{
				Key: key, Title: fresh.Title, Status: fresh.Status,
				Category: fresh.StatusCategory,
				Says:     []string{"new: " + fresh.Type + " in " + fresh.Status},
			})
		case existed && !exists:
			change.Dropped = append(change.Dropped, taskChange{
				Key: key, Title: old.Title, Status: old.Status,
				Category: old.StatusCategory,
				Says:     []string{"was " + old.Status},
			})
		default:
			if says := whatChanged(old, fresh, relationFields(now)); len(says) > 0 {
				change.Changed = append(change.Changed, taskChange{
					Key: key, Title: fresh.Title, Status: fresh.Status,
					Category: fresh.StatusCategory, Says: says,
				})
				continue
			}
			change.Untouched++
		}
	}

	change.Vocabulary = whatChangedInVocabulary(was, now)
	return change
}

// whatChanged is every difference between two versions of one task, said once
// each. Order matters: a move is what somebody is looking for, and a body
// rewrite is what they will miss.
func whatChanged(old, fresh *task.Task, relations []string) []string {
	var says []string

	if old.Status != fresh.Status {
		says = append(says, "moved from "+old.Status+" to "+fresh.Status)
	}
	if old.Title != fresh.Title {
		says = append(says, "renamed from “"+old.Title+"”")
	}
	if old.Type != fresh.Type {
		says = append(says, "now a "+fresh.Type+", was a "+old.Type)
	}
	if old.Priority != fresh.Priority {
		says = append(says, "priority "+old.Priority+" → "+fresh.Priority)
	}
	if old.Assignee != fresh.Assignee {
		says = append(says, assigneeChange(old.Assignee, fresh.Assignee))
	}
	if old.Parent != fresh.Parent {
		says = append(says, parentChange(old.Parent, fresh.Parent))
	}
	if said := setChange("label", old.Labels, fresh.Labels); said != "" {
		says = append(says, said)
	}
	if said := setChange("tag", old.Tags, fresh.Tags); said != "" {
		says = append(says, said)
	}
	for _, name := range relations {
		if said := setChange(name, old.Related(name), fresh.Related(name)); said != "" {
			says = append(says, said)
		}
	}
	if said := bodyChange(old.Body(), fresh.Body()); said != "" {
		says = append(says, said)
	}
	return says
}

// bodyChange says what happened to the text, and counts criteria rather than
// lines.
//
// A line count says nothing a reviewer wants — "eleven lines changed" is the
// diff again. Criteria are the part of a task's body that is a commitment, so
// they are what gets counted: added, taken away, and ticked.
func bodyChange(old, fresh string) string {
	if old == fresh {
		return ""
	}

	wasOpen, wasDone := criteria(old)
	nowOpen, nowDone := criteria(fresh)

	var said []string
	if added := (nowOpen + nowDone) - (wasOpen + wasDone); added > 0 {
		said = append(said, fmt.Sprintf("%s added", plural(added, "criterion", "criteria")))
	} else if added < 0 {
		said = append(said, fmt.Sprintf("%s taken away", plural(-added, "criterion", "criteria")))
	}
	if ticked := nowDone - wasDone; ticked > 0 {
		said = append(said, fmt.Sprintf("%s ticked", plural(ticked, "criterion", "criteria")))
	} else if ticked < 0 {
		said = append(said, fmt.Sprintf("%s unticked", plural(-ticked, "criterion", "criteria")))
	}
	if len(said) == 0 {
		return "the description was rewritten"
	}
	return strings.Join(said, ", ")
}

// criteria counts the open and ticked checkboxes in a body.
func criteria(body string) (open, done int) {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "- [ ]"), strings.HasPrefix(trimmed, "* [ ]"):
			open++
		case strings.HasPrefix(trimmed, "- [x]"), strings.HasPrefix(trimmed, "* [x]"),
			strings.HasPrefix(trimmed, "- [X]"), strings.HasPrefix(trimmed, "* [X]"):
			done++
		}
	}
	return open, done
}

// setChange says what came and went in a list, naming the members rather than
// counting them: which label was added is the fact, and how many is not.
func setChange(what string, old, fresh []string) string {
	gone, came := difference(old, fresh), difference(fresh, old)
	if len(gone) == 0 && len(came) == 0 {
		return ""
	}

	var said []string
	if len(came) > 0 {
		said = append(said, what+" "+strings.Join(came, ", ")+" added")
	}
	if len(gone) > 0 {
		said = append(said, what+" "+strings.Join(gone, ", ")+" taken off")
	}
	return strings.Join(said, "; ")
}

func difference(from, without []string) []string {
	has := map[string]bool{}
	for _, v := range without {
		has[strings.ToLower(v)] = true
	}
	var out []string
	for _, v := range from {
		if !has[strings.ToLower(v)] {
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func assigneeChange(old, fresh string) string {
	switch {
	case old == "":
		return "assigned to " + fresh
	case fresh == "":
		return "unassigned, was " + old
	}
	return "reassigned from " + old + " to " + fresh
}

func parentChange(old, fresh string) string {
	switch {
	case old == "":
		return "moved under " + fresh
	case fresh == "":
		return "taken out of " + old
	}
	return "moved from " + old + " to " + fresh
}

// whatChangedInVocabulary is what the proposal does to docket.yaml.
//
// Reported apart from the tasks, and first, because a proposal that adds a
// status is a proposal about how the team works — a bigger thing than any card
// it moves, and the one most likely to be skimmed past in a diff.
func whatChangedInVocabulary(was, now *project.Config) []string {
	if was == nil || now == nil {
		return nil
	}
	var says []string

	for _, said := range []struct {
		what       string
		old, fresh []string
	}{
		{"status", was.StatusNames(), now.StatusNames()},
		{"type", was.TypeNames(), now.TypeNames()},
		{"priority", was.Priorities, now.Priorities},
		{"project", was.ProjectKeys(), now.ProjectKeys()},
	} {
		if change := setChange(said.what, said.old, said.fresh); change != "" {
			says = append(says, change)
		}
	}
	if was.Name != now.Name {
		says = append(says, "the vault is renamed to “"+now.Name+"”")
	}
	if !sameWorkflow(was.Transitions, now.Transitions) {
		says = append(says, "the workflow changes: which status may follow which")
	}
	return says
}

func sameWorkflow(was, now map[string][]string) bool {
	if len(was) != len(now) {
		return false
	}
	for from, targets := range was {
		other, ok := now[from]
		if !ok || len(other) != len(targets) {
			return false
		}
		a, b := append([]string(nil), targets...), append([]string(nil), other...)
		sort.Strings(a)
		sort.Strings(b)
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
	}
	return true
}

func byKey(entries []vault.Entry) map[string]*task.Task {
	out := map[string]*task.Task{}
	for _, e := range entries {
		if e.Task != nil {
			out[e.Key] = e.Task
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
