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
	"strconv"
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
	Created        string   `yaml:"created"`
	Updated        string   `yaml:"updated"`
	Aliases        []string `yaml:"aliases"`
	// Tags are Obsidian's own, and are passed through untouched.
	Tags []string `yaml:"tags,omitempty"`

	// Parent is the key of the task this one belongs to, and Labels are the
	// names of the labels it carries — both resolved from what is on disk.
	//
	// On disk they are wikilinks: `parent: "[[ACME-4 Session model]]"`. That is
	// what Obsidian resolves, draws in the graph and counts as a backlink,
	// which is the whole reason an epic and a label exist. See
	// docs/purpose.md §3.
	//
	// Here they are a key and a list of names, because that is what everything
	// asking the question wants. The link form is written by SetParent and
	// SetLabels and read back by RawParent and RawLabels; a vault written
	// before this still parses, because a bare string reads as itself.
	Parent string   `yaml:"-"`
	Labels []string `yaml:"-"`

	rawParent string
	rawLabels []string
	// Order is where the task sits among the others in its column, when
	// somebody has said. Absent — the usual case — the task sorts after every
	// task that has one. See SetOrder.
	Order *int `yaml:"order,omitempty"`

	front yaml.Node
	body  string
	// bodyLine is the file line the body starts on, so that findings about
	// links can be reported at a place an editor can jump to.
	bodyLine int
}

// Parse reads a task file.
func Parse(data []byte) (*Task, error) {
	front, body, err := split(data)
	if err != nil {
		return nil, err
	}

	// The file is "---\n" + frontmatter + "---\n" + body, so the body starts
	// after both delimiters and every frontmatter line.
	t := &Task{body: body, bodyLine: bytes.Count(front, []byte("\n")) + 3}
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
	t.resolve()
	return t, nil
}

// resolve turns what is written into what callers ask for: a parent link into a
// key, and label links into names. Both forms are read; only the link form is
// written.
func (t *Task) resolve() {
	var raw struct {
		Parent string   `yaml:"parent"`
		Labels []string `yaml:"labels"`
	}
	_ = t.front.Decode(&raw)

	t.rawParent, t.rawLabels = raw.Parent, raw.Labels
	t.Parent = KeyOf(NoteOf(raw.Parent))
	t.Labels = t.Labels[:0]
	for _, l := range raw.Labels {
		if name := labelName(l); name != "" {
			t.Labels = append(t.Labels, name)
		}
	}
}

// labelName is what a label is called: the note it points at, without any
// folder. `[[docs/labels/auth]]` and `[[auth]]` and `auth` are all "auth", so
// filing a label under a folder does not rename it.
func labelName(value string) string {
	note := NoteOf(value)
	if slash := strings.LastIndex(note, "/"); slash >= 0 {
		note = note[slash+1:]
	}
	return strings.TrimSpace(note)
}

// RawParent and RawLabels are the values exactly as the file has them, for
// `docket check` to say which are still strings rather than links.
func (t *Task) RawParent() string   { return t.rawParent }
func (t *Task) RawLabels() []string { return t.rawLabels }

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
func (t *Task) Sync() error {
	if err := t.front.Decode(t); err != nil {
		return err
	}
	t.resolve()
	return nil
}

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

// SetOrder writes where the task sits among the others in its column.
//
// It is a number rather than a list because the order has to live in the task
// files: a separate list of keys is a second source of truth that a rename, a
// merge or an edit in Obsidian can put out of step with the tasks it orders.
// Numbers are spaced far apart by Between, so inserting a card between two
// others usually rewrites one file rather than the column.
func (t *Task) SetOrder(order int) {
	t.Order = &order
	t.SetPlain("order", strconv.Itoa(order))
}

// ClearOrder returns the task to the default order, which is by key.
func (t *Task) ClearOrder() {
	t.Order = nil
	t.Remove("order")
}

// Step is the gap left between neighbours, so an insertion between two cards
// has somewhere to land without renumbering them.
const Step = 1000

// Between returns an order value that sorts between two neighbours, and reports
// whether it found room. A nil neighbour is the end of the column.
//
// It fails only when two neighbours are adjacent integers, which takes about
// ten insertions into the same gap. The caller renumbers the column then.
func Between(above, below *int) (int, bool) {
	switch {
	case above == nil && below == nil:
		return 0, true
	case above == nil:
		return *below - Step, true
	case below == nil:
		return *above + Step, true
	case *below-*above > 1:
		return *above + (*below-*above)/2, true
	default:
		return 0, false
	}
}

// Touch stamps the update time.
func (t *Task) Touch(now time.Time) {
	t.Updated = now.UTC().Format(TimeFormat)
	t.SetPlain("updated", t.Updated)
}

// PropertyLine is the line in the file a property sits on, or 0 when the task
// does not have it. Frontmatter starts on line 2, after the opening delimiter.
func (t *Task) PropertyLine(name string) int {
	mapping := t.front.Content[0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i].Line + 1
		}
	}
	return 0
}

// LineOf finds the first line of the body mentioning a wikilink target, in file
// coordinates. It returns the body's first line when the target is not found
// on any single line.
func (t *Task) LineOf(target string) int {
	for i, line := range strings.Split(t.body, "\n") {
		if strings.Contains(line, "[["+target) {
			return t.bodyLine + i
		}
	}
	return t.bodyLine
}

// CommentsHeading is where comments are appended.
const CommentsHeading = "## Comments"

// AppendComment adds a comment at the end of the body, creating the section
// when the task has none.
//
// Comments live in the task file rather than in files of their own. Two agents
// commenting in parallel branches then conflict at the end of one file, which
// is trivial to resolve — and the whole conversation stays readable without
// tooling, which the alternative gives up.
func (t *Task) AppendComment(author string, when time.Time, text string) {
	body := strings.TrimRight(t.body, "\n")
	if !strings.Contains(body, CommentsHeading) {
		body += "\n\n" + CommentsHeading
	}
	entry := fmt.Sprintf("**%s · %s** — %s",
		author, when.UTC().Format("2006-01-02 15:04"), strings.TrimSpace(text))
	t.body = body + "\n\n" + entry + "\n"
}

// Comment is one entry from the task's comment section.
type Comment struct {
	Author string
	When   string
	Text   string
}

// commentHeader matches the line a comment starts with.
var commentHeader = regexp.MustCompile(`(?m)^\*\*(.+?) · (.+?)\*\* — `)

// Description is the body up to the comment section — what the task is about,
// without the conversation underneath it.
func (t *Task) Description() string {
	if at := strings.Index(t.body, CommentsHeading); at >= 0 {
		return strings.TrimSpace(t.body[:at])
	}
	return strings.TrimSpace(t.body)
}

// AttachmentsHeading is where attachments are listed.
const AttachmentsHeading = "## Attachments"

// AppendAttachment records a file against the task.
//
// An attachment is a file in the vault and a line in the task, which is all it
// needs to be: Obsidian embeds an image from that line, git versions the file,
// and nothing needs a database row to say the two belong together.
func (t *Task) AppendAttachment(link string, embed bool) {
	entry := "- [[" + link + "]]"
	if embed {
		entry = "![[" + link + "]]"
	}

	at := strings.Index(t.body, AttachmentsHeading)
	if at < 0 {
		// Before the comments, if there are any: attachments belong to the
		// description, not to the conversation.
		if comments := strings.Index(t.body, CommentsHeading); comments >= 0 {
			t.body = strings.TrimRight(t.body[:comments], "\n") +
				"\n\n" + AttachmentsHeading + "\n\n" + entry + "\n\n" + t.body[comments:]
			return
		}
		t.body = strings.TrimRight(t.body, "\n") + "\n\n" + AttachmentsHeading + "\n\n" + entry + "\n"
		return
	}

	end := len(t.body)
	if comments := strings.Index(t.body[at:], CommentsHeading); comments >= 0 {
		end = at + comments
	}
	section := strings.TrimRight(t.body[at:end], "\n")
	t.body = t.body[:at] + section + "\n" + entry + "\n\n" + t.body[end:]
}

// Attachments lists the files the task points at.
func (t *Task) Attachments() []string {
	at := strings.Index(t.body, AttachmentsHeading)
	if at < 0 {
		return nil
	}
	end := len(t.body)
	if comments := strings.Index(t.body[at:], CommentsHeading); comments >= 0 {
		end = at + comments
	}

	var out []string
	for _, m := range wikilink.FindAllStringSubmatch(t.body[at:end], -1) {
		out = append(out, m[1])
	}
	return out
}

// SetDescription replaces the body above the comment section, leaving the
// conversation underneath it untouched.
func (t *Task) SetDescription(description string) {
	description = strings.TrimRight(description, "\n")

	at := strings.Index(t.body, CommentsHeading)
	if at < 0 {
		t.body = "\n" + description + "\n"
		return
	}
	t.body = "\n" + description + "\n\n" + t.body[at:]
}

// Comments reads the comment section back into its entries.
//
// They are stored as text in the task file rather than as structured data, so
// this is a parse rather than a lookup. That is the trade the format makes: the
// conversation stays readable to anyone opening the file, and the cost is here.
func (t *Task) Comments() []Comment {
	at := strings.Index(t.body, CommentsHeading)
	if at < 0 {
		return nil
	}
	section := t.body[at+len(CommentsHeading):]

	headers := commentHeader.FindAllStringSubmatchIndex(section, -1)
	comments := make([]Comment, 0, len(headers))

	for i, h := range headers {
		end := len(section)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		comments = append(comments, Comment{
			Author: section[h[2]:h[3]],
			When:   section[h[4]:h[5]],
			Text:   strings.TrimSpace(section[h[1]:end]),
		})
	}
	return comments
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
