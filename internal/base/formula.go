package base

import (
	"fmt"
	"strings"
)

// Formulas — the computed columns a base declares.
//
// Bases has a whole expression language and this reads one shape of it:
// `if(condition, then, else)`, nested. That is not an arbitrary subset. It is
// what docket itself generates — the board's stage column is a chain of them —
// so a board shipped with the vault draws here rather than being reported as
// beyond us. Anything else is refused by name, the same as a filter.

// Value is something a formula evaluates to, for one note.
type Value interface {
	Eval(n Note) string
}

type literal struct{ text string }

func (v literal) Eval(Note) string { return v.text }

type property struct{ name string }

func (v property) Eval(n Note) string { return n.Values[v.name] }

type conditional struct {
	left, right  Value
	equal        bool
	then, orElse Value
}

func (v conditional) Eval(n Note) string {
	if same(v.left.Eval(n), v.right.Eval(n)) == v.equal {
		return v.then.Eval(n)
	}
	return v.orElse.Eval(n)
}

// ParseFormula reads one formula.
func ParseFormula(raw string) (Value, error) {
	p := &formulaReader{text: raw}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if rest := strings.TrimSpace(p.text[p.at:]); rest != "" {
		return nil, fmt.Errorf("this formula has %q left over that the board cannot read", rest)
	}
	return v, nil
}

type formulaReader struct {
	text string
	at   int
}

func (p *formulaReader) skip() {
	for p.at < len(p.text) && (p.text[p.at] == ' ' || p.text[p.at] == '\n' || p.text[p.at] == '\t') {
		p.at++
	}
}

func (p *formulaReader) value() (Value, error) {
	p.skip()
	if p.at >= len(p.text) {
		return nil, fmt.Errorf("a formula that says nothing")
	}

	switch {
	case p.text[p.at] == '"' || p.text[p.at] == '\'':
		return p.quoted()
	case strings.HasPrefix(p.text[p.at:], "if("):
		return p.conditional()
	}

	// A bare word: a property, with or without its note. prefix.
	start := p.at
	for p.at < len(p.text) && !strings.ContainsRune(",()", rune(p.text[p.at])) {
		p.at++
	}
	word := strings.TrimSpace(p.text[start:p.at])
	if word == "" {
		return nil, fmt.Errorf("a formula that says nothing")
	}
	if strings.ContainsAny(word, "+*/") {
		return nil, fmt.Errorf("this formula does arithmetic, which the board does not read: %s", word)
	}
	return property{Property(word)}, nil
}

func (p *formulaReader) quoted() (Value, error) {
	quote := p.text[p.at]
	p.at++
	start := p.at
	for p.at < len(p.text) && p.text[p.at] != quote {
		p.at++
	}
	if p.at >= len(p.text) {
		return nil, fmt.Errorf("a formula with a quote left open")
	}
	text := p.text[start:p.at]
	p.at++
	return literal{text}, nil
}

func (p *formulaReader) conditional() (Value, error) {
	p.at += len("if(")

	left, op, right, err := p.condition()
	if err != nil {
		return nil, err
	}
	if err := p.comma(); err != nil {
		return nil, err
	}
	then, err := p.value()
	if err != nil {
		return nil, err
	}
	if err := p.comma(); err != nil {
		return nil, err
	}
	orElse, err := p.value()
	if err != nil {
		return nil, err
	}

	p.skip()
	if p.at >= len(p.text) || p.text[p.at] != ')' {
		return nil, fmt.Errorf("an if( with no closing bracket")
	}
	p.at++

	return conditional{left: left, right: right, equal: op == "==", then: then, orElse: orElse}, nil
}

// condition reads `x == y` or `x != y` up to the comma.
func (p *formulaReader) condition() (left Value, op string, right Value, err error) {
	if left, err = p.upToOperator(); err != nil {
		return nil, "", nil, err
	}
	p.skip()
	if p.at+1 >= len(p.text) {
		return nil, "", nil, fmt.Errorf("an if( with nothing to compare")
	}
	op = p.text[p.at : p.at+2]
	if op != "==" && op != "!=" {
		return nil, "", nil, fmt.Errorf("this formula compares with %q, and the board reads == and != only", op)
	}
	p.at += 2
	if right, err = p.value(); err != nil {
		return nil, "", nil, err
	}
	return left, op, right, nil
}

// upToOperator reads the left side, which stops at == or != rather than at a
// comma.
func (p *formulaReader) upToOperator() (Value, error) {
	p.skip()
	if p.at < len(p.text) && (p.text[p.at] == '"' || p.text[p.at] == '\'') {
		return p.quoted()
	}
	start := p.at
	for p.at < len(p.text) {
		if strings.HasPrefix(p.text[p.at:], "==") || strings.HasPrefix(p.text[p.at:], "!=") {
			break
		}
		if p.text[p.at] == ',' || p.text[p.at] == ')' {
			break
		}
		p.at++
	}
	word := strings.TrimSpace(p.text[start:p.at])
	if word == "" {
		return nil, fmt.Errorf("an if( with nothing to compare")
	}
	return property{Property(word)}, nil
}

func (p *formulaReader) comma() error {
	p.skip()
	if p.at >= len(p.text) || p.text[p.at] != ',' {
		return fmt.Errorf("an if( missing a comma between its parts")
	}
	p.at++
	return nil
}

// Property is a property name as a view may write it: `note.status` and
// `status` are the same property, and Bases accepts both.
func Property(written string) string {
	return strings.TrimPrefix(strings.TrimSpace(written), "note.")
}
