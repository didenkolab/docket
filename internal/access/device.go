package access

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Signing in without anybody pasting a token.
//
// Asking a person for a personal access token works and is ugly: it means
// leaving the page, finding the right settings screen, choosing scopes nobody
// wants to think about, and pasting a secret into a form. The device
// authorization grant (RFC 8628) is the same thing done properly — the server
// asks the host for a short code, the person types it on the host's own site,
// and the host hands the server a token.
//
// It is the right flow for this program rather than the usual redirect
// because docket is run by whoever wants it, on whatever address they like.
// A redirect flow needs a client secret and a callback URL registered in
// advance, so every person who runs a copy would have to register their own
// application and configure it. Device flow needs neither: the client id is
// public, there is no callback, and it works identically on localhost, behind
// NAT and on a server. It is what `gh` does, for the same reasons.
//
// What it does not change is the model: the token is still the host's, and
// what it may do is still the host's answer. See ADR-0004.

// Device is a code pair the host issued, waiting for somebody to use it.
type Device struct {
	// UserCode is what the person types. Short, and shown to them.
	UserCode string
	// VerificationURI is where they type it.
	VerificationURI string
	// DeviceCode is the server's half. It is a secret: whoever holds it
	// receives the token, so it never reaches the browser.
	DeviceCode string
	// Interval is how often the host permits polling.
	Interval time.Duration
	// Expires is when the code stops working.
	Expires time.Time
}

// Errors a poll can end in. Pending and SlowDown are the flow working
// normally; the other three end it.
var (
	// ErrDevicePending means the person has not finished yet.
	ErrDevicePending = errors.New("waiting for the code to be entered")
	// ErrDeviceSlowDown means the same, and asks for a longer interval.
	ErrDeviceSlowDown = errors.New("polling too often")
	// ErrDeviceExpired means the code timed out. A new one has to be issued.
	ErrDeviceExpired = errors.New("the code expired")
	// ErrDeviceDenied means the person said no.
	ErrDeviceDenied = errors.New("the request was declined")
)

// DeviceHost is a Host that can also issue a device code. Not every host can:
// Bitbucket has no device flow, and one that cannot simply keeps the token
// field, which is why this is a separate interface rather than more of Host.
type DeviceHost interface {
	Host
	// StartDevice asks the host for a code pair.
	StartDevice(ctx context.Context, clientID string) (Device, error)
	// PollDevice asks whether the person has finished, returning the token
	// when they have and one of the errors above while they have not.
	PollDevice(ctx context.Context, clientID, deviceCode string) (string, error)
	// DeviceScope is what the token will be asked to be allowed to do. The
	// sign-in page shows it, because agreeing to something on another site
	// without being told what is not consent.
	DeviceScope() string
}

/* ---------- GitHub ---------- */

// DeviceScope is the narrowest scope that can read a private repository and
// the permissions on it, which is the whole of what docket asks a host.
//
// GitHub has nothing finer for this: `public_repo` cannot see a private vault,
// and the fine-grained permissions are a different product — a GitHub App,
// which needs installing per repository rather than approving once. This is
// the same scope `gh auth login` asks for, and the sign-in page says so before
// anybody agrees.
func (g *GitHub) DeviceScope() string { return "repo" }

// OnGitHubCom reports whether this is the public GitHub rather than an
// Enterprise server, which decides whether docket's own OAuth application
// applies: one is registered per instance.
func (g *GitHub) OnGitHubCom() bool { return strings.EqualFold(g.Ref.Host, "github.com") }

// StartDevice asks GitHub for a code pair.
func (g *GitHub) StartDevice(ctx context.Context, clientID string) (Device, error) {
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		Description     string `json:"error_description"`
	}
	form := url.Values{"client_id": {clientID}, "scope": {g.DeviceScope()}}
	if err := postForm(ctx, g.HTTP, g.webBase()+"/login/device/code", form, &out); err != nil {
		return Device{}, err
	}
	if out.Error != "" {
		return Device{}, deviceProblem(out.Error, out.Description)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return Device{}, fmt.Errorf("GitHub issued no code")
	}

	return Device{
		UserCode:        out.UserCode,
		VerificationURI: orDefault(out.VerificationURI, g.webBase()+"/login/device"),
		DeviceCode:      out.DeviceCode,
		Interval:        seconds(out.Interval, 5*time.Second),
		Expires:         time.Now().Add(seconds(out.ExpiresIn, 15*time.Minute)),
	}, nil
}

// PollDevice asks GitHub whether the code has been used yet.
func (g *GitHub) PollDevice(ctx context.Context, clientID, deviceCode string) (string, error) {
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	if err := postForm(ctx, g.HTTP, g.webBase()+"/login/oauth/access_token", form, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", deviceProblem(out.Error, out.Description)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("GitHub returned no token and no reason")
	}
	return out.AccessToken, nil
}

// webBase is the site rather than the API. The device endpoints live on the
// site — github.com, not api.github.com — and on Enterprise they are on the
// host itself.
//
// It follows --api when that was given, because somebody who had to say where
// the API is has told us where the instance is, and guessing https://host
// after being told otherwise would be ignoring them.
func (g *GitHub) webBase() string {
	if g.API == "" {
		return webBase(g.Ref)
	}
	trimmed := strings.TrimRight(g.API, "/")
	// Enterprise serves the API under a path on the site itself.
	if cut, ok := strings.CutSuffix(trimmed, "/api/v3"); ok {
		return cut
	}
	// github.com serves it on a host of its own.
	if u, err := url.Parse(trimmed); err == nil && strings.HasPrefix(u.Host, "api.") {
		u.Host, u.Path = strings.TrimPrefix(u.Host, "api."), ""
		return u.String()
	}
	return trimmed
}

/* ---------- shared ---------- */

// deviceProblem turns the protocol's error codes into the four outcomes that
// matter. RFC 8628 names them and every host uses the same words.
func deviceProblem(code, description string) error {
	switch code {
	case "authorization_pending":
		return ErrDevicePending
	case "slow_down":
		return ErrDeviceSlowDown
	case "expired_token":
		return ErrDeviceExpired
	case "access_denied":
		return ErrDeviceDenied
	}
	if description != "" {
		return fmt.Errorf("%s: %s", code, description)
	}
	return errors.New(code)
}

// postForm posts a form and decodes JSON, which is what every device endpoint
// wants and answers. It is separate from fetch because that one is for
// authenticated GETs — here there is nothing to authenticate with yet.
func postForm(ctx context.Context, client *http.Client, endpoint string,
	form url.Values, into any) error {

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := clientOr(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	// The device endpoints answer 200 with an error field for the ordinary
	// "not yet" case, so the body is decoded whatever the status was and the
	// status only matters when there is nothing usable in it.
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("%s answered with %s", endpoint, trim(string(body)))
	}
	return nil
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func seconds(n int, fallback time.Duration) time.Duration {
	if n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

/* ---------- GitLab ---------- */

// GitLab has the same grant, on its own endpoints, since 17.2. It matters more
// here than on GitHub: a self-hosted GitLab is exactly the case where a
// redirect flow is worst — the callback URL would have to be registered for
// every address anybody runs a board on — and where a device code costs
// nothing.
//
// The application has to be registered on that instance and must be public
// (not confidential), which is what the device grant means. Its id goes in
// --device-client-id.

// DeviceScope for GitLab is reading the API, which is all docket does with it:
// who is this, and what is their access level on this project. GitLab's scopes
// are finer than GitHub's, so this one is genuinely narrow — it cannot write
// anything, and it cannot read repository contents.
func (g *GitLab) DeviceScope() string { return "read_api" }

// StartDevice asks GitLab for a code pair.
func (g *GitLab) StartDevice(ctx context.Context, clientID string) (Device, error) {
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		Description     string `json:"error_description"`
	}
	form := url.Values{"client_id": {clientID}, "scope": {g.DeviceScope()}}
	if err := postForm(ctx, g.HTTP, g.oauthBase()+"/oauth/authorize_device", form, &out); err != nil {
		return Device{}, err
	}
	if out.Error != "" {
		return Device{}, deviceProblem(out.Error, out.Description)
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return Device{}, fmt.Errorf("GitLab issued no code — the application may not be " +
			"registered for the device flow, which needs it to be public rather than confidential")
	}

	return Device{
		UserCode:        out.UserCode,
		VerificationURI: orDefault(out.VerificationURI, g.oauthBase()+"/oauth/device"),
		DeviceCode:      out.DeviceCode,
		Interval:        seconds(out.Interval, 5*time.Second),
		Expires:         time.Now().Add(seconds(out.ExpiresIn, 15*time.Minute)),
	}, nil
}

// PollDevice asks GitLab whether the code has been used yet.
func (g *GitLab) PollDevice(ctx context.Context, clientID, deviceCode string) (string, error) {
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	if err := postForm(ctx, g.HTTP, g.oauthBase()+"/oauth/token", form, &out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", deviceProblem(out.Error, out.Description)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("GitLab returned no token and no reason")
	}
	return out.AccessToken, nil
}

// oauthBase is the instance itself rather than its API.
//
// GitLab serves /oauth off the root, not under /api/v4 — so this cannot reuse
// base(). It follows --api when that names a self-hosted instance, because
// somebody who had to say where the API is has told us where the instance is.
func (g *GitLab) oauthBase() string {
	if g.API == "" {
		return webBase(g.Ref)
	}
	trimmed := strings.TrimRight(g.API, "/")
	return strings.TrimSuffix(trimmed, "/api/v4")
}
