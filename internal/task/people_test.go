package task

import (
	"strings"
	"testing"
)

// A handle is read the way a label is not: a label filed under
// docs/labels/auth is "auth", and a person called agent/claude is
// "agent/claude". Stripping the folder from both turned every agent in a real
// vault into "claude".
func TestAHandleKeepsItsSlash(t *testing.T) {
	for _, c := range []struct{ written, want string }{
		{`"[[marina]]"`, "marina"},
		{"marina", "marina"},
		{`"[[people/marina]]"`, "marina"},
		{"agent/claude", "agent/claude"},
		{`"[[agent/claude]]"`, "agent/claude"},
		{`"[[people/agent/claude]]"`, "agent/claude"},
		{"", ""},
	} {
		parsed, err := Parse([]byte("---\nkey: A-1\ntitle: x\nassignee: " + c.written + "\n---\n"))
		if err != nil {
			t.Fatalf("%s: %v", c.written, err)
		}
		if parsed.Assignee != c.want {
			t.Errorf("assignee: %s read as %q, want %q", c.written, parsed.Assignee, c.want)
		}
	}
}

// Written as a link, because that is what Obsidian draws and counts as a
// backlink — the whole reason a person is a page.
func TestAnAssigneeIsWrittenAsALink(t *testing.T) {
	parsed, err := Parse([]byte("---\nkey: A-1\ntitle: x\nassignee:\n---\n"))
	if err != nil {
		t.Fatal(err)
	}

	parsed.SetAssignee("marina")
	body, err := parsed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); !strings.Contains(got, `assignee: "[[marina]]"`) {
		t.Errorf("written as:\n%s", got)
	}
	if parsed.Assignee != "marina" {
		t.Errorf("the struct says %q", parsed.Assignee)
	}

	// Taken off everybody, which is a real state and the one a backlog is
	// full of.
	parsed.SetAssignee("  ")
	body, _ = parsed.Bytes()
	if got := string(body); !strings.Contains(got, "assignee:\n") {
		t.Errorf("cleared as:\n%s", got)
	}
	if parsed.Assignee != "" {
		t.Errorf("the struct still says %q", parsed.Assignee)
	}
}
