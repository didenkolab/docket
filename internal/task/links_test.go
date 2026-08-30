package task

import "testing"

func TestTargetReadsALink(t *testing.T) {
	cases := map[string]string{
		"[[ACME-12 A title]]":         "ACME-12 A title",
		"[[ACME-12 A title|the one]]": "ACME-12 A title",
		"[[docs/auth#Sessions]]":      "docs/auth",
		"  [[ auth ]]  ":              "auth",
		"auth":                        "", // a string is not a link, and saying so is the point
		"":                            "",
		"[[unclosed":                  "",
	}
	for value, want := range cases {
		if got := Target(value); got != want {
			t.Errorf("Target(%q) = %q, want %q", value, got, want)
		}
	}
}

// A vault written before relationships became links still has to open.
func TestNoteOfReadsBothForms(t *testing.T) {
	if got := NoteOf("[[ACME-12 A title]]"); got != "ACME-12 A title" {
		t.Errorf("link: %q", got)
	}
	if got := NoteOf("ACME-12"); got != "ACME-12" {
		t.Errorf("bare: %q", got)
	}
}

func TestKeyOfFindsTheKeyInANoteName(t *testing.T) {
	cases := map[string]string{
		"ACME-12 Fix login redirect loop": "ACME-12",
		"ACME/ACME-12 Fix login":          "ACME-12",
		"B2B-7 Something":                 "B2B-7",
		"ACME-12":                         "ACME-12",
		"auth":                            "",
		"docs/auth":                       "",
		"lower-12 Something":              "",
		"ACME- Something":                 "",
	}
	for note, want := range cases {
		if got := KeyOf(note); got != want {
			t.Errorf("KeyOf(%q) = %q, want %q", note, got, want)
		}
	}
}

func TestRetargetFollowsARename(t *testing.T) {
	in := "See [[Old name]] and [[Old name|this]] and [[Old name#Heading]].\n" +
		"Not [[Other]].\n"
	out, changed := Retarget(in, "Old name", "New name")
	if !changed {
		t.Fatal("nothing changed")
	}
	want := "See [[New name]] and [[New name|this]] and [[New name#Heading]].\n" +
		"Not [[Other]].\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// A wikilink inside code is an example of the syntax, not a pointer. Rewriting
// it would edit somebody's documentation as a side effect of a rename.
func TestRetargetLeavesCodeAlone(t *testing.T) {
	in := "Link: [[Old name]].\n\n```\nWrite it as [[Old name]].\n```\n\nAnd `[[Old name]]` inline.\n"
	out, _ := Retarget(in, "Old name", "New name")

	if got := countOf(out, "[[New name]]"); got != 1 {
		t.Errorf("%d links repointed, want 1:\n%s", got, out)
	}
	if got := countOf(out, "[[Old name]]"); got != 2 {
		t.Errorf("%d examples left alone, want 2:\n%s", got, out)
	}
}

// Frontmatter is text like any other, and a parent link lives there.
func TestRetargetReachesFrontmatter(t *testing.T) {
	in := "---\nkey: ACME-2\nparent: \"[[ACME-1 Old name]]\"\n---\n\nBody.\n"
	out, changed := Retarget(in, "ACME-1 Old name", "ACME-1 New name")
	if !changed || !contains(out, `parent: "[[ACME-1 New name]]"`) {
		t.Errorf("frontmatter not repointed:\n%s", out)
	}
}

func countOf(s, sub string) int {
	n := 0
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			n++
		}
	}
	return n
}

func contains(s, sub string) bool { return countOf(s, sub) > 0 }
