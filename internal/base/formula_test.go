package base

import (
	"strings"
	"testing"
)

// The stage column docket writes into every board is a chain of ifs. A reader
// that could not evaluate it would report the vault's own flagship board as
// beyond us — and draw its columns as one group of everything.
func TestTheStageFormulaIsEvaluated(t *testing.T) {
	b, err := Parse([]byte(`
filters: 'note.key'
formulas:
  stage: 'if(note.status == "Discovery", "1. Discovery", if(note.status == "В работе", "2. В работе", ""))'
views:
  - type: cards
    name: Board
    groupBy:
      property: formula.stage
      direction: ASC
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Beyond) != 0 {
		t.Fatalf("the formula was not read: %v", b.Beyond)
	}

	for _, c := range []struct{ status, want string }{
		{"Discovery", "1. Discovery"},
		{"В работе", "2. В работе"},
		{"Готово", ""},
	} {
		got, ok := b.Compute("formula.stage", note("A/A-1 x.md", "status", c.status))
		if !ok {
			t.Fatalf("%s: the formula is not there", c.status)
		}
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.status, got, c.want)
		}
	}
}

// A property may be written with its note. prefix or without. The generated
// board does both in one file — `note.status` in a filter, `assignee` in the
// column order — so the two spellings have to be the same property.
func TestAPropertyIsTheSameWithOrWithoutItsPrefix(t *testing.T) {
	for _, c := range []struct{ written, want string }{
		{"note.assignee", "assignee"},
		{"assignee", "assignee"},
		{" note.status_category ", "status_category"},
	} {
		if got := Property(c.written); got != c.want {
			t.Errorf("%q read as %q, want %q", c.written, got, c.want)
		}
	}
}

// Bases computes more than this. A formula beyond the reader is remembered as
// one rather than failing the file: the view using it may not be the view
// somebody asked for.
func TestAFormulaBeyondTheReaderIsNamedNotGuessed(t *testing.T) {
	b, err := Parse([]byte(`
filters: 'note.key'
formulas:
  age: 'date(today) - note.created'
`))
	if err != nil {
		t.Fatalf("the whole file was refused for one formula: %v", err)
	}
	if _, ok := b.Formulas["age"]; ok {
		t.Error("a formula it cannot read was accepted anyway")
	}
	if _, ok := b.Beyond["age"]; !ok {
		t.Errorf("the formula is not named as beyond the reader: %v", b.Beyond)
	}
	if _, ok := b.Compute("formula.age", note("A/A-1 x.md")); ok {
		t.Error("it answered for a formula it cannot read")
	}
}

func TestAFormulaSaysWhatItCannotRead(t *testing.T) {
	_, err := ParseFormula(`if(note.status > "Done", "a", "b")`)
	if err == nil {
		t.Fatal("a comparison it does not do was accepted")
	}
	if !strings.Contains(err.Error(), "==") {
		t.Errorf("the refusal does not say what it reads: %v", err)
	}
}
