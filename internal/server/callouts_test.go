package server

import (
	"strings"
	"testing"
)

func render(t *testing.T, body string) string {
	t.Helper()
	return string(renderMarkdown(body, &index{targets: map[string]string{}}))
}

// A callout reads as a coloured box in Obsidian. Rendered as a plain
// blockquote, the syntax leaks into the text and the two clients disagree
// about what the same file says.
func TestACalloutBecomesACallout(t *testing.T) {
	html := render(t, "> [!warning] Mind the gap\n> The cookie is dropped on the redirect.\n")

	for _, want := range []string{
		`data-callout="warning"`,
		`class="callout-title"`,
		"Mind the gap",
		"The cookie is dropped on the redirect.",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in:\n%s", want, html)
		}
	}
	if strings.Contains(html, "[!warning]") {
		t.Errorf("the syntax leaked into the text:\n%s", html)
	}
}

func TestCalloutTypesAndAliases(t *testing.T) {
	cases := map[string]string{
		"> [!tldr] x\n":     "abstract", // an alias Obsidian ships
		"> [!CAUTION] x\n":  "warning",  // the type is case-insensitive
		"> [!nonsense] x\n": "note",     // anything unknown falls back, as in Obsidian
		"> [!bug] x\n":      "bug",
	}
	for body, want := range cases {
		if html := render(t, body); !strings.Contains(html, `data-callout="`+want+`"`) {
			t.Errorf("%q rendered as something other than %s:\n%s", body, want, html)
		}
	}
}

// The default title is the type, in title case, which is what Obsidian shows.
func TestACalloutWithoutATitleIsNamedAfterItsType(t *testing.T) {
	html := render(t, "> [!question]\n> Is it?\n")
	if !strings.Contains(html, ">Question<") {
		t.Errorf("no default title:\n%s", html)
	}
}

// `-` is collapsed, `+` is expanded — and a fold needs no script.
func TestAFoldableCalloutIsADetails(t *testing.T) {
	closed := render(t, "> [!faq]- Are callouts foldable?\n> Yes.\n")
	if !strings.Contains(closed, "<details") || strings.Contains(closed, "<details open") {
		t.Errorf("a - callout should be a closed details:\n%s", closed)
	}
	open := render(t, "> [!faq]+ Are callouts foldable?\n> Yes.\n")
	if !strings.Contains(open, "<details class=\"callout\" data-callout=\"question\" open>") {
		t.Errorf("a + callout should be an open details:\n%s", open)
	}
}

// The body is still Markdown, including links to other notes.
func TestACalloutBodyIsStillMarkdown(t *testing.T) {
	ix := &index{targets: map[string]string{}}
	ix.put("ACME-1 Something", "/task/ACME-1")
	html := string(renderMarkdown(
		"> [!note] See also\n> **Bold** and [[ACME-1 Something]].\n", ix))

	if !strings.Contains(html, "<strong>Bold</strong>") {
		t.Errorf("Markdown in the body was not rendered:\n%s", html)
	}
	if !strings.Contains(html, `href="/task/ACME-1"`) {
		t.Errorf("a wikilink in the body did not resolve:\n%s", html)
	}
}

// An ordinary blockquote is left an ordinary blockquote.
func TestAPlainQuoteIsUntouched(t *testing.T) {
	html := render(t, "> Just a quotation.\n")
	if strings.Contains(html, "callout") {
		t.Errorf("a plain quote became a callout:\n%s", html)
	}
	if !strings.Contains(html, "<blockquote>") {
		t.Errorf("a plain quote stopped being one:\n%s", html)
	}
}
