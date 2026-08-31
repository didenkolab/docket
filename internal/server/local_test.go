package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/gitvcs"
)

// localServer is a guarded server told whether it is reachable only from this
// machine, and whether something is proxying to it.
func localServer(t *testing.T, loopback, proxied bool) (*Server, http.Handler) {
	t.Helper()

	s, err := New(vaultUnderGit(t), Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Host:        &stubHost{revoked: map[string]bool{}},
		Recheck:     time.Nanosecond,
		SessionLife: time.Hour,
		OnLoopback:  loopback,
		BehindProxy: proxied,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, s.Handler()
}

// The condition that matters, and the one that is easy to get wrong: a reverse
// proxy binds loopback and is reachable from the world, so "you can reach this
// port" stops meaning "you are on this machine".
func TestBehindAProxyTheMachinesCredentialsAreNotOffered(t *testing.T) {
	_, h := localServer(t, true, true)

	if body := as(t, h, nil, "GET", "/sign-in", nil).Body.String(); strings.Contains(body, "/sign-in/local") {
		t.Error("behind a proxy, anybody who can reach the proxy is offered a way in")
	}
	if w := as(t, h, nil, "POST", "/sign-in/local", url.Values{}); w.Code != http.StatusSeeOther {
		t.Errorf("asking anyway: code = %d, want a redirect back rather than a session", w.Code)
	}
}

func TestOffTheLoopbackTheMachinesCredentialsAreNotOffered(t *testing.T) {
	_, h := localServer(t, false, false)

	if body := as(t, h, nil, "GET", "/sign-in", nil).Body.String(); strings.Contains(body, "/sign-in/local") {
		t.Error("a server reachable from elsewhere offered the machine's credentials")
	}
}

func TestOnTheLoopbackTheMachinesCredentialsAreOffered(t *testing.T) {
	s, h := localServer(t, true, false)

	if !s.onLoopback {
		t.Fatal("the server did not take the loopback fact")
	}
	body := as(t, h, nil, "GET", "/sign-in", nil).Body.String()
	if !strings.Contains(body, "/sign-in/local") {
		t.Error("the sign-in page does not offer to use what is already here")
	}
	// And it says what it is doing, because signing somebody in with a
	// credential they did not type is a thing to be told about.
	if !strings.Contains(body, "already has on this machine") {
		t.Errorf("the page does not say where the credential comes from:\n%s", body)
	}
	// The token field stays: the machine may have nothing for that host.
	if !strings.Contains(body, `name="token"`) {
		t.Error("the token field is gone")
	}
}

// Whatever the machine hands over, whose it is remains the host's answer.
func TestAMachineCredentialTheHostRefusesDoesNotSignYouIn(t *testing.T) {
	s, h := localServer(t, true, false)
	// stubHost only knows the three role names, so whatever git or gh returns
	// here is a token it will refuse — which is the case under test.
	w := as(t, h, nil, "POST", "/sign-in/local", url.Values{})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want it refused; body:\n%s", w.Code, w.Body)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			t.Error("a refused credential opened a session")
		}
	}
	if s.auth == nil {
		t.Fatal("no authority")
	}
	// The reason reaches the page rather than a bare 401.
	if body := w.Body.String(); !strings.Contains(body, "machine") {
		t.Errorf("the page does not say what went wrong:\n%s", body)
	}
}

// A workspace spanning hosts cannot have one button: which host would it mean?
func TestWithSeveralHostsTheMachinesCredentialsAreNotOffered(t *testing.T) {
	s, h, _, _ := workspaceOnTwoHosts(t)
	s.onLoopback = true

	if _, ok := s.localSignIn(); ok {
		t.Error("with two hosts, a single button was offered")
	}
	if body := as(t, h, nil, "GET", "/sign-in", nil).Body.String(); strings.Contains(body, "/sign-in/local") {
		t.Error("the page offered it anyway")
	}
	_ = access.RoleViewer
}
