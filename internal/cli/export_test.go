package cli

import "testing"

// Three of the marketplace's top hundred are checklists, and the list is
// already Markdown in the body. Counting it here means every app that wants
// progress on a card does not write this loop again — and gets the same answer
// Obsidian draws.
func TestAnAcceptanceListIsCounted(t *testing.T) {
	for _, c := range []struct {
		what           string
		body           string
		checked, total int
	}{
		{"nothing at all", "Just prose.\n", 0, 0},
		{"an empty list", "- [ ] one\n- [ ] two\n", 0, 2},
		{"half of it", "- [ ] one\n- [x] two\n", 1, 2},
		{"a capital X, which Obsidian also ticks", "- [X] one\n", 1, 1},
		{"an ordinary bullet is not a box", "- one\n- [ ] two\n", 0, 1},
		{"a box in an example is an example",
			"- [x] real\n```\n- [ ] not real\n- [x] nor this\n```\n", 1, 1},
		{"indented, as a nested list is", "  - [x] one\n", 1, 1},
	} {
		checked, total := boxes(c.body)
		if checked != c.checked || total != c.total {
			t.Errorf("%s: %d of %d, want %d of %d", c.what, checked, total, c.checked, c.total)
		}
	}
}

// An app reads columns by name because a title may hold a comma. Every name the
// flag accepts has to be a column that exists.
func TestEveryNamedColumnCanBeRead(t *testing.T) {
	for _, name := range everyColumn {
		if _, ok := column[name]; !ok {
			t.Errorf("%q is offered and cannot be read", name)
		}
	}
	if _, ok := column["path"]; !ok {
		t.Error("path is readable by name but not offered")
	}
}
