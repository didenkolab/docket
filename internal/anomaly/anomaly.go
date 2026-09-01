// Package anomaly finds what is odd about how the work is connected.
//
// Not a marketplace category. Nothing in the Atlassian top hundred does this,
// because in Jira the links between issues are rows in a table and nobody looks
// at their shape. Here the vault is a graph — Obsidian draws it, and the tool
// measures it — so questions become answerable that a board cannot ask at all:
// what is adrift, what only one person can touch, which label has stopped
// meaning anything, and where two tasks disagree about their own relationship.
//
// Every finding is a question, not a verdict. A task nobody links to may be the
// most important thing in the project; a cluster one person owns may be the
// only person who should. What the tool can say is "this is unusual, and here
// is why it noticed" — and then somebody who knows the work decides.
package anomaly

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Kinds of finding, so a caller can filter and a page can group.
const (
	Adrift      = "adrift"       // nothing links to it and it links to nothing
	OnlyOwner   = "only-owner"   // a body of work one person is the whole of
	Overgrown   = "overgrown"    // a label on so much that it distinguishes nothing
	OneSided    = "one-sided"    // two tasks disagree about their relationship
	Contained   = "contained"    // a container and its children disagree about being done
	Unattended  = "unattended"   // in progress, and nobody is on it
	Concentrate = "concentrated" // one note holds a fifth of every link
)

// Finding is one thing worth a look.
type Finding struct {
	Kind string
	// About is the task key or note name it is about.
	About string
	// Says is the finding in a sentence.
	Says string
	// Why is what made it noticeable — the number behind the sentence.
	Why string
}

// Look is everything the graph and the tasks say together.
//
// Ordered by kind and then by name, so two runs over an unchanged vault print
// the same thing: a report that shuffles is a report nobody can diff.
func Look(entries []vault.Entry, shape vault.Shape, c *project.Config) []Finding {
	var out []Finding
	out = append(out, adrift(entries)...)
	out = append(out, onlyOwner(entries)...)
	out = append(out, overgrown(entries)...)
	out = append(out, oneSided(entries, c)...)
	out = append(out, contained(entries, c)...)
	out = append(out, unattended(entries, c)...)
	out = append(out, concentrated(shape)...)

	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Kind != out[b].Kind {
			return out[a].Kind < out[b].Kind
		}
		return out[a].About < out[b].About
	})
	return out
}

// open reports whether a task is still work.
func open(e vault.Entry) bool {
	return e.Task != nil && e.Task.StatusCategory != project.CategoryDone
}

// adrift is work nothing links to and which links to nothing.
//
// Not "unloved": a task with no parent, no label, no relation and no backlink
// is a task that fell out of the plan. It will not appear under any epic, in
// any label's page, or in anybody's search for the thing it belongs to — it can
// only be found by scrolling the column it is in.
func adrift(entries []vault.Entry) []Finding {
	linkedTo := map[string]bool{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if e.Task.Parent != "" {
			linkedTo[e.Task.Parent] = true
		}
		for _, link := range e.Task.Links() {
			linkedTo[task.KeyOf(link)] = true
		}
	}

	var out []Finding
	for _, e := range entries {
		if !open(e) {
			continue
		}
		t := e.Task
		if t.Parent != "" || len(t.Labels) > 0 || len(t.Tags) > 0 || t.Sprint != "" {
			continue
		}
		if len(t.Links()) > 0 || linkedTo[e.Key] {
			continue
		}
		out = append(out, Finding{
			Kind: Adrift, About: e.Key,
			Says: "nothing links to it and it links to nothing",
			Why:  "no parent, no label, no sprint, no relation, no backlink",
		})
	}
	return out
}

// onlyOwner is a body of related work one person is the whole of.
//
// The bus factor, asked of the graph rather than of a spreadsheet: an epic
// whose every open child is on one person. Often correct and worth knowing
// anyway — it is the thing that becomes a problem on the day somebody is ill,
// and nobody notices it from a board.
func onlyOwner(entries []vault.Entry) []Finding {
	children := map[string][]vault.Entry{}
	for _, e := range entries {
		if open(e) && e.Task.Parent != "" {
			children[e.Task.Parent] = append(children[e.Task.Parent], e)
		}
	}

	var out []Finding
	for parent, held := range children {
		if len(held) < 3 {
			continue
		}
		who := ""
		alone := true
		for _, e := range held {
			assignee := strings.TrimSpace(e.Task.Assignee)
			if assignee == "" {
				alone = false
				break
			}
			if who == "" {
				who = assignee
			}
			if assignee != who {
				alone = false
				break
			}
		}
		if !alone || who == "" {
			continue
		}
		out = append(out, Finding{
			Kind: OnlyOwner, About: parent,
			Says: "every open task under it is on " + who,
			Why:  fmt.Sprintf("%d of %d", len(held), len(held)),
		})
	}
	return out
}

// overgrownAt is the share of open work a label may carry before it has stopped
// telling anybody anything.
const overgrownAt = 0.4

// overgrown is a label on so much that it no longer distinguishes.
//
// A label is a set somebody asks for. One on two thirds of the board is not a
// set, it is the board — and the cost is real: it is the label people put on
// everything instead of thinking about which one applies.
func overgrown(entries []vault.Entry) []Finding {
	carrying := 0
	count := map[string]int{}
	for _, e := range entries {
		if !open(e) {
			continue
		}
		carrying++
		for _, label := range e.Task.Labels {
			count[label]++
		}
	}
	if carrying < 10 {
		// Too small to say anything. On a board of six tasks a label on three
		// of them is a label doing its job.
		return nil
	}

	var out []Finding
	for label, held := range count {
		share := float64(held) / float64(carrying)
		if share < overgrownAt {
			continue
		}
		out = append(out, Finding{
			Kind: Overgrown, About: label,
			Says: "carried by most of the open work, so it no longer says which",
			Why:  fmt.Sprintf("%d of %d open tasks — %.0f%%", held, carrying, share*100),
		})
	}
	return out
}

// oneSided is two tasks disagreeing about their own relationship.
//
// A says it blocks B and B does not say it is blocked. Not an error — the
// inverse is not required, and Obsidian shows the link from the other side as a
// backlink either way — but it is how a board comes to show one of them as
// clear to start and the other as waiting.
func oneSided(entries []vault.Entry, c *project.Config) []Finding {
	byKey := map[string]vault.Entry{}
	for _, e := range entries {
		if e.Task != nil {
			byKey[e.Key] = e
		}
	}

	var out []Finding
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		for _, r := range c.Relations() {
			if r.Inverse == "" {
				continue
			}
			for _, key := range e.Task.Related(r.Name) {
				other, known := byKey[key]
				if !known {
					continue // rule 10 reports a relation to nothing
				}
				if said(other.Task.Related(r.Inverse), e.Key) {
					continue
				}
				out = append(out, Finding{
					Kind: OneSided, About: e.Key,
					Says: fmt.Sprintf("says it %s %s, and %s does not say it %s %s",
						r.Says, key, key, r.Said, e.Key),
					Why: r.Name + " has an inverse and only one side carries it",
				})
			}
		}
	}
	return out
}

func said(keys []string, want string) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

// contained is a container and its children disagreeing about being finished.
//
// A closed epic with open children is the one that matters: the work is still
// there, and it is no longer on anybody's board.
func contained(entries []vault.Entry, c *project.Config) []Finding {
	if !c.Layered() {
		return nil
	}
	byKey := map[string]vault.Entry{}
	for _, e := range entries {
		if e.Task != nil {
			byKey[e.Key] = e
		}
	}

	openChildren := map[string]int{}
	for _, e := range entries {
		if open(e) && e.Task.Parent != "" {
			openChildren[e.Task.Parent]++
		}
	}

	var out []Finding
	for key, count := range openChildren {
		parent, known := byKey[key]
		if !known || parent.Task.StatusCategory != project.CategoryDone {
			continue
		}
		out = append(out, Finding{
			Kind: Contained, About: key,
			Says: "is finished, and work under it is not",
			Why:  fmt.Sprintf("%d open children", count),
		})
	}
	return out
}

// unattended is work in progress that nobody is on.
//
// The status says somebody is doing it and the file says nobody is. One of the
// two is wrong, and either way the board is telling people something untrue
// about who to ask.
func unattended(entries []vault.Entry, c *project.Config) []Finding {
	var out []Finding
	for _, e := range entries {
		if e.Task == nil || e.Task.StatusCategory != project.CategoryDoing {
			continue
		}
		if strings.TrimSpace(e.Task.Assignee) != "" {
			continue
		}
		out = append(out, Finding{
			Kind: Unattended, About: e.Key,
			Says: "is in " + e.Task.Status + " and nobody is on it",
			Why:  "a doing status with no assignee",
		})
	}
	return out
}

// concentratedAt is the share of every link one note may hold before it is
// doing what an index does.
const concentratedAt = 15.0

// concentrated is one note holding a disproportionate share of every link.
//
// The measurement that caught two real mistakes in this project: navigation
// pages held eighteen per cent of every edge between them, and sprint pages
// reached twenty eight. Both looked like structure and were noise — a note
// everything points at connects nothing to anything.
func concentrated(shape vault.Shape) []Finding {
	var out []Finding
	for _, hub := range shape.Hubs {
		if hub.Share < concentratedAt {
			continue
		}
		out = append(out, Finding{
			Kind: Concentrate, About: hub.Note,
			Says: "holds a large share of every link in the vault",
			Why: fmt.Sprintf("%d edges — %.0f%% of all of them",
				hub.Edges, hub.Share),
		})
	}
	return out
}
