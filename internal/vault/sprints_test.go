package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSprint(t *testing.T) {
	page := []byte(`---
title: Спринт 13
type: sprint
starts: 2026-08-31
ends: 2026-09-11
---

# Спринт 13

Закрыть возвраты.
`)
	s, ok := ParseSprint(page)
	if !ok {
		t.Fatal("a page saying type: sprint was not read as one")
	}
	if s.Title != "Спринт 13" {
		t.Errorf("title %q", s.Title)
	}
	if s.Trouble != "" {
		t.Errorf("trouble %q", s.Trouble)
	}
	if s.Days() != 12 {
		t.Errorf("Days() = %d, want 12 — both ends counted", s.Days())
	}
	if want := "Закрыть возвраты."; !strings.Contains(s.Body, want) {
		t.Errorf("body does not hold the prose: %q", s.Body)
	}

	// A sprint runs between two dates, and whether it is on is a question about
	// today — which is the whole reason there is no state field.
	for _, c := range []struct {
		day                     string
		on, over, ahead, itself bool
	}{
		{"2026-08-30", false, false, true, false},
		{"2026-08-31", true, false, false, false}, // the first day is in it
		{"2026-09-11", true, false, false, false}, // and so is the last
		{"2026-09-12", false, true, false, false},
	} {
		day, err := time.Parse(DateFormat, c.day)
		if err != nil {
			t.Fatal(err)
		}
		if s.On(day) != c.on || s.Over(day) != c.over || s.Ahead(day) != c.ahead {
			t.Errorf("%s: on=%v over=%v ahead=%v, want %v/%v/%v",
				c.day, s.On(day), s.Over(day), s.Ahead(day), c.on, c.over, c.ahead)
		}
	}
}

// A page that says it is a sprint is one even if its dates are wrong. Hiding it
// is a worse answer than showing it with what is wrong said out loud.
func TestParseSprintKeepsAPageWithBadDates(t *testing.T) {
	for _, c := range []struct{ what, front, says string }{
		{"backwards", "starts: 2026-09-11\nends: 2026-08-31", "ends before it starts"},
		{"not a date", "starts: soon\nends: 2026-08-31", "starts is not a date"},
		{"neither", "starts: soon\nends: later", "neither starts nor ends"},
		{"missing", "", "neither starts nor ends"},
	} {
		s, ok := ParseSprint([]byte("---\ntitle: S\ntype: sprint\n" + c.front + "\n---\n"))
		if !ok {
			t.Errorf("%s: the page was dropped instead of reported", c.what)
			continue
		}
		if !strings.Contains(s.Trouble, c.says) {
			t.Errorf("%s: said %q, want something about %q", c.what, s.Trouble, c.says)
		}
		if s.On(time.Now()) {
			t.Errorf("%s: a sprint with unreadable dates claims to be running", c.what)
		}
	}
}

func TestParseSprintRefusesWhatIsNotASprint(t *testing.T) {
	for _, c := range []struct {
		what string
		page string
	}{
		{"a wiki page", "---\ntitle: Notes\ntype: page\n---\nWords.\n"},
		{"a task", "---\nkey: PIER-1\ntype: story\nstatus: Done\n---\n"},
		{"no frontmatter", "# Just a heading\n"},
		{"an unclosed block", "---\ntype: sprint\n"},
	} {
		if _, ok := ParseSprint([]byte(c.page)); ok {
			t.Errorf("%s was read as a sprint", c.what)
		}
	}
}

func TestSprintsReadsAVault(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("docs/sprints/Sprint 11.md", "---\ntitle: Sprint 11\ntype: sprint\nstarts: 2026-08-03\nends: 2026-08-14\n---\n")
	write("docs/sprints/Sprint 12.md", "---\ntitle: Sprint 12\ntype: sprint\nstarts: 2026-08-17\nends: 2026-08-28\n---\n")
	// Filed somewhere else on purpose: a sprint is found by its type, not by a
	// folder the format would otherwise have to mandate.
	write("planning/Sprint 13.md", "---\ntitle: Sprint 13\ntype: sprint\nstarts: 2026-08-31\nends: 2026-09-11\n---\n")
	write("docs/notes.md", "---\ntitle: Notes\ntype: page\n---\n")
	write(".obsidian/workspace.json", "{}")

	sprints, err := Sprints(root)
	if err != nil {
		t.Fatalf("Sprints: %v", err)
	}
	if len(sprints) != 3 {
		t.Fatalf("found %d sprints: %+v", len(sprints), sprints)
	}
	if sprints[0].Note != "Sprint 13" {
		t.Errorf("newest first is %q", sprints[0].Note)
	}
	if sprints[0].Path != "planning/Sprint 13.md" {
		t.Errorf("path %q", sprints[0].Path)
	}

	on, err := time.Parse(DateFormat, "2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	running, ok := Running(sprints, on)
	if !ok || running.Note != "Sprint 13" {
		t.Errorf("Running = %q, %v", running.Note, ok)
	}
	between, err := time.Parse(DateFormat, "2026-08-16")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Running(sprints, between); ok {
		t.Error("a day between two sprints reports one running")
	}
}
