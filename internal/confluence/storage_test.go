package confluence

import (
	"strings"
	"testing"
)

func convert(t *testing.T, storage string) string {
	t.Helper()
	got, err := Convert(storage)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return got
}

func TestHeadingsAndParagraphs(t *testing.T) {
	got := convert(t, `<h2>Session model</h2><p>How a session is established.</p>`)

	if !strings.Contains(got, "## Session model") {
		t.Errorf("heading missing:\n%q", got)
	}
	if !strings.Contains(got, "How a session is established.") {
		t.Errorf("paragraph missing:\n%q", got)
	}
}

func TestInlineFormatting(t *testing.T) {
	got := convert(t, `<p>A <strong>bold</strong> and <em>italic</em> and <code>literal</code> word.</p>`)

	for _, want := range []string{"**bold**", "*italic*", "`literal`"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestLists(t *testing.T) {
	got := convert(t, `<ul><li>one</li><li>two</li></ul>`)
	if !strings.Contains(got, "- one") || !strings.Contains(got, "- two") {
		t.Errorf("got %q", got)
	}
}

func TestNestedListsKeepTheirIndent(t *testing.T) {
	got := convert(t, `<ul><li>outer<ul><li>inner</li></ul></li></ul>`)
	if !strings.Contains(got, "\n  - inner") {
		t.Errorf("nesting was flattened:\n%q", got)
	}
}

func TestOrderedListsAreNumbered(t *testing.T) {
	got := convert(t, `<ol><li>first</li><li>second</li></ol>`)
	if !strings.Contains(got, "1. first") || !strings.Contains(got, "2. second") {
		t.Errorf("got %q", got)
	}
}

func TestLinks(t *testing.T) {
	got := convert(t, `<p>See <a href="https://example.com/x">the page</a>.</p>`)
	if !strings.Contains(got, "[the page](https://example.com/x)") {
		t.Errorf("got %q", got)
	}
}

func TestPageLinksBecomeWikilinks(t *testing.T) {
	// A link between pages should keep working inside the vault, and a
	// wikilink is how the vault expresses that.
	got := convert(t, `<p>See <ac:link><ri:page ri:content-title="Session model" /></ac:link>.</p>`)
	if !strings.Contains(got, "[[Session model]]") {
		t.Errorf("got %q", got)
	}
}

func TestTable(t *testing.T) {
	got := convert(t, `<table><tbody>
		<tr><th>Field</th><th>Value</th></tr>
		<tr><td>key</td><td>ACME-1</td></tr>
	</tbody></table>`)

	if !strings.Contains(got, "| Field | Value |") {
		t.Errorf("header row missing:\n%q", got)
	}
	if !strings.Contains(got, "| --- | --- |") {
		t.Errorf("separator missing:\n%q", got)
	}
	if !strings.Contains(got, "| key | ACME-1 |") {
		t.Errorf("body row missing:\n%q", got)
	}
}

func TestCodeMacro(t *testing.T) {
	got := convert(t, `<ac:structured-macro ac:name="code">`+
		`<ac:plain-text-body><![CDATA[func main() {}]]></ac:plain-text-body>`+
		`</ac:structured-macro>`)

	if !strings.Contains(got, "```") || !strings.Contains(got, "func main() {}") {
		t.Errorf("got %q", got)
	}
}

func TestQueryMacrosSayWhatWasThere(t *testing.T) {
	// A children macro is navigation, not content — a whole space's structure
	// can hang off one. An empty gap where it used to be is a thing nobody
	// notices until they need it.
	for _, name := range []string{"children", "recently-updated", "pagetree", "toc"} {
		got := convert(t, `<ac:structured-macro ac:name="`+name+`" />`)
		if !strings.Contains(got, name) {
			t.Errorf("macro %q left no trace:\n%q", name, got)
		}
		if !strings.Contains(got, "query") {
			t.Errorf("macro %q does not explain itself:\n%q", name, got)
		}
	}
}

func TestPanelMacrosKeepTheirEmphasis(t *testing.T) {
	got := convert(t, `<ac:structured-macro ac:name="warning"><ac:rich-text-body>`+
		`<p>Careful.</p></ac:rich-text-body></ac:structured-macro>`)

	if !strings.Contains(got, "**WARNING**") || !strings.Contains(got, "Careful.") {
		t.Errorf("got %q", got)
	}
}

func TestUnknownMacrosAreVisible(t *testing.T) {
	got := convert(t, `<ac:structured-macro ac:name="jira-issues" />`)
	if !strings.Contains(got, "unconverted macro: jira-issues") {
		t.Errorf("got %q", got)
	}
}

func TestEntitiesAndEmptyInput(t *testing.T) {
	if got := convert(t, `<p>a &amp; b &lt; c</p>`); !strings.Contains(got, "a & b < c") {
		t.Errorf("entities were not decoded: %q", got)
	}
	if got := convert(t, ""); got != "" {
		t.Errorf("empty storage produced %q", got)
	}
}

func TestBlankLinesAreCollapsed(t *testing.T) {
	got := convert(t, `<p>one</p><p>two</p>`)
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("a run of blank lines survived:\n%q", got)
	}
}
