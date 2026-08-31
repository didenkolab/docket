// Package vaulttest builds a template repository for tests.
//
// A vault is scaffolded from a git remote now, and a test must not reach the
// network: it would be slow, it would fail on a machine without one, and it
// would make every test depend on what somebody put in the published template
// this morning.
//
// So a test gets its own template — a real git repository in a temporary
// directory, holding the smallest thing that is a vault. `git clone` is happy
// with a path, so the code under test takes exactly the path it takes in life.
//
// It is deliberately not a copy of the published template. What is being tested
// is that scaffolding works, not what the scaffold says; a test that asserted on
// the template's prose would break every time somebody improved it.
package vaulttest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// files are the smallest template that is a vault: a configuration, a project
// folder, a task template, and the two files an agent is told to read.
var files = map[string]string{
	"docket.yaml": `name: A template
projects:
  - key: PROJ
    name: Placeholder
statuses:
  - name: Backlog
    category: todo
  - name: Ready
    category: todo
  - name: In progress
    category: doing
  - name: In review
    category: doing
  - name: Done
    category: done
  - name: Dropped
    category: done
types:
  - name: epic
    level: 1
  - task
  - bug
  - story
  - name: subtask
    level: -1
priorities:
  - low
  - normal
  - high
  - urgent
`,
	"PROJ/.gitkeep":        "",
	"attachments/.gitkeep": "",
	"docs/index.md": `---
title: A template
type: page
---

# A template

The front page of this vault's knowledge base.
`,
	"templates/task.md": `---
key:
title:
type: task
status: Backlog
status_category: todo
priority:
assignee:
labels: []
created:
updated:
aliases: []
---

## Comments
`,
	"templates/page.md": `---
title:
type: page
---
`,
	"AGENTS.md":  "# Working in this vault\n\nRead this before changing anything.\n",
	"CLAUDE.md":  "# A template\n\nRead [AGENTS.md](AGENTS.md).\n",
	"README.md":  "# A template\n\nA docket vault.\n",
	".gitignore": ".obsidian/workspace.json\n.DS_Store\n",
	// A file that explains the template and must not survive into a vault made
	// from it, so tests can check that it does not.
	"TEMPLATE.md": "# The template\n\nThis file belongs to the template.\n",
}

// Template writes a template repository and returns its path, for
// vault.Options.Template.
func Template(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "template")
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "template@example.com"},
		{"config", "user.name", "Template"},
		{"add", "-A"},
		{"commit", "-q", "-m", "the template"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return dir
}
