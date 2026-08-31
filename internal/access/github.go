package access

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// GitHub asks github.com, or a GitHub Enterprise server, about a repository.
type GitHub struct {
	Ref Ref
	// API is the base URL. Empty means the public one, or the Enterprise
	// convention for any other host.
	API  string
	HTTP *http.Client
}

// Name is what to call this host in the interface.
func (g *GitHub) Name() string { return "GitHub" }

// HostName is the host a token for this repository is good for.
func (g *GitHub) HostName() string { return g.Ref.Host }

// Repository is owner/name.
func (g *GitHub) Repository() string { return g.Ref.Path() }

// SettingsURL is where access is actually granted — which is the point: it is
// not here.
func (g *GitHub) SettingsURL() string {
	return webBase(g.Ref) + "/" + g.Repository() + "/settings/access"
}

func (g *GitHub) base() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	if strings.EqualFold(g.Ref.Host, "github.com") {
		return "https://api.github.com"
	}
	// GitHub Enterprise serves its API under /api/v3 on the same host.
	return webBase(g.Ref) + "/api/v3"
}

// Identify asks the two questions a session needs: who is this, and what may
// they do with this repository.
func (g *GitHub) Identify(ctx context.Context, token string) (Identity, error) {
	var user struct {
		Login string `json:"login"`
		Name  string `json:"name"`
		Email string `json:"email"`
		ID    int64  `json:"id"`
	}
	if err := g.get(ctx, token, "/user", &user); err != nil {
		return Identity{}, err
	}
	if user.Login == "" {
		return Identity{}, fmt.Errorf("GitHub did not say who this token belongs to")
	}

	var repo struct {
		Permissions struct {
			Admin    bool `json:"admin"`
			Maintain bool `json:"maintain"`
			Push     bool `json:"push"`
			Triage   bool `json:"triage"`
			Pull     bool `json:"pull"`
		} `json:"permissions"`
	}
	if err := g.get(ctx, token, "/repos/"+g.Repository(), &repo); err != nil {
		return Identity{}, fmt.Errorf("%s: %w", g.Repository(), err)
	}

	var permission string
	switch {
	case repo.Permissions.Admin:
		permission = "admin"
	case repo.Permissions.Maintain:
		permission = "maintain"
	case repo.Permissions.Push:
		permission = "write"
	case repo.Permissions.Triage, repo.Permissions.Pull:
		permission = "read"
	default:
		return Identity{}, fmt.Errorf("%s has no access to %s", user.Login, g.Repository())
	}

	return Identity{
		Login: user.Login,
		Name:  user.Name,
		// A GitHub account can hide its address. The noreply form is a real,
		// routable address and is what git itself uses in that case, so a
		// commit stays attributable either way.
		Email: fallbackEmail(user.Email, fmt.Sprintf("%d+%s@users.noreply.github.com", user.ID, user.Login)),
		Role:  RoleFor(permission),
	}, nil
}

// Collaborators lists who has access. GitHub only answers this for a token that
// may administer the repository, so a member's admin page shows what it can and
// says why it cannot show more.
func (g *GitHub) Collaborators(ctx context.Context, token string) ([]Collaborator, error) {
	var people []struct {
		Login       string `json:"login"`
		Name        string `json:"name"`
		RoleName    string `json:"role_name"`
		Permissions struct {
			Admin    bool `json:"admin"`
			Maintain bool `json:"maintain"`
			Push     bool `json:"push"`
		} `json:"permissions"`
	}
	if err := g.get(ctx, token, "/repos/"+g.Repository()+"/collaborators?per_page=100", &people); err != nil {
		return nil, err
	}

	out := make([]Collaborator, 0, len(people))
	for _, p := range people {
		permission := p.RoleName
		if permission == "" {
			switch {
			case p.Permissions.Admin:
				permission = "admin"
			case p.Permissions.Maintain:
				permission = "maintain"
			case p.Permissions.Push:
				permission = "write"
			default:
				permission = "read"
			}
		}
		out = append(out, Collaborator{Login: p.Login, Name: p.Name, Role: RoleFor(permission)})
	}
	return out, nil
}

func (g *GitHub) get(ctx context.Context, token, path string, into any) error {
	return fetch(ctx, g.HTTP, "GitHub", g.base()+path, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}, into)
}

func fallbackEmail(email, fallback string) string {
	if strings.TrimSpace(email) != "" {
		return email
	}
	return fallback
}

func escapePath(path string) string { return url.PathEscape(path) }
