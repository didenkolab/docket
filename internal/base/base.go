// Package base reads Obsidian Bases files — the query language the vault
// already has.
//
// A board is a question about the tasks: what is in this sprint, what is mine,
// what is not done. The vault answers it in `boards/*.base`, which Obsidian
// evaluates with no tool running, which lives in git, and which survives docket
// being deleted. Adding a second query language beside it would mean two
// answers to the same question and a day when they disagree.
//
// So the saved views on the web are these files. What is written here is a
// reader for the part of the language a board uses, and a refusal for the rest:
// a filter this cannot evaluate is reported as one, because a view drawn from
// half a filter is a wrong list that looks like a right one.
package base

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Note is one task as a filter sees it.
type Note struct {
	// Path is where the file is, relative to the vault, in slashes. Folders are
	// what a board selects projects by.
	Path string
	// Values are its frontmatter properties as written. A list property is
	// joined here as well as being in Lists, so that == on a single-item list
	// does the obvious thing.
	Values map[string]string
	Lists  map[string][]string
}

// Expr is a filter that can be asked about a note.
type Expr interface {
	Match(n Note) bool
	// String is the expression as it would be written, for a page that has to
	// say what it is drawing.
	String() string
}

// View is one saved view in a base file.
type View struct {
	Name string
	// Type is what Bases calls it: "table" or "cards".
	Type string
	// GroupBy is the property the view is grouped by, or empty. A grouped view
	// is what a board is: one column per value.
	GroupBy   string
	Direction string
	// Order is the properties to show, in order.
	Order  []string
	Filter Expr
}

// Base is one .base file.
type Base struct {
	// Note is the file name without .base — what the view is called.
	Note string
	// Path is relative to the space root.
	Path       string
	Filter     Expr
	Properties map[string]string
	Views      []View
	// Formulas are the computed columns, by name without the formula. prefix.
	// A formula this cannot read is left out and named in Beyond, so a view
	// that uses it says so instead of drawing a column of nothing.
	Formulas map[string]Value
	Beyond   map[string]string
}

// Compute is what a formula evaluates to for a note, and whether the board can
// evaluate it at all.
func (b *Base) Compute(name string, n Note) (string, bool) {
	f, ok := b.Formulas[strings.TrimPrefix(name, "formula.")]
	if !ok {
		return "", false
	}
	return f.Eval(n), true
}

// Displayed is the label a property was given, or the property itself.
func (b *Base) Displayed(property string) string {
	if label, ok := b.Properties[property]; ok && label != "" {
		return label
	}
	return strings.TrimPrefix(strings.TrimPrefix(property, "note."), "file.")
}

// Matches reports whether a note is in this base, before any view's own filter.
func (b *Base) Matches(n Note) bool {
	return b.Filter == nil || b.Filter.Match(n)
}

// Parse reads a .base file.
//
// An unreadable filter is an error rather than a base with no filter: a view
// that quietly stopped filtering would show every task in the vault under the
// name of a board somebody trusts.
func Parse(raw []byte) (*Base, error) {
	var read struct {
		Filters    yaml.Node                    `yaml:"filters"`
		Formulas   map[string]string            `yaml:"formulas"`
		Properties map[string]map[string]string `yaml:"properties"`
		Views      []struct {
			Name    string    `yaml:"name"`
			Type    string    `yaml:"type"`
			Order   []string  `yaml:"order"`
			Filters yaml.Node `yaml:"filters"`
			GroupBy struct {
				Property  string `yaml:"property"`
				Direction string `yaml:"direction"`
			} `yaml:"groupBy"`
		} `yaml:"views"`
	}
	if err := yaml.Unmarshal(raw, &read); err != nil {
		return nil, err
	}

	b := &Base{
		Properties: map[string]string{},
		Formulas:   map[string]Value{},
		Beyond:     map[string]string{},
	}
	var err error
	if b.Filter, err = parseNode(&read.Filters); err != nil {
		return nil, err
	}
	// A formula that cannot be read is not a broken file: the view using it may
	// not be the view being drawn. It is remembered as beyond us and named
	// where it matters.
	for name, written := range read.Formulas {
		value, err := ParseFormula(written)
		if err != nil {
			b.Beyond[name] = err.Error()
			continue
		}
		b.Formulas[name] = value
	}
	for property, about := range read.Properties {
		b.Properties[property] = about["displayName"]
	}
	for _, v := range read.Views {
		filter, err := parseNode(&v.Filters)
		if err != nil {
			return nil, fmt.Errorf("view %q: %w", v.Name, err)
		}
		b.Views = append(b.Views, View{
			Name: v.Name, Type: v.Type, Order: v.Order, Filter: filter,
			GroupBy: v.GroupBy.Property, Direction: v.GroupBy.Direction,
		})
	}
	return b, nil
}

// parseNode reads `filters:` — a string, or and/or/not over more of the same.
func parseNode(n *yaml.Node) (Expr, error) {
	if n == nil || n.Kind == 0 || n.Tag == "!!null" {
		return nil, nil
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return parseExpr(n.Value)

	case yaml.SequenceNode:
		// A bare list is an and: every line has to hold.
		return group("and", n.Content)

	case yaml.MappingNode:
		var out []Expr
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			switch key {
			case "and", "or", "not":
				inner, err := group(key, n.Content[i+1].Content)
				if err != nil {
					return nil, err
				}
				out = append(out, inner)
			default:
				return nil, fmt.Errorf("%q is not something a filter can say here; "+
					"a filter is and, or, not, or an expression", key)
			}
		}
		return and(out), nil
	}
	return nil, fmt.Errorf("a filter cannot be written that way")
}

func group(kind string, items []*yaml.Node) (Expr, error) {
	var out []Expr
	for _, item := range items {
		e, err := parseNode(item)
		if err != nil {
			return nil, err
		}
		if e != nil {
			out = append(out, e)
		}
	}
	switch kind {
	case "or":
		return either(out), nil
	case "not":
		return negate(and(out)), nil
	default:
		return and(out), nil
	}
}

// ---- the expressions themselves ----

type allOf struct{ parts []Expr }

func (e allOf) Match(n Note) bool {
	for _, p := range e.parts {
		if !p.Match(n) {
			return false
		}
	}
	return true
}
func (e allOf) String() string { return join(e.parts, " and ") }

type anyOf struct{ parts []Expr }

func (e anyOf) Match(n Note) bool {
	for _, p := range e.parts {
		if p.Match(n) {
			return true
		}
	}
	return false
}
func (e anyOf) String() string { return "(" + join(e.parts, " or ") + ")" }

type not struct{ inner Expr }

func (e not) Match(n Note) bool { return !e.inner.Match(n) }
func (e not) String() string    { return "not " + e.inner.String() }

func and(parts []Expr) Expr {
	if len(parts) == 1 {
		return parts[0]
	}
	return allOf{parts}
}

func either(parts []Expr) Expr {
	if len(parts) == 1 {
		return parts[0]
	}
	return anyOf{parts}
}

func negate(inner Expr) Expr { return not{inner} }

func join(parts []Expr, with string) string {
	var out []string
	for _, p := range parts {
		out = append(out, p.String())
	}
	return strings.Join(out, with)
}

// inFolder is file.inFolder("X"): the note is in that folder or below it.
type inFolder struct{ folder string }

func (e inFolder) Match(n Note) bool {
	dir := path.Dir(n.Path)
	return dir == e.folder || strings.HasPrefix(dir, e.folder+"/")
}
func (e inFolder) String() string { return `file.inFolder("` + e.folder + `")` }

// hasTag is file.hasTag("x"), which reads Obsidian's own tags.
type hasTag struct{ tag string }

func (e hasTag) Match(n Note) bool {
	for _, t := range n.Lists["tags"] {
		if strings.EqualFold(strings.TrimPrefix(t, "#"), e.tag) {
			return true
		}
	}
	return false
}
func (e hasTag) String() string { return `file.hasTag("` + e.tag + `")` }

// present is a bare `note.x`: the property is there and says something.
type present struct{ property string }

func (e present) Match(n Note) bool {
	if len(n.Lists[e.property]) > 0 {
		return true
	}
	return strings.TrimSpace(n.Values[e.property]) != ""
}
func (e present) String() string { return "note." + e.property }

// compare is `note.x == "y"` and its relatives.
type compare struct {
	property, op, want string
}

func (e compare) Match(n Note) bool {
	got, ok := n.Values[e.property]
	if !ok {
		got = ""
	}
	switch e.op {
	case "==":
		return same(got, e.want)
	case "!=":
		return !same(got, e.want)
	}

	left, err1 := strconv.ParseFloat(strings.TrimSpace(got), 64)
	right, err2 := strconv.ParseFloat(e.want, 64)
	if err1 != nil || err2 != nil {
		// Dates and names compare as text, which is what makes
		// `note.date > "2026-01-01"` mean what it looks like.
		return textually(e.op, got, e.want)
	}
	switch e.op {
	case ">":
		return left > right
	case "<":
		return left < right
	case ">=":
		return left >= right
	case "<=":
		return left <= right
	}
	return false
}

func (e compare) String() string { return "note." + e.property + " " + e.op + ` "` + e.want + `"` }

func textually(op, got, want string) bool {
	switch op {
	case ">":
		return got > want
	case "<":
		return got < want
	case ">=":
		return got >= want
	case "<=":
		return got <= want
	}
	return false
}

// same compares as written, except that a link is compared by what it points
// at: `sprint: "[[Sprint 12]]"` is the same answer as `sprint: "Sprint 12"`,
// and a filter should not have to know which spelling the file used.
func same(got, want string) bool {
	return strings.EqualFold(strings.TrimSpace(unlink(got)), strings.TrimSpace(unlink(want)))
}

func unlink(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") {
		return s
	}
	inside := s[2 : len(s)-2]
	if bar := strings.Index(inside, "|"); bar >= 0 {
		inside = inside[:bar]
	}
	return inside
}

// contains is `note.x.contains("y")`, which is how a list is asked about.
type contains struct{ property, want string }

func (e contains) Match(n Note) bool {
	for _, v := range n.Lists[e.property] {
		if same(v, e.want) {
			return true
		}
	}
	return strings.Contains(strings.ToLower(n.Values[e.property]), strings.ToLower(e.want))
}
func (e contains) String() string { return "note." + e.property + `.contains("` + e.want + `")` }

// ---- reading one expression ----

var (
	folderCall = regexp.MustCompile(`^file\.inFolder\(\s*"([^"]*)"\s*\)$`)
	tagCall    = regexp.MustCompile(`^file\.hasTag\(\s*"([^"]*)"\s*\)$`)
	containsRe = regexp.MustCompile(`^note\.([\p{L}\p{N}_]+)\.contains\(\s*"([^"]*)"\s*\)$`)
	compareRe  = regexp.MustCompile(`^note\.([\p{L}\p{N}_]+)\s*(==|!=|>=|<=|>|<)\s*(.+)$`)
	bareRe     = regexp.MustCompile(`^(!?)note\.([\p{L}\p{N}_]+)$`)
)

// parseExpr reads one line of the filter language.
//
// What it does not understand it refuses by name. Bases has more in it than
// this — formulas, dates, functions over links — and a board that quietly
// ignored the half it could not read would be the worst of the three possible
// behaviours.
func parseExpr(raw string) (Expr, error) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return nil, nil
	}

	if m := folderCall.FindStringSubmatch(line); m != nil {
		return inFolder{strings.Trim(m[1], "/")}, nil
	}
	if m := tagCall.FindStringSubmatch(line); m != nil {
		return hasTag{m[1]}, nil
	}
	if m := containsRe.FindStringSubmatch(line); m != nil {
		return contains{m[1], m[2]}, nil
	}
	if m := compareRe.FindStringSubmatch(line); m != nil {
		return compare{m[1], m[2], strings.Trim(strings.TrimSpace(m[3]), `"'`)}, nil
	}
	if m := bareRe.FindStringSubmatch(line); m != nil {
		if m[1] == "!" {
			return negate(present{m[2]}), nil
		}
		return present{m[2]}, nil
	}

	return nil, fmt.Errorf("this filter is beyond what the board can evaluate: %s", line)
}
