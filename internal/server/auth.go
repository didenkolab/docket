package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/gitvcs"
)

// sessionCookie holds nothing but an opaque id. The token that proves who
// someone is stays in the server's memory and is never written anywhere: not
// to the cookie, not to disk. A restart signs everyone out, which is the
// honest consequence.
const sessionCookie = "docket_session"

type session struct {
	token    string
	identity access.Identity
	started  time.Time
}

// authority is the signed-in half of the server. It is nil when the server runs
// unauthenticated, and every check below reads as "no authority, no restriction"
// — which is exactly what --auth none means and what it prints at startup.
type authority struct {
	checker *access.Checker
	life    time.Duration

	mu       sync.Mutex
	sessions map[string]*session
}

func newAuthority(host access.Host, recheck, life time.Duration) *authority {
	return &authority{
		checker:  access.NewChecker(host, recheck),
		life:     life,
		sessions: map[string]*session{},
	}
}

func (a *authority) open(token string, identity access.Identity) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	a.sessions[id] = &session{token: token, identity: identity, started: time.Now()}
	a.mu.Unlock()
	return id, nil
}

func (a *authority) close(id string) {
	a.mu.Lock()
	if s, ok := a.sessions[id]; ok {
		a.checker.Forget(s.token)
		delete(a.sessions, id)
	}
	a.mu.Unlock()
}

func (a *authority) lookup(id string) (*session, bool) {
	a.mu.Lock()
	s, ok := a.sessions[id]
	if ok && time.Since(s.started) > a.life {
		delete(a.sessions, id)
		ok = false
	}
	a.mu.Unlock()
	return s, ok
}

func randomID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

/* ---------- the request's identity ---------- */

type identityKey struct{}

// identityOf is who is asking. Without an authority everyone is an admin,
// because there is nobody to be anyone else.
func identityOf(r *http.Request) access.Identity {
	if identity, ok := r.Context().Value(identityKey{}).(access.Identity); ok {
		return identity
	}
	return access.Identity{Role: access.RoleAdmin}
}

// guard resolves the session, enforces what the role may do, and puts the
// identity where handlers can read it.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		if open(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			s.demandSignIn(w, r)
			return
		}
		current, ok := s.auth.lookup(cookie.Value)
		if !ok {
			s.demandSignIn(w, r)
			return
		}

		// Re-ask the host on a timer, so access removed there stops working
		// here without waiting for the session to expire.
		identity, err := s.auth.checker.Identify(r.Context(), current.token)
		if err != nil {
			s.auth.close(cookie.Value)
			s.demandSignIn(w, r)
			return
		}
		current.identity = identity

		if !allowed(identity, r) {
			s.refuse(w, identity, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity)))
	})
}

// open lists what is reachable without signing in: the sign-in page itself,
// the stylesheet it needs, and the health check.
func open(path string) bool {
	return path == "/sign-in" || path == "/healthz" || strings.HasPrefix(path, "/static/")
}

func allowed(identity access.Identity, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		// The vault's vocabulary is not a secret, but the page that edits it
		// should not be offered to someone who cannot save.
		if strings.HasPrefix(r.URL.Path, "/settings") {
			return identity.CanConfigure()
		}
		return true
	}
	if r.URL.Path == "/sign-out" {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/settings") {
		return identity.CanConfigure()
	}
	return identity.CanWrite()
}

func (s *Server) demandSignIn(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		apiError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	http.Redirect(w, r, "/sign-in?next="+urlEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

func (s *Server) refuse(w http.ResponseWriter, identity access.Identity, r *http.Request) {
	message := "Your access to " + s.auth.checker.Host.Repository() + " is read-only, so this " +
		"is not yours to change. Access is granted on " + s.auth.checker.Host.Name() +
		", not here."
	if strings.HasPrefix(r.URL.Path, "/settings") && identity.CanWrite() {
		message = "Changing the vault's vocabulary needs administrator access to " +
			s.auth.checker.Host.Repository() + ". Yours is write."
	}
	if wantsJSON(r) {
		apiError(w, http.StatusForbidden, message)
		return
	}
	c, _ := loadConfigQuietly(s.root)
	w.WriteHeader(http.StatusForbidden)
	s.render(w, r, "error.html", c, "Not yours to change", message)
}

func wantsJSON(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.Contains(r.Header.Get("Accept"), "application/json")
}

func urlEscape(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "&", "%26"), "?", "%3F")
}

// backTo is where to send somebody after they sign in.
//
// Starting with a slash is not enough to mean "a page here". `//example.com`
// starts with a slash and is a URL to another host — a sign-in page that
// forwards to wherever a link says is a sign-in page an attacker can use to
// look like this one. Only a path on this server is accepted; anything else
// becomes the board.
func backTo(next string) string {
	if next == "" || next[0] != '/' {
		return "/"
	}
	if len(next) > 1 && (next[1] == '/' || next[1] == '\\') {
		return "/"
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "/"
	}
	return next
}

/* ---------- signing in and out ---------- */

func (s *Server) handleSignInForm(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	c, _ := loadConfigQuietly(s.root)
	s.render(w, r, "sign-in.html", c, "Sign in", signInView{
		Host:       s.auth.checker.Host.Name(),
		Repository: s.auth.checker.Host.Repository(),
		Next:       r.URL.Query().Get("next"),
	})
}

type signInView struct {
	Host       string
	Repository string
	Next       string
	Error      string
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	next := backTo(r.FormValue("next"))

	c, _ := loadConfigQuietly(s.root)
	fail := func(message string) {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "sign-in.html", c, "Sign in", signInView{
			Host:       s.auth.checker.Host.Name(),
			Repository: s.auth.checker.Host.Repository(),
			Next:       next,
			Error:      message,
		})
	}

	if token == "" {
		fail("A token is needed to ask " + s.auth.checker.Host.Name() + " who you are.")
		return
	}

	identity, err := s.auth.checker.Identify(r.Context(), token)
	if err != nil {
		fail(err.Error())
		return
	}
	id, err := s.auth.open(token, identity)
	if err != nil {
		fail(err.Error())
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(s.auth.life.Seconds()),
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			s.auth.close(cookie.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
}

/* ---------- who has access ---------- */

type adminView struct {
	Host          string
	Repository    string
	SettingsURL   string
	Recheck       string
	You           access.Identity
	Collaborators []access.Collaborator
	Note          string
	Unauthed      bool
}

// handleAdmin shows who has access and where it is granted. It grants nothing:
// a button here would appear to hand out something it cannot, because what
// matters is a clone, and that is the host's to give.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	c, _ := loadConfigQuietly(s.root)

	if s.auth == nil {
		s.render(w, r, "admin.html", c, "Access", adminView{
			Unauthed: true,
			Note: "This server is running unauthenticated. Anyone who can reach it can write, " +
				"and every change is attributed to the author it was started with. Start it " +
				"against a vault with a GitHub remote to sign people in as themselves.",
		})
		return
	}

	host := s.auth.checker.Host
	view := adminView{
		Host:        host.Name(),
		Repository:  host.Repository(),
		SettingsURL: host.SettingsURL(),
		Recheck:     s.auth.checker.TTL.String(),
		You:         identityOf(r),
	}

	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if current, ok := s.auth.lookup(cookie.Value); ok {
			people, err := host.Collaborators(r.Context(), current.token)
			if err != nil {
				view.Note = "Cannot list who has access: " + err.Error() +
					". " + host.Name() + " only answers that for an administrator's token."
			}
			view.Collaborators = people
		}
	}

	s.render(w, r, "admin.html", c, "Access", view)
}

// authorFor is who a write is attributed to.
//
// With an authority it is the signed-in person, using the name and email the
// host reports, so git log becomes a truthful record of who moved what. Without
// one it is the author the server was started with, which is the only honest
// answer available.
func (s *Server) authorFor(r *http.Request) gitvcs.Author {
	if s.auth != nil {
		if identity, ok := r.Context().Value(identityKey{}).(access.Identity); ok {
			return gitvcs.Author{Name: identity.DisplayName(), Email: identity.Email}
		}
	}
	if header := r.Header.Get("X-Docket-Author"); header != "" {
		if a, err := gitvcs.ParseAuthor(header); err == nil {
			return a
		}
	}
	if form := r.FormValue("author"); form != "" {
		if a, err := gitvcs.ParseAuthor(form); err == nil {
			return a
		}
	}
	return s.author
}
