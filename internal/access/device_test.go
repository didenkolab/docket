package access

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// deviceEndpoints stands in for a host's two device endpoints. It records what
// was asked and answers what the test told it to.
type deviceEndpoints struct {
	t     *testing.T
	forms map[string]url.Values
	reply map[string]string
}

func newEndpoints(t *testing.T, reply map[string]string) (*deviceEndpoints, *httptest.Server) {
	e := &deviceEndpoints{t: t, forms: map[string]url.Values{}, reply: reply}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("%s %s: a device endpoint is a POST", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Accept"); !strings.Contains(got, "application/json") {
			t.Errorf("%s: Accept = %q, so the host may answer in a form encoding", r.URL.Path, got)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		e.forms[r.URL.Path] = r.PostForm

		body, ok := e.reply[r.URL.Path]
		if !ok {
			t.Errorf("nothing asked for %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return e, server
}

func TestGitHubIssuesAndRedeemsACode(t *testing.T) {
	e, server := newEndpoints(t, map[string]string{
		"/login/device/code": `{"device_code":"secret","user_code":"WDJB-MJHT",
			"verification_uri":"https://example.com/login/device","expires_in":900,"interval":5}`,
		"/login/oauth/access_token": `{"access_token":"gho_theToken","token_type":"bearer"}`,
	})
	host := &GitHub{Ref: Ref{Host: "github.example.com", Owner: "acme", Name: "platform"},
		API: server.URL + "/api/v3"}

	device, err := host.StartDevice(context.Background(), "Ov23liEXAMPLE")
	if err != nil {
		t.Fatalf("StartDevice: %v", err)
	}
	if device.UserCode != "WDJB-MJHT" || device.DeviceCode != "secret" {
		t.Errorf("got %+v", device)
	}
	if device.Interval.Seconds() != 5 {
		t.Errorf("interval = %s, want the one the host asked for", device.Interval)
	}
	if got := e.forms["/login/device/code"].Get("scope"); got != host.DeviceScope() {
		t.Errorf("asked for scope %q, want %q", got, host.DeviceScope())
	}
	if got := e.forms["/login/device/code"].Get("client_id"); got != "Ov23liEXAMPLE" {
		t.Errorf("client_id = %q", got)
	}

	token, err := host.PollDevice(context.Background(), "Ov23liEXAMPLE", device.DeviceCode)
	if err != nil {
		t.Fatalf("PollDevice: %v", err)
	}
	if token != "gho_theToken" {
		t.Errorf("token = %q", token)
	}
	poll := e.forms["/login/oauth/access_token"]
	if got := poll.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:device_code" {
		t.Errorf("grant_type = %q", got)
	}
	if got := poll.Get("device_code"); got != "secret" {
		t.Errorf("device_code = %q", got)
	}
}

// GitLab's endpoints are its own, and they hang off the instance rather than
// off /api/v4 — which is why this is not a copy of the GitHub path with a
// different string.
func TestGitLabIssuesAndRedeemsACode(t *testing.T) {
	e, server := newEndpoints(t, map[string]string{
		"/oauth/authorize_device": `{"device_code":"secret","user_code":"ABCD-EFGH",
			"verification_uri":"https://gitlab.example.com/oauth/device","expires_in":600,"interval":5}`,
		"/oauth/token": `{"access_token":"glpat_theToken","token_type":"bearer"}`,
	})
	host := &GitLab{Ref: Ref{Host: "gitlab.example.com", Owner: "group/sub", Name: "platform"},
		API: server.URL + "/api/v4"}

	device, err := host.StartDevice(context.Background(), "gitlab-client")
	if err != nil {
		t.Fatalf("StartDevice: %v", err)
	}
	if device.UserCode != "ABCD-EFGH" {
		t.Errorf("user code = %q", device.UserCode)
	}
	// Reading the API answers who somebody is; writing the repository is what
	// lets the board push what it commits. Still narrower than GitHub's `repo`:
	// no issues, no members, no settings, no CI.
	if got := e.forms["/oauth/authorize_device"].Get("scope"); got != host.DeviceScope() {
		t.Errorf("scope = %q, want %q", got, host.DeviceScope())
	}
	for _, want := range []string{"read_api", "write_repository"} {
		if !strings.Contains(host.DeviceScope(), want) {
			t.Errorf("the scope does not ask for %s, so the board could not %s", want,
				map[string]string{"read_api": "say who you are", "write_repository": "push"}[want])
		}
	}
	// And not GitLab's widest. Checked as a whole word: "read_api" contains
	// "api", which is how the first version of this check failed.
	for _, granted := range strings.Fields(host.DeviceScope()) {
		if granted == "api" {
			t.Error("the scope is GitLab's widest, which is more than this needs")
		}
	}

	token, err := host.PollDevice(context.Background(), "gitlab-client", device.DeviceCode)
	if err != nil {
		t.Fatalf("PollDevice: %v", err)
	}
	if token != "glpat_theToken" {
		t.Errorf("token = %q", token)
	}
}

// The four outcomes of a poll drive what the sign-in page does next, so they
// have to come back as the four errors and not as prose.
func TestAPollsOutcomesAreTheFourThatMatter(t *testing.T) {
	cases := map[string]error{
		"authorization_pending": ErrDevicePending,
		"slow_down":             ErrDeviceSlowDown,
		"expired_token":         ErrDeviceExpired,
		"access_denied":         ErrDeviceDenied,
	}
	for code, want := range cases {
		_, server := newEndpoints(t, map[string]string{
			"/oauth/token": `{"error":"` + code + `","error_description":"…"}`,
		})
		host := &GitLab{Ref: Ref{Host: "gitlab.example.com", Owner: "g", Name: "p"},
			API: server.URL + "/api/v4"}

		_, err := host.PollDevice(context.Background(), "id", "secret")
		if !errors.Is(err, want) {
			t.Errorf("%s → %v, want %v", code, err, want)
		}
	}
}

// An unknown error is not one of the four and must not be mistaken for one:
// treating it as "still pending" would leave somebody watching a page forever.
func TestAnUnknownProblemEndsTheFlow(t *testing.T) {
	_, server := newEndpoints(t, map[string]string{
		"/oauth/token": `{"error":"unauthorized_client","error_description":"no such application"}`,
	})
	host := &GitLab{Ref: Ref{Host: "gitlab.example.com", Owner: "g", Name: "p"},
		API: server.URL + "/api/v4"}

	_, err := host.PollDevice(context.Background(), "id", "secret")
	switch {
	case err == nil:
		t.Fatal("an unknown problem was accepted")
	case errors.Is(err, ErrDevicePending), errors.Is(err, ErrDeviceSlowDown):
		t.Errorf("an unknown problem reads as something to keep waiting for: %v", err)
	case !strings.Contains(err.Error(), "no such application"):
		t.Errorf("the host's explanation was dropped: %v", err)
	}
}

// The device endpoints are on the instance, not on the API — the one detail
// that is easy to get wrong and impossible to notice until a real host says no.
func TestTheDeviceEndpointsAreOnTheInstance(t *testing.T) {
	github := &GitHub{Ref: Ref{Host: "github.com", Owner: "acme", Name: "platform"}}
	if got := github.webBase(); got != "https://github.com" {
		t.Errorf("github.com → %q, want the site rather than api.github.com", got)
	}
	explicit := &GitHub{Ref: Ref{Host: "github.com"}, API: "https://api.github.com"}
	if got := explicit.webBase(); got != "https://github.com" {
		t.Errorf("an explicit api.github.com → %q", got)
	}
	enterprise := &GitHub{Ref: Ref{Host: "git.acme.dev"}, API: "https://git.acme.dev/api/v3"}
	if got := enterprise.webBase(); got != "https://git.acme.dev" {
		t.Errorf("enterprise → %q", got)
	}

	gitlab := &GitLab{Ref: Ref{Host: "gitlab.acme.dev"}, API: "https://gitlab.acme.dev/api/v4"}
	if got := gitlab.oauthBase(); got != "https://gitlab.acme.dev" {
		t.Errorf("gitlab → %q", got)
	}
	plain := &GitLab{Ref: Ref{Host: "gitlab.com"}}
	if got := plain.oauthBase(); got != "https://gitlab.com" {
		t.Errorf("gitlab.com → %q", got)
	}
}

// Bitbucket has no device flow, and the sign-in page decides what to offer by
// asking whether the host implements one. That has to stay true.
func TestOnlyTheHostsThatCanDoItSaySo(t *testing.T) {
	var _ DeviceHost = &GitHub{}
	var _ DeviceHost = &GitLab{}
	if _, ok := any(&Bitbucket{}).(DeviceHost); ok {
		t.Error("Bitbucket claims a device flow it does not have")
	}
}
