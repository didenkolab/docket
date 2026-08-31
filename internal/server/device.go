package server

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/vadymdidenkolab/docket/internal/access"
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
	device   access.Device
	next     string
	interval time.Duration
	// due is when the host may be asked again. Polling faster than the host
	// permits is how a flow earns slow_down and then nothing at all.
	due time.Time
}

// deviceHost is the host if it can issue codes, and whether device sign-in is
// available at all — which needs both a host that supports it and a client id
// to identify this application to that host.
func (s *Server) deviceHost() (access.DeviceHost, bool) {
	if s.auth == nil || s.deviceClientID == "" {
		return nil, false
	}
	host, ok := s.auth.checker.Host.(access.DeviceHost)
	return host, ok
}

// handleDeviceStart asks the host for a code and sends the browser to the page
// that waits for it.
func (s *Server) handleDeviceStart(w http.ResponseWriter, r *http.Request) {
	host, ok := s.deviceHost()
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}

	next := backTo(r.FormValue("next"))
	device, err := host.StartDevice(r.Context(), s.deviceClientID)
	if err != nil {
		s.signInProblem(w, r, next, "Cannot start sign-in with "+host.Name()+": "+err.Error())
		return
	}

	id, err := s.auth.startWaiting(device, next)
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
	host, ok := s.deviceHost()
	if !ok {
		http.Redirect(w, r, "/sign-in", http.StatusSeeOther)
		return
	}

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

	// Only ask when the host permits. In between, the page still redraws, so
	// the code stays on screen and the countdown keeps moving.
	if time.Now().After(pending.due) {
		token, err := host.PollDevice(r.Context(), s.deviceClientID, pending.device.DeviceCode)
		switch {
		case err == nil:
			s.finishDevice(w, r, cookie.Value, token, pending.next)
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
func (s *Server) finishDevice(w http.ResponseWriter, r *http.Request, id, token, next string) {
	s.auth.stopWaiting(id)
	clearCookie(w, deviceCookie)

	identity, err := s.auth.checker.Identify(r.Context(), token)
	if err != nil {
		s.signInProblem(w, r, next, err.Error())
		return
	}
	session, err := s.auth.open(token, identity)
	if err != nil {
		s.signInProblem(w, r, next, err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(s.auth.life.Seconds()),
	})
	http.Redirect(w, r, backTo(next), http.StatusSeeOther)
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
	s.render(w, r, "sign-in.html", c, "Sign in", s.signInPage(next, message))
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

func (a *authority) startWaiting(device access.Device, next string) (string, error) {
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
		device: device, next: next, interval: device.Interval,
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
