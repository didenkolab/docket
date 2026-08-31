package access

import (
	"context"
	"os"
	"testing"
	"time"
)

// Driving the real thing.
//
// Every other test here runs against a stub, which proves the code does what
// the specification says. This one proves the specification is what the host
// does — and the two came apart twice on the way in: GitHub needs a checkbox
// that is not part of creating an application, and GitLab needs two, one of
// which is not mentioned in its own documentation for this grant.
//
// Off by default, because a test suite must not depend on somebody else's
// server. Run it deliberately:
//
//	DOCKET_LIVE_GITLAB=https://gitlab.example.com \
//	DOCKET_LIVE_CLIENT_ID=... go test ./internal/access -run Live -v
//
// It only starts a device flow. Nothing is authorised, nothing is polled, and
// no token is asked for — the code it prints expires on its own.
func TestLiveDeviceFlow(t *testing.T) {
	clientID := os.Getenv("DOCKET_LIVE_CLIENT_ID")
	if clientID == "" {
		t.Skip("set DOCKET_LIVE_CLIENT_ID and one of DOCKET_LIVE_GITLAB / DOCKET_LIVE_GITHUB")
	}

	var host DeviceHost
	switch {
	case os.Getenv("DOCKET_LIVE_GITLAB") != "":
		host = &GitLab{Ref: Ref{Host: hostOf(os.Getenv("DOCKET_LIVE_GITLAB")), Owner: "x", Name: "y"}}
	case os.Getenv("DOCKET_LIVE_GITHUB") != "":
		host = &GitHub{Ref: Ref{Host: "github.com", Owner: "x", Name: "y"}}
	default:
		t.Skip("set DOCKET_LIVE_GITLAB or DOCKET_LIVE_GITHUB")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	d, err := host.StartDevice(ctx, clientID)
	if err != nil {
		t.Fatalf("StartDevice against the live host: %v", err)
	}

	if d.UserCode == "" {
		t.Error("no code for somebody to type, which is the whole of the flow")
	}
	if d.VerificationURI == "" {
		t.Error("no address to type it at")
	}
	if d.DeviceCode == "" {
		t.Error("no device code, so nothing could redeem a token")
	}
	// The wait page counts down from this, so a host that says five minutes
	// must not be reported as fifteen.
	if left := time.Until(d.Expires); left <= 0 || left > 20*time.Minute {
		t.Errorf("expiry is %v, which is not a code somebody could type in time", left)
	}
	if d.Interval <= 0 {
		t.Error("no polling interval, and asking a host faster than it allows is how a client gets slowed down")
	}

	t.Logf("scope asked for: %s", host.DeviceScope())
	t.Logf("type %s at %s — expires in %v, poll every %v",
		d.UserCode, d.VerificationURI, time.Until(d.Expires).Round(time.Second), d.Interval)
}

// hostOf is the bare hostname of a base URL, for a test given one either way.
func hostOf(s string) string {
	for _, prefix := range []string{"https://", "http://"} {
		if len(s) > len(prefix) && s[:len(prefix)] == prefix {
			s = s[len(prefix):]
		}
	}
	if slash := len(s); slash > 0 {
		for i, r := range s {
			if r == '/' {
				return s[:i]
			}
		}
	}
	return s
}
