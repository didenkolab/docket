// Package importer brings an existing Jira and Confluence instance into a
// vault, in three phases.
//
// Extract pulls a cold snapshot and is the only phase that touches the network.
// Plan proposes how source vocabulary maps onto the vault's, as files a person
// edits. Apply writes the vault from the snapshot and those maps, in one commit
// that can be reviewed and reverted whole.
//
// The split exists because mapping is iterative and extraction is slow: getting
// the mapping wrong should cost a rerun of apply, not another pull of twenty
// thousand issues.
package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one Atlassian Cloud site.
type Client struct {
	Site  string // https://example.atlassian.net
	Email string
	Token string

	HTTP *http.Client

	// Attempts is how many times a throttled or failed request is retried.
	Attempts int

	// Backoff is how long to wait when the server does not say.
	Backoff time.Duration
}

// NewClient prepares a client with sane defaults.
func NewClient(site, email, token string) *Client {
	return &Client{
		Site:     strings.TrimRight(site, "/"),
		Email:    email,
		Token:    token,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		Attempts: 5,
		Backoff:  2 * time.Second,
	}
}

// Get fetches one JSON document.
//
// A 429 is retried, honouring Retry-After. Rate limits on an instance of any
// size are normal rather than exceptional, and an extraction that gives up on
// the first one would never finish.
func (c *Client) Get(ctx context.Context, path string, query url.Values, into any) error {
	endpoint := c.Site + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	attempts := max(c.Attempts, 1)
	var lastErr error

	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.Backoff * time.Duration(attempt)):
			}
		}

		body, again, wait, err := c.once(ctx, endpoint)
		switch {
		case err == nil:
			if into == nil {
				return nil
			}
			return json.Unmarshal(body, into)
		case again:
			lastErr = err
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		default:
			return err
		}
	}
	return fmt.Errorf("%s: gave up after %d attempts: %w", path, attempts, lastErr)
}

// once performs a single request.
//
// again says whether trying the same request could help, which is separate from
// how long to wait first: a server that answers "Retry-After: 0" means retry
// immediately, not do not retry.
func (c *Client) once(ctx context.Context, endpoint string) (body []byte, again bool, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, 0, err
	}
	req.SetBasicAuth(c.Email, c.Token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		// A connection that failed may succeed on the next try.
		return nil, true, c.Backoff, err
	}
	defer resp.Body.Close()

	raw, readErr := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		wait = c.Backoff
		if header := resp.Header.Get("Retry-After"); header != "" {
			if seconds, convErr := strconv.Atoi(header); convErr == nil {
				wait = time.Duration(seconds) * time.Second
			}
		}
		return nil, true, wait, fmt.Errorf("%s: %s", endpoint, resp.Status)

	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		// Retrying a rejected token cannot help, and hammering an endpoint
		// with bad credentials is how an account gets locked.
		return nil, false, 0, fmt.Errorf("%s: %s — check the email and API token", endpoint, resp.Status)

	case resp.StatusCode != http.StatusOK:
		return nil, false, 0, fmt.Errorf("%s: %s: %s", endpoint, resp.Status, trim(string(raw)))
	}

	if readErr != nil {
		return nil, false, 0, readErr
	}
	return raw, false, 0, nil
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
