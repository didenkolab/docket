package access

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Asking a host for a new repository.
//
// A new project needs somewhere to live before it can be a project at all, and
// making that somewhere is one API call. Doing it here rather than sending
// somebody to a web form is the difference between "a project is a repository"
// being a slogan and being how the thing works.
//
// One path for both hosts: create an empty repository, scaffold into it from the
// template, push. GitHub can generate a repository from a template repository in
// a single call, and that was tempting and wrong — the template's placeholder
// key would arrive verbatim, and stamping it afterwards means a local commit and
// a push anyway. An empty repository plus the scaffolding that already exists is
// one code path instead of two.
//
// What this does not do is widen anybody's access. It asks with the credential
// already in hand — the token somebody signed in with, or the one on the
// machine — and when the host says the scope is not enough, it says so in the
// host's own words. A token that cannot create a repository is a fact about the
// token, and inventing a second consent flow to get around it is not this
// button's business.

// NewRepository is what to ask for.
type NewRepository struct {
	// Name is the repository's name on the host.
	Name string
	// Owner is the user or group to put it under. Empty means whoever the token
	// belongs to.
	Owner string
	// Description is shown on the host, and is the one place a sentence about
	// the project can go that is not inside it.
	Description string
	// Private is the default, because a project is somebody's work until they
	// say otherwise.
	Private bool
}

// Created is where the new repository is.
type Created struct {
	// Remote is what to clone or push to.
	Remote string
	// Web is where a person looks at it.
	Web string
	// FullName is owner/name as the host says it.
	FullName string
}

// Creator is a host that can be asked for a new repository. Not every host can
// be, and one that cannot simply is not offered.
type Creator interface {
	Host
	// Create makes an empty repository and says where it is.
	Create(ctx context.Context, token string, want NewRepository) (Created, error)
	// CreateScope is what a token needs to be allowed to do this, for a message
	// that has to explain a refusal.
	CreateScope() string
}

/* ---------- GitHub ---------- */

// CreateScope is the scope a token needs. The same one signing in asks for, so
// somebody who signed in with the device flow can already do this.
func (g *GitHub) CreateScope() string { return "repo" }

// Create asks GitHub for an empty repository.
func (g *GitHub) Create(ctx context.Context, token string, want NewRepository) (Created, error) {
	path := "/user/repos"
	if owner := strings.TrimSpace(want.Owner); owner != "" && !strings.EqualFold(owner, "me") {
		// An organisation has its own endpoint; GitHub refuses an owner in the
		// body of the personal one rather than honouring it.
		path = "/orgs/" + escapePath(owner) + "/repos"
	}

	body := map[string]any{
		"name":        want.Name,
		"private":     want.Private,
		"description": want.Description,
		// The scaffold arrives as the first commit and would collide with
		// anything GitHub put there.
		"auto_init": false,
	}

	var out struct {
		CloneURL string `json:"clone_url"`
		HTMLURL  string `json:"html_url"`
		FullName string `json:"full_name"`
		Message  string `json:"message"`
		Errors   []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := postJSON(ctx, g.HTTP, g.base()+path, token, body, &out); err != nil {
		return Created{}, err
	}
	if out.CloneURL == "" {
		return Created{}, fmt.Errorf("GitHub did not create it: %s", firstReason(out.Message,
			flatten(out.Errors)))
	}
	return Created{Remote: out.CloneURL, Web: out.HTMLURL, FullName: out.FullName}, nil
}

/* ---------- GitLab ---------- */

// CreateScope is GitLab's widest scope, and it is why creating is separate from
// signing in: read_api and write_repository are enough to use a board and not
// enough to make a project, and asking everybody for `api` so that somebody
// might one day create one would be asking for too much too early.
func (g *GitLab) CreateScope() string { return "api" }

// Create asks GitLab for an empty project.
func (g *GitLab) Create(ctx context.Context, token string, want NewRepository) (Created, error) {
	visibility := "private"
	if !want.Private {
		visibility = "public"
	}
	body := map[string]any{
		"name":                   want.Name,
		"path":                   slug(want.Name),
		"visibility":             visibility,
		"description":            want.Description,
		"initialize_with_readme": false,
	}
	// A group is named rather than looked up: GitLab takes a full path here, and
	// resolving it to an id first would be a second call to learn something the
	// caller already typed.
	if owner := strings.TrimSpace(want.Owner); owner != "" {
		body["namespace_id"] = nil
		body["namespace"] = owner
	}

	var out struct {
		HTTPURL           string `json:"http_url_to_repo"`
		WebURL            string `json:"web_url"`
		PathWithNamespace string `json:"path_with_namespace"`
		Message           any    `json:"message"`
		Error             string `json:"error"`
	}
	if err := postJSON(ctx, g.HTTP, g.base()+"/projects", token, body, &out); err != nil {
		return Created{}, err
	}
	if out.HTTPURL == "" {
		return Created{}, fmt.Errorf("GitLab did not create it: %s",
			firstReason(fmt.Sprint(out.Message), out.Error))
	}
	return Created{Remote: out.HTTPURL, Web: out.WebURL, FullName: out.PathWithNamespace}, nil
}

/* ---------- shared ---------- */

// postJSON sends JSON with a bearer token and decodes whatever comes back,
// whatever the status.
//
// The body is decoded even on a refusal, because the useful part of a refusal is
// what the host said about it: "name already exists" and "insufficient scope"
// are different problems and a status code says neither.
func postJSON(ctx context.Context, client *http.Client, endpoint, token string,
	body any, into any) error {

	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := clientOr(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	answer, _ := io.ReadAll(resp.Body)
	if len(answer) == 0 {
		return fmt.Errorf("%s answered %s and said nothing", endpoint, resp.Status)
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("%s answered %s: %s", endpoint, resp.Status, trim(string(answer)))
	}
	return nil
}

// firstReason is the first thing the host said that is worth repeating.
func firstReason(reasons ...string) string {
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason != "" && reason != "<nil>" && reason != "null" {
			return reason
		}
	}
	return "and gave no reason"
}

func flatten(errs []struct {
	Message string `json:"message"`
}) string {
	var out []string
	for _, e := range errs {
		if e.Message != "" {
			out = append(out, e.Message)
		}
	}
	return strings.Join(out, "; ")
}

// slug is a name as a path segment: what a host would call the repository's
// directory.
func slug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == '/':
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
