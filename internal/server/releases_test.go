package server

import (
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

func tag(t *testing.T, root, name, message string, at string) {
	t.Helper()
	args := []string{"-c", "user.email=t@example.com", "-c", "user.name=T",
		"tag", "-a", name, "-m", message}
	if at != "" {
		args = append(args, at)
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git tag: %v: %s", err, out)
	}
}

func commitAll(t *testing.T, root, message string) {
	t.Helper()
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", message)
}

// A release is a tag. What went into it is the work whose files changed since
// the tag before it — a question git answers, so there is nothing to maintain
// and nothing that can be out of date.
func TestAReleaseIsATag(t *testing.T) {
	_, h, root := newServer(t)

	tag(t, root, "v0.1.0", "The first cut", "")

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{Title: "Session model", Now: noon}); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "ACME-2")
	tag(t, root, "v0.2.0", "Sessions", "")

	body := get(t, h, "/releases").Body.String()

	for _, want := range []string{"v0.1.0", "v0.2.0", "The first cut", "Sessions", "ACME-2"} {
		if !strings.Contains(body, want) {
			t.Errorf("the releases page does not mention %q", want)
		}
	}
	// v0.2.0 contains the task that appeared in it, and says it is new.
	after := body[strings.Index(body, "v0.2.0"):]
	if !strings.Contains(after[:min(len(after), 1200)], "ACME-2") {
		t.Errorf("v0.2.0 does not contain the work that went into it:\n%s", after)
	}
	if !strings.Contains(body, `class="new"`) {
		t.Error("a task that first appeared in a release is not marked new")
	}
	// Newest first.
	if strings.Index(body, "v0.2.0") > strings.Index(body, "v0.1.0") {
		t.Error("releases are not newest first")
	}
}

// Tagging is often retroactive: three releases labelled in one afternoon have
// tag dates minutes apart and an order that means nothing. The commit each tag
// points at is when the work existed.
func TestReleasesAreOrderedByTheirCommits(t *testing.T) {
	_, h, root := newServer(t)

	first := strings.TrimSpace(run(t, root, "rev-parse", "HEAD"))

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{Title: "Later work", Now: noon}); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "ACME-2")
	second := strings.TrimSpace(run(t, root, "rev-parse", "HEAD"))

	// Tagged newest-first, seconds apart, so tag order is the wrong order.
	tag(t, root, "v2.0.0", "Later", second)
	tag(t, root, "v1.0.0", "Earlier", first)

	body := get(t, h, "/releases").Body.String()
	if strings.Index(body, "v2.0.0") > strings.Index(body, "v1.0.0") {
		t.Error("tags were ordered by when somebody typed them, not by their commits")
	}
}

func TestARepositoryWithNoTagsSaysSo(t *testing.T) {
	_, h, _ := newServer(t)
	body := get(t, h, "/releases").Body.String()
	if !strings.Contains(body, "No tags in this repository yet") {
		t.Errorf("no explanation for an untagged repository:\n%s", body)
	}
	if strings.Contains(body, `class="release"`) {
		t.Error("a release appeared out of nowhere")
	}
}

func TestReleasesAreReachableFromEveryPage(t *testing.T) {
	_, h, _ := newServer(t)
	if w := get(t, h, "/"); !strings.Contains(w.Body.String(), `href="/releases"`) {
		t.Error("nothing links to the releases")
	} else if w.Code != http.StatusOK {
		t.Errorf("code = %d", w.Code)
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
