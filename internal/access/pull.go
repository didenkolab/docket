package access

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// A pull request is a branch, said in a URL.
//
// Somebody reviewing a proposal has a link, not a branch name: it arrived in a
// message, or it is the tab they are looking at. Asking them to find the branch
// name inside the page and retype it is asking them to do a lookup the host
// will do in one call.
//
// What comes back is only ever a branch name, and it is checked against this
// repository before anything is done with it. A URL is somebody else's string:
// a pull request in another repository names a branch that is not ours, and
// resolving it would draw a board from a ref that has nothing to do with this
// project.

// PullRequestHost is a host that can say which branch a pull request is for.
type PullRequestHost interface {
	Host
	// PullRequest turns a number into the branch the request is from.
	PullRequest(ctx context.Context, token string, number string) (branch string, err error)
	// PullRequestPath is what the host calls them in a URL — "pull" on GitHub,
	// "merge_requests" on GitLab — so a URL can be recognised before an API
	// call is made.
	PullRequestPath() string
	// PullRequestName is what to call one in a sentence.
	PullRequestName() string
}

// number is the digits at the end of a pull request URL.
var number = regexp.MustCompile(`^\d+$`)

// PullRequestNumber reads a URL and says which request it names, if it names
// one in this repository at all.
//
// The repository has to match. A URL for somebody else's project resolves to a
// branch name that means nothing here, and a board drawn from it would be a
// board of another project's plan with this project's vocabulary.
func PullRequestNumber(host PullRequestHost, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("paste the address of a %s", host.PullRequestName())
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("that address cannot be read: %w", err)
	}
	if u.Hostname() != "" && !strings.EqualFold(u.Hostname(), host.HostName()) {
		return "", fmt.Errorf("that is on %s, and this repository is on %s",
			u.Hostname(), host.HostName())
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	at := -1
	for i, segment := range segments {
		if segment == host.PullRequestPath() {
			at = i
			break
		}
	}
	if at < 0 || at+1 >= len(segments) || !number.MatchString(segments[at+1]) {
		return "", fmt.Errorf("that does not look like a %s address", host.PullRequestName())
	}

	// Everything before it is the repository, and it has to be this one. GitLab
	// puts a "-" in the way, which is part of its URLs and not of the path.
	named := strings.Join(segments[:at], "/")
	named = strings.TrimSuffix(named, "/-")
	if named != "" && !strings.EqualFold(named, host.Repository()) {
		return "", fmt.Errorf("that is for %s, and this repository is %s", named, host.Repository())
	}
	return segments[at+1], nil
}

/* ---------- GitHub ---------- */

func (g *GitHub) PullRequestPath() string { return "pull" }
func (g *GitHub) PullRequestName() string { return "pull request" }

// PullRequest asks GitHub which branch a pull request is from.
func (g *GitHub) PullRequest(ctx context.Context, token, id string) (string, error) {
	var out struct {
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Message string `json:"message"`
	}
	if err := g.get(ctx, token, "/repos/"+g.Repository()+"/pulls/"+id, &out); err != nil {
		return "", err
	}
	if out.Head.Ref == "" {
		return "", fmt.Errorf("GitHub did not say which branch pull request %s is from: %s",
			id, firstReason(out.Message))
	}
	return out.Head.Ref, nil
}

/* ---------- GitLab ---------- */

func (g *GitLab) PullRequestPath() string { return "merge_requests" }
func (g *GitLab) PullRequestName() string { return "merge request" }

// PullRequest asks GitLab which branch a merge request is from.
func (g *GitLab) PullRequest(ctx context.Context, token, id string) (string, error) {
	var out struct {
		SourceBranch string `json:"source_branch"`
		Message      any    `json:"message"`
	}
	path := "/projects/" + escapePath(g.Repository()) + "/merge_requests/" + id
	if err := g.get(ctx, token, path, &out); err != nil {
		return "", err
	}
	if out.SourceBranch == "" {
		return "", fmt.Errorf("GitLab did not say which branch merge request %s is from: %s",
			id, firstReason(fmt.Sprint(out.Message)))
	}
	return out.SourceBranch, nil
}
