package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A vault with no remote commits every change and publishes none of them, and
// the board used to report neither.
//
// The repository was skipped before the panel was drawn, so a person moving
// cards saw no badge, no warning and no explanation — which is the silence
// IGL-37 exists to prevent, one step earlier in the chain. That rule catches a
// push that failed; this is a push that was never attempted.
func TestAVaultWithNoRemoteSaysSo(t *testing.T) {
	s, handler, root := newServer(t)
	_ = root

	// Nothing written yet: a local-only vault nobody has touched is a normal
	// thing to have, and a badge that is always there is a badge nobody reads.
	if notes := s.pushNotes(); len(notes) != 0 {
		t.Errorf("an untouched local vault is nagging about its remote: %+v", notes)
	}

	// Move a card, which is a commit.
	w := as(t, handler, nil, http.MethodPost, "/task/ACME-1/status",
		url.Values{"status": {"In review"}})
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("the move was refused: %d %s", w.Code, w.Body)
	}

	notes := s.pushNotes()
	if len(notes) != 1 {
		t.Fatalf("after a write, the board says %d things about its remote, want 1", len(notes))
	}
	if !notes[0].NoRemote {
		t.Errorf("it does not say there is no remote: %+v", notes[0])
	}
	if notes[0].Wrote < 1 {
		t.Errorf("it does not say how much is stranded: %+v", notes[0])
	}

	// And the page says it in words, with what to do.
	page := as(t, handler, nil, http.MethodGet, "/", nil).Body.String()
	for _, want := range []string{"no remote", "nothing has left this folder", "git remote add origin"} {
		if !strings.Contains(page, want) {
			t.Errorf("the board does not say %q", want)
		}
	}
}

// The assignee changes from the page you are reading the task on.
//
// The status could already be changed there and the assignee could not, which
// is an odd place to draw the line: they are the two things somebody changes
// while looking at a task rather than while editing one.
func TestAssigneeChangesFromTheTaskPage(t *testing.T) {
	_, handler, _ := newServer(t)

	page := as(t, handler, nil, http.MethodGet, "/task/ACME-1", nil).Body.String()
	if !strings.Contains(page, `action="/task/ACME-1/assignee"`) {
		t.Fatal("the task page offers no way to change who it is on")
	}

	w := as(t, handler, nil, http.MethodPost, "/task/ACME-1/assignee",
		url.Values{"assignee": {"dana"}})
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("refused: %d %s", w.Code, w.Body)
	}
	if got := as(t, handler, nil, http.MethodGet, "/task/ACME-1", nil).Body.String(); !strings.Contains(got, "dana") {
		t.Error("the task is not on dana")
	}

	// Empty unassigns, which is a real thing to want and not a mistake.
	if w := as(t, handler, nil, http.MethodPost, "/task/ACME-1/assignee",
		url.Values{"assignee": {""}}); w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("unassigning was refused: %d %s", w.Code, w.Body)
	}
	if got := as(t, handler, nil, http.MethodGet, "/task/ACME-1", nil).Body.String(); strings.Contains(got, "dana") {
		t.Error("it is still on dana")
	}
}

// The task page renders to the end.
//
// A template naming a field the view does not have stops where it stands, and
// what is left is a page that ends early with nothing saying so. That happened:
// the assignee form referenced People before the task view had it, and
// everything below the properties — the comments, the history, the delete
// button — silently vanished. Two other tests caught it on the build; this one
// says what the fault was.
func TestTheTaskPageRendersToTheEnd(t *testing.T) {
	_, handler, _ := newServer(t)

	page := as(t, handler, nil, http.MethodGet, "/task/ACME-1", nil).Body.String()

	// One marker from each region, in the order they appear, so a page cut off
	// anywhere is caught rather than only a page cut off early.
	for _, want := range []string{
		`action="/task/ACME-1/assignee"`, // the properties
		`action="/task/ACME-1/comment"`,  // the comments
		`action="/task/ACME-1/delete"`,   // the very bottom
		"</html>",                        // and the document actually closed
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s — it ends before that", want)
		}
	}
}
