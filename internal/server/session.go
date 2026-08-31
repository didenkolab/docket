package server

import (
	"context"
	"net/http"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/access"
	"github.com/vadymdidenkolab/docket/internal/project"
)

// What one person may do, across repositories that may be on different hosts.
//
// A session holds one token per host — never more, because a token identifies a
// person on a whole host — and the role is asked per repository, because that is
// where the host draws the line. See hosts.go.

// standing is what a session amounts to for one request: who you are, and what
// you may do in each repository.
type standing struct {
	// SignedIn is true once at least one host knows you. Nothing is readable
	// before that: the repositories are private, and a board that drew itself
	// for a stranger would be answering a question the host has not been asked.
	SignedIn bool
	// Best is the widest role held anywhere, for the parts of the interface
	// that are not about one repository — offering "New task" at all, for
	// instance, when at least one project would accept one.
	Best access.Identity
	// byProject is the role per project key.
	byProject map[string]access.Identity
	// readable and writable are project keys, in space order.
	readable []string
	writable []string
	// missing are hosts holding repositories you have not signed into yet.
	missing []signInHost
}

// In is what you may do in one project.
func (s *standing) In(projectKey string) access.Identity {
	if s == nil {
		return access.Identity{Role: access.RoleAdmin}
	}
	return s.byProject[strings.ToUpper(projectKey)]
}

// CanRead and CanWrite answer for one project.
func (s *standing) CanRead(projectKey string) bool {
	if s == nil {
		return true
	}
	return s.In(projectKey).Role != ""
}

func (s *standing) CanWrite(projectKey string) bool {
	if s == nil {
		return true
	}
	return s.In(projectKey).CanWrite()
}

func (s *standing) CanConfigure(projectKey string) bool {
	if s == nil {
		return true
	}
	return s.In(projectKey).CanConfigure()
}

// Sees reports whether any project is readable at all.
func (s *standing) Sees() bool { return s == nil || len(s.readable) > 0 }

/* ---------- working it out ---------- */

// standingOf asks every repository what this session may do there.
//
// The answers are cached per token by each repository's own checker, so a page
// load costs at most one call per repository per recheck interval, and a
// revocation on one host cannot be masked by a cached answer from another.
func (a *authority) standingOf(ctx context.Context, s *session) *standing {
	out := &standing{byProject: map[string]access.Identity{}}
	signedInto := map[string]bool{}

	for _, r := range a.repos {
		if r.host == nil {
			// Nobody vouches for it. It is readable — it is on the disk of a
			// server somebody chose to run — and never writable.
			for _, key := range r.projects {
				out.byProject[key] = access.Identity{Role: access.RoleViewer}
				out.readable = append(out.readable, key)
			}
			continue
		}

		token, held := s.tokenFor(r.hostKey)
		if !held {
			continue
		}
		identity, err := r.checker.Identify(ctx, token)
		if err != nil {
			// The host says this token cannot see this repository. That is an
			// answer, not a failure: the repository is simply not yours.
			continue
		}
		signedInto[r.hostKey] = true
		out.SignedIn = true
		if wider(identity, out.Best) {
			out.Best = identity
		}
		for _, key := range r.projects {
			out.byProject[key] = identity
			out.readable = append(out.readable, key)
			if identity.CanWrite() {
				out.writable = append(out.writable, key)
			}
		}
	}

	// A host holding repositories nobody has signed into yet is something to
	// offer rather than to hide: half a workspace is not an error, it is a
	// sign-in that has not happened.
	for _, h := range hosts(a.repos) {
		if !s.has(h.Key) {
			out.missing = append(out.missing, h)
		}
	}
	if len(signedInto) > 0 {
		out.SignedIn = true
	}
	return out
}

// wider reports whether one role can do more than another.
func wider(a, b access.Identity) bool {
	return rank(a.Role) > rank(b.Role)
}

func rank(role string) int {
	switch role {
	case access.RoleAdmin:
		return 3
	case access.RoleMember:
		return 2
	case access.RoleViewer:
		return 1
	}
	return 0
}

/* ---------- the request's standing ---------- */

type standingKey struct{}

// standingIn is what the person asking may do. Without an authority there is
// nobody to be anyone else, and nil reads as "everything is allowed" — which is
// what --auth none means.
func standingIn(r *http.Request) *standing {
	if s, ok := r.Context().Value(standingKey{}).(*standing); ok {
		return s
	}
	return nil
}

// projectOf is which project a request is about, or "" when it is about all of
// them. It is how a permission check finds the repository that decides.
//
// A task key in the path is the usual case. A form field covers creating one,
// where there is no key yet and the project is chosen. Getting this wrong in
// the safe direction means "" — which every caller treats as "then ask about
// every repository", not as "then allow it".
func projectOf(r *http.Request) string {
	// The path, segment by segment, rather than r.PathValue: this runs as
	// middleware, before the mux has matched a pattern, so PathValue is empty
	// here. Reading the path directly also means a route added later cannot
	// quietly escape the check by not being listed.
	for _, segment := range strings.Split(r.URL.Path, "/") {
		if projectKey, _, err := project.SplitKey(segment); err == nil {
			return projectKey
		}
	}
	for _, field := range []string{"project", "key"} {
		v := strings.TrimSpace(r.FormValue(field))
		if v == "" {
			continue
		}
		if projectKey, _, err := project.SplitKey(v); err == nil {
			return projectKey
		}
		if project.KeyPattern.MatchString(strings.ToUpper(v)) {
			return strings.ToUpper(v)
		}
	}
	return ""
}

// canWriteAnything and canConfigureAnything are for the parts of the interface
// that are not about one project: whether to offer a "New task" link at all.
func (s *standing) canWriteAnything() bool {
	if s == nil {
		return true
	}
	return len(s.writable) > 0
}

func (s *standing) canConfigureAnything() bool {
	if s == nil {
		return true
	}
	for _, identity := range s.byProject {
		if identity.CanConfigure() {
			return true
		}
	}
	return false
}

// hostHolding is which host answers for a project, for a message that has to
// tell somebody where to sign in.
func (s *standing) hostHolding(projectKey string) string {
	if s == nil {
		return ""
	}
	projectKey = strings.ToUpper(projectKey)
	for _, h := range s.missing {
		for _, name := range h.Repos {
			for _, key := range strings.Split(name, ", ") {
				if strings.ToUpper(strings.TrimSpace(key)) == projectKey {
					return h.Name
				}
			}
		}
	}
	return ""
}

// Readable and Writable are the projects, for a page that has to list them.
func (s *standing) Readable() []string {
	if s == nil {
		return nil
	}
	return s.readable
}

func (s *standing) Missing() []signInHost {
	if s == nil {
		return nil
	}
	return s.missing
}
