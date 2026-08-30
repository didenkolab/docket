package adf

import (
	"strings"
	"testing"
)

func convert(t *testing.T, raw string) string {
	t.Helper()
	got, err := ConvertJSON([]byte(raw))
	if err != nil {
		t.Fatalf("ConvertJSON: %v", err)
	}
	return got
}

func doc(content string) string {
	return `{"type":"doc","version":1,"content":[` + content + `]}`
}

func TestParagraphAndMarks(t *testing.T) {
	got := convert(t, doc(`{"type":"paragraph","content":[
		{"type":"text","text":"plain "},
		{"type":"text","text":"bold","marks":[{"type":"strong"}]},
		{"type":"text","text":" and "},
		{"type":"text","text":"code","marks":[{"type":"code"}]},
		{"type":"text","text":" and a ","marks":[]},
		{"type":"text","text":"link","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}
	]}`))

	want := "plain **bold** and `code` and a [link](https://example.com)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodeMarkIsNotWrappedInEmphasis(t *testing.T) {
	// Everything inside a code span is literal, so emphasis outside it would
	// show up as asterisks in the rendered text.
	got := convert(t, doc(`{"type":"paragraph","content":[
		{"type":"text","text":"x","marks":[{"type":"code"},{"type":"strong"}]}]}`))

	if got != "**`x`**\n" {
		t.Errorf("got %q, want %q", got, "**`x`**\n")
	}
}

func TestHeadings(t *testing.T) {
	got := convert(t, doc(`{"type":"heading","attrs":{"level":3},
		"content":[{"type":"text","text":"Session model"}]}`))

	if got != "### Session model\n" {
		t.Errorf("got %q", got)
	}
}

func TestLists(t *testing.T) {
	got := convert(t, doc(`{"type":"bulletList","content":[
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}
	]}`))

	if got != "- one\n- two\n" {
		t.Errorf("got %q", got)
	}
}

func TestNestedList(t *testing.T) {
	got := convert(t, doc(`{"type":"bulletList","content":[
		{"type":"listItem","content":[
			{"type":"paragraph","content":[{"type":"text","text":"outer"}]},
			{"type":"bulletList","content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"inner"}]}]}
			]}
		]}
	]}`))

	if !strings.Contains(got, "- outer\n  - inner") {
		t.Errorf("nesting was lost:\n%q", got)
	}
}

func TestOrderedListKeepsItsStart(t *testing.T) {
	got := convert(t, doc(`{"type":"orderedList","attrs":{"order":5},"content":[
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"five"}]}]},
		{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"six"}]}]}
	]}`))

	if got != "5. five\n6. six\n" {
		t.Errorf("got %q", got)
	}
}

func TestCodeBlock(t *testing.T) {
	got := convert(t, doc(`{"type":"codeBlock","attrs":{"language":"go"},
		"content":[{"type":"text","text":"func main() {}"}]}`))

	if got != "```go\nfunc main() {}\n```\n" {
		t.Errorf("got %q", got)
	}
}

func TestTable(t *testing.T) {
	got := convert(t, doc(`{"type":"table","content":[
		{"type":"tableRow","content":[
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Field"}]}]},
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Value"}]}]}]},
		{"type":"tableRow","content":[
			{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"key"}]}]},
			{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"ACME-1"}]}]}]}
	]}`))

	want := "| Field | Value |\n| --- | --- |\n| key | ACME-1 |\n"
	if got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestTablePipesAreEscaped(t *testing.T) {
	got := convert(t, doc(`{"type":"table","content":[
		{"type":"tableRow","content":[
			{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"a|b"}]}]}]}
	]}`))

	if !strings.Contains(got, `a\|b`) {
		t.Errorf("an unescaped pipe would break the table:\n%q", got)
	}
}

func TestTaskList(t *testing.T) {
	got := convert(t, doc(`{"type":"taskList","content":[
		{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"done thing"}]},
		{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"open thing"}]}
	]}`))

	if got != "- [x] done thing\n- [ ] open thing\n" {
		t.Errorf("got %q", got)
	}
}

func TestPanelKeepsItsEmphasis(t *testing.T) {
	got := convert(t, doc(`{"type":"panel","attrs":{"panelType":"warning"},
		"content":[{"type":"paragraph","content":[{"type":"text","text":"Careful."}]}]}`))

	if !strings.Contains(got, "> **WARNING**") || !strings.Contains(got, "> Careful.") {
		t.Errorf("got %q", got)
	}
}

func TestMediaBecomesAResolvableReference(t *testing.T) {
	got := convert(t, doc(`{"type":"mediaSingle","content":[
		{"type":"media","attrs":{"id":"abc-123","alt":"screenshot","type":"file"}}]}`))

	if got != "![screenshot](media:abc-123)\n" {
		t.Errorf("got %q", got)
	}
}

func TestMentionsAndInlineExtras(t *testing.T) {
	got := convert(t, doc(`{"type":"paragraph","content":[
		{"type":"mention","attrs":{"text":"Dana"}},
		{"type":"text","text":" see "},
		{"type":"inlineCard","attrs":{"url":"https://example.com/x"}},
		{"type":"text","text":" "},
		{"type":"status","attrs":{"text":"BLOCKED"}}
	]}`))

	for _, want := range []string{"@Dana", "<https://example.com/x>", "`BLOCKED`"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestUnknownNodesAreVisibleNotSilent(t *testing.T) {
	// A silent hole in an imported description is worse than an ugly one:
	// nobody goes looking for text they were never told went missing.
	got := convert(t, doc(`{"type":"someFutureThing","content":[
		{"type":"paragraph","content":[{"type":"text","text":"inner text"}]}]}`))

	if !strings.Contains(got, "<!-- unconverted someFutureThing -->") {
		t.Errorf("the unknown node left no trace:\n%q", got)
	}
	if !strings.Contains(got, "inner text") {
		t.Errorf("the text inside an unknown node was dropped:\n%q", got)
	}
}

func TestEmptyInput(t *testing.T) {
	for _, raw := range []string{"", "null", `{"type":"notADoc"}`} {
		if got := convert(t, raw); got != "" {
			t.Errorf("ConvertJSON(%q) = %q, want empty", raw, got)
		}
	}
}

func TestBrokenJSONIsAnError(t *testing.T) {
	if _, err := ConvertJSON([]byte("{not json")); err == nil {
		t.Error("broken JSON was accepted")
	}
}
