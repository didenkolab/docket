package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A discussion nobody can find is a discussion that happens somewhere else. On
// a real task the box sat eight screens down, under the description, the
// criteria and six long comments — so it is above them now, and the top of the
// page says how many there are and goes there.
func TestTheCommentBoxIsReachable(t *testing.T) {
	_, h, _ := newServer(t)

	for _, said := range []string{"First thing said", "Second thing said"} {
		w := postForm(t, h, "/task/ACME-1/comment", url.Values{"text": {said}})
		if w.Code != http.StatusSeeOther {
			t.Fatalf("commenting: %d — %s", w.Code, w.Body.String())
		}
	}

	page := get(t, h, "/task/ACME-1").Body.String()

	box := strings.Index(page, `class="comment-form"`)
	first := strings.Index(page, `<article class="comment">`)
	switch {
	case box < 0:
		t.Fatal("there is no way to comment on the page")
	case first < 0:
		t.Fatal("the comments are not on the page")
	case box > first:
		t.Error("the box is under what has already been said, which is where nobody finds it")
	}
	if strings.Count(page, `class="comment-form"`) != 1 {
		t.Error("there is more than one box, and a page with two forms for one thing is a page " +
			"where the second one is a mistake")
	}
	if !strings.Contains(page, `href="#activity"`) || !strings.Contains(page, `id="activity"`) {
		t.Error("nothing at the top of the page goes to the discussion")
	}
	if !strings.Contains(page, ">Activity 2<") {
		t.Error("the link does not say how much has been said")
	}
}

// What was said is in the file, so it is in the history and in Obsidian.
func TestACommentIsInTheFile(t *testing.T) {
	_, h, root := newServer(t)

	w := postForm(t, h, "/task/ACME-1/comment", url.Values{"text": {"Возвращаю: сроки не сходятся"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("commenting: %d", w.Code)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "comment") {
		t.Errorf("the comment was not committed as one: %s", got)
	}
	if !strings.Contains(get(t, h, "/task/ACME-1").Body.String(), "Возвращаю: сроки не сходятся") {
		t.Error("the comment is not on the page it was left on")
	}
}
