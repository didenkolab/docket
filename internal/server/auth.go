package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/gitvcs"
)

// sessionCookie holds nothing but an opaque id. The token that proves who
// someone is stays in the server's memory and is never written anywhere: not
// to the cookie, not to disk. A restart signs everyone out, which is the
// honest consequence.
const sessionCookie = "docket_session"

// session is one person's sign-ins: a token per host, and nothing else.
//
// The tokens live here, in memory, and are never written anywhere — not to the
// cookie, not to disk. The browser holds an opaque id. A restart signs everyone
// out, which is the honest consequence of not storing them.
type session struct {
	tokens  map[string]string // host → token
	started time.Time
}

func (s *session) tokenFor(hostKey string) (string, bool) {
	token, ok := s.tokens[strings.ToLower(hostKey)]
	return token, ok
}

func (s *session) has(hostKey string) bool {
	_, ok := s.tokenFor(hostKey)
	return ok
}

// authority is the signed-in half of the server. It is nil when the server runs
// unauthenticated, and every check below reads as "no authority, no restriction"
// — which is exactly what --auth none means and what it prints at startup.
type authority struct {
	life time.Duration

	// repos is every repository in the space and the host that answers for it,
	// in space order. See hosts.go for why this is per repository.
	//
	// Held atomically rather than under mu, and that is not a micro-optimisation
	// — it is what makes the list safe to read from anywhere. Guarding it with
	// mu meant a method that already held mu deadlocked the server by reading
	// it, which happened twice: once in close(), where a token going stale hung
	// every request, and once in adopt(). A lock-free read cannot be misused
	// that way.
	repos atomic.Pointer[[]*repository]

	mu       sync.Mutex
	sessions map[string]*session
	// pending are sign-ins that have been started and not finished: a code the
	// host issued, waiting for somebody to type it. See device.go.
	pending map[string]*waiting
}

func newAuthority(repos []*repository, life time.Duration) *authority {
	a := &authority{
		life:     life,
		sessions: map[string]*session{},
		pending:  map[string]*waiting{},
	}
	a.repos.Store(&repos)
	return a
}

// repositories is the list as it stands. Safe to call while holding mu, which
// is the whole reason it is not guarded by it.
func (a *authority) repositories() []*repository {
	if held := a.repos.Load(); held != nil {
		return *held
	}
	return nil
}

// adopt replaces the list, keeping the checker of every repository that is
// still here.
//
// Keeping them matters: a checker caches what a host said about a token, and
// throwing that away would mean an API call per repository per page load for
// everybody signed in, every time somebody connects a project.
func (a *authority) adopt(repos []*repository) {
	a.mu.Lock()
	defer a.mu.Unlock()

	was := map[string]*repository{}
	for _, r := range a.repositories() {
		was[r.prefix] = r
	}
	for _, r := range repos {
		if old, ok := was[r.prefix]; ok && old.checker != nil && old.hostKey == r.hostKey {
			r.checker = old.checker
		}
	}
	a.repos.Store(&repos)
}

// hostFor is the host a token would be for, and the repositories it covers.
func (a *authority) hostFor(hostKey string) (access.Host, string, bool) {
	hostKey = strings.ToLower(hostKey)
	for _, r := range a.repositories() {
		if r.host != nil && r.hostKey == hostKey {
			return r.host, r.clientID, true
		}
	}
	return nil, "", false
}

// oneHost is the only host there is, when there is only one. A space of a
// single repository — the ordinary case — should never make anybody choose.
func (a *authority) oneHost() (signInHost, bool) {
	all := hosts(a.repositories())
	if len(all) == 1 {
		return all[0], true
	}
	return signInHost{}, false
}

// keep records a token against the host it identifies somebody on, opening a
// session if this is the first one. Signing into a second host adds to the
// session rather than replacing it.
func (a *authority) keep(id, hostKey, token string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	current, ok := a.sessions[id]
	if !ok || id == "" {
		fresh, err := randomID()
		if err != nil {
			return "", err
		}
		id, current = fresh, &session{tokens: map[string]string{}, started: time.Now()}
		a.sessions[id] = current
	}
	current.tokens[strings.ToLower(hostKey)] = token
	return id, nil
}

func (a *authority) close(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[id]
	if !ok {
		return
	}
	// Forgetting the cached answer everywhere the token was used, so signing out
	// cannot leave a repository still believing in it.
	for _, r := range a.repositories() {
		if r.checker == nil {
			continue
		}
		if token, held := s.tokenFor(r.hostKey); held {
			r.checker.Forget(token)
		}
	}
	delete(a.sessions, id)
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

// identityOf is who is asking, taken as one role for the whole space.
//
// It is the widest role held anywhere, and it exists for the parts of the
// interface that are not about one repository: whether to offer "New task" at
// all. Anything that acts on a task asks about that task's project instead —
// standingIn(r).CanWrite(projectKey) — because that is where the host draws
// the line. See hosts.go.
func identityOf(r *http.Request) access.Identity {
	if st := standingIn(r); st != nil {
		return st.Best
	}
	return access.Identity{Role: access.RoleAdmin}
}

// guard resolves the session, works out what it may do in each repository, and
// refuses what it may not.
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

		// Every repository is re-asked on its own timer, so access removed on
		// one host stops working here without waiting for the session to
		// expire, and without a cached answer from another host masking it.
		st := s.auth.standingOf(r.Context(), current)
		if !st.SignedIn {
			// Every token in the session has stopped working. That is a
			// sign-out, not an error.
			s.auth.close(cookie.Value)
			s.demandSignIn(w, r)
			return
		}

		if refusal := allowed(st, r); refusal != "" {
			s.refuse(w, r, refusal)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), standingKey{}, st)))
	})
}

// open lists what is reachable without signing in: the sign-in page itself,
// the stylesheet it needs, and the health check.
func open(path string) bool {
	// /in/ is open to the guard and closed by its own secret. A CI job has no
	// session and never will: it is a machine, and what says it may post is a
	// secret held where the server runs rather than a person's sign-in. See
	// internal/server/inbox.go, which refuses an inbox that names no secret on
	// a server that signs people in.
	return path == "/sign-in" || strings.HasPrefix(path, "/sign-in/") ||
		path == "/healthz" || strings.HasPrefix(path, "/static/") ||
		strings.HasPrefix(path, "/in/")
}

// allowed says why a request is refused, or "" when it is not.
//
// It returns a sentence rather than a boolean because the reason is the useful
// part: "you may read this project but not change it" and "you have not signed
// into the host that holds it" are different problems with different next
// steps, and a bare 403 tells somebody neither.
//
// Which project a request is about decides who answers. A request about none of
// them in particular — the board, a search — is allowed through and filtered:
// see standing.readable.
func allowed(st *standing, r *http.Request) string {
	reading := r.Method == http.MethodGet || r.Method == http.MethodHead
	key := projectOf(r)

	if reading {
		// The vault's vocabulary is not a secret, but the page that edits it
		// should not be offered to somebody who cannot save.
		if strings.HasPrefix(r.URL.Path, "/settings") && !st.canConfigureAnything() {
			return "Changing what the vault calls things needs administrator access to the " +
				"repository, which is granted on its host rather than here."
		}
		if key != "" && !st.CanRead(key) {
			return notYours(st, key, "see")
		}
		return ""
	}

	if r.URL.Path == "/sign-out" {
		return ""
	}
	// Which repositories the workspace holds is not about any one project, and
	// must not be checked as though it were: a project being made does not
	// exist yet, so asking whether this person may write to it would refuse
	// every creation. It takes administering something here, which is what the
	// handlers check again for themselves.
	if strings.HasPrefix(r.URL.Path, "/projects") {
		if !st.canConfigureAnything() {
			return "Changing which repositories this workspace holds needs administrator " +
				"access to a repository already in it."
		}
		return ""
	}

	// How the vault describes itself, and how people get into it, are the two
	// things a member may read and may not change. /admin is a page anybody
	// signed in may look at — it says who has access — but writing there sets
	// up signing in for everybody.
	if strings.HasPrefix(r.URL.Path, "/settings") || strings.HasPrefix(r.URL.Path, "/admin") {
		if key == "" && !st.canConfigureAnything() {
			return "Changing what the vault calls things, or how people sign in, needs " +
				"administrator access to the repository."
		}
		if key != "" && !st.CanConfigure(key) {
			return notYours(st, key, "configure")
		}
		return ""
	}

	if key == "" {
		// A write that does not name a project — creating a task with none
		// chosen, say. It is allowed only if any project would take it, and the
		// handler picks one it may write to.
		if !st.canWriteAnything() {
			return "Your access to every project here is read-only. Access is granted on the " +
				"host that holds each repository, not here."
		}
		return ""
	}
	if !st.CanWrite(key) {
		return notYours(st, key, "change")
	}
	return ""
}

// notYours is the sentence for a project you may not do that to, and it says
// which of the two reasons it is.
func notYours(st *standing, key, verb string) string {
	if st.In(key).Role == "" {
		if host := st.hostHolding(key); host != "" {
			return "That is in " + key + ", which is on " + host + ". You have not signed " +
				"into " + host + " yet."
		}
		return "You have no access to " + key + ". Access is granted on the host that holds " +
			"its repository, not here."
	}
	return "Your access to " + key + " is " + plainly(st.In(key).Role) + ", so this is not " +
		"yours to " + verb + ". Access is granted on the host that holds it, not here."
}

// plainly is a role in the words somebody would use about it. The three names
// are the product's own vocabulary and belong on the Access page, where they are
// defined; a refusal should say what you can and cannot do.
func plainly(role string) string {
	switch role {
	case access.RoleViewer:
		return "read-only"
	case access.RoleMember:
		return "write"
	case access.RoleAdmin:
		return "administrator"
	}
	return "none"
}

func (s *Server) demandSignIn(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		apiError(w, http.StatusUnauthorized, "sign in first")
		return
	}
	http.Redirect(w, r, "/sign-in?next="+urlEscape(r.URL.RequestURI()), http.StatusSeeOther)
}

func (s *Server) refuse(w http.ResponseWriter, r *http.Request, message string) {
	if wantsJSON(r) {
		apiError(w, http.StatusForbidden, message)
		return
	}
	c, _ := s.config()
	w.WriteHeader(http.StatusForbidden)
	s.render(w, r, "error.html", c, "Not yours to change", message)
}

func wantsJSON(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/api/") ||
		strings.Contains(r.Header.Get("Accept"), "application/json")
}

// urlEscape makes a value safe to carry in a query parameter.
//
// It replaced two characters by hand — & and ? — which is enough for a path
// being handed back after a sign-in and wrong for everything else it grew to be
// used for. A sentence redirected as a message came back with its spaces intact
// and the browser refused the whole header, so the message never arrived; a
// message with a per cent sign in it would have been decoded as an escape.
func urlEscape(s string) string { return url.QueryEscape(s) }

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
	c, _ := s.config()
	s.render(w, r, "sign-in.html", c, "Sign in",
		s.signInPage(r, r.URL.Query().Get("next"), ""))
}

type signInView struct {
	Next  string
	Error string
	// Hosts are the hosts holding repositories in this space, in space order.
	// Usually one; a workspace may span several, and then signing in is
	// something you do once per host rather than once.
	Hosts []signInHostView
	// Signed are the hosts already signed into, so a page reached while
	// half-way through says so rather than looking like a fresh start.
	Signed []string
	// Local says the credentials already on this machine can be used, and names
	// the host they are for. Offered only on a loopback listener that nothing is
	// proxying — see local.go.
	Local     bool
	LocalHost string
}

// signInHostView is one host to sign into.
type signInHostView struct {
	Key  string
	Name string
	// Repos is what signing in here gets you, so a choice between two hosts is
	// a choice between named things rather than between two brand names.
	Repos []string
	// Device says this host can hand over a token without anybody pasting one.
	Device bool
	Scope  string
}

// signInPage is everything the sign-in screen needs, in one place, so the
// callers that render it cannot drift from each other.
func (s *Server) signInPage(r *http.Request, next, problem string) signInView {
	view := signInView{Next: next, Error: problem}

	held := map[string]bool{}
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if current, ok := s.auth.lookup(cookie.Value); ok {
			for key := range current.tokens {
				held[key] = true
			}
		}
	}

	if host, ok := s.localSignIn(); ok && !held[host.Key] {
		view.Local, view.LocalHost = true, host.Name
	}

	for _, h := range hosts(s.auth.repositories()) {
		if held[h.Key] {
			view.Signed = append(view.Signed, h.Name)
			continue
		}
		one := signInHostView{Key: h.Key, Name: h.Name, Repos: h.Repos}
		if device, ok := s.deviceHostFor(h.Key); ok {
			one.Device, one.Scope = true, device.DeviceScope()
		}
		view.Hosts = append(view.Hosts, one)
	}
	return view
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	token := strings.TrimSpace(r.FormValue("token"))
	next := backTo(r.FormValue("next"))

	c, _ := s.config()
	fail := func(message string) {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, r, "sign-in.html", c, "Sign in", s.signInPage(r, next, message))
	}

	hostKey, ok := s.askedHost(r)
	if !ok {
		fail("Say which host this token is for.")
		return
	}
	host, _, ok := s.auth.hostFor(hostKey)
	if !ok {
		fail("No repository here is on that host.")
		return
	}
	if token == "" {
		fail("A token is needed to ask " + host.Name() + " who you are.")
		return
	}

	// The token has to work for at least one repository on that host, or it is
	// not a sign-in — it is a token for somewhere else.
	if err := s.auth.verify(r.Context(), hostKey, token); err != nil {
		fail(err.Error())
		return
	}
	s.establish(w, r, hostKey, token, next, fail)
}

// askedHost is which host a sign-in is for. A space with one host never asks.
func (s *Server) askedHost(r *http.Request) (string, bool) {
	if named := strings.TrimSpace(r.FormValue("host")); named != "" {
		return strings.ToLower(named), true
	}
	if only, ok := s.auth.oneHost(); ok {
		return only.Key, true
	}
	return "", false
}

// establish keeps the token and points the browser at what it was after.
//
// Signing into a second host adds to the session that already exists, so
// somebody who signed into GitHub and then into GitLab is one person with two
// tokens rather than two half-sessions.
func (s *Server) establish(w http.ResponseWriter, r *http.Request,
	hostKey, token, next string, fail func(string)) {

	existing := ""
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		existing = cookie.Value
	}
	id, err := s.auth.keep(existing, hostKey, token)
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

	// Still a host to sign into, and the page asked for is in it? Then the
	// place to go is back to the sign-in page, which now offers what is left.
	if s.auth.stillMissing(id) && next != "/sign-in" {
		http.Redirect(w, r, "/sign-in?next="+urlEscape(next), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if s.auth != nil {
		if cookie, err := r.Cookie(sessionCookie); err == nil {
			s.auth.close(cookie.Value)
		}
		if cookie, err := r.Cookie(deviceCookie); err == nil {
			s.auth.stopWaiting(cookie.Value)
		}
	}
	clearCookie(w, sessionCookie)
	clearCookie(w, deviceCookie)
	http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
}

/* ---------- who has access ---------- */

// adminView is the Access page: every repository, who vouches for it, and what
// you may do there.
//
// One row per repository rather than one page per repository, because the thing
// somebody comes here to understand is exactly the thing that used to be
// invisible: that these are separate repositories on separate hosts, and access
// to one says nothing about the next.
type adminView struct {
	Repos    []repoAccess
	Hosts    []hostAccess
	Recheck  string
	You      access.Identity
	Note     string
	Unauthed bool
	Saved    string
	Problem  string
}

// repoAccess is one repository on the Access page.
type repoAccess struct {
	Name        string
	Projects    []string
	Host        string
	HostKey     string
	Repository  string
	SettingsURL string
	// Role is what you may do here, or "" when you have not signed into this
	// host or the host says this repository is not yours.
	Role string
	// SignedIn says the host knows you; Role empty with SignedIn true means the
	// host was asked and said no.
	SignedIn bool
	// ReadOnly says nobody can vouch for it at all, so it can only be read.
	ReadOnly bool
	Why      string
	// Collaborators are who else has access, when the host will say.
	Collaborators []access.Collaborator
	Trouble       string
	// Configurable says you may change how people sign into this repository.
	Configurable bool
	// ClientID and CanDevice are the sign-in setup for this repository's host.
	CanDevice bool
	ClientID  string
	FromFlag  bool
	Scope     string
}

// hostAccess is one host, for the summary at the top.
type hostAccess struct {
	Key      string
	Name     string
	SignedIn bool
	Repos    []string
}

// handleAdmin shows who has access and where it is granted. It grants nothing:
// a button here would appear to hand out something it cannot, because what
// matters is a clone, and that is the host's to give.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request) {
	c, _ := s.config()

	if s.auth == nil {
		s.render(w, r, "admin.html", c, "Access", adminView{
			Unauthed: true,
			Note: "This server is running unauthenticated. Anyone who can reach it can write, " +
				"and every change is attributed to the author it was started with. Start it " +
				"against a vault whose repositories have remotes to sign people in as " +
				"themselves.",
		})
		return
	}

	st := standingIn(r)
	view := adminView{
		Recheck: s.recheck.String(),
		You:     identityOf(r),
		Saved:   r.URL.Query().Get("saved"),
		Problem: r.URL.Query().Get("problem"),
	}

	var current *session
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		current, _ = s.auth.lookup(cookie.Value)
	}

	for _, h := range hosts(s.auth.repositories()) {
		view.Hosts = append(view.Hosts, hostAccess{
			Key: h.Key, Name: h.Name, Repos: h.Repos,
			SignedIn: current != nil && current.has(h.Key),
		})
	}

	for _, repo := range s.auth.repositories() {
		row := repoAccess{
			Name:     repo.name,
			Projects: repo.projects,
			ReadOnly: repo.readOnly(),
			Why:      repo.why,
			FromFlag: s.deviceClientID != "" && s.onlyHost(repo.hostKey),
		}
		if repo.host != nil {
			row.Host, row.HostKey = repo.host.Name(), repo.hostKey
			row.Repository, row.SettingsURL = repo.host.Repository(), repo.host.SettingsURL()
			row.SignedIn = current != nil && current.has(repo.hostKey)
			if device, ok := repo.host.(access.DeviceHost); ok {
				row.CanDevice, row.Scope = true, device.DeviceScope()
				row.ClientID = s.clientIDFor(repo.hostKey)
			}
		}

		// The role is per repository, so it is asked per repository — this is
		// the whole point of the page.
		if len(repo.projects) > 0 {
			identity := st.In(repo.projects[0])
			row.Role = identity.Role
			row.Configurable = identity.CanConfigure()
		}

		// Who else has access, when the host will say. Most hosts only answer
		// that for an administrator, so a member's page shows what it can and
		// says why it cannot show more.
		if current != nil && repo.host != nil && row.Role != "" {
			if token, held := current.tokenFor(repo.hostKey); held {
				people, err := repo.host.Collaborators(r.Context(), token)
				if err != nil {
					row.Trouble = repo.host.Name() + " only answers who has access for an " +
						"administrator's token: " + err.Error()
				}
				row.Collaborators = people
			}
		}
		view.Repos = append(view.Repos, row)
	}

	s.render(w, r, "admin.html", c, "Access", view)
}

// authorFor is who a write is attributed to.
//
// With an authority it is the signed-in person, using the name and email the
// host that vouches for the repository being written to reports — so git log
// becomes a truthful record of who moved what, in the words of the host that
// knows them. Without one it is the author the server was started with, which
// is the only honest answer available.
func (s *Server) authorFor(r *http.Request) gitvcs.Author {
	if st := standingIn(r); st != nil {
		identity := st.In(projectOf(r))
		if identity.Role == "" {
			identity = st.Best
		}
		if identity.Email != "" || identity.DisplayName() != "" {
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

// verify asks a host whether a token is good for anything here.
//
// At least one repository on that host has to accept it. A token that no
// repository accepts is not a sign-in, and saying so at once is better than
// letting somebody in to an empty board.
func (a *authority) verify(ctx context.Context, hostKey, token string) error {
	hostKey = strings.ToLower(hostKey)
	var last error
	asked := false
	for _, r := range a.repositories() {
		if r.host == nil || r.hostKey != hostKey {
			continue
		}
		asked = true
		if _, err := r.checker.Identify(ctx, token); err == nil {
			return nil
		} else {
			last = err
		}
	}
	if !asked {
		return errors.New("no repository here is on that host")
	}
	return last
}

// stillMissing reports whether a session has a host left to sign into.
func (a *authority) stillMissing(id string) bool {
	a.mu.Lock()
	current, ok := a.sessions[id]
	a.mu.Unlock()
	if !ok {
		return false
	}
	for _, h := range hosts(a.repositories()) {
		if !current.has(h.Key) {
			return true
		}
	}
	return false
}
