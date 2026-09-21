package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
)

// stubHost stands in for a git host. Tokens are the role they buy, so a test
// reads as "sign in as a viewer" rather than as a fixture lookup.
type stubHost struct {
	revoked map[string]bool
}

func (h *stubHost) Name() string        { return "StubHub" }
func (h *stubHost) HostName() string    { return "stub.example.com" }
func (h *stubHost) GitUser() string     { return "x-token" }
func (h *stubHost) Repository() string  { return "acme/platform" }
func (h *stubHost) SettingsURL() string { return "https://example.com/settings" }

func (h *stubHost) Identify(_ context.Context, token string) (access.Identity, error) {
	if h.revoked[token] {
		return access.Identity{}, fmt.Errorf("StubHub rejected the token")
	}
	switch token {
	case access.RoleViewer, access.RoleMember, access.RoleAdmin:
		return access.Identity{
			Login: token + "-person",
			Name:  strings.ToUpper(token[:1]) + token[1:] + " Person",
			Email: token + "@example.com",
			Role:  token,
		}, nil
	default:
		return access.Identity{}, fmt.Errorf("StubHub rejected the token")
	}
}

func (h *stubHost) Collaborators(context.Context, string) ([]access.Collaborator, error) {
	return []access.Collaborator{{Login: "dana", Name: "Dana", Role: access.RoleAdmin}}, nil
}

// guardedServer is a server that makes people sign in.
func guardedServer(t *testing.T) (*Server, http.Handler, *stubHost, string) {
	t.Helper()

	root := vaultUnderGit(t)
	host := &stubHost{revoked: map[string]bool{}}

	s, err := New(root, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Host:        host,
		Recheck:     time.Nanosecond, // re-ask every time, so revocation is testable
		SessionLife: time.Hour,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.now = func() time.Time { return noon.Add(time.Hour) }
	return s, s.Handler(), host, root
}

// vaultUnderGit scaffolds a vault under git with one task, the same starting
// state every server test uses.
func vaultUnderGit(t *testing.T) string {
	t.Helper()
	_, _, root := newServer(t)
	return root
}

// signIn exchanges a token for a session cookie.
func signIn(t *testing.T, h http.Handler, token string) *http.Cookie {
	t.Helper()
	w := postForm(t, h, "/sign-in", url.Values{"token": {token}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("sign-in as %s: code = %d; body:\n%s", token, w.Code, w.Body)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatalf("sign-in as %s set no session cookie", token)
	return nil
}

func as(t *testing.T, h http.Handler, cookie *http.Cookie, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if form == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWithoutASessionEveryPageAsksYouToSignIn(t *testing.T) {
	_, h, _, _ := guardedServer(t)

	w := as(t, h, nil, "GET", "/", nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want a redirect", w.Code)
	}
	if got := w.Header().Get("Location"); !strings.HasPrefix(got, "/sign-in") {
		t.Errorf("redirected to %q", got)
	}
}

func TestTheSignInPageAndTheStylesheetStayReachable(t *testing.T) {
	// A sign-in page that needs a session to render cannot be signed in to.
	_, h, _, _ := guardedServer(t)

	for _, path := range []string{"/sign-in", "/static/style.css", "/healthz"} {
		if w := as(t, h, nil, "GET", path, nil); w.Code != http.StatusOK {
			t.Errorf("%s: code = %d, want 200", path, w.Code)
		}
	}
}

func TestABadTokenIsRefusedWithTheHostsReason(t *testing.T) {
	_, h, _, _ := guardedServer(t)

	w := postForm(t, h, "/sign-in", url.Values{"token": {"nonsense"}})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "rejected the token") {
		t.Errorf("the page does not say why:\n%s", w.Body)
	}
}

func TestAViewerReadsAndCannotWrite(t *testing.T) {
	s, h, _, _ := guardedServer(t)
	cookie := signIn(t, h, access.RoleViewer)

	if w := as(t, h, cookie, "GET", "/", nil); w.Code != http.StatusOK {
		t.Errorf("a viewer cannot see the board: %d", w.Code)
	}
	if w := as(t, h, cookie, "GET", "/task/ACME-1", nil); w.Code != http.StatusOK {
		t.Errorf("a viewer cannot open a task: %d", w.Code)
	}

	w := as(t, h, cookie, "POST", "/task/ACME-1/status", url.Values{
		"version": {currentVersion(t, s, "ACME-1")},
		"status":  {"Done"},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("a viewer moved a task: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "read-only") {
		t.Errorf("the refusal does not explain itself:\n%s", w.Body)
	}
}

func TestAMemberWritesButDoesNotConfigure(t *testing.T) {
	s, h, _, root := guardedServer(t)
	cookie := signIn(t, h, access.RoleMember)

	w := as(t, h, cookie, "POST", "/task/ACME-1/status", url.Values{
		"version": {currentVersion(t, s, "ACME-1")},
		"status":  {"In progress"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("a member could not move a task: %d; body:\n%s", w.Code, w.Body)
	}

	// The commit is the person, not the server. That is the point of asking
	// the host who they are.
	if got := lastCommit(t, root); !strings.Contains(got, "member@example.com") {
		t.Errorf("commit = %q, want it attributed to the signed-in person", got)
	}

	if w := as(t, h, cookie, "GET", "/settings", nil); w.Code != http.StatusForbidden {
		t.Errorf("a member reached the settings page: %d", w.Code)
	}
	c, _ := project.Load(root)
	if w := as(t, h, cookie, "POST", "/settings", settingsForm(c, nil)); w.Code != http.StatusForbidden {
		t.Errorf("a member saved settings: %d", w.Code)
	}
}

func TestAnAdminMayConfigure(t *testing.T) {
	_, h, _, root := guardedServer(t)
	cookie := signIn(t, h, access.RoleAdmin)

	if w := as(t, h, cookie, "GET", "/settings", nil); w.Code != http.StatusOK {
		t.Errorf("an admin cannot open settings: %d", w.Code)
	}
	c, _ := project.Load(root)
	if w := as(t, h, cookie, "POST", "/settings", settingsForm(c, nil)); w.Code != http.StatusSeeOther {
		t.Errorf("an admin cannot save settings: %d", w.Code)
	}
}

func TestTheAPIAnswersUnauthenticatedCallsInJSON(t *testing.T) {
	// A redirect to a sign-in page is useless to a program.
	_, h, _, _ := guardedServer(t)

	w := as(t, h, nil, "GET", "/api/tasks", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("content type = %q", w.Header().Get("Content-Type"))
	}
}

func TestAccessRemovedOnTheHostStopsWorkingHere(t *testing.T) {
	_, h, host, _ := guardedServer(t)
	cookie := signIn(t, h, access.RoleMember)

	if w := as(t, h, cookie, "GET", "/", nil); w.Code != http.StatusOK {
		t.Fatalf("the session did not work to begin with: %d", w.Code)
	}

	host.revoked[access.RoleMember] = true

	w := as(t, h, cookie, "GET", "/", nil)
	if w.Code != http.StatusSeeOther {
		t.Errorf("code = %d, want the session to stop working", w.Code)
	}
}

func TestSigningOutEndsTheSession(t *testing.T) {
	_, h, _, _ := guardedServer(t)
	cookie := signIn(t, h, access.RoleMember)

	if w := as(t, h, cookie, "POST", "/sign-out", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("sign-out: %d", w.Code)
	}
	if w := as(t, h, cookie, "GET", "/", nil); w.Code != http.StatusSeeOther {
		t.Errorf("the session survived signing out: %d", w.Code)
	}
}

func TestTheNavigationOffersOnlyWhatTheRoleMay(t *testing.T) {
	_, h, _, _ := guardedServer(t)

	viewer := as(t, h, signIn(t, h, access.RoleViewer), "GET", "/", nil).Body.String()
	if strings.Contains(viewer, `href="/new"`) || strings.Contains(viewer, `href="/settings"`) {
		t.Error("a viewer is offered pages they cannot use")
	}

	member := as(t, h, signIn(t, h, access.RoleMember), "GET", "/", nil).Body.String()
	if !strings.Contains(member, `href="/new"`) {
		t.Error("a member is not offered the page they need most")
	}
	if strings.Contains(member, `href="/settings"`) {
		t.Error("a member is offered settings they cannot save")
	}

	admin := as(t, h, signIn(t, h, access.RoleAdmin), "GET", "/", nil).Body.String()
	if !strings.Contains(admin, `href="/settings"`) {
		t.Error("an admin is not offered settings")
	}
	if !strings.Contains(admin, "admin") {
		t.Error("the page does not say which role you have")
	}
}

func TestTheAccessPageSaysWhereAccessIsGranted(t *testing.T) {
	// It grants nothing, and has to say so: what actually matters is who can
	// clone, and that is the host's to give.
	_, h, _, _ := guardedServer(t)
	cookie := signIn(t, h, access.RoleAdmin)

	body := as(t, h, cookie, "GET", "/admin", nil).Body.String()
	for _, want := range []string{"StubHub", "acme/platform", "https://example.com/settings", "dana"} {
		if !strings.Contains(body, want) {
			t.Errorf("the access page does not mention %q", want)
		}
	}
}

func TestWithoutAHostTheServerSaysItIsOpen(t *testing.T) {
	_, h, _ := newServer(t)

	body := get(t, h, "/admin").Body.String()
	if !strings.Contains(body, "unauthenticated") {
		t.Errorf("an open server does not admit it:\n%s", body)
	}
	// And it stays usable, which is the point of the mode.
	if w := get(t, h, "/"); w.Code != http.StatusOK {
		t.Errorf("board: %d", w.Code)
	}
}

// The repository list is read under a mutex, and close() already holds it —
// calling the accessor there deadlocked the whole server the moment a token
// stopped working. The symptom was a request that never returned, which is why
// this asserts on a deadline rather than on a value.
func TestClosingASessionDoesNotDeadlock(t *testing.T) {
	_, h, host, _ := guardedServer(t)
	cookie := signIn(t, h, access.RoleMember)

	// Access is taken away on the host, so the next request has to close the
	// session — the path that deadlocked.
	host.revoked[access.RoleMember] = true

	done := make(chan int, 1)
	go func() { done <- as(t, h, cookie, "GET", "/", nil).Code }()

	select {
	case code := <-done:
		if code != http.StatusSeeOther {
			t.Errorf("code = %d, want a redirect to sign in again", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the request never returned: closing the session deadlocked")
	}
}
