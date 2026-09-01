package check

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A sprint is a page, so most of what could go wrong with one is caught by the
// rules that already exist: a dead wikilink is rule 8, a nested property is
// rule 6. What is left is the three things that are specific to a sprint, and
// none of them can be answered from one file.
//
// A `sprint:` that is a bare string is the same mistake rule 10 catches for
// relations, and it is worth catching separately because it is silent: the
// board reads it, the vault works, and the graph has no edge — which is the
// whole reason the property is a link.
//
// A `sprint:` pointing at something that is not a sprint page usually means the
// page was never written. The task is then in a sprint nobody can open, and the
// commitment it claims to be part of has no goal, no dates and no retrospective.
//
// Two sprints covering the same day is a planning mistake, and the interface has
// to pick one, so it says which and why rather than choosing quietly.
//
// See docs/spec/vault-format.md and docs/design/how-things-connect.md.

// And one that was found by measuring rather than by reasoning: a sprint page
// that names its tasks in prose.
//
// The `sprint:` property draws one edge per task — thirteen per cent of a real
// vault's edges, which is what a label costs and is the mechanism working. But
// sprint pages whose prose cites every task with a wikilink took a further
// twenty-eight per cent, made the three sprints the three most connected notes
// in the vault, and pushed the largest cluster from thirty of forty-three notes
// to forty-three of forty-six — the graph collapsed into one blob. That is the
// same failure this project already removed once: how-things-connect §5 records
// index pages at eighteen per cent and judges it unacceptable.
//
// By §2's test it is not close. Standing at a task, "the sprint page mentions
// me" says nothing the property has not already said; and where the prose cites
// a task that is not in that sprint, the sprint becomes a hub over work it does
// not contain. So a wikilink from a sprint page to a task is reported either
// way, and the contents of a sprint are its backlinks.
//
// Links to pages are left alone. A sprint citing the decision that produced it
// is an edge that teaches something, which is exactly what §5 asks for.

// checkSprints applies to the whole vault at once. `now` is what the vault's
// today is, passed in so a test is not a race against the clock.
func checkSprints(entries []vault.Entry, sprints []vault.Sprint, now time.Time) []Finding {
	var findings []Finding

	byNote := map[string]vault.Sprint{}
	for _, s := range sprints {
		byNote[strings.ToLower(s.Note)] = s
		if s.Trouble != "" {
			findings = append(findings, Finding{s.Path, 0, RuleSprints,
				"this sprint's dates cannot be read: " + s.Trouble})
		}
	}

	for _, e := range entries {
		if e.Task == nil || e.Task.Sprint == "" {
			continue
		}
		line := e.Task.PropertyLine("sprint")
		raw := strings.TrimSpace(e.Task.RawSprint())

		// A link, not a string. Same reasoning as rule 10 and the same fix.
		if !strings.HasPrefix(raw, "[[") {
			findings = append(findings, Finding{e.Path, line, RuleSprints,
				fmt.Sprintf("sprint %q is a string, so it is not an edge in the graph and the "+
					"sprint's own page has no backlink to this task — write it as "+
					"%s", raw, task.Link(raw))})
			continue
		}

		if _, ok := byNote[strings.ToLower(e.Task.Sprint)]; !ok {
			findings = append(findings, Finding{e.Path, line, RuleSprints,
				fmt.Sprintf("sprint %q is not a sprint page: a task in a sprint nobody wrote "+
					"is in a commitment with no goal and no dates. Add %s/%s.md with "+
					"type: %s, or take the property off",
					e.Task.Sprint, vault.SprintDir, e.Task.Sprint, vault.SprintType)})
		}
	}

	findings = append(findings, overlapping(sprints, now)...)
	findings = append(findings, listedInProse(entries, sprints)...)
	return findings
}

// listedInProse reports a sprint page that names tasks with wikilinks.
//
// One finding per page rather than one per link: it is one decision to make,
// and a page citing nine tasks would otherwise bury everything else `docket
// check` has to say.
func listedInProse(entries []vault.Entry, sprints []vault.Sprint) []Finding {
	byNote := map[string]vault.Entry{} // a task's note name → the task
	for _, e := range entries {
		if e.Task != nil {
			byNote[strings.ToLower(e.Note())] = e
		}
	}

	var findings []Finding
	for _, s := range sprints {
		var named []vault.Entry
		seen := map[string]bool{}
		for _, target := range task.Links(s.Body) {
			e, isTask := byNote[strings.ToLower(lastSegment(target))]
			if !isTask || seen[e.Key] {
				continue
			}
			seen[e.Key] = true
			named = append(named, e)
		}
		if len(named) == 0 {
			continue
		}
		// By key the way a board orders them, so PIER-2 comes before PIER-11.
		sort.SliceStable(named, func(a, b int) bool {
			if named[a].Project != named[b].Project {
				return named[a].Project < named[b].Project
			}
			return named[a].Number < named[b].Number
		})

		// How many of them are not even in this sprint, which is the sharper
		// half of the fault: a hub over work it does not contain.
		outside := 0
		keys := make([]string, 0, len(named))
		for _, e := range named {
			keys = append(keys, e.Key)
			if !strings.EqualFold(e.Task.Sprint, s.Note) {
				outside++
			}
		}

		message := fmt.Sprintf("this page links %s: %s. A sprint's contents are its "+
			"backlinks — write the key in backticks instead. See "+
			"docs/design/how-things-connect.md §2",
			plural(len(keys), "1 task", "tasks"), strings.Join(keys, ", "))
		if outside > 0 {
			message = fmt.Sprintf("this page links %s, %d of them not in this sprint: %s. "+
				"A sprint's contents are its backlinks, and a link to work it does not "+
				"hold makes it a hub over that work — write the key in backticks "+
				"instead. See docs/design/how-things-connect.md §2",
				plural(len(keys), "1 task", "tasks"), outside, strings.Join(keys, ", "))
		}
		findings = append(findings, Finding{s.Path, 0, RuleSprints, message})
	}
	return findings
}

// lastSegment is a wikilink target without its folder, and without the heading
// or display text a link may carry.
func lastSegment(target string) string {
	if bar := strings.Index(target, "|"); bar >= 0 {
		target = target[:bar]
	}
	if hash := strings.Index(target, "#"); hash >= 0 {
		target = target[:hash]
	}
	if slash := strings.LastIndex(target, "/"); slash >= 0 {
		target = target[slash+1:]
	}
	return strings.TrimSpace(target)
}

// plural writes a count with the right word. `one` is the whole phrase, so a
// caller can say "1 task" without the count being printed twice.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// overlapping reports sprints that cover the same day.
//
// Reported once per pair, on the later of the two, because that is the one
// somebody just wrote.
func overlapping(sprints []vault.Sprint, now time.Time) []Finding {
	var usable []vault.Sprint
	for _, s := range sprints {
		// A sprint whose dates were inferred cannot overlap wrongly: parallel
		// sprints are what an import of several teams' boards actually holds,
		// and the vault has already said the dates are a guess. See
		// `docket adopt`.
		if s.Inferred {
			continue
		}
		if s.Trouble == "" && !s.Starts.IsZero() {
			usable = append(usable, s)
		}
	}
	sort.SliceStable(usable, func(a, b int) bool { return usable[a].Starts.Before(usable[b].Starts) })

	var findings []Finding
	for i := 1; i < len(usable); i++ {
		before, after := usable[i-1], usable[i]
		if after.Starts.After(before.Ends) {
			continue
		}
		findings = append(findings, Finding{after.Path, 0, RuleSprints,
			fmt.Sprintf("this sprint overlaps %q, which runs to %s: a day belongs to one "+
				"sprint, and a board asked which one is running has to choose",
				before.Title, before.Ends.Format(vault.DateFormat))})
	}
	return findings
}
