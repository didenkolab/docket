package check

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
)

// A value has to mean what the vault said it would.
//
// A vault may add its own properties, and a property nobody checks is a column
// of anything. A real import carried eighteen of them out of Jira — a story
// point estimate, a date of first response, a responsible developer — as
// untyped text that no page showed and no rule looked at, which is a
// spreadsheet with extra steps.
//
// The check is per kind and it is deliberately shallow. A number must parse, a
// date must be a date, a choice must be on the list, a link must be a URL. What
// it does not do is judge the value: whether "2026-08-31" is the right date is
// not a question a validator can ask, and pretending otherwise is how a rule
// starts being switched off.
//
// A field on a type that does not have it is reported too. That is the case
// that goes wrong quietly: a property carried over from a task that was
// retyped, still holding a value, still shown by nothing.

// checkFields applies rule 15.
func checkFields(add func(Finding), e vault.Entry, c *project.Config) {
	t := e.Task
	if t == nil || len(c.Fields) == 0 {
		return
	}

	for _, f := range c.Fields {
		line := t.PropertyLine(f.Name)
		has := t.HasProperty(f.Name)
		value := strings.TrimSpace(t.Property(f.Name))

		if !f.AppliesTo(t.Type) {
			if has {
				add(Finding{e.Path, line, RuleFields,
					fmt.Sprintf("%s is a field of %s, and this is a %s. Nothing shows it here, "+
						"so whatever it says is not being read",
						f.Shown(), strings.Join(f.Types, " and "), t.Type)})
			}
			continue
		}

		if value == "" {
			if f.Required {
				add(Finding{e.Path, t.PropertyLine("type"), RuleFields,
					fmt.Sprintf("%s is required of every %s and this one has none",
						f.Shown(), t.Type)})
			}
			continue
		}
		if wrong := WrongFor(f, value); wrong != "" {
			add(Finding{e.Path, line, RuleFields, wrong})
		}
	}
}

// wrongFor is what is wrong with a value, or empty.
// WrongFor is why a value does not fit the field, or "" when it does.
//
// Exported because a script writing through `docket set` has to be held to the
// same rule as a person: a validator that only runs at check time is a
// validator that finds out after the fact.
func WrongFor(f project.Field, value string) string {
	switch f.Kind {
	case project.FieldNumber:
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			return fmt.Sprintf("%s is a number and this says %q", f.Shown(), value)
		}

	case project.FieldDate:
		if _, err := time.Parse(vault.DateFormat, value); err != nil {
			return fmt.Sprintf("%s is a date and this says %q — write it as YYYY-MM-DD, "+
				"which is what Obsidian shows as a date and what sorts", f.Shown(), value)
		}

	case project.FieldMoment:
		// The formats a real source writes: RFC 3339, and Jira's own, which
		// differs only in having no colon in the offset.
		for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05.999-0700"} {
			if _, err := time.Parse(layout, value); err == nil {
				return ""
			}
		}
		return fmt.Sprintf("%s is a moment in time and this says %q — write it as "+
			"2026-08-31T14:05:00Z", f.Shown(), value)

	case project.FieldChoice:
		if !f.Offers(value) {
			return fmt.Sprintf("%s is %q, which is not one of %s",
				f.Shown(), value, strings.Join(f.Choices, ", "))
		}

	case project.FieldFlag:
		switch strings.ToLower(value) {
		case "true", "false":
		default:
			return fmt.Sprintf("%s is yes or no and this says %q — write true or false, "+
				"which is what Obsidian shows as a checkbox", f.Shown(), value)
		}

	case project.FieldLink:
		u, err := url.Parse(value)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Sprintf("%s is a link somewhere else and %q is not one. A link to "+
				"another note is a [[wikilink]] and belongs in the body or in a relation",
				f.Shown(), value)
		}
	}
	return ""
}
