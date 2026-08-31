package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
)

// done builds a finished column of n cards, the newest first by number.
func doneColumn(n int) column {
	col := column{Status: project.Status{Name: "Done", Category: project.CategoryDone}}
	for i := 1; i <= n; i++ {
		col.Cards = append(col.Cards, card{
			Key:     fmt.Sprintf("ACME-%d", i),
			updated: fmt.Sprintf("2026-01-%02dT00:00:00Z", i),
		})
	}
	col.Count = len(col.Cards)
	return col
}

func keysOf(cards []card) []string {
	var out []string
	for _, c := range cards {
		out = append(out, c.Key)
	}
	return out
}

// A finished column is the one part of a board that only grows, and on a real
// board it grew to half the page. It is put aside by default — but only aside:
// the head still counts the whole column, and the rest is one link away.
func TestAFinishedColumnDrawsTheRecentPart(t *testing.T) {
	href := func(status string) string { return "/?full=" + url.QueryEscape(status) }

	col := doneColumn(recentlyDone + 5)
	holdBack(&col, "", href)

	if len(col.Cards) != recentlyDone {
		t.Errorf("drew %d cards, want %d", len(col.Cards), recentlyDone)
	}
	if col.Count != recentlyDone+5 {
		t.Errorf("the head says %d, and the column has %d", col.Count, recentlyDone+5)
	}
	if col.Hidden != 5 {
		t.Errorf("says %d are not drawn, want 5", col.Hidden)
	}
	if col.MoreHref != "/?full=Done" {
		t.Errorf("the way to the rest is %q", col.MoreHref)
	}
	// The five oldest are the ones put aside.
	drawn := strings.Join(keysOf(col.Cards), " ")
	for _, old := range []string{"ACME-1 ", "ACME-2 ", "ACME-3 ", "ACME-4 ", "ACME-5 "} {
		if strings.Contains(drawn+" ", old) {
			t.Errorf("%sis drawn, and it is one of the oldest: %s", old, drawn)
		}
	}
	if got := col.Cards[0].Key; got != fmt.Sprintf("ACME-%d", recentlyDone+5) {
		t.Errorf("the newest card is %q, want the last one finished", got)
	}
}

// Asked for the whole column, it draws the whole column — and says how to go
// back, because a page you can only leave with the browser's button is a page
// that has taken something away.
func TestTheWholeFinishedColumnCanBeAskedFor(t *testing.T) {
	href := func(status string) string {
		if status == "" {
			return "/"
		}
		return "/?full=" + url.QueryEscape(status)
	}

	col := doneColumn(recentlyDone + 5)
	holdBack(&col, "Done", href)

	if len(col.Cards) != recentlyDone+5 {
		t.Errorf("drew %d of %d", len(col.Cards), recentlyDone+5)
	}
	if col.Hidden != 0 {
		t.Errorf("says %d are hidden while showing everything", col.Hidden)
	}
	if col.LessHref != "/" {
		t.Errorf("the way back is %q", col.LessHref)
	}
}

// Only the finished column, and only when there is enough in it. A long "In
// progress" is a fact about the team, and a board that hid it would be hiding
// the fact.
func TestUnfinishedColumnsAreDrawnWhole(t *testing.T) {
	href := func(string) string { return "/?full=x" }

	doing := doneColumn(recentlyDone + 40)
	doing.Status = project.Status{Name: "In progress", Category: project.CategoryDoing}
	holdBack(&doing, "", href)
	if len(doing.Cards) != recentlyDone+40 || doing.Hidden != 0 {
		t.Errorf("an unfinished column was cut to %d, hiding %d", len(doing.Cards), doing.Hidden)
	}

	short := doneColumn(recentlyDone)
	holdBack(&short, "", href)
	if len(short.Cards) != recentlyDone || short.Hidden != 0 || short.MoreHref != "" {
		t.Errorf("a column that fits was cut to %d, hiding %d, linking to %q",
			len(short.Cards), short.Hidden, short.MoreHref)
	}
}

// The link has to keep whatever else the address was saying — a board filtered
// to one project that lost the filter on the way to its own archive would come
// back as a different board.
func TestTheLinkKeepsTheRestOfTheAddress(t *testing.T) {
	at, err := url.Parse("/?project=ACME&full=Backlog")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := elsewhere(at, "full", "Done"), "/?full=Done&project=ACME"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := elsewhere(at, "full", ""), "/?project=ACME"; got != want {
		t.Errorf("removing it gave %q, want %q", got, want)
	}
	plain, err := url.Parse("/?full=Done")
	if err != nil {
		t.Fatal(err)
	}
	if got := elsewhere(plain, "full", ""); got != "/" {
		t.Errorf("the last parameter left %q behind", got)
	}
}

// End to end: the page itself, because the count in the head is written by the
// template and a column that put cards aside must not report the smaller
// number.
func TestTheBoardPageCountsTheWholeFinishedColumn(t *testing.T) {
	_, h, root := newServer(t)

	for i := 2; i <= recentlyDone+4; i++ {
		body := fmt.Sprintf(`---
key: ACME-%d
title: Something finished %d
type: task
status: Done
status_category: done
priority: medium
created: 2026-01-01T00:00:00Z
updated: 2026-01-%02dT00:00:00Z
aliases: [ACME-%d]
---
Done.
`, i, i, i%28+1, i)
		name := fmt.Sprintf("ACME-%d Something finished %d.md", i, i)
		if err := os.WriteFile(filepath.Join(root, "ACME", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	w := get(t, h, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	page := w.Body.String()

	if !strings.Contains(page, `<span class="count">28</span>`) {
		t.Error("the head does not count the whole column")
	}
	if !strings.Contains(page, "3 more, finished earlier") {
		t.Error("the column does not say how many it is not drawing")
	}
	if strings.Count(page, `class="card"`) != recentlyDone+1 {
		t.Errorf("drew %d cards, want %d", strings.Count(page, `class="card"`), recentlyDone+1)
	}

	whole := get(t, h, "/?full=Done")
	if whole.Code != http.StatusOK {
		t.Fatalf("code = %d", whole.Code)
	}
	if n := strings.Count(whole.Body.String(), `class="card"`); n != recentlyDone+4 {
		t.Errorf("asked for the whole column and got %d cards, want %d", n, recentlyDone+4)
	}
}
