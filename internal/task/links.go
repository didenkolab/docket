package task

import "strings"

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
