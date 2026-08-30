package access

import (
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// Ref is a git remote taken apart: which server, and what the repository is
// called on it.
type Ref struct {
	Host  string // git.example.com
	Owner string // may hold slashes: GitLab nests groups
	Name  string
}

// Path is owner/name, the form every host's API takes.
func (r Ref) Path() string { return r.Owner + "/" + r.Name }

// ParseRemote reads the forms git actually writes:
//
//	git@github.com:owner/repo.git
//	https://gitlab.example.com/group/subgroup/repo.git
//	ssh://git@bitbucket.org:7999/owner/repo.git
func ParseRemote(remote string) (Ref, error) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return Ref{}, fmt.Errorf("no remote")
	}

	var host, path string

	switch {
	case strings.Contains(remote, "://"):
		parsed, err := url.Parse(remote)
		if err != nil {
			return Ref{}, fmt.Errorf("remote %q: %w", remote, err)
		}
		host, path = parsed.Hostname(), parsed.Path

	case strings.Contains(remote, ":"):
		// scp-like: user@host:path
		at := strings.Index(remote, "@")
		colon := strings.Index(remote, ":")
		if colon < 0 || colon < at {
			return Ref{}, fmt.Errorf("remote %q is not a form docket understands", remote)
		}
		host, path = remote[at+1:colon], remote[colon+1:]

	default:
		return Ref{}, fmt.Errorf("remote %q is not a form docket understands", remote)
	}

	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")

	slash := strings.LastIndex(path, "/")
	if host == "" || slash < 1 {
		return Ref{}, fmt.Errorf("remote %q names no repository", remote)
	}
	return Ref{Host: host, Owner: path[:slash], Name: path[slash+1:]}, nil
}

// Kinds docket knows how to ask.
const (
	KindGitHub    = "github"
	KindGitLab    = "gitlab"
	KindBitbucket = "bitbucket"
)

// Kinds lists them, for error messages and flag help.
var Kinds = []string{KindGitHub, KindGitLab, KindBitbucket}

// kindByHost covers the hosted services. A self-hosted server can be anything,
// so it has to be named.
var kindByHost = map[string]string{
	"github.com":    KindGitHub,
	"gitlab.com":    KindGitLab,
	"bitbucket.org": KindBitbucket,
}

// New builds a host from a remote.
//
// kind may be empty for the hosted services, whose domains say what they are.
// A self-hosted GitLab or Bitbucket has to be named, because a hostname alone
// does not tell you what API is behind it — and guessing wrong would show
// someone a sign-in page that can never work.
func New(remote, kind, apiBase string) (Host, error) {
	ref, err := ParseRemote(remote)
	if err != nil {
		return nil, err
	}

	if kind == "" {
		kind = kindByHost[strings.ToLower(ref.Host)]
	}
	if kind == "" {
		return nil, fmt.Errorf("%s is not a host docket recognises on its own: say which it is "+
			"with --host (%s)", ref.Host, strings.Join(Kinds, ", "))
	}

	switch strings.ToLower(kind) {
	case KindGitHub:
		return &GitHub{Ref: ref, API: apiBase}, nil
	case KindGitLab:
		return &GitLab{Ref: ref, API: apiBase}, nil
	case KindBitbucket:
		return &Bitbucket{Ref: ref, API: apiBase}, nil
	default:
		return nil, fmt.Errorf("unknown host kind %q: want one of %s", kind, strings.Join(Kinds, ", "))
	}
}

// FromRepository works out which host a vault belongs to by reading its origin
// remote. A vault whose remote nobody hosts has no authority to ask, and the
// caller has to fall back to running unauthenticated.
func FromRepository(dir, kind, apiBase string) (Host, error) {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("no origin remote to authenticate against: %s",
			strings.TrimSpace(string(out)))
	}
	return New(strings.TrimSpace(string(out)), kind, apiBase)
}

// webBase is where a person goes to see the repository in a browser.
func webBase(ref Ref) string { return "https://" + ref.Host }
