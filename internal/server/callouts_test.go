package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
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

/* ---------- what links here ---------- */

// Obsidian shows every note that links to this one. For a tracker that is the
// answer to "what else refers to this task", and it needs no relationship to
// have been declared in advance — a link written in a sentence is one.
func TestATaskShowsWhatLinksToIt(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title:       "Session model",
		Description: "Blocked by [[ACME-1 Fix login redirect loop]] until the cookie is fixed.",
		Now:         noon,
	}); err != nil {
		t.Fatal(err)
	}
	page := "---\ntitle: Auth\n---\n\nSee [[ACME-1 Fix login redirect loop]] for the redirect bug.\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "auth.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/task/ACME-1").Body.String()

	if !strings.Contains(body, "Referenced by") {
		t.Fatalf("no backlinks section:\n%s", body)
	}
	for _, want := range []string{"ACME-2", "Session model", "docs/auth", "until the cookie is fixed"} {
		if !strings.Contains(body, want) {
			t.Errorf("the backlinks do not mention %q", want)
		}
	}
}

// The note's name appearing in a sentence is a coincidence until somebody makes
// it a link. Obsidian calls that an unlinked mention and keeps it separate.
func TestAnUnlinkedMentionIsNotABacklink(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title:       "Session model",
		Description: "Related to ACME-1 Fix login redirect loop, but not linked to it.",
		Now:         noon,
	}); err != nil {
		t.Fatal(err)
	}

	if body := get(t, h, "/task/ACME-1").Body.String(); strings.Contains(body, "Referenced by") {
		t.Errorf("a plain mention was counted as a link:\n%s", body)
	}
}

// A parent is a link, so an epic is referenced by its children without anything
// else being written down.
func TestAParentLinkIsABacklink(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "A child", Parent: "ACME-1", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/task/ACME-1").Body.String()
	if !strings.Contains(body, "Referenced by") || !strings.Contains(body, "A child") {
		t.Errorf("a child does not reference its parent:\n%s", body)
	}
}

// An excerpt cut at a fixed width starts mid-word and reads as a typo.
func TestAnExcerptStartsAndEndsOnAWord(t *testing.T) {
	long := strings.Repeat("alpha beta gamma delta ", 12)
	text := long + "the needle here " + long

	got := excerpt(text, "needle")
	if strings.HasPrefix(got, "lpha") || strings.HasPrefix(got, "eta") {
		t.Errorf("the excerpt begins mid-word: %q", got)
	}
	if !strings.Contains(got, "needle") {
		t.Errorf("the excerpt lost what it was looking for: %q", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("a clipped start should say so: %q", got)
	}
}
