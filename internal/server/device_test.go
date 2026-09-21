package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
)

// deviceStub is a host that can also hand out codes. It answers the way a real
// one does: pending until somebody says otherwise, then a token.
type deviceStub struct {
	stubHost

	clientID string
	// answer is what the next poll returns. A test sets it to say what the
	// person did on the host's site.
	answer error
	token  string
	polls  int
	expiry time.Duration
}

func (d *deviceStub) StartDevice(_ context.Context, clientID string) (access.Device, error) {
	d.clientID = clientID
	life := d.expiry
	if life == 0 {
		life = 15 * time.Minute
	}
	return access.Device{
		UserCode:        "WDJB-MJHT",
		VerificationURI: "https://example.com/login/device",
		DeviceCode:      "the-secret-half",
		Interval:        5 * time.Second,
		Expires:         time.Now().Add(life),
	}, nil
}

func (d *deviceStub) DeviceScope() string { return "read_everything" }

func (d *deviceStub) PollDevice(_ context.Context, _, deviceCode string) (string, error) {
	d.polls++
	if deviceCode != "the-secret-half" {
		return "", access.ErrDeviceDenied
	}
	if d.answer != nil {
		return "", d.answer
	}
	return d.token, nil
}

// deviceServer is a server whose host can issue codes.
func deviceServer(t *testing.T) (*Server, http.Handler, *deviceStub) {
	t.Helper()

	host := &deviceStub{
		stubHost: stubHost{revoked: map[string]bool{}},
		answer:   access.ErrDevicePending,
	}
	s, err := New(vaultUnderGit(t), Options{
		Author:         gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Host:           host,
		Recheck:        time.Nanosecond,
		SessionLife:    time.Hour,
		DeviceClientID: "Ov23liEXAMPLE",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, s.Handler(), host
}

// elapse pretends the interval the host asked for has passed, so a test can
// say "and then they approved it" without sleeping through five seconds.
func elapse(s *Server, cookie *http.Cookie) {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	if w, ok := s.auth.pending[cookie.Value]; ok {
		w.due = time.Now().Add(-time.Second)
	}
}

// startDevice presses the button and returns the cookie that stands for the
// sign-in in progress.
func startDevice(t *testing.T, h http.Handler, next string) *http.Cookie {
	t.Helper()
	w := postForm(t, h, "/sign-in/device", url.Values{"next": {next}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("starting a device sign-in: code = %d; body:\n%s", w.Code, w.Body)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == deviceCookie {
			return cookie
		}
	}
	t.Fatal("starting a device sign-in set no cookie")
	return nil
}

func TestTheCodeIsShownAndTheSecretHalfIsNot(t *testing.T) {
	_, h, _ := deviceServer(t)
	cookie := startDevice(t, h, "/")

	w := as(t, h, cookie, "GET", "/sign-in/device", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want the waiting page", w.Code)
	}
	body := w.Body.String()

	if !strings.Contains(body, "WDJB-MJHT") {
		t.Error("the page does not show the code somebody has to type")
	}
	if !strings.Contains(body, "https://example.com/login/device") {
		t.Error("the page does not say where to type it")
	}
	// The device code redeems the token. A browser that has it can finish
	// somebody else's sign-in, so it must never be in the page or the cookie.
	if strings.Contains(body, "the-secret-half") {
		t.Error("the page leaked the device code")
	}
	if strings.Contains(cookie.Value, "the-secret-half") {
		t.Error("the cookie holds the device code")
	}
	if !cookie.HttpOnly {
		t.Error("the cookie is readable by scripts")
	}
}

// The waiting page has to come back by itself, and without a script: this
// board works with scripting off and a sign-in screen is the last place to
// start requiring it.
func TestTheWaitingPageAsksAgainByItself(t *testing.T) {
	_, h, _ := deviceServer(t)
	cookie := startDevice(t, h, "/")

	body := as(t, h, cookie, "GET", "/sign-in/device", nil).Body.String()
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("the waiting page does not reload itself")
	}
	if strings.Contains(body, "<script") {
		t.Error("the waiting page needs a script")
	}
}

func TestApprovingOnTheHostSignsYouIn(t *testing.T) {
	s, h, host := deviceServer(t)
	cookie := startDevice(t, h, "/pages")

	// Nothing has happened on the host's site yet.
	if w := as(t, h, cookie, "GET", "/sign-in/device", nil); w.Code != http.StatusOK {
		t.Fatalf("code = %d while still waiting", w.Code)
	}

	// Now it has. The interval has to have passed, or the poll is skipped —
	// which is the behaviour the next test is about.
	host.answer, host.token = nil, access.RoleMember
	elapse(s, cookie)

	w := as(t, h, cookie, "GET", "/sign-in/device", nil)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want a redirect into the board; body:\n%s", w.Code, w.Body)
	}
	if got := w.Header().Get("Location"); got != "/pages" {
		t.Errorf("landed on %q, want the page that asked for a sign-in", got)
	}

	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			session = c
		}
		if c.Name == deviceCookie && c.MaxAge >= 0 {
			t.Error("the finished sign-in was left in the browser")
		}
	}
	if session == nil {
		t.Fatal("approving on the host opened no session")
	}
	if w := as(t, h, session, "GET", "/", nil); w.Code != http.StatusOK {
		t.Errorf("the session does not work: code = %d", w.Code)
	}
}

// A refresh every five seconds must not become a request to the host every
// five seconds when the host asked for longer.
func TestTheHostIsNotAskedFasterThanItAllows(t *testing.T) {
	_, h, host := deviceServer(t)
	cookie := startDevice(t, h, "/")

	for i := 0; i < 5; i++ {
		as(t, h, cookie, "GET", "/sign-in/device", nil)
	}
	if host.polls != 1 {
		t.Errorf("asked the host %d times for five page loads, want 1", host.polls)
	}
}

func TestDecliningOnTheHostEndsTheSignIn(t *testing.T) {
	_, h, host := deviceServer(t)
	cookie := startDevice(t, h, "/")
	host.answer = access.ErrDeviceDenied

	w := as(t, h, cookie, "GET", "/sign-in/device", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want the sign-in page again", w.Code)
	}
	if !strings.Contains(w.Body.String(), "declined") {
		t.Errorf("the page does not say what happened:\n%s", w.Body)
	}
	if w := as(t, h, cookie, "GET", "/sign-in/device", nil); w.Code == http.StatusOK {
		t.Error("the declined sign-in can still be waited on")
	}
}

func TestAnExpiredCodeIsNotWaitedOnForever(t *testing.T) {
	_, h, host := deviceServer(t)
	host.expiry = -time.Second
	cookie := startDevice(t, h, "/")
	host.answer = access.ErrDeviceExpired

	w := as(t, h, cookie, "GET", "/sign-in/device", nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "expired") {
		t.Errorf("code = %d; body:\n%s", w.Code, w.Body)
	}
}

func TestCancellingForgetsTheCode(t *testing.T) {
	_, h, _ := deviceServer(t)
	cookie := startDevice(t, h, "/")

	w := as(t, h, cookie, "POST", "/sign-in/device/stop", url.Values{})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	if w := as(t, h, cookie, "GET", "/sign-in/device", nil); w.Code == http.StatusOK {
		t.Error("the cancelled sign-in can still be waited on")
	}
}

// Without a client id there is no application to sign in as, so the page must
// offer the token field alone rather than a button that cannot work.
func TestWithoutAClientIdThereIsNoButton(t *testing.T) {
	_, h, _, _ := guardedServer(t)

	body := as(t, h, nil, "GET", "/sign-in", nil).Body.String()
	if strings.Contains(body, "/sign-in/device") {
		t.Error("the sign-in page offers a device sign-in that cannot work")
	}
	if !strings.Contains(body, `name="token"`) {
		t.Error("the sign-in page offers no way in at all")
	}

	if w := postForm(t, h, "/sign-in/device", url.Values{}); w.Code != http.StatusSeeOther {
		t.Errorf("starting one anyway: code = %d, want a redirect back", w.Code)
	}
}

func TestTheButtonIsOfferedWhenTheHostCanDoIt(t *testing.T) {
	_, h, _ := deviceServer(t)

	body := as(t, h, nil, "GET", "/sign-in", nil).Body.String()
	if !strings.Contains(body, `action="/sign-in/device"`) {
		t.Error("the sign-in page does not offer to do it for you")
	}
	if !strings.Contains(body, `name="token"`) {
		t.Error("the token field is gone, so a host that cannot do this has no way in")
	}
}

// The id belongs to the repository, so the Access page writes it to docket.yaml
// and commits it. Setting it takes effect at once: the next sign-in page has
// the button.
func TestTheAccessPageSetsUpSigningIn(t *testing.T) {
	host := &deviceStub{stubHost: stubHost{revoked: map[string]bool{}}, answer: access.ErrDevicePending}
	root := vaultUnderGit(t)
	s, err := New(root, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Host:        host,
		Recheck:     time.Nanosecond,
		SessionLife: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	admin := signIn(t, h, access.RoleAdmin)

	// Nothing set: the token field is the only way in.
	if body := as(t, h, nil, "GET", "/sign-in", nil).Body.String(); strings.Contains(body, "/sign-in/device") {
		t.Fatal("a button is offered before anything is set up")
	}

	w := as(t, h, admin, "POST", "/admin/sign-in",
		url.Values{"device_client_id": {"Ov23liFROMTHEPAGE"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("saving: code = %d; body:\n%s", w.Code, w.Body)
	}

	// Written to the vault, and committed.
	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.DeviceClientID(); got != "Ov23liFROMTHEPAGE" {
		t.Errorf("docket.yaml holds %q", got)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "sign in") {
		t.Errorf("not committed as a change to signing in: %q", got)
	}

	// And in effect, without a restart.
	if body := as(t, h, nil, "GET", "/sign-in", nil).Body.String(); !strings.Contains(body, "/sign-in/device") {
		t.Error("the button is not there after being set up")
	}

	// Clearing it turns the button off again and leaves no empty section behind.
	if w := as(t, h, admin, "POST", "/admin/sign-in", url.Values{"device_client_id": {"  "}}); w.Code != http.StatusSeeOther {
		t.Fatalf("clearing: code = %d", w.Code)
	}
	raw, err := os.ReadFile(filepath.Join(root, project.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sign_in") {
		t.Errorf("clearing left the section behind:\n%s", raw)
	}
}

// An id the host will not accept must not be saved: it would leave a button
// that fails for everybody, and the host's own refusal is the useful message.
func TestAnIdTheHostRefusesIsNotSaved(t *testing.T) {
	host := &refusingStub{deviceStub: deviceStub{
		stubHost: stubHost{revoked: map[string]bool{}}, answer: access.ErrDevicePending}}
	root := vaultUnderGit(t)
	s, err := New(root, Options{
		Author:      gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Host:        host,
		Recheck:     time.Nanosecond,
		SessionLife: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	w := as(t, h, signIn(t, h, access.RoleAdmin), "POST", "/admin/sign-in",
		url.Values{"device_client_id": {"not-an-application"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Header().Get("Location"); !strings.Contains(got, "problem=") {
		t.Errorf("redirected to %q, want the page saying why", got)
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.DeviceClientID(); got != "" {
		t.Errorf("saved anyway: %q", got)
	}
}

// A member cannot change how everybody signs in.
func TestOnlyAnAdministratorSetsUpSigningIn(t *testing.T) {
	s, h, _ := deviceServer(t)
	_ = s

	w := as(t, h, signIn(t, h, access.RoleMember), "POST", "/admin/sign-in",
		url.Values{"device_client_id": {"Ov23liSNEAKY"}})
	if w.Code != http.StatusForbidden {
		t.Errorf("code = %d, want it refused", w.Code)
	}
}

type refusingStub struct{ deviceStub }

func (r *refusingStub) StartDevice(context.Context, string) (access.Device, error) {
	return access.Device{}, errors.New("unknown client")
}
