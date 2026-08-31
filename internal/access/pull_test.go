package access

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPullRequestNumberReadsAnAddress(t *testing.T) {
	github := &GitHub{Ref: Ref{Host: "github.com", Owner: "acme", Name: "board"}}
	gitlab := &GitLab{Ref: Ref{Host: "git.example.com", Owner: "team/sub", Name: "board"}}

	for _, c := range []struct {
		what string
		host PullRequestHost
		raw  string
		want string
	}{
		{"a github address", github, "https://github.com/acme/board/pull/17", "17"},
		{"without the scheme", github, "github.com/acme/board/pull/17", "17"},
		{"with the files tab on it", github, "https://github.com/acme/board/pull/17/files", "17"},
		{"a gitlab address, dash and all", gitlab,
			"https://git.example.com/team/sub/board/-/merge_requests/4", "4"},
		{"a branch of one line", github, "/pull/9", "9"},
	} {
		got, err := PullRequestNumber(c.host, c.raw)
		if err != nil {
			t.Errorf("%s: %v", c.what, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.what, got, c.want)
		}
	}
}

// A URL is somebody else's string. One for another repository names a branch
// that means nothing here, and resolving it would draw this project's board
// from another project's plan.
func TestPullRequestNumberRefusesSomebodyElsesRepository(t *testing.T) {
	host := &GitHub{Ref: Ref{Host: "github.com", Owner: "acme", Name: "board"}}

	for _, c := range []struct{ what, raw, says string }{
		{"another repository", "https://github.com/other/board/pull/17", "other/board"},
		{"another host", "https://gitlab.com/acme/board/pull/17", "gitlab.com"},
		{"an issue", "https://github.com/acme/board/issues/17", "pull request address"},
		{"a commit", "https://github.com/acme/board/commit/abc123", "pull request address"},
		{"no number", "https://github.com/acme/board/pull/", "pull request address"},
		{"a word where the number goes", "https://github.com/acme/board/pull/new", "pull request address"},
		{"nothing at all", "  ", "paste the address"},
	} {
		got, err := PullRequestNumber(host, c.raw)
		if err == nil {
			t.Errorf("%s: resolved to %q, and it should not have", c.what, got)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: said %q, which does not mention %q", c.what, err, c.says)
		}
	}
}

func TestPullRequestAsksTheHost(t *testing.T) {
	var asked string
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.EscapedPath()
		switch {
		case strings.Contains(r.URL.Path, "/pulls/"):
			json.NewEncoder(w).Encode(map[string]any{
				"head": map[string]string{"ref": "proposal/split-the-epic"},
			})
		case strings.Contains(r.URL.Path, "/merge_requests/"):
			json.NewEncoder(w).Encode(map[string]string{"source_branch": "предложение/сроки"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer host.Close()

	gh := &GitHub{Ref: Ref{Host: "github.com", Owner: "acme", Name: "board"}, API: host.URL}
	ref, err := gh.PullRequest(context.Background(), "t", "17")
	if err != nil {
		t.Fatalf("github: %v", err)
	}
	if ref != "proposal/split-the-epic" {
		t.Errorf("github said %q", ref)
	}
	if asked != "/repos/acme/board/pulls/17" {
		t.Errorf("asked %q", asked)
	}

	gl := &GitLab{Ref: Ref{Host: "git.example.com", Owner: "team/sub", Name: "board"}, API: host.URL}
	ref, err = gl.PullRequest(context.Background(), "t", "4")
	if err != nil {
		t.Fatalf("gitlab: %v", err)
	}
	// A Cyrillic branch name has to survive, because the vaults this is for are
	// written in it.
	if ref != "предложение/сроки" {
		t.Errorf("gitlab said %q", ref)
	}
	if asked != "/projects/team%2Fsub%2Fboard/merge_requests/4" {
		t.Errorf("asked %q", asked)
	}
}
