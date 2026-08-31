package access

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// GitLab asks gitlab.com or a self-hosted GitLab about a project.
type GitLab struct {
	Ref  Ref
	API  string
	HTTP *http.Client
}

// Name is what to call this host in the interface.
func (g *GitLab) Name() string { return "GitLab" }

// HostName is the host a token for this repository is good for.
func (g *GitLab) HostName() string { return g.Ref.Host }

// GitUser is what git should send as the username beside a token.
//
// GitLab requires this word beside an OAuth token; a personal access token
// works with anything, and this works with both.
func (g *GitLab) GitUser() string { return "oauth2" }

// Repository is the project path, groups and all.
func (g *GitLab) Repository() string { return g.Ref.Path() }

// SettingsURL is where access is actually granted.
func (g *GitLab) SettingsURL() string {
	return webBase(g.Ref) + "/" + g.Repository() + "/-/project_members"
}

func (g *GitLab) base() string {
	if g.API != "" {
		return strings.TrimRight(g.API, "/")
	}
	return webBase(g.Ref) + "/api/v4"
}

// GitLab access levels. The numbers are the API's, and the mapping is the same
// coarse read / write / configure line every host is reduced to.
const (
	gitlabGuest      = 10
	gitlabReporter   = 20
	gitlabDeveloper  = 30
	gitlabMaintainer = 40
)

func gitlabRole(level int) string {
	switch {
	case level >= gitlabMaintainer:
		return RoleAdmin
	case level >= gitlabDeveloper:
		return RoleMember
	case level >= gitlabGuest:
		return RoleViewer
	default:
		return ""
	}
}

// Identify asks who the token belongs to and what they may do with the project.
func (g *GitLab) Identify(ctx context.Context, token string) (Identity, error) {
	var user struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
		Email    string `json:"email"`
	}
	if err := g.get(ctx, token, "/user", &user); err != nil {
		return Identity{}, err
	}
	if user.Username == "" {
		return Identity{}, fmt.Errorf("GitLab did not say who this token belongs to")
	}

	// A project's `permissions` carries both the personal membership and the
	// one inherited from its group. The higher of the two is what the person
	// actually has.
	var project struct {
		Permissions struct {
			ProjectAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"project_access"`
			GroupAccess *struct {
				AccessLevel int `json:"access_level"`
			} `json:"group_access"`
		} `json:"permissions"`
	}
	if err := g.get(ctx, token, "/projects/"+escapePath(g.Repository()), &project); err != nil {
		return Identity{}, fmt.Errorf("%s: %w", g.Repository(), err)
	}

	level := 0
	if a := project.Permissions.ProjectAccess; a != nil && a.AccessLevel > level {
		level = a.AccessLevel
	}
	if a := project.Permissions.GroupAccess; a != nil && a.AccessLevel > level {
		level = a.AccessLevel
	}

	role := gitlabRole(level)
	if role == "" {
		// The project answered, so the token can see it — a public project read
		// by someone with no membership. Reading is exactly what they may do.
		role = RoleViewer
	}

	return Identity{
		Login: user.Username,
		Name:  user.Name,
		Email: fallbackEmail(user.Email,
			fmt.Sprintf("%d-%s@users.noreply.%s", user.ID, user.Username, g.Ref.Host)),
		Role: role,
	}, nil
}

// Collaborators lists the project's members, inherited ones included.
func (g *GitLab) Collaborators(ctx context.Context, token string) ([]Collaborator, error) {
	var members []struct {
		Username    string `json:"username"`
		Name        string `json:"name"`
		AccessLevel int    `json:"access_level"`
	}
	if err := g.get(ctx, token,
		"/projects/"+escapePath(g.Repository())+"/members/all?per_page=100", &members); err != nil {
		return nil, err
	}

	out := make([]Collaborator, 0, len(members))
	for _, m := range members {
		role := gitlabRole(m.AccessLevel)
		if role == "" {
			role = RoleViewer
		}
		out = append(out, Collaborator{Login: m.Username, Name: m.Name, Role: role})
	}
	return out, nil
}

func (g *GitLab) get(ctx context.Context, token, path string, into any) error {
	return fetch(ctx, g.HTTP, "GitLab", g.base()+path, bearer(token), into)
}
