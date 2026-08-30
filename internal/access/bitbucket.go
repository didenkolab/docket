package access

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Bitbucket asks Bitbucket Cloud about a repository.
type Bitbucket struct {
	Ref  Ref
	API  string
	HTTP *http.Client
}

// Name is what to call this host in the interface.
func (b *Bitbucket) Name() string { return "Bitbucket" }

// Repository is workspace/repo.
func (b *Bitbucket) Repository() string { return b.Ref.Path() }

// SettingsURL is where access is actually granted.
func (b *Bitbucket) SettingsURL() string {
	return webBase(b.Ref) + "/" + b.Repository() + "/admin/permissions"
}

func (b *Bitbucket) base() string {
	if b.API != "" {
		return strings.TrimRight(b.API, "/")
	}
	return "https://api.bitbucket.org/2.0"
}

// authorize handles both shapes of Bitbucket credential. An app password is
// used with a username, so a token containing a colon is read as user:secret
// and sent as Basic; anything else is a Bearer token.
func (b *Bitbucket) authorize(token string) func(*http.Request) {
	if user, secret, ok := strings.Cut(token, ":"); ok {
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + secret))
		return func(r *http.Request) { r.Header.Set("Authorization", "Basic "+encoded) }
	}
	return bearer(token)
}

// Identify asks who the token belongs to and what they may do here.
func (b *Bitbucket) Identify(ctx context.Context, token string) (Identity, error) {
	var user struct {
		Username    string `json:"username"`
		Nickname    string `json:"nickname"`
		DisplayName string `json:"display_name"`
		AccountID   string `json:"account_id"`
	}
	if err := b.get(ctx, token, "/user", &user); err != nil {
		return Identity{}, err
	}

	login := user.Username
	if login == "" {
		login = user.Nickname
	}
	if login == "" {
		login = user.AccountID
	}
	if login == "" {
		return Identity{}, fmt.Errorf("Bitbucket did not say who this token belongs to")
	}

	var permissions struct {
		Values []struct {
			Permission string `json:"permission"`
		} `json:"values"`
	}
	query := "/user/permissions/repositories?q=" +
		url.QueryEscape(`repository.full_name="`+b.Repository()+`"`)
	if err := b.get(ctx, token, query, &permissions); err != nil {
		return Identity{}, fmt.Errorf("%s: %w", b.Repository(), err)
	}
	if len(permissions.Values) == 0 {
		return Identity{}, fmt.Errorf("%s has no access to %s", login, b.Repository())
	}

	// Bitbucket's own words are read / write / admin, which is the same three
	// lines every host is reduced to.
	return Identity{
		Login: login,
		Name:  user.DisplayName,
		Email: b.email(ctx, token, login),
		Role:  RoleFor(permissions.Values[0].Permission),
	}, nil
}

// email asks for the primary confirmed address, and falls back to the noreply
// form when the token may not read it — a commit still has to be attributable.
func (b *Bitbucket) email(ctx context.Context, token, login string) string {
	var emails struct {
		Values []struct {
			Email       string `json:"email"`
			IsPrimary   bool   `json:"is_primary"`
			IsConfirmed bool   `json:"is_confirmed"`
		} `json:"values"`
	}
	if err := b.get(ctx, token, "/user/emails", &emails); err == nil {
		for _, e := range emails.Values {
			if e.IsPrimary && e.IsConfirmed {
				return e.Email
			}
		}
	}
	return login + "@users.noreply.bitbucket.org"
}

// Collaborators lists who the workspace grants access to this repository.
func (b *Bitbucket) Collaborators(ctx context.Context, token string) ([]Collaborator, error) {
	var page struct {
		Values []struct {
			Permission string `json:"permission"`
			User       struct {
				Username    string `json:"username"`
				Nickname    string `json:"nickname"`
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"values"`
	}
	path := "/repositories/" + b.Ref.Owner + "/" + b.Ref.Name + "/permissions-config/users?pagelen=100"
	if err := b.get(ctx, token, path, &page); err != nil {
		return nil, err
	}

	out := make([]Collaborator, 0, len(page.Values))
	for _, v := range page.Values {
		login := v.User.Username
		if login == "" {
			login = v.User.Nickname
		}
		out = append(out, Collaborator{
			Login: login, Name: v.User.DisplayName, Role: RoleFor(v.Permission),
		})
	}
	return out, nil
}

func (b *Bitbucket) get(ctx context.Context, token, path string, into any) error {
	return fetch(ctx, b.HTTP, "Bitbucket", b.base()+path, b.authorize(token), into)
}
