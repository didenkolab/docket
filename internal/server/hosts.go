package server

import (
	"errors"
	"strings"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/space"
)

// A token belongs to a host; a role belongs to a repository.
//
// This is ADR-0004 taken literally, and it took a workspace spanning two hosts
// to notice it had not been. The server used to build one authority from one
// host — the one belonging to whichever repository happened to be first — and
// check permissions by URL path. In a workspace of a GitHub repository and a
// private GitLab one that means somebody with write access to the first may
// write to the second, where they may have no access at all, and somebody with
// access only to the second cannot get in.
//
// So: signing in happens once per host, because one token identifies a person
// everywhere on that host. What they may do is asked per repository, because
// that is where the host draws the line — the same person is an administrator
// of one repository and a stranger to the next.
//
// A repository says which host vouches for it, in its own docket.yaml. Nothing
// keeps a central list, for the same reason nothing keeps a central vocabulary:
// a project has to be a repository you can hand over whole.

// repository is one repository in the space, and the host that answers for it.
type repository struct {
	// prefix is the path from the space root, "" for a vault opened directly.
	prefix string
	// projects are the project keys it holds, which is how a task finds it.
	projects []string
	// name is what to call it on a page that lists several.
	name string

	// host is nil when nobody can vouch for this repository — no remote, or a
	// self-hosted one that has not said what kind it is. Then it is readable
	// and never writable: see readOnly.
	host    access.Host
	hostKey string
	checker *access.Checker

	// clientID is the OAuth application to sign into this host with, when the
	// repository has named one.
	clientID string
	// why is the reason there is no host, for a page that has to explain it.
	why string
}

// readOnly reports whether this repository can only be read.
//
// Nobody can say who you are for it, so nothing may be written to it. The
// alternative — treating "cannot ask" as "anyone may" — is how a workspace
// quietly ends up with one repository that is not guarded at all.
func (r *repository) readOnly() bool { return r.host == nil }

// signIn is what a host offers, for the page that lists them.
type signInHost struct {
	Key      string
	Name     string
	ClientID string
	Repos    []string
}

// hosts groups the repositories by the host that vouches for them, in the order
// the space lists them, because a person signs in per host rather than per
// repository.
func hosts(repos []*repository) []signInHost {
	var out []signInHost
	at := map[string]int{}
	for _, r := range repos {
		if r.host == nil {
			continue
		}
		i, seen := at[r.hostKey]
		if !seen {
			at[r.hostKey] = len(out)
			out = append(out, signInHost{Key: r.hostKey, Name: r.host.Name(), ClientID: r.clientID})
			i = len(out) - 1
		}
		// One host may hold several repositories, and any client id among them
		// signs into it. The first that named one wins, so a workspace only has
		// to configure it once.
		if out[i].ClientID == "" {
			out[i].ClientID = r.clientID
		}
		out[i].Repos = append(out[i].Repos, r.name)
	}
	return out
}

// newRepositories works out who vouches for each repository in the space.
//
// override is the host named on the command line. It applies only to a space of
// one repository: in a workspace the repositories are on whatever hosts they
// are on, and one flag cannot be right about all of them.
func newRepositories(sp *space.Space, override access.Host, recheck time.Duration) ([]*repository, error) {
	vaults := sp.Vaults()
	out := make([]*repository, 0, len(vaults))

	for _, v := range vaults {
		c, err := project.Load(v.Root)
		if err != nil {
			return nil, err
		}
		r := &repository{
			prefix:   v.Prefix,
			projects: c.ProjectKeys(),
			name:     repositoryName(v, c),
			clientID: c.DeviceClientID(),
		}

		switch {
		case override != nil && len(vaults) == 1:
			r.host = override
		default:
			host, err := hostOf(v, c)
			if err != nil {
				r.why = err.Error()
			} else {
				r.host = host
			}
		}
		if r.host != nil {
			r.hostKey = strings.ToLower(r.host.HostName())
			r.checker = access.NewChecker(r.host, recheck)
		}
		out = append(out, r)
	}
	return out, nil
}

// hostOf asks the repository who vouches for it: its own remote, and its own
// docket.yaml for anything the hostname cannot say.
func hostOf(v *space.Vault, c *project.Config) (access.Host, error) {
	if v.Repo == nil {
		return nil, errors.New("not a git repository, so there is no remote to ask about")
	}
	return access.FromRepository(v.Root, c.HostKind(), c.HostAPI())
}

// repositoryName is what to call one repository on a page showing several: the
// projects it holds, which is what somebody looking at a board recognises.
func repositoryName(v *space.Vault, c *project.Config) string {
	if keys := c.ProjectKeys(); len(keys) > 0 {
		return strings.Join(keys, ", ")
	}
	if v.Prefix != "" {
		return v.Prefix
	}
	return c.Name
}

// anyHost reports whether anybody vouches for anything here, which is what
// decides whether the server signs people in at all.
func anyHost(repos []*repository) bool {
	for _, r := range repos {
		if r.host != nil {
			return true
		}
	}
	return false
}
