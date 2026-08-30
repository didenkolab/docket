package task

import (
	"slices"
	"testing"
)

func TestCleanTagKeepsWhatObsidianAccepts(t *testing.T) {
	cases := map[string]string{
		"auth":           "auth",
		"#auth":          "auth",
		"  #area/auth  ": "area/auth",
		"needs review":   "needs-review",
		"needs  review":  "needs--review",
		"под-вопросом":   "под-вопросом", // a vault is not required to be in English
		"2026":           "",             // a tag may not be all digits
		"":               "",
		"#":              "",
		"a!b@c":          "abc",
		"/leading/":      "leading",
	}
	for in, want := range cases {
		if got := CleanTag(in); got != want {
			t.Errorf("CleanTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTagTreeNests(t *testing.T) {
	got := TagTree("area/auth/session")
	want := []string{"area", "area/auth", "area/auth/session"}
	if !slices.Equal(got, want) {
		t.Errorf("TagTree = %v, want %v", got, want)
	}
	if got := TagTree("auth"); !slices.Equal(got, []string{"auth"}) {
		t.Errorf("a flat tag is itself: %v", got)
	}
}

func TestTagsRoundTrip(t *testing.T) {
	parsed, err := Parse([]byte("---\nkey: ACME-1\ntitle: T\n---\n\nBody.\n"))
	if err != nil {
		t.Fatal(err)
	}
	parsed.SetTags([]string{"#area/auth", "needs review", "2026"})

	out, err := parsed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Tags, []string{"area/auth", "needs-review"}) {
		t.Errorf("tags came back as %v from:\n%s", back.Tags, out)
	}
}
