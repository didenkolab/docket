package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// browser drives a handler the way one does: it keeps the cookies it is given
// and sends them back, and it labels its requests the way a browser labels
// them. Everything the cross-site defence looks at is here.
type browser struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]string
	token   string
}

func newBrowser(t *testing.T, h http.Handler) *browser {
	t.Helper()
	b := &browser{t: t, h: h, cookies: map[string]string{}}
	b.visit("/")
	return b
}

func (b *browser) send(r *http.Request) *httptest.ResponseRecorder {
	b.t.Helper()
	for name, value := range b.cookies {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	w := httptest.NewRecorder()
	b.h.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = c.Value
	}
	return w
}

// visit loads a page and remembers the token it carries, which is what a form
// on that page would send back.
func (b *browser) visit(path string) *httptest.ResponseRecorder {
	b.t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Sec-Fetch-Site", "none")
	w := b.send(r)
	b.token = tokenIn(w.Body.String())
	return w
}

// submit posts a form from the page the browser last loaded.
func (b *browser) submit(path string, form url.Values) *httptest.ResponseRecorder {
	b.t.Helper()
	if _, given := form["csrf_token"]; !given {
		form.Set("csrf_token", b.token)
	}
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Origin", "http://"+r.Host)
	return b.send(r)
}

// tokenIn reads the token out of a rendered page, the way a script does.
func tokenIn(body string) string {
	const marker = `<meta name="csrf-token" content="`
	at := strings.Index(body, marker)
	if at < 0 {
		return ""
	}
	rest := body[at+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

/* ---------- headers ---------- */

func TestEveryPageCarriesItsPolicy(t *testing.T) {
	_, h, _ := newServer(t)
	head := get(t, h, "/").Result().Header

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
		"X-Frame-Options":        "DENY",
	} {
		if got := head.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := head.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("the policy does not say %q: %s", want, csp)
		}
	}
}

/* ---------- cross-site ---------- */

func TestAFormFromThisSiteIsAccepted(t *testing.T) {
	_, h, root := newServer(t)
	b := newBrowser(t, h)

	if b.token == "" {
		t.Fatal("the page carries no token for its forms to send back")
	}
	b.visit("/task/ACME-1")
	w := b.submit("/task/ACME-1/comment", url.Values{"text": {"From the page."}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303: %s", w.Code, w.Body)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "commented") &&
		!strings.Contains(got, "ACME-1") {
		t.Errorf("commit = %q", got)
	}
}

func TestAFormWithoutTheTokenIsRefused(t *testing.T) {
	_, h, root := newServer(t)
	b := newBrowser(t, h)
	before := lastCommit(t, root)

	w := b.submit("/task/ACME-1/comment", url.Values{
		"text":       {"No token."},
		"csrf_token": {""},
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403: %s", w.Code, w.Body)
	}
	if lastCommit(t, root) != before {
		t.Error("a refused write produced a commit")
	}
}

func TestAFormWithSomebodyElsesTokenIsRefused(t *testing.T) {
	_, h, _ := newServer(t)
	b := newBrowser(t, h)

	w := b.submit("/task/ACME-1/comment", url.Values{
		"text":       {"Guessed."},
		"csrf_token": {"nBFq0Lxk8bXbP4nQyzr3qKuJx4mS2sYm"},
	})
	if w.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", w.Code)
	}
}

// The token alone is not the whole defence. A request that says outright that
// it came from elsewhere is refused before the token is even looked at.
func TestAPostFromAnotherSiteIsRefused(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"Sec-Fetch-Site says so", map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"a sibling on the same domain", map[string]string{"Sec-Fetch-Site": "same-site"}},
		{"Origin says so", map[string]string{"Origin": "https://elsewhere.example"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, h, _ := newServer(t)
			b := newBrowser(t, h)

			form := url.Values{"text": {"Forged."}, "csrf_token": {b.token}}
			r := httptest.NewRequest("POST", "/task/ACME-1/comment", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			if w := b.send(r); w.Code != http.StatusForbidden {
				t.Errorf("code = %d, want 403: %s", w.Code, w.Body)
			}
		})
	}
}

// An agent or a script has no cookie and so no session to hijack. Demanding a
// token from it would only mean demanding a GET before every write.
func TestAClientWithNoCookiesIsNotAskedForAToken(t *testing.T) {
	s, h, _ := newServer(t)

	r := httptest.NewRequest("POST", "/task/ACME-1/status",
		strings.NewReader(url.Values{
			"status":  {"In progress"},
			"version": {currentVersion(t, s, "ACME-1")},
		}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303: %s", w.Code, w.Body)
	}
}

// The board drags through the API, and its fetch sets the header rather than a
// form field.
func TestTheApiTakesTheTokenInAHeader(t *testing.T) {
	_, h, _ := newServer(t)
	b := newBrowser(t, h)

	patch := func(token string) int {
		r := httptest.NewRequest("PATCH", "/api/tasks/ACME-1",
			strings.NewReader(`{"status":"In progress"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if token != "" {
			r.Header.Set("X-CSRF-Token", token)
		}
		return b.send(r).Code
	}

	if code := patch(""); code != http.StatusForbidden {
		t.Errorf("without the header: %d, want 403", code)
	}
	if code := patch(b.token); code != http.StatusOK {
		t.Errorf("with the header: %d, want 200", code)
	}
}

// An upload's token arrives before the file, and reading it must not eat the
// file on the way past.
func TestAnUploadCarriesItsTokenAndStillArrives(t *testing.T) {
	_, h, root := newServer(t)
	b := newBrowser(t, h)

	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	_ = form.WriteField("csrf_token", b.token)
	part, err := form.CreateFormFile("file", "screenshot.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("\x89PNG\r\n\x1a\nnot really a png")); err != nil {
		t.Fatal(err)
	}
	form.Close()

	r := httptest.NewRequest("POST", "/task/ACME-1/attach", body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Sec-Fetch-Site", "same-origin")

	if w := b.send(r); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303: %s", w.Code, w.Body)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "attached") {
		t.Errorf("commit = %q", got)
	}
}

/* ---------- what an attachment may do ---------- */

func TestAnAttachmentCannotRunAsAPage(t *testing.T) {
	_, h, root := newServer(t)
	writeAttachment(t, root, "ACME-1 diagram.svg", `<svg xmlns="http://www.w3.org/2000/svg"/>`)
	writeAttachment(t, root, "ACME-1 notes.html", `<script>alert(1)</script>`)

	svg := get(t, h, "/file/attachments/ACME-1%20diagram.svg").Result().Header
	if !strings.Contains(svg.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("an SVG is served without a sandbox: %q", svg.Get("Content-Security-Policy"))
	}
	// It is still embeddable, which is what the interface does with an image.
	if got := svg.Get("Content-Disposition"); got != "" {
		t.Errorf("an image is served as a download: %q", got)
	}

	page := get(t, h, "/file/attachments/ACME-1%20notes.html").Result().Header
	if !strings.Contains(page.Get("Content-Disposition"), "attachment") {
		t.Errorf("HTML is served to be rendered: %q", page.Get("Content-Disposition"))
	}
	if page.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("an attachment is served with sniffing left on")
	}
}

/* ---------- where sign-in sends you ---------- */

func TestSignInOnlyReturnsToThisServer(t *testing.T) {
	cases := map[string]string{
		"/task/ACME-1":         "/task/ACME-1",
		"/search?q=redirect":   "/search?q=redirect",
		"//elsewhere.example":  "/",
		"/\\elsewhere.example": "/",
		"https://elsewhere":    "/",
		"":                     "/",
		"task/ACME-1":          "/",
	}
	for next, want := range cases {
		if got := backTo(next); got != want {
			t.Errorf("backTo(%q) = %q, want %q", next, got, want)
		}
	}
}

/* ---------- how often ---------- */

func TestABurstIsAllowedAndAFloodIsNot(t *testing.T) {
	at := noon
	l := newLimiter(60, 5, at) // one a second, five in hand

	for i := range 5 {
		if !l.allow("here", at) {
			t.Fatalf("the burst was refused at %d", i)
		}
	}
	if l.allow("here", at) {
		t.Error("the sixth was allowed with nothing in the bucket")
	}
	// Somebody else has their own bucket.
	if !l.allow("elsewhere", at) {
		t.Error("one client's flood refused another client's first request")
	}
	// And it refills.
	if !l.allow("here", at.Add(2*time.Second)) {
		t.Error("the bucket did not refill")
	}
}

func TestSigningInIsMeteredHard(t *testing.T) {
	s, h, _ := newServer(t)
	s.signIns = newLimiter(10, 2, noon)
	s.now = func() time.Time { return noon }

	attempt := func() int {
		r := httptest.NewRequest("POST", "/sign-in", strings.NewReader("token=guess"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	attempt()
	attempt()
	if code := attempt(); code != http.StatusTooManyRequests {
		t.Errorf("the third attempt in a moment: %d, want 429", code)
	}
	// Reading is not held to the sign-in limit.
	if w := get(t, h, "/"); w.Code != http.StatusOK {
		t.Errorf("a read was refused: %d", w.Code)
	}
}

func TestABodyTooLargeIsRefused(t *testing.T) {
	_, h, _ := newServer(t)

	form := url.Values{"text": {strings.Repeat("x", maxBody+1)}}
	r := httptest.NewRequest("POST", "/task/ACME-1/comment", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusSeeOther {
		t.Errorf("a body over the limit was accepted: %d", w.Code)
	}
}

func writeAttachment(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
