package server

import (
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

// commits is the vault's history, newest first.
func commits(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%h")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	return strings.Fields(string(out))
}

// A board is the present tense, and every question about last week is answered
// by somebody's memory. Here the past is the repository.
func TestTheBoardCanBeReadAtACommit(t *testing.T) {
	_, h, root := newServer(t)

	// One more task, so the two commits differ in what they hold.
	if w := postForm(t, h, "/new", url.Values{
		"title": {"Written later"}, "project": {"ACME"}, "type": {"task"},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("making a task: %d — %s", w.Code, w.Body.String())
	}

	history := commits(t, root)
	if len(history) < 2 {
		t.Fatalf("only %d commits", len(history))
	}
	before := history[1]

	now := get(t, h, "/").Body.String()
	if !strings.Contains(now, "Written later") {
		t.Fatal("the new task is not on the board")
	}

	then := get(t, h, "/?at="+before).Body.String()
	if strings.Contains(then, "Written later") {
		t.Error("a task written after that commit is on the board of that commit")
	}
	if !strings.Contains(then, "Fix login redirect loop") {
		t.Error("what existed then is not there")
	}
	if !strings.Contains(then, before) {
		t.Error("the page does not say which commit it is showing")
	}
}

// The past is read, not written. A board of last Tuesday that accepted a drop
// would write today's file from a page showing something else.
func TestTheBoardOfACommitCannotBeDragged(t *testing.T) {
	_, h, root := newServer(t)
	before := commits(t, root)[0]

	page := get(t, h, "/?at="+before).Body.String()
	if !strings.Contains(page, `data-ref="`) {
		t.Error("the board of a commit is offered as draggable")
	}
	// And it is not called a proposal: a commit is not a branch, and the words
	// on the page have to say which of the two this is.
	if strings.Contains(page, "a board as it would be on that branch") {
		t.Error("a commit was described as a branch")
	}
}

// A commit name goes into a git command. One that is not a commit name is
// refused as what it is.
func TestSomethingThatIsNotACommitIsRefused(t *testing.T) {
	_, h, _ := newServer(t)

	// The board is still drawn — refusing the whole page because one parameter
	// is nonsense would be worse — but it is today's board, and it says so.
	for _, bad := range []string{"; rm -rf /", "HEAD~1", "../../etc/passwd", "zzzzzzz"} {
		page := get(t, h, "/?at="+url.QueryEscape(bad)).Body.String()
		if strings.Contains(page, `data-ref="`) {
			t.Errorf("%q was read as a commit", bad)
		}
		if !strings.Contains(page, "This is the board as it is now") {
			t.Errorf("%q: the page does not say it ignored the commit", bad)
		}
	}
}
