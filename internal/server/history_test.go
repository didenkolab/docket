package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/vault"
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
	if _, err := vault.Init(root, vault.Options{Key: "NEW", Name: "A New Thing"}); err != nil {
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
