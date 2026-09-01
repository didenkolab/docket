package task

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// A wikilink is how one note points at another, and it is the only kind of
// pointer Obsidian resolves, draws in its graph and counts as a backlink. A key
// written as a bare string is a string; the same key written as a link is a
// relationship. That distinction is the whole of docs/purpose.md §3, and it is
// what this file exists to make easy to get right.

// Link wraps a note name as a wikilink, ready to be written into frontmatter or
// a body.
func Link(note string) string { return "[[" + note + "]]" }

// Target is the note a wikilink points at, with any display text and any
// heading removed. It returns "" for anything that is not a wikilink, so a
// value can be tested and read in one step.
//
// A plain string is deliberately not treated as a target. A caller that accepts
// both forms — as everything does while old vaults are still being migrated —
// asks for that explicitly with NoteOf.
func Target(value string) string {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[[") || !strings.HasSuffix(value, "]]") {
		return ""
	}
	inner := value[2 : len(value)-2]
	if i := strings.IndexAny(inner, "|#"); i >= 0 {
		inner = inner[:i]
	}
	return strings.TrimSpace(inner)
}

// NoteOf reads a value that may be a link or may be a bare string, and returns
// the note name either way.
//
// Both forms are read because a vault written before relationships became links
// still has to open. Only the link form is ever written.
func NoteOf(value string) string {
	if target := Target(value); target != "" {
		return target
	}
	return strings.TrimSpace(value)
}

// IsLink says whether a value is written as a link. `docket check` reports the
// ones that are not, because those are invisible in Obsidian.
func IsLink(value string) bool { return Target(value) != "" }

// KeyOf is the task key at the head of a note name: `ACME-12` in
// `ACME-12 Fix login redirect loop`. It is "" when the name does not start with
// something shaped like a key, which is how a link to a page is told apart from
// a link to a task.
func KeyOf(note string) string {
	note = strings.TrimSpace(strings.TrimPrefix(note, "./"))
	if slash := strings.LastIndex(note, "/"); slash >= 0 {
		note = note[slash+1:]
	}
	head := note
	if space := strings.Index(head, " "); space >= 0 {
		head = head[:space]
	}
	if !looksLikeKey(head) {
		return ""
	}
	return head
}

// looksLikeKey is the shape of a key without the project package's opinion
// about which projects exist — LETTERS-DIGITS. Callers that need to know
// whether the project is real ask project.SplitKey.
func looksLikeKey(s string) bool {
	dash := strings.LastIndex(s, "-")
	if dash <= 0 || dash == len(s)-1 {
		return false
	}
	for i, r := range s[:dash] {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	for _, r := range s[dash+1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Retarget rewrites every link to one note so that it points at another,
// leaving code alone.
//
// This is what makes retitling safe. A title lives in the file name, so
// changing it renames the file, and every `[[ACME-12 Its old title]]` in the
// vault would otherwise point at nothing — including the `parent` of every
// child task, once a parent is a link. Obsidian does this when it renames a
// note; a tool that writes to the same vault has to do it too, or the two
// disagree about what a rename means.
//
// A link inside a code span or a fenced block is an example of the syntax
// rather than a pointer, and is left as written.
func Retarget(content, from, to string) (string, bool) {
	if from == to || from == "" {
		return content, false
	}

	changed := false
	out := replaceOutsideCode(content, func(inner string) string {
		target, rest := inner, ""
		if i := strings.IndexAny(inner, "|#"); i >= 0 {
			target, rest = inner[:i], inner[i:]
		}
		if strings.TrimSpace(target) != from {
			return "[[" + inner + "]]"
		}
		changed = true
		return "[[" + to + rest + "]]"
	})
	return out, changed
}

// replaceOutsideCode rewrites the inside of every wikilink except those in code
// spans and fenced blocks.
func replaceOutsideCode(content string, rewrite func(inner string) string) string {
	var out strings.Builder
	rest := content

	for len(rest) > 0 {
		code := nextCode(rest)
		if code == nil {
			out.WriteString(rewriteLinks(rest, rewrite))
			break
		}
		out.WriteString(rewriteLinks(rest[:code[0]], rewrite))
		out.WriteString(rest[code[0]:code[1]])
		rest = rest[code[1]:]
	}
	return out.String()
}

func nextCode(s string) []int {
	fence := fencedBlock.FindStringIndex(s)
	span := inlineCode.FindStringIndex(s)

	switch {
	case fence == nil:
		return span
	case span == nil:
		return fence
	case fence[0] <= span[0]:
		return fence
	default:
		return span
	}
}

func rewriteLinks(s string, rewrite func(inner string) string) string {
	return wikilink.ReplaceAllStringFunc(s, func(m string) string {
		return rewrite(strings.TrimSuffix(strings.TrimPrefix(m, "[["), "]]"))
	})
}

/* ---------- writing a relationship ---------- */

// SetParent points the task at the one it belongs to, by note name —
// `ACME-4 Session model`, not `ACME-4`. The name is what a wikilink resolves,
// and a bare key resolves to nothing: Obsidian does not consult aliases.
//
// An empty note removes the property. `parent:` with no value is not "no
// parent", it is a parent that does not exist, and the validator is right to
// say so.
func (t *Task) SetParent(note string) {
	if strings.TrimSpace(note) == "" {
		t.Remove("parent")
		t.Parent, t.rawParent = "", ""
		return
	}
	link := Link(note)
	t.setNode("parent", quoted(link))
	t.Parent, t.rawParent = KeyOf(note), link
}

// SetSprint puts the task in a sprint, by note name — `Sprint 24`, which is
// what the page is called.
//
// An empty note takes it out of every sprint, which is a real state: work that
// is not committed to a fortnight. `sprint:` with no value would be a sprint
// that does not exist, and the validator would be right to say so.
func (t *Task) SetSprint(note string) {
	if strings.TrimSpace(note) == "" {
		t.Remove("sprint")
		t.Sprint, t.rawSprint = "", ""
		return
	}
	link := Link(note)
	t.setNode("sprint", quoted(link))
	t.Sprint, t.rawSprint = labelName(note), link
}

// SetLabels writes the labels as links, so each one is an edge in the graph and
// each one can be a page that says what it means.
func (t *Task) SetLabels(names []string) {
	links := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			links = append(links, Link(name))
		}
	}
	t.setLinkList("labels", links)

	t.rawLabels = links
	t.Labels = t.Labels[:0]
	for _, link := range links {
		t.Labels = append(t.Labels, labelName(link))
	}
}

// setLinkList writes a flow list of quoted links — the form Obsidian's own
// property editor writes, and the only one that is a list of links rather than
// a list of lists.
func (t *Task) setLinkList(name string, links []string) {
	list := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, link := range links {
		list.Content = append(list.Content, quoted(link))
	}
	t.setNode(name, list)
}

// quoted is a scalar YAML has to quote. `[[auth]]` unquoted is a nested
// sequence, and a link written that way is not a link, it is two empty lists.
func quoted(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: value}
}

/* ---------- tags ---------- */

// SetTags writes Obsidian's own tags.
//
// A tag is not a link and does not want to be one: it is the other thing
// Obsidian offers for grouping, with a pane of its own, a `tag:` search
// operator and hierarchy in the name — `#area/auth` is inside `#area`. A label
// says what a task is about and can be a page; a tag says which slice of the
// work it belongs to, and nests.
//
// Written unquoted and without the hash, which is how Obsidian writes them in
// frontmatter and the only form its tag pane reads.
func (t *Task) SetTags(tags []string) {
	cleaned := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = CleanTag(tag); tag != "" {
			cleaned = append(cleaned, tag)
		}
	}
	t.SetList("tags", cleaned)
	t.Tags = cleaned
}

// CleanTag makes a string into something Obsidian will accept as a tag.
//
// A tag may hold letters, digits, underscore, hyphen and the slash that nests
// it, and may not be all digits. A space ends a tag, so a space becomes a
// hyphen rather than two tags — losing half of what somebody typed is worse
// than changing it visibly. The leading hash is Obsidian's inline syntax and is
// never written in frontmatter.
func CleanTag(tag string) string {
	tag = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(tag), "#"))

	var b strings.Builder
	for _, r := range tag {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-' || r == '/':
			b.WriteRune(r)
		case r == ' ' || r == '\t':
			b.WriteRune('-')
		case r > 127:
			// Obsidian accepts non-Latin letters in a tag, and a vault whose
			// language is not English should not have its tags mangled.
			b.WriteRune(r)
		}
	}

	cleaned := strings.Trim(b.String(), "-/")
	if cleaned == "" || allDigits(cleaned) {
		return ""
	}
	return cleaned
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// TagTree is a tag and everything above it: `area/auth/session` is also in
// `area/auth` and in `area`. Obsidian's tag pane nests them, and a search for
// the parent finds the child.
func TagTree(tag string) []string {
	parts := strings.Split(tag, "/")
	out := make([]string, 0, len(parts))
	for i := range parts {
		out = append(out, strings.Join(parts[:i+1], "/"))
	}
	return out
}

// SetAssignee puts the work on somebody, by handle — `marina`, which is what
// their page is called.
//
// An empty handle takes it off everybody, which is a real state and the one a
// backlog is full of. `assignee:` with no value is how Obsidian writes an empty
// text property, so it is left as that rather than removed: a board reads it as
// nobody either way, and removing the property would make the file differ from
// every other one for no gain.
func (t *Task) SetAssignee(handle string) {
	if handle = strings.TrimSpace(handle); handle == "" {
		t.Set("assignee", "")
		t.Assignee, t.rawAssignee = "", ""
		return
	}
	link := Link(handle)
	t.setNode("assignee", quoted(link))
	t.Assignee, t.rawAssignee = personName(handle), link
}

// PeopleFolder is where a person's page lives. Named here because a handle is
// read here: it is the one folder that may appear in front of one.
const PeopleFolder = "people/"

// personName is the handle a value points at.
//
// Not labelName: a handle may have a slash in it. `agent/claude` is one name,
// and stripping everything before the last slash — which is right for a label
// filed under docs/labels/ — turned every agent in the vault into "claude".
// Only the people folder comes off, and only from the front.
func personName(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(NoteOf(value)), PeopleFolder)
}
