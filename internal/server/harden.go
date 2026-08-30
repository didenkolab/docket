package server

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

/* ---------- headers ---------- */

// The policy the interface actually needs. Every script and stylesheet is
// served from /static, nothing is inline, and images come from the vault or are
// data: URIs, so the policy can be strict without being a lie somebody later
// has to loosen.
const policy = "default-src 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self'; " +
	"script-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"base-uri 'none'; " +
	"frame-ancestors 'none'"

// maxBody is the ceiling on a request that is not an upload. A task body is
// prose, and a megabyte of it is already a document nobody will read. An upload
// is bounded separately, by a limit that says megabytes on purpose.
const maxBody = 1 << 20

// harden sets the headers that decide what a browser will do with a page, and
// caps how much of a request the server will read.
func (s *Server) harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", policy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// frame-ancestors covers this for anything current. Kept for browsers
		// that read one header and not the other.
		h.Set("X-Frame-Options", "DENY")

		if !safe(r.Method) && !s.acceptBody(w, r) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func safe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// acceptBody decides whether to read a request at all, and reports whether the
// handler should run.
//
// A cap on its own is not enough. MaxBytesReader stops the read, but ParseForm
// throws the error away and hands the handler whatever it managed to decode —
// so an oversized comment would be committed with its end missing and nothing
// would say so. The length is checked before anything is read, the form is
// parsed here so a failure is seen, and the cap stays as the backstop for a
// body that did not declare a length.
func (s *Server) acceptBody(w http.ResponseWriter, r *http.Request) bool {
	limit := int64(maxBody)
	upload := strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/")
	if upload {
		limit = maxAttachment + maxBody
	}

	if r.ContentLength > limit {
		s.tooLarge(w, r, limit)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	if !upload && strings.HasPrefix(r.Header.Get("Content-Type"),
		"application/x-www-form-urlencoded") {
		if err := r.ParseForm(); err != nil {
			s.tooLarge(w, r, limit)
			return false
		}
	}
	return true
}

func (s *Server) tooLarge(w http.ResponseWriter, r *http.Request, limit int64) {
	why := fmt.Sprintf("That request is larger than the %d MB this accepts.", limit>>20)
	if limit < 1<<20 {
		why = fmt.Sprintf("That request is larger than the %d KB this accepts.", limit>>10)
	}
	if wantsJSON(r) {
		apiError(w, http.StatusRequestEntityTooLarge, why)
		return
	}
	c, _ := loadConfigQuietly(s.root)
	w.WriteHeader(http.StatusRequestEntityTooLarge)
	s.render(w, r, "error.html", c, "Too large", why)
}

/* ---------- cross-site requests ---------- */

const csrfCookie = "docket_csrf"

type csrfKey struct{}

// tokenOf is the value a page must echo back for its writes to be accepted.
func tokenOf(r *http.Request) string {
	token, _ := r.Context().Value(csrfKey{}).(string)
	return token
}

// crossSite reports whether a request came from somewhere other than this
// server's own pages, as far as the request itself admits.
//
// Two independent signals, because neither is universal. Sec-Fetch-Site is sent
// by every current browser and says outright where the request came from.
// Origin is older and is sent on every unsafe request. A request carrying
// neither is not a browser under somebody else's page — it is a script, a curl,
// an agent — and those have no ambient session to hijack.
func crossSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site", "same-site":
		// same-site is a different origin on the same registrable domain, which
		// is exactly the neighbour a cookie would be shared with.
		return true
	case "same-origin", "none":
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return false
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return true
	}
	return !strings.EqualFold(parsed.Host, r.Host)
}

// checkOrigin refuses a write that came from another site, and checks the token
// on one that came from this site.
//
// The token is the half that does not depend on the browser being current. It
// lives in a cookie the page cannot read and is rendered into the page the
// browser can, so another site has no way to learn it: it can make a browser
// send the cookie, but it cannot make it send the field.
func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.issueToken(w, r)
		r = r.WithContext(context.WithValue(r.Context(), csrfKey{}, token))

		if safe(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if crossSite(r) {
			s.refuseCrossSite(w, r,
				"That came from another site, and a change has to come from this one.")
			return
		}

		// No cookie at all is a client with nothing to hijack: an agent, a
		// script, curl. A browser always has one by the time it can submit a
		// form, because the page it submitted was served with it.
		cookie, err := r.Cookie(csrfCookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		if !sameToken(cookie.Value, presented(r)) {
			s.refuseCrossSite(w, r, "This form is older than the page it belongs to. "+
				"Reload and try again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// presented is where a page puts the token back: a header on a fetch, a hidden
// field on a form.
//
// An upload is parsed here rather than in the handler, with a small memory
// limit so the file itself lands in a temporary file instead of in RAM. Go
// keeps the parsed form on the request, so the handler's own parse is a no-op
// and reads the same body — this is the same parse, done early enough to see
// the token that came before the file.
func presented(r *http.Request) string {
	if header := r.Header.Get("X-CSRF-Token"); header != "" {
		return header
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			return ""
		}
	}
	return r.FormValue("csrf_token")
}

func sameToken(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// issueToken hands the browser a token if it does not have one, and returns the
// one it should be using either way.
func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	token, err := randomID()
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})
	return token
}

func (s *Server) refuseCrossSite(w http.ResponseWriter, r *http.Request, why string) {
	if wantsJSON(r) {
		apiError(w, http.StatusForbidden, why)
		return
	}
	c, _ := loadConfigQuietly(s.root)
	w.WriteHeader(http.StatusForbidden)
	s.render(w, r, "error.html", c, "Refused", why)
}

/* ---------- how often ---------- */

// A bucket refills steadily and holds a burst. Every limit here sits far above
// what a person does and far below what a loop does, which is the only line
// worth drawing: this is a tracker a team runs for itself, not a public
// endpoint under attack.
type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

func newLimiter(perMinute, burst float64, now time.Time) *limiter {
	return &limiter{
		rate:    perMinute / 60,
		burst:   burst,
		buckets: map[string]*bucket{},
		swept:   now,
	}
}

// allow takes a token for who, and reports whether there was one to take.
func (l *limiter) allow(who string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b := l.buckets[who]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[who] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets idle long enough to have refilled completely, which makes
// them indistinguishable from a client that has never been seen. Without it the
// map is a slow leak keyed by every address that ever connected.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < time.Minute {
		return
	}
	l.swept = now
	full := time.Duration(l.burst / l.rate * float64(time.Second))
	for who, b := range l.buckets {
		if now.Sub(b.last) > full {
			delete(l.buckets, who)
		}
	}
}

// meter refuses a client asking far faster than a person can.
//
// Signing in is metered separately and much harder, because every attempt costs
// a call to the git host: a loop against it burns that host's rate limit for
// everyone using this server, whether or not it ever guesses a token.
func (s *Server) meter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var l *limiter
		switch {
		case r.URL.Path == "/sign-in" && !safe(r.Method):
			l = s.signIns
		case safe(r.Method):
			l = s.reads
		default:
			l = s.changes
		}

		if l.allow(s.clientOf(r), s.now()) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Retry-After", "5")
		if wantsJSON(r) {
			apiError(w, http.StatusTooManyRequests, "too fast — wait a few seconds and try again")
			return
		}
		c, _ := loadConfigQuietly(s.root)
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, r, "error.html", c, "Too fast",
			"That was a great many requests at once. Wait a few seconds and try again.")
	})
}

// clientOf is who to hold to a limit.
//
// The peer address, unless the server was told it sits behind a proxy — in
// which case the last entry in X-Forwarded-For is the one that proxy appended,
// and so the only one in the list not written by whoever is being limited.
// Reading the header without being told to would let anybody claim to be
// somebody else, which is a rate limiter that does not limit.
func (s *Server) clientOf(r *http.Request) string {
	if s.behindProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
				return last
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
