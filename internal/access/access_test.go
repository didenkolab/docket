package access

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseRemote(t *testing.T) {
	cases := map[string]Ref{
		"git@github.com:acme/platform.git":                  {"github.com", "acme", "platform"},
		"https://github.com/acme/platform.git":              {"github.com", "acme", "platform"},
		"https://github.com/acme/platform":                  {"github.com", "acme", "platform"},
		"ssh://git@bitbucket.org:7999/acme/platform.git":    {"bitbucket.org", "acme", "platform"},
		"https://gitlab.example.com/group/sub/platform.git": {"gitlab.example.com", "group/sub", "platform"},
		"git@gitlab.com:group/sub/platform.git":             {"gitlab.com", "group/sub", "platform"},
	}
	for remote, want := range cases {
		got, err := ParseRemote(remote)
		if err != nil {
			t.Errorf("%s: %v", remote, err)
			continue
		}
		if got != want {
			t.Errorf("%s → %+v, want %+v", remote, got, want)
		}
	}
}

func TestParseRemoteRejectsWhatItCannotUse(t *testing.T) {
	for _, remote := range []string{"", "   ", "github.com", "/local/path", "git@github.com:repo"} {
		if _, err := ParseRemote(remote); err == nil {
			t.Errorf("ParseRemote(%q) was accepted", remote)
		}
	}
}

func TestHostedServicesAreRecognisedByTheirDomain(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/p.git":    "GitHub",
		"git@gitlab.com:acme/p.git":    "GitLab",
		"git@bitbucket.org:acme/p.git": "Bitbucket",
	}
	for remote, want := range cases {
		host, err := New(remote, "", "")
		if err != nil {
			t.Errorf("%s: %v", remote, err)
			continue
		}
		if host.Name() != want {
			t.Errorf("%s → %s, want %s", remote, host.Name(), want)
		}
	}
}

func TestASelfHostedServerHasToBeNamed(t *testing.T) {
	// A hostname alone does not say what API is behind it, and guessing wrong
	// would show someone a sign-in page that can never work.
	remote := "git@git.example.com:acme/platform.git"

	if _, err := New(remote, "", ""); err == nil {
		t.Error("an unknown host was accepted without being named")
	} else if !strings.Contains(err.Error(), "--host") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}

	host, err := New(remote, KindGitLab, "https://git.example.com/api/v4")
	if err != nil {
		t.Fatalf("naming the host did not help: %v", err)
	}
	if host.Name() != "GitLab" {
		t.Errorf("host = %s", host.Name())
	}
}

func TestRoleFor(t *testing.T) {
	cases := map[string]string{
		"admin": RoleAdmin, "maintain": RoleAdmin,
		"write": RoleMember, "push": RoleMember,
		"read": RoleViewer, "triage": RoleViewer, "": RoleViewer, "nonsense": RoleViewer,
	}
	for permission, want := range cases {
		if got := RoleFor(permission); got != want {
			t.Errorf("RoleFor(%q) = %q, want %q", permission, got, want)
		}
	}
}

func TestWhatEachRoleMay(t *testing.T) {
	cases := []struct {
		role             string
		write, configure bool
	}{
		{RoleViewer, false, false},
		{RoleMember, true, false},
		{RoleAdmin, true, true},
	}
	for _, c := range cases {
		identity := Identity{Role: c.role}
		if identity.CanWrite() != c.write {
			t.Errorf("%s CanWrite = %v", c.role, identity.CanWrite())
		}
		if identity.CanConfigure() != c.configure {
			t.Errorf("%s CanConfigure = %v", c.role, identity.CanConfigure())
		}
	}
}

/* ---------- the hosts, against stubs ---------- */

func stub(t *testing.T, routes map[string]any) *httptest.Server {
	t.Helper()

	// Longest prefix wins, and the order is fixed. Iterating the map would let
	// /user answer a request for /user/permissions whenever Go felt like it,
	// which is a test that passes locally and fails in CI.
	prefixes := make([]string, 0, len(routes))
	for prefix := range routes {
		prefixes = append(prefixes, prefix)
	}
	sort.Slice(prefixes, func(i, j int) bool { return len(prefixes[i]) > len(prefixes[j]) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(r.URL.RequestURI(), prefix) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(routes[prefix])
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestGitHubIdentify(t *testing.T) {
	api := stub(t, map[string]any{
		"/user": map[string]any{"login": "dana", "name": "Dana Example", "id": 42},
		"/repos/acme/platform": map[string]any{
			"permissions": map[string]bool{"push": true, "pull": true},
		},
	})
	host := &GitHub{Ref: Ref{"github.com", "acme", "platform"}, API: api.URL}

	identity, err := host.Identify(context.Background(), "t")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if identity.Login != "dana" || identity.Role != RoleMember {
		t.Errorf("got %+v, want dana as a member", identity)
	}
	if identity.Email != "42+dana@users.noreply.github.com" {
		t.Errorf("a hidden address did not fall back to the routable form: %q", identity.Email)
	}
}

func TestGitHubRefusesSomeoneWithNoAccess(t *testing.T) {
	api := stub(t, map[string]any{
		"/user":                map[string]any{"login": "stranger", "id": 1},
		"/repos/acme/platform": map[string]any{"permissions": map[string]bool{}},
	})
	host := &GitHub{Ref: Ref{"github.com", "acme", "platform"}, API: api.URL}

	if _, err := host.Identify(context.Background(), "t"); err == nil {
		t.Error("someone with no permission at all was let in")
	}
}

func TestGitHubEnterpriseUsesItsOwnAPIPath(t *testing.T) {
	host := &GitHub{Ref: Ref{"github.example.com", "acme", "platform"}}
	if got := host.base(); got != "https://github.example.com/api/v3" {
		t.Errorf("base = %q", got)
	}
}

func TestGitLabTakesTheHigherOfPersonalAndGroupAccess(t *testing.T) {
	// Someone can be a reporter on the project and a maintainer of its group.
	// The one that decides what they may do is the higher.
	api := stub(t, map[string]any{
		"/user": map[string]any{"id": 7, "username": "dana", "name": "Dana", "email": "dana@example.com"},
		"/projects/": map[string]any{
			"permissions": map[string]any{
				"project_access": map[string]int{"access_level": gitlabReporter},
				"group_access":   map[string]int{"access_level": gitlabMaintainer},
			},
		},
	})
	host := &GitLab{Ref: Ref{"gitlab.com", "group", "platform"}, API: api.URL}

	identity, err := host.Identify(context.Background(), "t")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if identity.Role != RoleAdmin {
		t.Errorf("role = %q, want admin from the group membership", identity.Role)
	}
	if identity.Email != "dana@example.com" {
		t.Errorf("email = %q", identity.Email)
	}
}

func TestGitLabAccessLevels(t *testing.T) {
	cases := map[int]string{
		50: RoleAdmin, 40: RoleAdmin, 30: RoleMember, 20: RoleViewer, 10: RoleViewer, 0: "",
	}
	for level, want := range cases {
		if got := gitlabRole(level); got != want {
			t.Errorf("gitlabRole(%d) = %q, want %q", level, got, want)
		}
	}
}

func TestBitbucketIdentify(t *testing.T) {
	api := stub(t, map[string]any{
		"/user/permissions/repositories": map[string]any{
			"values": []any{map[string]string{"permission": "admin"}},
		},
		"/user/emails": map[string]any{
			"values": []any{map[string]any{"email": "d@example.com", "is_primary": true, "is_confirmed": true}},
		},
		"/user": map[string]any{"username": "dana", "display_name": "Dana Example"},
	})
	host := &Bitbucket{Ref: Ref{"bitbucket.org", "acme", "platform"}, API: api.URL}

	identity, err := host.Identify(context.Background(), "t")
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if identity.Role != RoleAdmin || identity.Login != "dana" {
		t.Errorf("got %+v", identity)
	}
	if identity.Email != "d@example.com" {
		t.Errorf("email = %q", identity.Email)
	}
}

func TestBitbucketAppPasswordsGoAsBasic(t *testing.T) {
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	host := &Bitbucket{Ref: Ref{"bitbucket.org", "acme", "platform"}, API: server.URL}
	_, _ = host.Identify(context.Background(), "dana:app-password")

	if !strings.HasPrefix(seen, "Basic ") {
		t.Errorf("Authorization = %q, want Basic for a user:secret credential", seen)
	}
}

func TestARejectedTokenSaysSo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	host := &GitHub{Ref: Ref{"github.com", "acme", "platform"}, API: server.URL}
	_, err := host.Identify(context.Background(), "wrong")
	if err == nil || !strings.Contains(err.Error(), "rejected the token") {
		t.Errorf("err = %v", err)
	}
}

/* ---------- the cache ---------- */

type countingHost struct {
	asks int
	fail bool
}

func (c *countingHost) Name() string        { return "Stub" }
func (c *countingHost) Repository() string  { return "acme/platform" }
func (c *countingHost) SettingsURL() string { return "" }
func (c *countingHost) Identify(context.Context, string) (Identity, error) {
	c.asks++
	if c.fail {
		return Identity{}, fmt.Errorf("no")
	}
	return Identity{Login: "dana", Role: RoleMember}, nil
}
func (c *countingHost) Collaborators(context.Context, string) ([]Collaborator, error) {
	return nil, nil
}

func TestTheCheckerAsksOncePerTTL(t *testing.T) {
	host := &countingHost{}
	checker := NewChecker(host, time.Minute)
	now := time.Now()
	checker.now = func() time.Time { return now }

	for range 3 {
		if _, err := checker.Identify(context.Background(), "t"); err != nil {
			t.Fatal(err)
		}
	}
	if host.asks != 1 {
		t.Errorf("asked %d times inside the TTL, want 1", host.asks)
	}

	now = now.Add(2 * time.Minute)
	if _, err := checker.Identify(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	if host.asks != 2 {
		t.Errorf("asked %d times, want it to re-ask after the TTL", host.asks)
	}
}

func TestATokenThatStopsWorkingStopsWorkingHere(t *testing.T) {
	// Access revoked on the host has to bite, even though the answer was
	// cached a moment ago.
	host := &countingHost{}
	checker := NewChecker(host, time.Hour)
	now := time.Now()
	checker.now = func() time.Time { return now }

	if _, err := checker.Identify(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	host.fail = true
	checker.Forget("t")

	if _, err := checker.Identify(context.Background(), "t"); err == nil {
		t.Error("a revoked token was still accepted")
	}
	if _, err := checker.Identify(context.Background(), "t"); err == nil {
		t.Error("the failure was cached as a success")
	}
}
