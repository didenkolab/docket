package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A board with work on it, spread over two people and three columns.
func peopledBoard(t *testing.T) http.Handler {
	t.Helper()
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, made := range []struct{ title, status, who string }{
		{"Marina writes the importer", "In progress", "marina"},
		{"Marina reviews the plan", "In review", "marina"},
		{"Timur fixes the parser", "In progress", "timur"},
		{"Nobody has taken this", "Backlog", ""},
	} {
		_, written, err := vault.Create(root, c, vault.NewOptions{
			Title: made.title, Assignee: made.who, Now: noon,
		})
		if err != nil {
			t.Fatal(err)
		}
		w := postForm(t, h, "/task/"+written.Key+"/status", url.Values{"status": {made.status}})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
			t.Fatalf("%s to %s: %d", written.Key, made.status, w.Code)
		}
	}
	return h
}

// A board of a thousand cards is read by asking it two questions: whose, and
// which column. Both have to be one click, and both have to be in the address
// so the narrowed board is a link somebody can send.
func TestTheBoardNarrowsToOnePerson(t *testing.T) {
	h := peopledBoard(t)

	page := get(t, h, "/?assignee=marina").Body.String()
	if !strings.Contains(page, "Marina writes the importer") {
		t.Error("her work is not on her board")
	}
	if strings.Contains(page, "Timur fixes the parser") {
		t.Error("somebody else's work is on her board")
	}
	if !strings.Contains(page, "not shown") {
		t.Error("the board does not say it is hiding anything")
	}
}

// What nobody has picked up is the filter a board is asked for most.
func TestTheBoardNarrowsToWhatNobodyHasTaken(t *testing.T) {
	h := peopledBoard(t)

	page := get(t, h, "/?assignee=%21unassigned").Body.String()
	if !strings.Contains(page, "Nobody has taken this") {
		t.Error("the unassigned card is not there")
	}
	if strings.Contains(page, "Marina writes the importer") {
		t.Error("an assigned card is there")
	}
}

// A status draws that column and no other: "just show me what is in review",
// asked of a board of fourteen columns.
func TestTheBoardNarrowsToOneColumn(t *testing.T) {
	h := peopledBoard(t)

	page := get(t, h, "/?status=In+review").Body.String()
	if n := strings.Count(page, `<section class="column`); n != 1 {
		t.Errorf("drew %d columns, want 1", n)
	}
	if !strings.Contains(page, "Marina reviews the plan") {
		t.Error("the column it drew is not the one asked for")
	}
	// Everything in the columns it did not draw is also not shown, and the line
	// above the board counts all of it.
	if !strings.Contains(page, "4 cards not shown") {
		t.Errorf("the board miscounts what its column filter left out:\n%s",
			page[strings.Index(page, "finder-bar"):strings.Index(page, "finder-bar")+400])
	}
}

// The two narrow together, and each menu option carries the other filter with
// it — a bar where choosing one thing forgets the other is a bar nobody can
// use twice.
func TestTheFiltersCombineAndTheLinksKeepEachOther(t *testing.T) {
	h := peopledBoard(t)

	page := get(t, h, "/?assignee=marina&status=In+progress").Body.String()
	if !strings.Contains(page, "Marina writes the importer") {
		t.Error("the two filters together dropped the card that satisfies both")
	}
	if strings.Contains(page, "Marina reviews the plan") {
		t.Error("the status filter did not apply")
	}
	if strings.Contains(page, "Timur fixes the parser") {
		t.Error("the assignee filter did not apply")
	}
	// The template escapes what it writes, so a plus in a query is &#43; here.
	if !strings.Contains(page, "assignee=timur&amp;status=In&#43;progress") {
		t.Errorf("the assignee options do not keep the column in force:\n%s", page)
	}
}

// The menu offers everybody who has work on the board, not only the person
// already chosen — which is what counting after the filter would give.
func TestTheAssigneeMenuOffersEverybodyOnTheBoard(t *testing.T) {
	h := peopledBoard(t)

	page := get(t, h, "/?assignee=marina").Body.String()
	for _, want := range []string{">marina<", ">timur<", ">Nobody<", ">Anyone<"} {
		if !strings.Contains(page, want) {
			t.Errorf("the menu does not offer %q", want)
		}
	}
}
