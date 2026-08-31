// Package access answers who someone is and what they may do, by asking the
// git host that already holds the repository.
//
// docket keeps no users of its own. The repository is the trust boundary —
// anyone who can clone the vault has everything in it — so a separate account
// system would claim to protect what git already hands over, and would need
// password hashes living next to the repository it pretends to guard. See
// ADR-0004.
package access

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Roles, in increasing order of what they may do. Three, because every extra
// one is a question — can they do X? — that nobody remembers the answer to.
const (
	RoleViewer = "viewer" // read tasks and pages
	RoleMember = "member" // create, move, comment, edit
	RoleAdmin  = "admin"  // everything, plus the vault's vocabulary
)

// Identity is a signed-in person, as the host describes them.
type Identity struct {
	Login string
	Name  string
	Email string
	Role  string
}

// CanWrite reports whether the role may change tasks.
func (i Identity) CanWrite() bool { return i.Role == RoleMember || i.Role == RoleAdmin }

// CanConfigure reports whether the role may change the vault's vocabulary.
func (i Identity) CanConfigure() bool { return i.Role == RoleAdmin }

// DisplayName is what to show, falling back to the login when the host has no
// real name on file.
func (i Identity) DisplayName() string {
	if i.Name != "" {
		return i.Name
	}
	return i.Login
}

// Collaborator is one person the host says has access.
type Collaborator struct {
	Login string
	Name  string
	Role  string
}

// Host is a git host that can answer the two questions a session needs.
type Host interface {
	// Name is what to call it in the interface.
	Name() string
	// HostName is the host itself — "github.com", "git.example.com". Two
	// repositories with the same one share a sign-in, because a token belongs
	// to a host rather than to a repository.
	HostName() string
	// Repository is owner/name, for showing and for linking.
	Repository() string
	// SettingsURL is where access is actually granted.
	SettingsURL() string
	// Identify returns who the token belongs to and what they may do here.
	Identify(ctx context.Context, token string) (Identity, error)
	// Collaborators lists who has access. It may need an administrator's token.
	Collaborators(ctx context.Context, token string) ([]Collaborator, error)
}

// RoleFor maps a host's permission onto one of the three roles.
//
// The mapping is deliberately coarse. The line that matters is read / write /
// configure, and the host already draws the first two.
func RoleFor(permission string) string {
	switch strings.ToLower(permission) {
	case "admin", "maintain":
		return RoleAdmin
	case "write", "push":
		return RoleMember
	default:
		return RoleViewer
	}
}

// Checker caches what a host said, so every page load does not become an API
// call. The cost is that revocation takes effect within TTL rather than at
// once, which the interface says out loud rather than hiding.
type Checker struct {
	Host Host
	TTL  time.Duration

	mu      sync.Mutex
	answers map[string]answer
	now     func() time.Time
}

type answer struct {
	identity Identity
	asked    time.Time
}

// NewChecker prepares a checker.
func NewChecker(host Host, ttl time.Duration) *Checker {
	return &Checker{Host: host, TTL: ttl, answers: map[string]answer{}, now: time.Now}
}

// Identify returns who a token belongs to, asking the host at most once per TTL.
func (c *Checker) Identify(ctx context.Context, token string) (Identity, error) {
	c.mu.Lock()
	cached, ok := c.answers[token]
	c.mu.Unlock()

	if ok && c.now().Sub(cached.asked) < c.TTL {
		return cached.identity, nil
	}

	identity, err := c.Host.Identify(ctx, token)
	if err != nil {
		// A token that stopped working must stop working here too, even if it
		// worked a minute ago.
		c.mu.Lock()
		delete(c.answers, token)
		c.mu.Unlock()
		return Identity{}, err
	}

	c.mu.Lock()
	c.answers[token] = answer{identity, c.now()}
	c.mu.Unlock()
	return identity, nil
}

// Forget drops a cached answer, so signing out cannot leave one behind.
func (c *Checker) Forget(token string) {
	c.mu.Lock()
	delete(c.answers, token)
	c.mu.Unlock()
}
