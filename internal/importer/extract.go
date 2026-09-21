package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/didenkolab/docket/internal/snapshot"
)

// ExtractOptions selects what to pull.
type ExtractOptions struct {
	Projects []string // Jira project keys
	Spaces   []string // Confluence space keys
	Now      time.Time
}

// Logf reports progress. Extraction of a large instance takes a long time and
// silence is indistinguishable from a hang.
type Logf func(format string, args ...any)

// Extract pulls a snapshot of the selected projects and spaces.
//
// Work already recorded as done in the manifest is skipped, so a run cut short
// by a network failure resumes instead of starting over.
func Extract(ctx context.Context, c *Client, snap *snapshot.Snapshot, opts ExtractOptions, log Logf) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	manifest, err := snap.Manifest()
	if err != nil {
		manifest = &snapshot.Manifest{Counts: map[string]int{}}
	}
	if manifest.Counts == nil {
		manifest.Counts = map[string]int{}
	}
	manifest.Instance = hostOf(c.Site)
	manifest.TakenAt = opts.Now.UTC().Format(time.RFC3339)
	manifest.Projects = opts.Projects
	manifest.Spaces = opts.Spaces
	if err := snap.SaveManifest(manifest); err != nil {
		return err
	}

	if len(opts.Projects) > 0 {
		if err := extractMeta(ctx, c, snap, manifest, log); err != nil {
			return err
		}
	}
	for _, project := range opts.Projects {
		if err := extractProject(ctx, c, snap, manifest, project, log); err != nil {
			return fmt.Errorf("project %s: %w", project, err)
		}
	}
	for _, space := range opts.Spaces {
		if err := extractSpace(ctx, c, snap, manifest, space, log); err != nil {
			return fmt.Errorf("space %s: %w", space, err)
		}
	}
	return nil
}

// extractMeta pulls the vocabulary an instance defines: statuses, issue types,
// priorities and the custom field catalogue. Plan reads all of it.
func extractMeta(ctx context.Context, c *Client, snap *snapshot.Snapshot, m *snapshot.Manifest, log Logf) error {
	if m.IsDone("meta") {
		log("meta: already extracted")
		return nil
	}

	for file, path := range map[string]string{
		"statuses.json":   "/rest/api/3/status",
		"types.json":      "/rest/api/3/issuetype",
		"priorities.json": "/rest/api/3/priority",
		"fields.json":     "/rest/api/3/field",
	} {
		var into json.RawMessage
		if err := c.Get(ctx, path, nil, &into); err != nil {
			return err
		}
		if err := snap.WriteJSON(snapshot.MetaDir+"/"+file, into); err != nil {
			return err
		}
		log("meta: %s", file)
	}
	return snap.MarkDone(m, "meta")
}

// searchPage is the shape of an enhanced-search response.
type searchPage struct {
	Issues        []json.RawMessage `json:"issues"`
	NextPageToken string            `json:"nextPageToken"`
	IsLast        bool              `json:"isLast"`
}

func extractProject(ctx context.Context, c *Client, snap *snapshot.Snapshot,
	m *snapshot.Manifest, project string, log Logf) error {

	unit := "project:" + project
	if m.IsDone(unit) {
		log("%s: already extracted", project)
		return nil
	}

	issuesFile := snapshot.Path(snapshot.IssuesDir, project)
	changelogFile := snapshot.Path(snapshot.ChangelogDir, project)
	commentsFile := snapshot.Path(snapshot.CommentsDir, project)

	// A project that was interrupted is re-pulled from the start. Resuming
	// mid-project would need a stable cursor across a changing result set,
	// which enhanced search does not promise; the unit of resume is a project.
	for _, file := range []string{issuesFile, changelogFile, commentsFile} {
		if err := snap.Truncate(file); err != nil {
			return err
		}
	}

	var (
		token string
		count int
	)
	for {
		query := url.Values{
			"jql":        {fmt.Sprintf("project = %q ORDER BY created ASC", project)},
			"maxResults": {"100"},
			"fields":     {"*all"},
			"expand":     {"changelog"},
		}
		if token != "" {
			query.Set("nextPageToken", token)
		}

		var page searchPage
		if err := c.Get(ctx, "/rest/api/3/search/jql", query, &page); err != nil {
			return err
		}

		for _, raw := range page.Issues {
			var issue struct {
				Key       string `json:"key"`
				Changelog struct {
					Total      int               `json:"total"`
					MaxResults int               `json:"maxResults"`
					Histories  []json.RawMessage `json:"histories"`
				} `json:"changelog"`
			}
			if err := json.Unmarshal(raw, &issue); err != nil {
				return err
			}

			if err := snap.Append(issuesFile, raw); err != nil {
				return err
			}
			count++

			histories := issue.Changelog.Histories
			// The expanded changelog is capped. Anything longer is fetched in
			// full, because the tail of a history is exactly where the
			// interesting early decisions are.
			if issue.Changelog.Total > len(histories) {
				full, err := allChangelog(ctx, c, issue.Key)
				if err != nil {
					return err
				}
				histories = full
			}
			for _, entry := range histories {
				if err := snap.Append(changelogFile, changelogRecord{issue.Key, entry}); err != nil {
					return err
				}
			}

			comments, err := allComments(ctx, c, issue.Key)
			if err != nil {
				return err
			}
			for _, comment := range comments {
				if err := snap.Append(commentsFile, changelogRecord{issue.Key, comment}); err != nil {
					return err
				}
			}
		}

		log("%s: %d issues", project, count)
		if page.IsLast || page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}

	m.Counts[project] = count
	return snap.MarkDone(m, unit)
}

// changelogRecord ties an entry to the issue it belongs to, since the entries
// themselves do not say.
type changelogRecord struct {
	Key   string          `json:"key"`
	Entry json.RawMessage `json:"entry"`
}

func allChangelog(ctx context.Context, c *Client, key string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for startAt := 0; ; {
		var page struct {
			Values     []json.RawMessage `json:"values"`
			IsLast     bool              `json:"isLast"`
			MaxResults int               `json:"maxResults"`
			Total      int               `json:"total"`
		}
		query := url.Values{"startAt": {strconv.Itoa(startAt)}, "maxResults": {"100"}}
		if err := c.Get(ctx, "/rest/api/3/issue/"+key+"/changelog", query, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Values...)

		startAt += len(page.Values)
		if page.IsLast || len(page.Values) == 0 || startAt >= page.Total {
			return out, nil
		}
	}
}

func allComments(ctx context.Context, c *Client, key string) ([]json.RawMessage, error) {
	var out []json.RawMessage
	for startAt := 0; ; {
		var page struct {
			Comments   []json.RawMessage `json:"comments"`
			Total      int               `json:"total"`
			MaxResults int               `json:"maxResults"`
		}
		query := url.Values{"startAt": {strconv.Itoa(startAt)}, "maxResults": {"100"}}
		if err := c.Get(ctx, "/rest/api/3/issue/"+key+"/comment", query, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Comments...)

		startAt += len(page.Comments)
		if len(page.Comments) == 0 || startAt >= page.Total {
			return out, nil
		}
	}
}

func extractSpace(ctx context.Context, c *Client, snap *snapshot.Snapshot,
	m *snapshot.Manifest, space string, log Logf) error {

	unit := "space:" + space
	if m.IsDone(unit) {
		log("%s: already extracted", space)
		return nil
	}

	id, err := spaceID(ctx, c, space)
	if err != nil {
		return err
	}

	file := snapshot.Path(snapshot.PagesDir, space)
	if err := snap.Truncate(file); err != nil {
		return err
	}

	var (
		cursor string
		count  int
	)
	for {
		query := url.Values{
			"space-id":    {id},
			"body-format": {"storage"},
			"limit":       {"100"},
		}
		if cursor != "" {
			query.Set("cursor", cursor)
		}

		var page struct {
			Results []json.RawMessage `json:"results"`
			Links   struct {
				Next string `json:"next"`
			} `json:"_links"`
		}
		if err := c.Get(ctx, "/wiki/api/v2/pages", query, &page); err != nil {
			return err
		}

		for _, raw := range page.Results {
			if err := snap.Append(file, raw); err != nil {
				return err
			}
			count++
		}
		log("%s: %d pages", space, count)

		cursor = cursorOf(page.Links.Next)
		if cursor == "" {
			break
		}
	}

	m.Counts[space] = count
	return snap.MarkDone(m, unit)
}

func spaceID(ctx context.Context, c *Client, key string) (string, error) {
	var page struct {
		Results []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"results"`
	}
	if err := c.Get(ctx, "/wiki/api/v2/spaces", url.Values{"keys": {key}}, &page); err != nil {
		return "", err
	}
	for _, space := range page.Results {
		if space.Key == key {
			return space.ID, nil
		}
	}
	return "", fmt.Errorf("no space with key %q", key)
}

// cursorOf pulls the cursor out of the next-page link Confluence returns.
func cursorOf(next string) string {
	if next == "" {
		return ""
	}
	parsed, err := url.Parse(next)
	if err != nil {
		return ""
	}
	return parsed.Query().Get("cursor")
}

func hostOf(site string) string {
	parsed, err := url.Parse(site)
	if err != nil || parsed.Host == "" {
		return site
	}
	return parsed.Host
}
