package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// A vault with tasks in three states, so a filter that selects can be told from
// one that does not.
func servedWithWork(t *testing.T) (http.Handler, string) {
	t.Helper()
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, made := range []struct{ title, status string }{
		{"Still to do", "Backlog"},
		{"Being done", "In progress"},
		{"Finished", "Done"},
	} {
		_, written, err := vault.Create(root, c, vault.NewOptions{Title: made.title, Now: noon})
		if err != nil {
			t.Fatal(err)
		}
		key := written.Key
		// Moved the way a person moves it, so the fixture is the application's
		// own writing rather than a second implementation of it.
		w := postForm(t, h, "/task/"+key+"/status", url.Values{"status": {made.status}})
		if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
			t.Fatalf("%s to %s: %d — %s", key, made.status, w.Code, w.Body.String())
		}
	}
	return h, root
}

// The views are the vault's own .base files. A vault scaffolded from the
// template has some, and they are what the page lists.
func TestTheViewsAreTheVaultsOwnFiles(t *testing.T) {
	h, _ := servedWithWork(t)

	w := get(t, h, "/views")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"boards/board.base", "boards/backlog.base", "file.inFolder"} {
		if !strings.Contains(body, want) {
			t.Errorf("the listing does not mention %q", want)
		}
	}
}

// The board view selects unfinished work. Drawing it has to give that answer
// and not "every task", which is what a filter quietly not applied looks like.
func TestAViewDrawsWhatItsFilterSelects(t *testing.T) {
	h, _ := servedWithWork(t)

	w := get(t, h, "/view/boards/board.base")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Still to do") || !strings.Contains(body, "Being done") {
		t.Error("the view is missing unfinished work")
	}
	if strings.Contains(body, "Finished") {
		t.Error("the view shows a finished task, so the filter did not apply")
	}

	// The key is the row's first column, and every generated view also lists it
	// among its properties. Drawn from both, each row said its key twice.
	if n := strings.Count(body, ">ACME-2<"); n != 1 {
		t.Errorf("the key appears %d times in a row, want once", n)
	}
}

// A filter changed on the web is the same file Obsidian reads, so it is saved
// through the same commit as anything else.
func TestAViewIsEditedAndCommitted(t *testing.T) {
	h, root := servedWithWork(t)

	form := get(t, h, "/views/edit/boards/board.base")
	if form.Code != http.StatusOK {
		t.Fatalf("the form: %d", form.Code)
	}
	if !strings.Contains(form.Body.String(), "status_category") {
		t.Error("the form does not hold the file")
	}

	kept, err := os.ReadFile(filepath.Join(root, "boards", "board.base"))
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(kept), `note.status_category != "done"`,
		`note.status_category == "doing"`, 1)
	if changed == string(kept) {
		t.Fatal("the template's board no longer says what this test rewrites")
	}

	w := postForm(t, h, "/views/save", url.Values{
		"path": {"boards/board.base"}, "body": {changed}, "version": {version(kept)},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving: %d — %s", w.Code, w.Body.String())
	}

	after := get(t, h, "/view/boards/board.base").Body.String()
	if !strings.Contains(after, "Being done") {
		t.Error("the edited filter does not select what it now says")
	}
	if strings.Contains(after, "Still to do") {
		t.Error("the edited filter still selects what it no longer says")
	}
	if got := lastCommit(t, root); !strings.Contains(got, "board.base") {
		t.Errorf("the change was not committed as itself: %s", got)
	}
}

// A file that would not draw is refused before it is written. Saved and then
// found broken means somebody has to go and fix it in a text editor, which is
// what this page exists to avoid.
func TestAnUnreadableFilterIsNotSaved(t *testing.T) {
	h, root := servedWithWork(t)

	at := filepath.Join(root, "boards", "board.base")
	kept, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}

	w := postForm(t, h, "/views/save", url.Values{
		"path":    {"boards/board.base"},
		"body":    {"filters:\n  and:\n    - 'note.due.isBefore(date(\"today\"))'\n"},
		"version": {version(kept)},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want a refusal", w.Code)
	}
	if !strings.Contains(w.Body.String(), "isBefore") {
		t.Errorf("the refusal does not say what it could not read:\n%s", w.Body.String())
	}

	now, err := os.ReadFile(at)
	if err != nil {
		t.Fatal(err)
	}
	if string(now) != string(kept) {
		t.Error("the file was written anyway")
	}
}

// A new view is a new file in a project's boards folder.
func TestANewViewIsWritten(t *testing.T) {
	h, root := servedWithWork(t)

	w := postForm(t, h, "/views/save", url.Values{
		"new":  {"1"},
		"in":   {""},
		"name": {"waiting-on-review"},
		"body": {"filters: 'note.status == \"In review\"'\nviews:\n  - type: table\n    name: Waiting\n    order:\n      - note.key\n"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if got, want := w.Header().Get("Location"), "/view/boards/waiting-on-review.base"; got != want {
		t.Errorf("went to %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "boards", "waiting-on-review.base")); err != nil {
		t.Errorf("the file is not there: %v", err)
	}
}
