package server

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Rights on the repository are how somebody becomes a person work can be put
// on. The host knows who has them; the vault has never heard of them until the
// moment the work is given — and then it writes them down, in the same commit,
// so the link points at a page that exists.
func TestAssigningToAMemberWritesTheirPage(t *testing.T) {
	_, h, _, root := guardedServer(t)
	session := signIn(t, h, access.RoleMember)

	w := as(t, h, session, http.MethodPost, "/task/ACME-1/assignee", url.Values{
		"assignee": {"dana"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}

	page := filepath.Join(root, vault.PeopleDir, "dana.md")
	raw, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("no page was written for a member the host named: %v", err)
	}
	body := string(raw)
	for _, want := range []string{"type: person", "name: Dana"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not say %q:\n%s", want, body)
		}
	}

	// And the task links to it, so the backlinks pane on Dana's page is her
	// work — which is the whole reason a person is a page.
	task, err := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task), `assignee: "[[dana]]"`) {
		t.Errorf("the task does not link to the person:\n%s", firstLines(string(task), 12))
	}

	// One commit, not two: the page and the assignment are one change.
	if got := lastCommit(t, root); !strings.Contains(got, "→ dana") {
		t.Errorf("committed as %q", got)
	}
	if !committedIn(t, root, "people/dana.md") {
		t.Error("the page was written but not committed with the assignment")
	}
}

// A handle nobody vouches for is still allowed — a vault may name somebody the
// host has never heard of — but no page is invented for them, and nothing links
// to a note that does not exist.
func TestAssigningToAStrangerWritesNoPage(t *testing.T) {
	_, h, _, root := guardedServer(t)
	session := signIn(t, h, access.RoleMember)

	w := as(t, h, session, http.MethodPost, "/task/ACME-1/assignee", url.Values{
		"assignee": {"someone_else"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, vault.PeopleDir, "someone_else.md")); err == nil {
		t.Error("a page was invented for somebody nobody vouched for")
	}

	task, err := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(task), "[[someone_else]]") {
		t.Error("the task links to a page that does not exist")
	}
	if !strings.Contains(string(task), "assignee: someone_else") {
		t.Errorf("the handle was not written plainly:\n%s", firstLines(string(task), 12))
	}
}

// Taking the work is the same write with the handle filled in from the session,
// so there is one way it can be wrong rather than two.
func TestTakingItAssignsToWhoeverIsSignedIn(t *testing.T) {
	_, h, _, root := guardedServer(t)
	session := signIn(t, h, access.RoleMember)

	w := as(t, h, session, http.MethodPost, "/task/ACME-1/assignee", url.Values{"me": {"1"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}

	task, err := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The stub host calls a member "member-person".
	if !strings.Contains(string(task), "member-person") {
		t.Errorf("the task is not on whoever is signed in:\n%s", firstLines(string(task), 12))
	}
}

// The suggestion list is all three sources at once, and the person already
// carrying work comes first.
func TestTheSuggestionsOfferMembersAndPeopleAndWhoeverCarriesWork(t *testing.T) {
	_, h, _, _ := guardedServer(t)
	session := signIn(t, h, access.RoleMember)

	page := as(t, h, session, http.MethodGet, "/task/ACME-1", nil).Body.String()
	for _, want := range []string{`value="dana"`, `value="agent/claude"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the suggestions do not offer %q", want)
		}
	}
	if at, first := strings.Index(page, `value="agent/claude"`), strings.Index(page, `value="dana"`); at > first {
		t.Error("somebody with no work here is offered before the person already doing it")
	}
}

func firstLines(text string, n int) string {
	lines := strings.SplitN(text, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// committedIn reports whether the last commit touched that path.
func committedIn(t *testing.T, root, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "show", "--name-only", "--format=", "HEAD")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %v: %s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == path {
			return true
		}
	}
	return false
}
