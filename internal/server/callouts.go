package server

import (
	"regexp"
	"strings"
)

// Callouts are Obsidian's, and this makes them render the same here.
//
// A callout is a blockquote whose first line is `> [!warning] A title`. Obsidian
// draws it as a coloured box; plain Markdown draws it as a blockquote with
// literal `[!warning]` at the front. A task that reads as a warning in one
// client and as a quote with syntax leaking into it in the other is the kind of
// disagreement between the two views that docs/What docket is for.md exists to prevent.
//
// This is a rewrite over the text rather than a Markdown extension: it turns
// the callout into HTML with the body still Markdown inside, which goldmark
// renders because raw HTML is allowed — the same reason Obsidian's own HTML
// passes through. See renderMarkdown.

// A callout opens a blockquote: `> [!type]`, then an optional fold marker, then
// an optional title.
var calloutOpen = regexp.MustCompile(`(?i)^\[!([a-z-]+)\]([+-]?)[ \t]*(.*)$`)

// Aliases Obsidian ships. The left-hand names are the ones the stylesheet
// knows; everything else falls back to a note, which is what Obsidian does with
// an unrecognised type.
var calloutAlias = map[string]string{
	"summary": "abstract", "tldr": "abstract",
	"hint": "tip", "important": "tip",
	"check": "success", "done": "success",
	"help": "question", "faq": "question",
	"caution": "warning", "attention": "warning",
	"fail": "failure", "missing": "failure",
	"error": "danger",
	"cite":  "quote",
}

var calloutKnown = map[string]bool{
	"note": true, "abstract": true, "info": true, "todo": true, "tip": true,
	"success": true, "question": true, "warning": true, "failure": true,
	"danger": true, "bug": true, "example": true, "quote": true,
}

// rewriteCallouts turns every callout in the text into HTML, leaving everything
// else alone.
func rewriteCallouts(body string) string {
	lines := strings.Split(body, "\n")
	var out []string

	for i := 0; i < len(lines); i++ {
		quoted, ok := quotedLine(lines[i])
		if !ok {
			out = append(out, lines[i])
			continue
		}
		match := calloutOpen.FindStringSubmatch(strings.TrimSpace(quoted))
		if match == nil {
			out = append(out, lines[i])
			continue
		}

		// Everything else in this blockquote is the callout's body.
		var inside []string
		for i+1 < len(lines) {
			next, ok := quotedLine(lines[i+1])
			if !ok {
				break
			}
			inside = append(inside, next)
			i++
		}
		out = append(out, callout(match[1], match[2], match[3], inside)...)
	}
	return strings.Join(out, "\n")
}

// quotedLine strips one level of blockquote, and says whether the line was one.
func quotedLine(line string) (string, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, ">") {
		return "", false
	}
	return strings.TrimPrefix(strings.TrimPrefix(trimmed, ">"), " "), true
}

func callout(kind, fold, title string, inside []string) []string {
	kind = strings.ToLower(kind)
	if alias, ok := calloutAlias[kind]; ok {
		kind = alias
	}
	if !calloutKnown[kind] {
		kind = "note"
	}
	if strings.TrimSpace(title) == "" {
		title = strings.ToUpper(kind[:1]) + kind[1:]
	}

	// A foldable callout is a <details>, which is the browser's own version of
	// what Obsidian draws and needs no script.
	openTag, titleTag, closeTag := `<div class="callout" data-callout="`+kind+`">`,
		`<div class="callout-title">`, `</div>`
	end := `</div>`
	if fold != "" {
		attr := ""
		if fold == "+" {
			attr = " open"
		}
		openTag = `<details class="callout" data-callout="` + kind + `"` + attr + `>`
		titleTag, closeTag = `<summary class="callout-title">`, `</summary>`
		end = `</details>`
	}

	// A blank line each side, so goldmark treats the block as HTML and still
	// renders the Markdown between the tags.
	out := []string{"", openTag, titleTag + inlineHTML(title) + closeTag, `<div class="callout-body">`, ""}
	out = append(out, inside...)
	return append(out, "", `</div>`, end, "")
}

// inlineHTML escapes a title, which is text somebody typed rather than markup.
var titleEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func inlineHTML(s string) string { return titleEscaper.Replace(strings.TrimSpace(s)) }
