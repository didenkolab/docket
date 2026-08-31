package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/project"
)

// Signing in without pasting anything.
//
// The person presses a button, the server asks GitHub for a short code, and
// they type it on GitHub's own site. Nothing secret is typed here and nothing
// secret is stored: the device code — the half that redeems the token — stays
// in this server's memory and never reaches the browser, which only ever holds
// an opaque id.
//
// The waiting page reloads itself with a meta tag rather than a script,
// because everything else on this board works without scripting and a sign-in
// screen is the last place to start requiring it.
//
// See access/device.go for the protocol and why this flow rather than a
// redirect.

// deviceCookie names the browser's half of a sign-in that has been started but
// not finished. It is an id and nothing else.
const deviceCookie = "docket_device"

// waiting is one sign-in in progress.
type waiting struct {
	device access.Device
	// hostKey is which host issued the code, so the poll goes back to the same
	// one — a workspace may be signing into two.
	hostKey  string
	next     string
	interval time.Duration
	// due is when the host may be asked again. Polling faster than the host
	// permits is how a flow earns slow_down and then nothing at all.
	due time.Time
}

// deviceHostFor is one host, if it can issue codes and has an application to do
// it as. A host that cannot keeps the token field; see access/device.go.
func (s *Server) deviceHostFor(hostKey string) (access.DeviceHost, bool) {
	if s.auth == nil {
		return nil, false
	}
	host, _, ok := s.auth.hostFor(hostKey)
	if !ok {
		return nil, false
	}
	device, ok := host.(access.DeviceHost)
	if !ok || s.clientIDFor(hostKey) == "" {
		return nil, false
	}
	return device, true
}

// clientIDFor is which OAuth application to sign into one host as.
//
// The flag or the environment wins, because somebody who said so on the
// command line meant this server rather than the vault — and it applies to one
// host only, since an application is registered on an instance. Otherwise it
// comes out of the docket.yaml of a repository on that host, read on every
// request, so setting it takes effect without a restart.
func (s *Server) clientIDFor(hostKey string) string {
	host, _, ok := s.auth.hostFor(hostKey)
	if !ok {
		return ""
	}
	if s.deviceClientID != "" && s.onlyHost(hostKey) {
		return s.deviceClientID
	}
	if id := s.vaultClientID(hostKey); id != "" {
		return id
	}
	return s.builtInFor(host)
}

// vaultClientID asks the repositories on a host what application to use, off
// the disk rather than from what was read at startup — so saving it on the
// Access page takes effect at once. Reading a small file per request is what
// this server does everywhere else.
func (s *Server) vaultClientID(hostKey string) string {
	hostKey = strings.ToLower(hostKey)
	for _, repo := range s.auth.repos {
		if repo.host == nil || repo.hostKey != hostKey {
			continue
		}
		c, err := project.Load(s.rootOf(repo))
		if err != nil {
			continue
		}
		if id := c.DeviceClientID(); id != "" {
			return id
		}
	}
	return ""
}

// onlyHost reports whether this is the single host in the space, which is when
// a flag naming one application can only have meant this one.
func (s *Server) onlyHost(hostKey string) bool {
	only, ok := s.auth.oneHost()
	return ok && only.Key == strings.ToLower(hostKey)
}

// builtInFor is docket's own application, for the hosts it is registered on.
//
// One host, because an OAuth application belongs to the instance it was
// registered on: docket's id on github.com means nothing to a self-hosted
// GitLab, and offering it there would show somebody a button that fails.
func (s *Server) builtInFor(host access.Host) string {
	if github, ok := host.(*access.GitHub); ok && github.OnGitHubCom() {
		return builtInGitHubClientID
	}
	return ""
}

// builtInGitHubClientID is docket's own OAuth application on github.com, so a
// vault hosted there needs no configuring at all. Empty is a working state:
// the sign-in page then offers the token field alone.
const builtInGitHubClientID = ""

// handleDeviceStart asks the host for a code and sends the browser to the page
// that waits for it.
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	hostKey, known := s.askedHost(r)
	if !known {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	host, ok := s.deviceHostFor(hostKey)
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}

	next := backTo(r.FormValue("next"))
	device, err := host.StartDevice(r.Context(), s.clientIDFor(hostKey))
	if err != nil {
		s.signInProblem(w, r, next, "Cannot start sign-in with "+host.Name()+": "+err.Error())
		return
	}

	id, err := s.auth.startWaiting(device, hostKey, next)
	if err != nil {
		s.signInProblem(w, r, next, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     deviceCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(time.Until(device.Expires).Seconds()),
	})
	http.Redirect(w, r, "/sign-in/device", http.StatusSeeOther)
}

type deviceView struct {
	Host            string
	Repository      string
	UserCode        string
	VerificationURI string
	Next            string
	Scope           string
	// Left is how much of the code's life is left, in words.
	Left string
}

// handleDeviceWait shows the code and asks the host, once per interval, whether
// it has been used. It is a GET that changes something — a session may begin
// here — which is unusual and is the shape the flow has: the browser is
// waiting, and the only thing it can do is ask again.
func (s *Server) handleDeviceWait(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(deviceCookie)
	if err != nil {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}
	pending, ok := s.auth.waitingFor(cookie.Value)
	if !ok {
		s.endWaiting(w, r, cookie.Value, "That sign-in expired before it finished. Here is a new code.")
		return
	}
	host, ok := s.deviceHostFor(pending.hostKey)
	if !ok {
		s.endWaiting(w, r, cookie.Value, "That host no longer signs people in this way.")
		return
	}

	// Only ask when the host permits. In between, the page still redraws, so
	// the code stays on screen and the countdown keeps moving.
	if time.Now().After(pending.due) {
		token, err := host.PollDevice(r.Context(), s.clientIDFor(pending.hostKey), pending.device.DeviceCode)
		switch {
		case err == nil:
			s.finishDevice(w, r, cookie.Value, pending.hostKey, token, pending.next)
			return
		case errors.Is(err, access.ErrDevicePending):
			s.auth.polled(cookie.Value, 0)
		case errors.Is(err, access.ErrDeviceSlowDown):
			// The host is saying the interval was too short. Take it at its
			// word and add to it rather than trying again at the same rate.
			s.auth.polled(cookie.Value, 5*time.Second)
		case errors.Is(err, access.ErrDeviceDenied):
			s.endWaiting(w, r, cookie.Value, "That sign-in was declined on "+host.Name()+".")
			return
		case errors.Is(err, access.ErrDeviceExpired):
			s.endWaiting(w, r, cookie.Value, "That code expired before it was used. Here is a new one.")
			return
		default:
			s.endWaiting(w, r, cookie.Value, host.Name()+": "+err.Error())
			return
		}
	}

	if time.Now().After(pending.device.Expires) {
		s.endWaiting(w, r, cookie.Value, "That code expired before it was used. Here is a new one.")
		return
	}

	c, _ := s.config()
	s.renderEvery(int(pending.interval.Seconds()), w, r, "device.html", c, "Sign in", deviceView{
		Host:            host.Name(),
		Repository:      host.Repository(),
		UserCode:        pending.device.UserCode,
		VerificationURI: pending.device.VerificationURI,
		Next:            pending.next,
		Scope:           host.DeviceScope(),
		Left:            leftOf(time.Until(pending.device.Expires)),
	})
}

// handleDeviceStop is the Cancel button: forget the pending sign-in and go back
// to the start. Nothing is left waiting on the host, but nothing here holds a
// code that might still be redeemed either.
func (s *Server) handleDeviceStop(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(deviceCookie); err == nil && s.auth != nil {
		s.auth.stopWaiting(cookie.Value)
	}
	clearCookie(w, deviceCookie)
	http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
}

// finishDevice turns a token the host just handed over into a session — the
// same session a pasted token would have produced, because from here on it is
// the same token.
func (s *Server) finishDevice(w http.ResponseWriter, r *http.Request, id, hostKey, token, next string) {
	s.auth.stopWaiting(id)
	clearCookie(w, deviceCookie)

	if err := s.auth.verify(r.Context(), hostKey, token); err != nil {
		s.signInProblem(w, r, next, err.Error())
		return
	}
	s.establish(w, r, hostKey, token, backTo(next), func(message string) {
		s.signInProblem(w, r, next, message)
	})
}

// endWaiting drops a pending sign-in and says why, on the page that can start
// another one.
func (s *Server) endWaiting(w http.ResponseWriter, r *http.Request, id, message string) {
	s.auth.stopWaiting(id)
	clearCookie(w, deviceCookie)
	s.signInProblem(w, r, "", message)
}

func (s *Server) signInProblem(w http.ResponseWriter, r *http.Request, next, message string) {
	c, _ := s.config()
	w.WriteHeader(http.StatusUnauthorized)
	s.render(w, r, "sign-in.html", c, "Sign in", s.signInPage(r, next, message))
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1})
}

// leftOf says how long is left in the words somebody waiting would use.
func leftOf(d time.Duration) string {
	switch minutes := int(d.Minutes()); {
	case minutes >= 2:
		return "about " + strconv.Itoa(minutes) + " minutes"
	case d > time.Minute:
		return "about a minute"
	default:
		return "less than a minute"
	}
}

/* ---------- the pending sign-ins ---------- */

func (a *authority) startWaiting(device access.Device, hostKey, next string) (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		a.pending = map[string]*waiting{}
	}
	a.sweepPending()
	a.pending[id] = &waiting{
		device: device, hostKey: strings.ToLower(hostKey), next: next, interval: device.Interval,
		// Due immediately: the first load of the waiting page asks once, so
		// somebody who was quick is not made to wait out an interval.
		due: time.Now(),
	}
	return id, nil
}

func (a *authority) waitingFor(id string) (*waiting, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.pending[id]
	return w, ok
}

// polled records that the host was just asked, and lengthens the interval when
// it said to.
func (a *authority) polled(id string, longer time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if w, ok := a.pending[id]; ok {
		w.interval += longer
		w.due = time.Now().Add(w.interval)
	}
}

func (a *authority) stopWaiting(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

// sweepPending drops codes nobody came back for. Called when one is issued,
// which is often enough: the map only grows when somebody starts signing in.
func (a *authority) sweepPending() {
	for id, w := range a.pending {
		if time.Now().After(w.device.Expires) {
			delete(a.pending, id)
		}
	}
}

/* ---------- setting it up, on the Access page ---------- */

// handleSignInSetup saves which OAuth application people sign into one
// repository's host with.
//
// It lives on the Access page because that page is about how people get in and
// where that is decided, and it is written to that repository's docket.yaml
// because it belongs to the repository — clone the vault and the button is
// already there. Nothing secret goes in: the device flow has no client secret.
//
// Per repository rather than per server, because a workspace may span two
// hosts and an OAuth application is registered on one instance. A repository
// says how its own host is asked, which is the same rule as everywhere else
// here.
//
// The id is checked against the host before it is saved, by asking for a code
// and throwing it away. A saved id that does not work would leave a button that
// fails for everybody, and the host's own refusal says more than we could —
// GitLab, for instance, says plainly when an application is confidential and
// therefore cannot do this.
func (s *Server) handleSignInSetup(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	repo, root, ok := s.repositoryAsked(r)
	if !ok {
		s.adminProblem(w, r, "Say which repository this is for.")
		return
	}
	if repo.host == nil {
		s.adminProblem(w, r, "Nobody vouches for "+repo.name+": "+repo.why+
			". There is no host to sign into.")
		return
	}
	host, canDevice := repo.host.(access.DeviceHost)
	if !canDevice {
		s.adminProblem(w, r, repo.host.Name()+" has no device flow, so there is nothing to "+
			"set: signing in there means pasting a token.")
		return
	}
	if !standingIn(r).CanConfigure(first(repo.projects)) {
		s.refuse(w, r, "Changing how people sign into "+repo.name+" needs administrator "+
			"access to it.")
		return
	}

	id := strings.TrimSpace(r.FormValue("device_client_id"))
	if id != "" {
		if _, err := host.StartDevice(r.Context(), id); err != nil {
			s.adminProblem(w, r, repo.host.Name()+" will not sign anybody in as that "+
				"application: "+err.Error()+". It has to exist on "+repo.host.Name()+
				", have the device flow enabled, and be public rather than confidential.")
			return
		}
	}

	c, err := project.Load(root)
	if err != nil {
		s.adminProblem(w, r, err.Error())
		return
	}
	c.SetDeviceClientID(id)

	s.writes.Lock()
	defer s.writes.Unlock()

	if err := c.Save(root); err != nil {
		s.adminProblem(w, r, err.Error())
		return
	}
	message := "Signing into " + repo.host.Name() + " with a code is on for " + repo.name + "."
	if id == "" {
		message = "Signing into " + repo.host.Name() + " with a code is off for " + repo.name +
			". A token still works."
	}
	changed := project.FileName
	if repo.prefix != "" {
		changed = repo.prefix + "/" + project.FileName
	}
	if err := s.commit([]string{changed}, "Access: how people sign in", s.authorFor(r)); err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Saved, but not committed", err.Error())
		return
	}
	http.Redirect(w, r, "/admin?saved="+urlEscape(message), http.StatusSeeOther)
}

// repositoryAsked is which repository a form is about, and where it is on disk.
//
// A space of one never has to say. A workspace does, and names it by a project
// key, because that is what somebody looking at the page sees.
func (s *Server) repositoryAsked(r *http.Request) (*repository, string, bool) {
	named := strings.ToUpper(strings.TrimSpace(r.FormValue("repo")))
	for _, repo := range s.auth.repos {
		if named == "" && len(s.auth.repos) == 1 {
			return repo, s.rootOf(repo), true
		}
		for _, key := range repo.projects {
			if strings.ToUpper(key) == named {
				return repo, s.rootOf(repo), true
			}
		}
	}
	return nil, "", false
}

// rootOf is where a repository is on disk.
func (s *Server) rootOf(repo *repository) string {
	for _, v := range s.space.Vaults() {
		if v.Prefix == repo.prefix {
			return v.Root
		}
	}
	return s.space.Root
}

func first(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

func (s *Server) adminProblem(w http.ResponseWriter, r *http.Request, message string) {
	http.Redirect(w, r, "/admin?problem="+urlEscape(message), http.StatusSeeOther)
}
