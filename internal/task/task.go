// Package task reads and writes a single task file: YAML frontmatter followed
// by a Markdown body.
//
// Parsing keeps the original frontmatter node, so writing a task back changes
// only the fields that were set. Key order, comments and the body survive a
// round trip — a tool that reshuffles a file every time it touches it makes
// every diff unreadable, which defeats the point of keeping tasks in git.
package task

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TimeFormat is how timestamps are written. RFC 3339 in UTC, so that string
// order is chronological order.
const TimeFormat = time.RFC3339

var (
	// ErrNoFrontmatter means the file does not start with a --- delimited block.
	ErrNoFrontmatter = errors.New("no YAML frontmatter")

	delimiter = []byte("---\n")
)

// Task is one parsed task file.
type Task struct {
	Key            string   `yaml:"key"`
	Title          string   `yaml:"title"`
	Type           string   `yaml:"type"`
	Status         string   `yaml:"status"`
	StatusCategory string   `yaml:"status_category"`
	Priority       string   `yaml:"priority"`
	Assignee       string   `yaml:"assignee"`
	Parent         string   `yaml:"parent"`
	Labels         []string `yaml:"labels"`
	Created        string   `yaml:"created"`
	Updated        string   `yaml:"updated"`
	Aliases        []string `yaml:"aliases"`

	front yaml.Node
	body  string
}

// Parse reads a task file.
func Parse(data []byte) (*Task, error) {
	front, body, err := split(data)
	if err != nil {
		return nil, err
	}

	t := &Task{body: body}
	if err := yaml.Unmarshal(front, &t.front); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	if t.front.Kind == 0 || len(t.front.Content) == 0 {
		return nil, errors.New("frontmatter is empty")
	}
	if t.front.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("frontmatter is not a set of properties")
	}
	if err := t.front.Decode(t); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	return t, nil
}

// split separates the frontmatter block from the body.
func split(data []byte) (front []byte, body string, err error) {
	if !bytes.HasPrefix(data, delimiter) {
		return nil, "", ErrNoFrontmatter
	}
	rest := data[len(delimiter):]

	end := bytes.Index(rest, []byte("\n---\n"))
	switch {
	case end >= 0:
		return rest[:end+1], string(rest[end+len("\n---\n"):]), nil
	case bytes.HasSuffix(rest, []byte("\n---")):
		return rest[:len(rest)-len("---")], "", nil
	default:
		return nil, "", fmt.Errorf("%w: the block is never closed", ErrNoFrontmatter)
	}
}

// Body returns the Markdown after the frontmatter.
func (t *Task) Body() string { return t.body }

// SetBody replaces the Markdown after the frontmatter.
func (t *Task) SetBody(body string) { t.body = body }

// Bytes renders the task back to file content.
func (t *Task) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&t.front); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.Write(delimiter)
	out.Write(buf.Bytes())
	out.WriteString("---\n")
	out.WriteString(t.body)
	return out.Bytes(), nil
}

// Set writes a text property, adding it if the task does not have it yet. The
// value is quoted only when leaving it bare would change its meaning.
func (t *Task) Set(name, value string) { t.setNode(name, textNode(value)) }

// SetPlain writes a property without forcing it to be text, so a timestamp
// stays unquoted and Obsidian shows it as a date rather than as a string.
func (t *Task) SetPlain(name, value string) {
	t.setNode(name, &yaml.Node{Kind: yaml.ScalarNode, Value: value})
}

// Sync refreshes the typed fields from the frontmatter. Call it after edits if
// the struct is going to be read again.
func (t *Task) Sync() error { return t.front.Decode(t) }

func textNode(value string) *yaml.Node {
	// A bare `assignee:` is how Obsidian writes an empty text property.
	// `assignee: ""` would be shown as the two-character string it is.
	if value == "" {
		return &yaml.Node{Kind: yaml.ScalarNode}
	}
	n := &yaml.Node{}
	n.SetString(value)
	return n
}

func (t *Task) setNode(name string, node *yaml.Node) {
	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content[i+1] = node
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: name}, node)
}

// SetList writes a list property in flow style — `labels: [auth, regression]` —
// which is how Obsidian's property editor writes lists and how they stay
// readable in a one-line diff.
func (t *Task) SetList(name string, values []string) {
	list := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, v := range values {
		list.Content = append(list.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v})
	}

	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content[i+1] = list
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: name}, list)
}

// Remove drops a property. Used for optional ones that would otherwise be
// present and empty — `parent:` with no value is not "no parent", it is a
// parent that does not exist, and the validator is right to say so.
func (t *Task) Remove(name string) {
	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// SetStatus moves the task to a status and its category together. They are one
// edit because a task carrying a status from one column and the category of
// another lands in a column the board cannot render.
func (t *Task) SetStatus(status, category string) {
	t.Status, t.StatusCategory = status, category
	t.Set("status", status)
	t.Set("status_category", category)
}

// Touch stamps the update time.
func (t *Task) Touch(now time.Time) {
	t.Updated = now.UTC().Format(TimeFormat)
	t.SetPlain("updated", t.Updated)
}

// NestedProperties lists properties whose value is not a scalar or a list of
// scalars. The format forbids them: Obsidian's property editor cannot edit a
// nested value and Bases cannot filter on one.
func (t *Task) NestedProperties() []string {
	var found []string
	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if nested(mapping.Content[i+1]) {
			found = append(found, mapping.Content[i].Value)
		}
	}
	return found
}

func nested(n *yaml.Node) bool {
	if n.Kind == yaml.MappingNode {
		return true
	}
	if n.Kind == yaml.SequenceNode {
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode {
				return true
			}
		}
	}
	return false
}

// ParseTime reads one of the task's timestamps.
func ParseTime(value string) (time.Time, error) {
	return time.Parse(TimeFormat, strings.TrimSpace(value))
}

var (
	fencedBlock = regexp.MustCompile("(?s)```.*?```")
	inlineCode  = regexp.MustCompile("`[^`\n]*`")
	wikilink    = regexp.MustCompile(`\[\[([^\]\n]+)\]\]`)
)

// Links lists the wikilink targets in the body, with any display text and
// heading anchor stripped. Links inside code spans and fenced blocks are
// examples rather than links, and are skipped — the same way Obsidian sees them.
func (t *Task) Links() []string { return Links(t.body) }

// Links lists the wikilink targets in a piece of Markdown.
func Links(markdown string) []string {
	stripped := inlineCode.ReplaceAllString(fencedBlock.ReplaceAllString(markdown, ""), "")

	var targets []string
	for _, m := range wikilink.FindAllStringSubmatch(stripped, -1) {
		target := m[1]
		if i := strings.IndexAny(target, "|#"); i >= 0 {
			target = target[:i]
		}
		if target = strings.TrimSpace(target); target != "" {
			targets = append(targets, target)
		}
	}
	return targets
}
