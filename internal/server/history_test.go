package server

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

// A history is read from git and says what moved, in the tracker's words.
func TestHistorySaysWhatChanged(t *testing.T) {
	s, h, _ := newServer(t)
	b := newBrowser(t, h)

	b.visit("/task/ACME-1")
	if w := b.submit("/task/ACME-1/status", url.Values{
		"status":  {"In progress"},
		"version": {currentVersion(t, s, "ACME-1")},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("move: %d %s", w.Code, w.Body)
	}
	b.visit("/task/ACME-1")
	if w := b.submit("/task/ACME-1/comment", url.Values{
		"text":    {"Reproduced on staging."},
		"version": {currentVersion(t, s, "ACME-1")},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("comment: %d %s", w.Code, w.Body)
	}

	body := get(t, h, "/task/ACME-1/history").Body.String()

	for _, want := range []string{
		"In progress",      // the status it moved to
		"Backlog",          // and the one it left
		"added 1 comment",  // said as a count, not as a diff
		"created the task", // the history reaches the beginning
		"Server",           // attributed to whoever wrote it
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the history does not mention %q:\n%s", want, body)
		}
	}

	// `updated` moves on every write and would bury everything else.
	if strings.Contains(body, ">updated<") {
		t.Error("the history reports the timestamp it sets itself")
	}
}

// A retitle renames the file. History has to cross that, or a task looks as if
// it were created on the day somebody reworded it.
func TestHistoryCrossesARetitle(t *testing.T) {
	s, h, root := newServer(t)
	b := newBrowser(t, h)

	b.visit("/task/ACME-1/edit")
	if w := b.submit("/task/ACME-1/edit", url.Values{
		"version":  {currentVersion(t, s, "ACME-1")},
		"title":    {"Fix the login redirect for expired sessions"},
		"type":     {"bug"},
		"status":   {"Backlog"},
		"priority": {"high"},
		"assignee": {"agent/claude"},
		"body":     {"A different body entirely, so nothing is similar."},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}

	// The file really did move.
	if _, err := os.Stat(filepath.Join(root, "ACME",
		"ACME-1 Fix the login redirect for expired sessions.md")); err != nil {
		t.Fatalf("the retitle did not rename the file: %v", err)
	}

	body := get(t, h, "/task/ACME-1/history").Body.String()
	if !strings.Contains(body, "created the task") {
		t.Errorf("the history stops at the rename:\n%s", body)
	}
	if !strings.Contains(body, "Fix login redirect loop") {
		t.Errorf("the history does not show the old title:\n%s", body)
	}
	if !strings.Contains(body, "renamed the file to match") {
		t.Errorf("the history does not say the file moved:\n%s", body)
	}
}

func TestHistoryOfAnUnknownTaskIsNotFound(t *testing.T) {
	_, h, _ := newServer(t)
	if w := get(t, h, "/task/ACME-404/history"); w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

// The task page has to offer the history, or nothing reaches it.
func TestTheTaskPageLinksToItsHistory(t *testing.T) {
	_, h, _ := newServer(t)
	body := get(t, h, "/task/ACME-1").Body.String()
	if !strings.Contains(body, `href="/task/ACME-1/history"`) {
		t.Error("the task page does not link to its history")
	}
}

/* ---------- deleting ---------- */

// The route existed before anything could reach it.
func TestATaskCanBeDeletedFromItsPage(t *testing.T) {
	_, h, root := newServer(t)
	b := newBrowser(t, h)

	page := b.visit("/task/ACME-1").Body.String()
	if !strings.Contains(page, `action="/task/ACME-1/delete"`) {
		t.Fatal("the task page offers no way to delete the task")
	}

	if w := b.submit("/task/ACME-1/delete", url.Values{}); w.Code != http.StatusSeeOther {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md")); !os.IsNotExist(err) {
		t.Error("the file is still there")
	}
	if got := lastCommit(t, root); !strings.Contains(got, "deleted ACME-1") {
		t.Errorf("commit = %q", got)
	}
}

// A parent that vanishes leaves its children pointing at nothing.
func TestDeletingAParentIsRefused(t *testing.T) {
	_, h, root := newServer(t)
	b := newBrowser(t, h)

	b.visit("/new")
	if w := b.submit("/new", url.Values{
		"title":    {"A child"},
		"type":     {"task"},
		"priority": {"normal"},
		"parent":   {"ACME-1"},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("creating the child: %d %s", w.Code, w.Body)
	}
	before := lastCommit(t, root)

	b.visit("/task/ACME-1")
	w := b.submit("/task/ACME-1/delete", url.Values{})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if lastCommit(t, root) != before {
		t.Error("a refused delete produced a commit")
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md")); err != nil {
		t.Error("the parent was deleted anyway")
	}
}

/* ---------- a board with nothing on it ---------- */

// A vault nobody has written to yet is somebody's first minute with this.
func TestAnEmptyBoardSaysWhatToDo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "NEW", Name: "A New Thing", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "vault")

	s, err := New(root, Options{Author: gitvcs.Author{Name: "T", Email: "t@example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	body := get(t, s.Handler(), "/").Body.String()
	if !strings.Contains(body, "No tasks yet") {
		t.Errorf("an empty board says nothing about being empty:\n%s", body)
	}
	if !strings.Contains(body, "NEW-1 Its title.md") {
		t.Error("it does not say where a first task would go")
	}
	if !strings.Contains(body, `href="/new"`) {
		t.Error("it does not offer to make one")
	}
}

// Retitling renames the file, and every link that pointed at it has to move
// too — in one commit, so no point in the history has the vault pointing at a
// note that is not there.
func TestRetitlingRepointsLinksInOneCommit(t *testing.T) {
	s, h, root := newServer(t)
	b := newBrowser(t, h)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Session model", Description: "Blocks [[ACME-1 Fix login redirect loop]].", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "-m", "ACME-2")

	b.visit("/task/ACME-1/edit")
	if w := b.submit("/task/ACME-1/edit", url.Values{
		"version":  {currentVersion(t, s, "ACME-1")},
		"title":    {"Fix the login redirect for expired sessions"},
		"type":     {"bug"},
		"status":   {"Backlog"},
		"priority": {"high"},
		"assignee": {"agent/claude"},
		"body":     {"A body."},
	}); w.Code != http.StatusSeeOther {
		t.Fatalf("retitle: %d %s", w.Code, w.Body)
	}

	pointing := readFile(t, filepath.Join(root, "ACME", "ACME-2 Session model.md"))
	if !strings.Contains(pointing, "[[ACME-1 Fix the login redirect for expired sessions]]") {
		t.Errorf("the inbound link was left pointing at nothing:\n%s", pointing)
	}

	// Both the rename and the repointing are in the same commit.
	if dirty := gitPorcelain(t, root); dirty != "" {
		t.Errorf("the retitle left files uncommitted:\n%s", dirty)
	}
	// What matters is that the move and the repointing are one commit, not how
	// many paths git chooses to print: whether it reports a rename as one path
	// or as a delete and an add depends on how similar the two files are, which
	// is nothing to do with this.
	files := strings.Join(commitFiles(t, root), "\n")
	for _, want := range []string{
		"ACME-1 Fix the login redirect for expired sessions.md", // the new name
		"ACME-2 Session model.md",                               // the file that linked to it
	} {
		if !strings.Contains(files, want) {
			t.Errorf("%q is not in the commit:\n%s", want, files)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func gitPorcelain(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func commitFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "show", "--name-only", "--format=", "HEAD")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %v: %s", err, out)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

/* ---------- tags ---------- */

// A tag nests, and narrowing by a parent finds everything under it — which is
// what the same word does in Obsidian's tag pane and its tag: search. Anything
// else would mean the two clients answer the same question differently.
func TestSearchByTagFollowsTheHierarchy(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, made := range []struct {
		title string
		tags  []string
	}{
		{"Session model", []string{"area/auth"}},
		{"Rewrite the importer", []string{"area/import", "needs-review"}},
		{"Something else", []string{"chore"}},
	} {
		if _, _, err := vault.Create(root, c, vault.NewOptions{
			Title: made.title, Tags: made.tags, Now: noon,
		}); err != nil {
			t.Fatal(err)
		}
	}

	cases := map[string][]string{
		"area":         {"ACME-2", "ACME-3"},
		"area/auth":    {"ACME-2"},
		"needs-review": {"ACME-3"},
		"chore":        {"ACME-4"},
	}
	for tag, want := range cases {
		body := get(t, h, "/search?tag="+url.QueryEscape(tag)).Body.String()
		results := body[strings.Index(body, `<ul class="hits">`):]
		for _, key := range want {
			if !strings.Contains(results, key) {
				t.Errorf("tag %q does not find %s", tag, key)
			}
		}
		if tag == "area/auth" && strings.Contains(results, "ACME-3") {
			t.Errorf("tag %q reached a sibling branch", tag)
		}
	}
}

// The form offers every level of every tag, so a parent can be picked.
func TestTheSearchFormOffersEveryTagLevel(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Session model", Tags: []string{"area/auth/session"}, Now: noon,
	}); err != nil {
		t.Fatal(err)
	}

	// The filter bar offers each level as a link that narrows to it. Asserting
	// on the label rather than on the href, because a href is percent-encoded
	// and this is about the vocabulary being offered, not about URL escaping.
	body := get(t, h, "/search").Body.String()
	for _, want := range []string{">#area<", ">#area/auth<", ">#area/auth/session<"} {
		if !strings.Contains(body, want) {
			t.Errorf("the filter bar does not offer %s", want)
		}
	}
}
