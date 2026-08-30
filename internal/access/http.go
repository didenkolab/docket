package access

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// fetch performs one authenticated GET and decodes it.
//
// The four answers every host gives to a bad request mean the same four things,
// so they are turned into the same four sentences here rather than three times
// over. Hosts answer 404 for a private repository a token cannot see, so that
// case and "really not there" have to share a sentence: the API will not tell
// us which it is, and guessing would be worse than saying so.
func fetch(ctx context.Context, client *http.Client, host, url string,
	authorize func(*http.Request), into any) error {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	authorize(req)

	resp, err := clientOr(client).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK:
		if into == nil {
			return nil
		}
		return json.Unmarshal(body, into)
	case http.StatusUnauthorized:
		return fmt.Errorf("%s rejected the token", host)
	case http.StatusForbidden:
		return fmt.Errorf("%s refused: the token exists but is not allowed to do this", host)
	case http.StatusNotFound:
		return fmt.Errorf("not found on %s, or this token cannot see it", host)
	default:
		return fmt.Errorf("%s: %s", host, trim(string(body)))
	}
}

func clientOr(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func trim(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func bearer(token string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }
}
