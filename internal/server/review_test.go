package server

import (
	"context"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/access"
)

// askingHost is a stubHost that also answers questions about pull requests.
type askingHost struct {
	*stubHost
	// branch is what it says the request is from; asked records the number.
	branch string
	asked  string
	err    error
}

func (h *askingHost) PullRequestPath() string { return "pull" }
func (h *askingHost) PullRequestName() string { return "pull request" }

func (h *askingHost) PullRequest(_ context.Context, _, id string) (string, error) {
	h.asked = id
	if h.err != nil {
		return "", h.err
	}
	return h.branch, nil
}

// reviewingServer is a guarded server whose host can be asked about pull
// requests, with a branch in the repository for one to point at.
func reviewingServer(t *testing.T) (http.Handler, *askingHost, *http.Cookie) {
	t.Helper()
	s, handler, stub, root := guardedServer(t)

	asking := &askingHost{stubHost: stub, branch: "предложение/сроки"}
	for _, repo := range s.auth.repositories() {
		repo.host = asking
	}

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	git("checkout", "-q", "-b", "предложение/сроки")
	git("checkout", "-q", "-")

	return handler, asking, signIn(t, handler, access.RoleMember)
}

// Nobody is sent a branch: they are sent a pull request address. Turning one
// into the other is a single call to the host, and asking a person to do it by
// hand is asking them to read the page and retype what is in it.
func TestReviewByLinkGoesToWhatTheProposalWouldDo(t *testing.T) {
	handler, asking, session := reviewingServer(t)

	w := as(t, handler, session, http.MethodPost, "/review",
		url.Values{"url": {"https://stub.example.com/acme/platform/pull/17"}})

	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if asking.asked != "17" {
		t.Errorf("asked the host about %q", asking.asked)
	}
	if got, want := w.Header().Get("Location"), "/change/"+url.PathEscape("предложение/сроки"); got != want {
		t.Errorf("went to %q, want %q", got, want)
	}
}

// A branch name in the box is a branch name. Somebody who already knows it
// should not have to go and find a URL for it.
func TestReviewByLinkTakesABranchName(t *testing.T) {
	handler, asking, session := reviewingServer(t)

	w := as(t, handler, session, http.MethodPost, "/review", url.Values{"url": {"предложение/сроки"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if asking.asked != "" {
		t.Errorf("the host was asked %q about a branch name it did not need to resolve", asking.asked)
	}
}

// A URL is somebody else's string. One for another repository names a branch
// that means nothing here, and a board drawn from it would be another
// project's plan in this project's words.
func TestReviewByLinkRefusesWhatItCannotUse(t *testing.T) {
	handler, asking, session := reviewingServer(t)

	for _, c := range []struct {
		what, raw string
		code      int
	}{
		{"another repository", "https://stub.example.com/other/thing/pull/17", http.StatusBadRequest},
		{"another host", "https://github.com/acme/platform/pull/17", http.StatusBadRequest},
		{"nothing at all", "", http.StatusBadRequest},
		{"a branch that is not there", "no-such-branch", http.StatusBadRequest},
	} {
		asking.asked = ""
		w := as(t, handler, session, http.MethodPost, "/review", url.Values{"url": {c.raw}})
		if w.Code != c.code {
			t.Errorf("%s: got %d, want %d", c.what, w.Code, c.code)
		}
		if w.Header().Get("Location") != "" {
			t.Errorf("%s: went to %q anyway", c.what, w.Header().Get("Location"))
		}
	}
}

// A proposal the host says is from a branch this repository does not have is
// not a proposal about this repository. Better a plain no than a board of
// nothing.
func TestReviewByLinkSaysWhenTheBranchIsNotHere(t *testing.T) {
	handler, asking, session := reviewingServer(t)
	asking.branch = "invented/by-the-host"

	w := as(t, handler, session, http.MethodPost, "/review",
		url.Values{"url": {"https://stub.example.com/acme/platform/pull/17"}})
	if w.Code == http.StatusSeeOther {
		t.Fatalf("went to %q for a branch that is not here", w.Header().Get("Location"))
	}
	if !strings.Contains(w.Body.String(), "invented/by-the-host") {
		t.Errorf("did not say which branch it could not find:\n%s", w.Body.String())
	}
}

// Asking the host which branch a pull request is from is signed-in work. On a
// guarded board the guard says so first, which is the right answer: what must
// not happen is a stranger being handed the proposal.
func TestReviewByLinkNeedsASignIn(t *testing.T) {
	handler, asking, _ := reviewingServer(t)

	w := as(t, handler, nil, http.MethodPost, "/review",
		url.Values{"url": {"https://stub.example.com/acme/platform/pull/17"}})
	if strings.HasPrefix(w.Header().Get("Location"), "/change/") {
		t.Errorf("a stranger was sent to the proposal at %q", w.Header().Get("Location"))
	}
	if asking.asked != "" {
		t.Errorf("the host was asked about %q on a stranger's behalf", asking.asked)
	}
}

// The box only appears where there is somebody to ask.
func TestBranchesOffersReviewByLink(t *testing.T) {
	handler, _, session := reviewingServer(t)

	w := as(t, handler, session, http.MethodGet, "/branches", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	for _, want := range []string{`action="/review"`, "StubHub pull request address"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the branches page does not offer %q", want)
		}
	}
}

// A branch name with a slash in it — "предложение/сроки", "origin/later" — has
// to survive the redirect. Escaped, a slash stays inside one path segment, and
// the change page has to match it there rather than treat it as a new segment.
func TestChangePageTakesABranchWithASlashInItsName(t *testing.T) {
	handler, _, session := reviewingServer(t)

	w := as(t, handler, session, http.MethodGet,
		"/change/"+url.PathEscape("предложение/сроки"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "предложение/сроки") {
		t.Errorf("the page does not say which proposal it is about:\n%s", w.Body.String())
	}
}
