package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
)

// drawingServer is a vault with a page and a panel drawn by programs.
func drawingServer(t *testing.T, allowed bool) http.Handler {
	t.Helper()
	_, _, root := newServer(t)

	for name, body := range map[string]string{
		"hooks/page.sh":  "#!/bin/sh\necho '## Where the work sits'\necho\necho '- Backlog: 3 days'\n",
		"hooks/panel.sh": "#!/bin/sh\ncat > /dev/null\necho 'Covered by 2 tests.'\n",
	} {
		at := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(at, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c.Pages = []project.Surface{{Name: "where", Title: "Where the work sits", Run: "hooks/page.sh"}}
	c.Panels = []project.Surface{{Name: "coverage", Title: "Coverage", Run: "hooks/panel.sh"}}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "surfaces")

	s, err := New(root, Options{
		Author:   gitvcs.Author{Name: "Server", Email: "server@example.com"},
		Programs: allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return noon.Add(time.Hour) }
	return s.Handler()
}

// The mechanism the reporting apps need: a program prints Markdown and it is a
// page, in the navigation, drawn each time it is opened.
func TestAPageIsDrawnByAProgram(t *testing.T) {
	h := drawingServer(t, true)

	w := get(t, h, "/app/where")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "<h2>Where the work sits</h2>") {
		t.Errorf("the program's Markdown was not rendered:\n%s", body)
	}
	if !strings.Contains(body, "Backlog: 3 days") {
		t.Error("what it printed is not on the page")
	}
	if !strings.Contains(body, `href="/app/where"`) {
		t.Error("the page is not in the navigation")
	}
}

// And a panel is the same thing on a task.
func TestAPanelIsDrawnOnTheTask(t *testing.T) {
	h := drawingServer(t, true)

	body := get(t, h, "/task/ACME-1").Body.String()
	if !strings.Contains(body, "Coverage") || !strings.Contains(body, "Covered by 2 tests.") {
		t.Errorf("the panel is not on the task:\n%s", body)
	}
}

// A repository can declare a program; only whoever starts the server can agree
// to run it. Without that, the page says so rather than running anything.
func TestAPageIsNotDrawnUnlessThisServerAgreed(t *testing.T) {
	h := drawingServer(t, false)

	w := get(t, h, "/app/where")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "Backlog: 3 days") {
		t.Fatal("a program in the repository ran on a server that never agreed to run one")
	}
	if !strings.Contains(body, "--programs") {
		t.Errorf("the page does not say why it is empty:\n%s", body)
	}

	// And no panel either.
	if strings.Contains(get(t, h, "/task/ACME-1").Body.String(), "Covered by 2 tests.") {
		t.Error("a panel ran on a server that does not run programs")
	}
}
