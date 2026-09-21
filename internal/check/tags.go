package check

import (
	"fmt"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/vault"
)

// A tag is a set somebody asks for, and it is said once.
//
// A tag is not a node in the graph and has no page, so its whole value is the
// list of what carries it: the tag pane, and `tag:` in search. That gives one
// test — will anybody ever ask for this list? — and two ways to fail it.
//
// A set of one is not a set. Somebody wrote a word about one task and nobody
// will ever look for it, so it sits in the tag pane making the pane harder to
// read. The finding disappears the moment a second note carries it, which is
// the point: it is not wrong to invent a tag, it is wrong to leave it alone.
//
// A label said again as a tag is one fact in two places, and the copy is the
// one that goes stale. A label is a topic with a page; a tag is a set with
// nothing to explain. Anything that is both is one of them written twice.
//
// See docs/spec/Vault format.md §5.2.

// checkTags applies both to the whole vault at once, because neither question
// can be answered from one file.
func checkTags(entries []vault.Entry) []Finding {
	carriers := map[string][]string{} // tag → the paths carrying it
	labels := map[string]bool{}

	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		for _, tag := range e.Task.Tags {
			clean := strings.TrimSpace(tag)
			if clean == "" {
				continue
			}
			key := strings.ToLower(clean)
			carriers[key] = append(carriers[key], e.Path)
		}
		for _, label := range e.Task.Labels {
			labels[strings.ToLower(strings.TrimSpace(label))] = true
		}
	}

	var findings []Finding
	for tag, paths := range carriers {
		if len(paths) == 1 {
			findings = append(findings, Finding{paths[0], 0, RuleTags,
				fmt.Sprintf("%q is on this task and nothing else — a set of one is not a set. "+
					"Put it on the others it is true of, or take it off: a tag's whole value "+
					"is the list of what carries it", tag)})
		}
		// The last segment, because `area/payments` and the label `payments`
		// are the same word on an axis.
		last := tag
		if i := strings.LastIndex(tag, "/"); i >= 0 {
			last = tag[i+1:]
		}
		if labels[tag] || labels[last] {
			sort.Strings(paths)
			findings = append(findings, Finding{paths[0], 0, RuleTags,
				fmt.Sprintf("%q is a tag here and a label elsewhere — one fact in two places, "+
					"and the copy is the one that goes stale. A label is a topic with a page; "+
					"a tag is a set with nothing to explain. Pick one", tag)})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Message < findings[j].Message
	})
	return findings
}
