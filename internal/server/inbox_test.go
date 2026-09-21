package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
)

// A vault that accepts something from outside.
func listeningServer(t *testing.T, allowed bool, secretEnv string) (http.Handler, string) {
	t.Helper()
	_, _, root := newServer(t)

	at := filepath.Join(root, "hooks", "take.sh")
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, []byte("#!/bin/sh\ncat > \"$DOCKET_ROOT/docs/posted.txt\"\n"+
		"echo taken\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c.Inbox = []project.Inbound{{Name: "results", Run: "hooks/take.sh", SecretEnv: secretEnv}}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "an inbox")

	s, err := New(root, Options{
		Author:   gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Programs: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return noon.Add(time.Hour) }
	return s.Handler(), root
}

func post(t *testing.T, h http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	// The Content-Type curl sends by default, which is what a machine posting
	// a file actually sends.
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// A result happens somewhere else and has to arrive without anybody opening a
// browser. What is posted reaches the program whole — the middleware must not
// have eaten it as a form.
func TestWhatIsPostedReachesTheProgram(t *testing.T) {
	t.Setenv("DOCKET_TEST_SECRET", "shh")
	h, root := listeningServer(t, true, "DOCKET_TEST_SECRET")

	w := post(t, h, "/in/results", "<testsuite><testcase name=\"x\"/></testsuite>",
		map[string]string{"X-Docket-Secret": "shh"})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}

	got, err := os.ReadFile(filepath.Join(root, "docs", "posted.txt"))
	if err != nil {
		t.Fatalf("the program was not given the body: %v", err)
	}
	if !strings.Contains(string(got), "<testcase") {
		t.Errorf("it got %q", got)
	}
	// And what it wrote is committed, so the result is in the history rather
	// than sitting dirty in a folder.
	if said := lastCommit(t, root); !strings.Contains(said, "results") {
		t.Errorf("committed as %q", said)
	}
}

// The secret is the machine's, not the repository's. Without it, nothing runs.
func TestAnInboxWithoutTheSecretIsRefused(t *testing.T) {
	t.Setenv("DOCKET_TEST_SECRET", "shh")
	h, root := listeningServer(t, true, "DOCKET_TEST_SECRET")

	for _, given := range []map[string]string{nil, {"X-Docket-Secret": "wrong"}} {
		w := post(t, h, "/in/results", "anything", given)
		if w.Code != http.StatusForbidden {
			t.Errorf("got %d, want 403", w.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "posted.txt")); err == nil {
		t.Error("the program ran for a caller with no secret")
	}
}

// And the same consent as everything else a vault declares.
func TestAnInboxDoesNotRunUnlessThisServerAgreed(t *testing.T) {
	t.Setenv("DOCKET_TEST_SECRET", "shh")
	h, root := listeningServer(t, false, "DOCKET_TEST_SECRET")

	w := post(t, h, "/in/results", "anything", map[string]string{"X-Docket-Secret": "shh"})
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", w.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "posted.txt")); err == nil {
		t.Error("a program ran on a server that never agreed to run one")
	}
}
