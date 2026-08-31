package check

import (
	"fmt"
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A document has a shape when the missing part is the expensive part.
//
// The knowledge base used to have no rules at all — the instructions said "there
// is no schema, it is a wiki" — and this project's own five decisions drifted
// into three different shapes. Nobody chose that. It happened because nothing
// said what the shape was, and each one was written by looking at whichever
// other one was open.
//
// The cost lands later and on somebody else. A decision without its alternatives
// is a decision nobody can revisit: the work of finding out what else was
// possible was done once, in somebody's head, and then not written down. Two
// years on, the only way to know whether the choice still holds is to do that
// work again.
//
// So only the decision has a required shape, and the rest do not. A design page
// is an argument and a required shape would flatten it into a form; a
// specification is whatever the format needs to say. A regime that shapes
// everything is a regime people write around.
//
// What is checkable is checked here. Whether the Context section actually states
// a problem is not, and that is where judgement lives — see
// docs/spec/documents.md.

// checkDocuments applies rule 14 to the knowledge base.
func checkDocuments(pages []vault.Page) []Finding {
	var findings []Finding

	numbered := map[string]string{} // decision number → path, so two cannot share one
	for _, p := range pages {
		if strings.TrimSpace(p.Type) == "" {
			findings = append(findings, Finding{p.Path, 0, RuleDocuments,
				"this page does not say what kind of document it is. Add type: — " +
					strings.Join(kinds(), ", ") + " — see docs/spec/documents.md"})
			continue
		}

		required := vault.RequiredSections(p.Type)
		missing := false
		for _, want := range required {
			if !p.Has(want) {
				missing = true
				findings = append(findings, Finding{p.Path, 0, RuleDocuments,
					fmt.Sprintf("a %s needs a ## %s section, and this one has %s",
						p.Type, want, listOf(p.Sections))})
			}
		}
		// Only when they are all there. Telling somebody the order is wrong
		// while a section is missing is telling them about the wrong problem.
		if !missing {
			if out, after := p.OutOfOrder(required); out != "" {
				findings = append(findings, Finding{p.Path, 0, RuleDocuments,
					fmt.Sprintf("## %s is above ## %s and belongs below it: a %s reads %s. "+
						"The order is what drifts first — a document written by glancing "+
						"at another picks up whatever sequence that one had",
						out, after, p.Type, strings.Join(required, ", then "))})
			}
		}

		if strings.EqualFold(p.Type, vault.PageDecision) {
			findings = append(findings, checkDecision(p, numbered)...)
		}
	}
	return findings
}

// checkDecision holds what is true of a decision and of nothing else.
func checkDecision(p vault.Page, numbered map[string]string) []Finding {
	var findings []Finding
	base := path.Base(p.Path)

	// Numbered in the order taken and never renumbered, because the number is
	// how one is cited — ADR-0004 is a name, and a name that moves is not one.
	if !vault.IsDecisionName(base) {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			"a decision is named NNNN-kebab-title.md, numbered in the order it was taken. " +
				base + " is not, so it has no stable number to cite"})
	} else {
		number := base[:4]
		if where, taken := numbered[number]; taken {
			findings = append(findings, Finding{p.Path, 0, RuleDocuments,
				fmt.Sprintf("decision %s is already %s. Two documents with one number "+
					"means a citation points at both", number, where)})
		} else {
			numbered[number] = p.Path
		}
	}

	// A status said in the body as well as in the frontmatter is one fact in two
	// places, and the copy is the one that goes stale — 0001 carried a ## Status
	// section for months after its frontmatter had moved on.
	if p.Has("Status") {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			"this decision keeps its status in a ## Status section as well as in its " +
				"frontmatter. Two records of one fact, and the section is the one nothing " +
				"reads, so it is the one that goes stale"})
	}

	if !known(p.Status, vault.DecisionStatuses) {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			fmt.Sprintf("a decision's status is one of %s, and this one says %q",
				strings.Join(vault.DecisionStatuses, ", "), p.Status)})
	}
	if strings.TrimSpace(p.Date) == "" {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			"a decision needs the date it was taken: a choice is only readable against " +
				"what was known at the time"})
	}

	// A superseded decision that does not say by what is the worst state of the
	// three: it tells you not to follow it and not where to look instead.
	if strings.EqualFold(p.Status, "superseded") && strings.TrimSpace(p.Supersedes) == "" {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			"this decision is superseded and does not say by what. Add " +
				`supersedes: "[[the one that replaced it]]"`})
	}
	if raw := strings.TrimSpace(p.Supersedes); raw != "" && !strings.HasPrefix(raw, "[[") {
		findings = append(findings, Finding{p.Path, 0, RuleDocuments,
			fmt.Sprintf("supersedes %q is a string, so the two decisions are not joined in "+
				"the graph — write it as %s", raw, task.Link(raw))})
	}
	return findings
}

func known(value string, of []string) bool {
	for _, one := range of {
		if strings.EqualFold(value, one) {
			return true
		}
	}
	return false
}

func kinds() []string {
	return []string{vault.PageDecision, vault.PageDesign, vault.PageSpec,
		vault.SprintType, vault.PagePlain}
}

// listOf names what a page does have, so a finding says what to change rather
// than only what is missing.
func listOf(sections []string) string {
	if len(sections) == 0 {
		return "no sections at all"
	}
	quoted := make([]string, 0, len(sections))
	for _, s := range sections {
		quoted = append(quoted, "## "+s)
	}
	return strings.Join(quoted, ", ")
}
