package base

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// A filter as a list of conditions, so it can be edited without writing YAML.
//
// This is a reading of the same file, not a second format. Most filters are one
// list of ands over a few properties, and that shape is what a form is for. A
// filter that is not that shape — nested ors, a not around a comparison — is
// left to the text, and the page says so. Offering a form that quietly flattens
// a filter would change what a board selects while looking like it was only
// being displayed.

// Operators a condition may use, in the order a form should offer them.
const (
	OpIs       = "is"
	OpIsNot    = "is not"
	OpContains = "contains"
	OpHasValue = "has any value"
	OpIsEmpty  = "is empty"
	OpAtLeast  = "is at least"
	OpAtMost   = "is at most"
	OpMoreThan = "is more than"
	OpLessThan = "is less than"
)

// propertyName is what a property may be called: the same rule a field name
// follows, so a condition cannot be written about something no file can hold.
var propertyName = regexp.MustCompile(`^\p{L}[\p{L}\p{N}_]*$`)

// Operators is every operator a condition may use.
var Operators = []string{
	OpIs, OpIsNot, OpContains, OpHasValue, OpIsEmpty,
	OpAtLeast, OpAtMost, OpMoreThan, OpLessThan,
}

// Condition is one line of a filter: a property, what is being asked of it, and
// what it is being compared to.
type Condition struct {
	Property string
	Op       string
	Value    string
}

// Filters is a filter as a form can show it: the folders it selects, whether
// the conditions are joined by and or by or, and the conditions themselves.
type Filters struct {
	Folders    []string
	Join       string
	Conditions []Condition
}

// Read is a filter as a list of conditions, and whether it is that shape at
// all.
func Read(e Expr) (Filters, bool) {
	out := Filters{Join: "and"}
	if e == nil {
		return out, true
	}

	parts := []Expr{e}
	switch top := e.(type) {
	case allOf:
		parts = top.parts
	case anyOf:
		// A filter that is one or of conditions, which a form can also show —
		// unless it is only the folder clause, which is the ordinary case and
		// stays an and of one.
		if folders, ok := onlyFolders(top.parts); ok {
			out.Folders = folders
			return out, true
		}
		out.Join, parts = "or", top.parts
	}

	for _, part := range parts {
		// One folder is written as an or of one, and an or of one collapses to
		// the thing itself when it is read — so a board about a single project
		// arrives here as a bare inFolder rather than as a group.
		if only, ok := part.(inFolder); ok {
			out.Folders = append(out.Folders, only.folder)
			continue
		}
		if inner, ok := part.(anyOf); ok {
			folders, ok := onlyFolders(inner.parts)
			if !ok {
				return Filters{}, false
			}
			out.Folders = append(out.Folders, folders...)
			continue
		}
		c, ok := condition(part)
		if !ok {
			return Filters{}, false
		}
		out.Conditions = append(out.Conditions, c)
	}
	return out, true
}

// onlyFolders reports whether these parts are all file.inFolder calls, which is
// how a board says which projects it is about.
func onlyFolders(parts []Expr) ([]string, bool) {
	var folders []string
	for _, p := range parts {
		in, ok := p.(inFolder)
		if !ok {
			return nil, false
		}
		folders = append(folders, in.folder)
	}
	return folders, len(folders) > 0
}

func condition(e Expr) (Condition, bool) {
	switch v := e.(type) {
	case compare:
		op, ok := map[string]string{
			"==": OpIs, "!=": OpIsNot,
			">=": OpAtLeast, "<=": OpAtMost, ">": OpMoreThan, "<": OpLessThan,
		}[v.op]
		if !ok {
			return Condition{}, false
		}
		return Condition{Property: v.property, Op: op, Value: v.want}, true
	case contains:
		return Condition{Property: v.property, Op: OpContains, Value: v.want}, true
	case present:
		return Condition{Property: v.property, Op: OpHasValue}, true
	case not:
		inner, ok := v.inner.(present)
		if !ok {
			return Condition{}, false
		}
		return Condition{Property: inner.property, Op: OpIsEmpty}, true
	}
	return Condition{}, false
}

// Write is the `filters:` block these conditions come to, as YAML.
func (f Filters) Write() (string, error) {
	var lines []string
	for _, folder := range f.Folders {
		if strings.TrimSpace(folder) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf(`      - file.inFolder(%q)`, strings.TrimSpace(folder)))
	}

	var body []string
	if len(lines) > 0 {
		body = append(body, "    - or:")
		body = append(body, lines...)
	}
	for _, c := range f.Conditions {
		written, err := c.Write()
		if err != nil {
			return "", err
		}
		if written == "" {
			continue
		}
		body = append(body, "    - "+written)
	}
	if len(body) == 0 {
		return "", nil
	}

	join := "and"
	if strings.EqualFold(f.Join, "or") {
		join = "or"
	}
	return "filters:\n  " + join + ":\n" + strings.Join(body, "\n") + "\n", nil
}

// Write is one condition as the expression it stands for.
func (c Condition) Write() (string, error) {
	property := strings.TrimSpace(c.Property)
	if property == "" {
		return "", nil // an empty row is a row nobody filled in
	}
	if property == "file.inFolder" {
		return fmt.Sprintf(`file.inFolder(%q)`, strings.TrimSpace(c.Value)), nil
	}
	name := Property(property)
	if !propertyName.MatchString(name) {
		return "", fmt.Errorf("%q is not a property name", c.Property)
	}
	value := strings.TrimSpace(c.Value)

	switch c.Op {
	case OpHasValue:
		return "note." + name, nil
	case OpIsEmpty:
		return "'!note." + name + "'", nil
	case OpContains:
		return fmt.Sprintf(`'note.%s.contains(%q)'`, name, value), nil
	}

	op, ok := map[string]string{
		OpIs: "==", OpIsNot: "!=",
		OpAtLeast: ">=", OpAtMost: "<=", OpMoreThan: ">", OpLessThan: "<",
	}[c.Op]
	if !ok {
		return "", fmt.Errorf("%q is not something a condition can ask", c.Op)
	}
	if value == "" {
		return "", fmt.Errorf("“%s %s” needs something to compare with", property, c.Op)
	}
	// A number is written as a number, so Obsidian compares it as one.
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return fmt.Sprintf(`'note.%s %s %s'`, name, op, value), nil
	}
	return fmt.Sprintf(`'note.%s %s %q'`, name, op, value), nil
}

// Rewrite is the file with its filters replaced and everything else left alone.
//
// Through the document rather than by regenerating it: a base holds views,
// formulas, display names and comments this reader does not model, and a form
// that edits the filter must not quietly drop them.
func Rewrite(raw []byte, filters string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("this file is not a base")
	}

	var written yaml.Node
	if err := yaml.Unmarshal([]byte(filters), &written); err != nil {
		return nil, err
	}
	if len(written.Content) == 0 {
		return nil, fmt.Errorf("the conditions came to nothing")
	}
	value := written.Content[0].Content[1]

	mapping := doc.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "filters" {
			mapping.Content[i+1] = value
			return marshal(&doc)
		}
	}
	// No filters at all yet: a file that selected everything.
	mapping.Content = append([]*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "filters"}, value,
	}, mapping.Content...)
	return marshal(&doc)
}

func marshal(doc *yaml.Node) ([]byte, error) {
	var out strings.Builder
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}
