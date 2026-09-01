package check

import (
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// An assignee is somebody, and somebody is a page.
//
// The rule exists because a string nobody checks makes colleagues out of
// typos. On a real board the suggestion list and the filter menu were built
// from whatever the files happened to say, so one slip — `vadym_didekno` — was
// a person with work on them, in two menus, forever.
//
// It is deliberately gentle about the two states a vault can be in. A vault
// that has written no people at all is not in breach: `assignee: marina` reads
// perfectly well as a name, and a project of two people never needs more. The
// rule starts asking once the vault has said that people are pages — that is,
// once there is a people/ folder — because from then on a handle with no page
// is either a typo or somebody nobody wrote down, and both are worth saying.

// checkPeople reports handles that name nobody, and assignees still written as
// bare strings once the vault keeps people as pages.
func checkPeople(entries []vault.Entry, people []vault.Person) []Finding {
	if len(people) == 0 {
		return nil
	}

	var findings []Finding
	unknown := map[string][]vault.Entry{}

	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		handle := strings.TrimSpace(e.Task.Assignee)
		if handle == "" {
			continue
		}
		if _, ok := vault.PersonOf(people, handle); !ok {
			unknown[handle] = append(unknown[handle], e)
			continue
		}
		// Known, but written as a name rather than as a link — so Obsidian
		// draws no edge and the person's page has no backlink to it. The same
		// finding the vault already makes about a parent and a label.
		if !task.IsLink(e.Task.RawAssignee()) {
			findings = append(findings, Finding{
				e.Path, e.Task.PropertyLine("assignee"), RulePeople,
				"assignee " + handle + " is written as a name rather than as a link, " +
					"so their page has no backlink to this task: " +
					`assignee: "[[` + handle + `]]"`,
			})
		}
	}

	// One finding per unknown handle rather than per task: a handle used by
	// four hundred tasks is one thing to fix, and four hundred lines saying so
	// is a report nobody reads to the end.
	var handles []string
	for handle := range unknown {
		handles = append(handles, handle)
	}
	sort.Strings(handles)
	for _, handle := range handles {
		where := unknown[handle]
		findings = append(findings, Finding{
			where[0].Path, where[0].Task.PropertyLine("assignee"), RulePeople,
			plural(len(where), "1 task is", "tasks are") + " assigned to " + handle + ", and no page in " +
				vault.PeopleDir + "/ says who that is. Write one, or fix the handle — " +
				"`docket check --fix` writes the page.",
		})
	}
	return findings
}
