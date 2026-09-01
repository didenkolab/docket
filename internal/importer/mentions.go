package importer

import (
	"regexp"
	"strconv"
	"strings"
)

// An imported page that names an issue should link it.
//
// This is the whole reason the two things live in one vault. A Confluence space
// and a Jira project are two systems that happen to reference each other by
// writing the key down; here they are notes in one folder, and a key written
// down is a link — so the task shows the specification in its backlinks, the
// graph draws them together, and neither had to be told about the other.
//
// A real space made the case for itself: sixteen pages, nearly every one a
// specification for a named issue, and not one of the issues knew. The
// information was there the whole time, and no page could be reached from the
// work it was about.

// browseLink is Jira's own link to an issue, which is how a page written in
// Confluence refers to one: [ACME-940 — the title](https://…/browse/ACME-940).
// The label allows one level of nesting, because a real one has it:
// [ACME-940 — [SPEC] Единый UUID](…). Stopping at the first ] left the link
// unmatched and the page unlinked.
var browseLink = regexp.MustCompile(
	`\[((?:[^\[\]]|\[[^\[\]]*\])*)\]\(https?://[^)]*/browse/([A-Z][A-Z0-9]+-\d{1,6})[^)]*\)`)

// keyLike is a key shaped like one. Whether it is really a mention depends on
// what sits either side, which is checked in code — Go's regexp has no
// lookaround, and a pattern that eats the character after a key cannot match
// two keys in a row.
var keyLike = regexp.MustCompile(`[A-Z][A-Z0-9]+-\d{1,6}`)

var (
	fenced = regexp.MustCompile("(?s)```.*?```")
	inline = regexp.MustCompile("`[^`\n]*`")
	// A link already made, so the pass over bare keys cannot rewrite the key
	// inside one — which turned a label into a link nested in a link.
	made = regexp.MustCompile(`\[\[(?:[^\[\]]|\[[^\[\]]*\])*\]\]`)
)

// linkMentions turns every mention of an imported issue into a wikilink.
//
// Only keys the import actually wrote: a wikilink to a task that is not in the
// vault is a dead link, and on a page naming forty issues it is forty of them.
//
// Code is left alone. An id inside a fenced block is a value in a request or a
// payload rather than a reference, and linking it would rewrite an example into
// something that no longer runs.
func linkMentions(body string, notes map[string]string) string {
	if len(notes) == 0 {
		return body
	}

	var kept []string
	keep := func(m string) string {
		kept = append(kept, m)
		return placeholder(len(kept) - 1)
	}
	hidden := inline.ReplaceAllStringFunc(fenced.ReplaceAllStringFunc(body, keep), keep)

	// The link first: it carries a label somebody wrote, and that label says
	// more than the note name would.
	hidden = browseLink.ReplaceAllStringFunc(hidden, func(m string) string {
		parts := browseLink.FindStringSubmatch(m)
		note, known := notes[parts[2]]
		if !known {
			return m
		}
		label := strings.TrimSpace(parts[1])
		if label == "" || label == parts[2] || label == note {
			return "[[" + note + "]]"
		}
		return "[[" + note + "|" + label + "]]"
	})
	hidden = made.ReplaceAllStringFunc(hidden, keep)
	hidden = linkBareKeys(hidden, notes)

	for i := len(kept) - 1; i >= 0; i-- {
		hidden = strings.Replace(hidden, placeholder(i), kept[i], 1)
	}
	return hidden
}

// linkBareKeys links a key written in a sentence, and leaves alone one that is
// part of something larger — a URL, a longer identifier, a wikilink already
// made, or a Markdown link's target.
func linkBareKeys(text string, notes map[string]string) string {
	var out strings.Builder
	last := 0
	for _, at := range keyLike.FindAllStringIndex(text, -1) {
		from, to := at[0], at[1]
		note, known := notes[text[from:to]]
		if !known || !standsAlone(text, from, to) {
			continue
		}
		out.WriteString(text[last:from])
		out.WriteString("[[" + note + "]]")
		last = to
	}
	out.WriteString(text[last:])
	return out.String()
}

// standsAlone reports whether the key at from:to is a mention rather than part
// of something else.
func standsAlone(text string, from, to int) bool {
	before, after := byte(' '), byte(' ')
	if from > 0 {
		before = text[from-1]
	}
	if to < len(text) {
		after = text[to]
	}
	// A letter, digit or dash either side means this is part of a longer word
	// or identifier — ACME-INV-055 must not become SP-INV linked and -055 loose.
	if wordish(before) || wordish(after) {
		return false
	}
	// Inside a link, a wikilink or a path, where it is a target rather than a
	// sentence: /browse/ACME-940, [[ACME-940 …]], docs/ACME-940.md.
	if before == '/' || after == '/' || before == '[' || after == ']' {
		return false
	}
	return true
}

func wordish(c byte) bool {
	return c == '-' || c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// placeholder is a marker no Markdown contains, so hiding code and putting it
// back cannot collide with the page's own text.
func placeholder(i int) string { return "\x00docket-code-" + strconv.Itoa(i) + "\x00" }
