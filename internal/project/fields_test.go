package project

import (
	"strings"
	"testing"
)

// A vault adds a field, and the vocabulary is where it says so.
func TestFieldsInTheVocabulary(t *testing.T) {
	c, err := Parse([]byte(`name: Acme
projects:
  - key: ACME
statuses:
  - {name: Backlog, category: todo}
types:
  - {name: epic, level: 1}
  - {name: bug}
priorities: [normal]
fields:
  - name: found_in
    label: Found in
    kind: text
    types: [bug]
    help: Which build it was seen on
  - name: risk
    kind: choice
    choices: [low, high]
  - name: due
    kind: date
    required: true
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// A field for one type is not offered on another; one with no types is on
	// everything, because that is the ordinary case.
	if got := len(c.FieldsFor("bug")); got != 3 {
		t.Errorf("a bug carries %d fields, want 3", got)
	}
	if got := len(c.FieldsFor("epic")); got != 2 {
		t.Errorf("an epic carries %d fields, want 2 — found_in is a bug's", got)
	}

	// A label when there is one, the name when there is not.
	found, _ := c.FieldNamed("found_in")
	if found.Shown() != "Found in" {
		t.Errorf("shown as %q", found.Shown())
	}
	risk, _ := c.FieldNamed("risk")
	if risk.Shown() != "risk" {
		t.Errorf("a field with no label is shown as %q", risk.Shown())
	}
	if !risk.Offers("high") || risk.Offers("medium") {
		t.Error("a choice offers what it declares and nothing else")
	}
}

// What a vault may not declare, and why each one would break something.
func TestFieldsRefused(t *testing.T) {
	vault := func(fields string) error {
		_, err := Parse([]byte(`name: Acme
projects:
  - key: ACME
statuses:
  - {name: Backlog, category: todo}
types:
  - {name: bug}
priorities: [normal]
fields:
` + fields))
		return err
	}

	for _, c := range []struct{ what, fields, says string }{
		{"a property the format owns", "  - {name: status, kind: text}", "already owns"},
		{"a relation", "  - {name: blocks, kind: text}", "already owns"},
		{"a name YAML would have to quote", "  - {name: Found In, kind: text}", "usable property name"},
		{"a name starting with a digit", "  - {name: 2fa, kind: flag}", "usable property name"},
		{"the same field twice",
			"  - {name: risk, kind: text}\n  - {name: risk, kind: number}", "declared twice"},
		{"a kind nobody implements", "  - {name: risk, kind: colour}", "want one of"},
		{"a choice offering nothing", "  - {name: risk, kind: choice}", "offers nothing to choose"},
		{"choices on something that is not a choice",
			"  - {name: risk, kind: text, choices: [a, b]}", "only a choice field has"},
		{"a type the vault does not have",
			"  - {name: risk, kind: text, types: [epic]}", "does not have"},
	} {
		err := vault(c.fields)
		if err == nil {
			t.Errorf("%s: accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: said %q, want something about %q", c.what, err, c.says)
		}
	}
}
