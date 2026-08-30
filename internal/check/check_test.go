package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// task is a valid task, which each test then breaks in exactly one way.
const validTask = `---
key: ACME-1
title: A task
type: task
status: Backlog
status_category: todo
priority: normal
assignee:
labels: []
created: 2026-08-30T12:00:00Z
updated: 2026-08-30T12:00:00Z
aliases: []
---

A body.
`

func newVault(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Name: "Acme Platform"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return root
}

func put(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, "ACME", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, root string) []Finding {
	t.Helper()
	findings, err := Run(root)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return findings
}

func TestAScaffoldedVaultIsClean(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)

	if got := run(t, root); len(got) != 0 {
		t.Errorf("a valid vault produced findings: %v", got)
	}
}

// Each case breaks the valid task in one way and names the rule that must fire.
func TestEachRuleFires(t *testing.T) {
	cases := []struct {
		name string
		rule int
		task string
	}{
		{
			"no frontmatter", RuleFrontmatter,
			"Just a body, no properties.\n",
		},
		{
			"key does not match the file name", RuleFrontmatter,
			strings.Replace(validTask, "key: ACME-1", "key: ACME-99", 1),
		},
		{
			"unknown status", RuleStatus,
			strings.Replace(validTask, "status: Backlog", "status: Pending", 1),
		},
		{
			"category disagrees with the status", RuleStatus,
			strings.Replace(validTask, "status_category: todo", "status_category: done", 1),
		},
		{
			"unknown type", RuleVocabulary,
			strings.Replace(validTask, "type: task", "type: saga", 1),
		},
		{
			"unknown priority", RuleVocabulary,
			strings.Replace(validTask, "priority: normal", "priority: screaming", 1),
		},
		{
			"parent that does not exist", RuleParent,
			strings.Replace(validTask, "labels: []", "parent: ACME-404\nlabels: []", 1),
		},
		{
			"nested property", RuleFlat,
			strings.Replace(validTask, "labels: []", "fields:\n  points: 3\nlabels: []", 1),
		},
		{
			"unparseable timestamp", RuleTimestamps,
			strings.Replace(validTask, "created: 2026-08-30T12:00:00Z", "created: yesterday", 1),
		},
		{
			"updated before created", RuleTimestamps,
			strings.Replace(validTask, "updated: 2026-08-30T12:00:00Z", "updated: 2020-01-01T00:00:00Z", 1),
		},
		{
			"link to nothing", RuleLinks,
			strings.Replace(validTask, "A body.", "A body linking to [[ACME-404]].", 1),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := newVault(t)
			put(t, root, "ACME-1 A task.md", c.task)

			findings := run(t, root)
			if len(findings) == 0 {
				t.Fatalf("no findings: rule %d did not fire", c.rule)
			}
			for _, f := range findings {
				if f.Rule == c.rule {
					return
				}
			}
			t.Errorf("rule %d did not fire; got %v", c.rule, findings)
		})
	}
}

func TestDuplicateKeys(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)
	// A second file claiming the same key. Its own name disagrees too, so both
	// rule 1 and rule 2 have something to say.
	put(t, root, "ACME-2 A task.md", validTask)

	if !fired(run(t, root), RuleUniqueKeys) {
		t.Error("a duplicated key was not reported")
	}
}

func TestParentCycle(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", strings.Replace(validTask,
		"labels: []", "parent: ACME-2\nlabels: []", 1))
	put(t, root, "ACME-2 A task.md", strings.Replace(
		strings.Replace(validTask, "key: ACME-1", "key: ACME-2", 1),
		"labels: []", "parent: ACME-1\nlabels: []", 1))

	findings := run(t, root)
	if !fired(findings, RuleParent) {
		t.Fatalf("a parent cycle was not reported: %v", findings)
	}

	cycleFindings := 0
	for _, f := range findings {
		if f.Rule == RuleParent && strings.Contains(f.Message, "cycle") {
			cycleFindings++
		}
	}
	if cycleFindings != 1 {
		t.Errorf("one cycle produced %d findings, want 1", cycleFindings)
	}
}

func TestLinksThatDoResolve(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", strings.Replace(validTask, "A body.",
		"Links: [[ACME-2 A task]], [[index]], [[docs/index]], `[[not-a-link]]`.", 1))

	// OLD-7 resolves through the alias on ACME/2 — a key that outlived its own
	// system keeps working.
	aliased := strings.Replace(
		strings.Replace(validTask, "key: ACME-1", "key: ACME-2", 1),
		"aliases: []", "aliases: [OLD-7]", 1)
	put(t, root, "ACME-2 A task.md", aliased)

	if findings := run(t, root); len(findings) != 0 {
		t.Errorf("working links were reported as broken: %v", findings)
	}
}

func TestBrokenLinksInPagesAreReported(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)
	page := filepath.Join(root, "docs", "note.md")
	if err := os.WriteFile(page, []byte("A page linking to [[nowhere]].\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := run(t, root)
	if !fired(findings, RuleLinks) {
		t.Fatalf("a broken link in a page was not reported: %v", findings)
	}
	if findings[0].Path != "docs/note.md" {
		t.Errorf("path = %q, want docs/note.md", findings[0].Path)
	}
}

func TestFindingsCarryALine(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", strings.Replace(validTask, "status: Backlog", "status: Pending", 1))

	findings := run(t, root)
	if len(findings) == 0 {
		t.Fatal("no findings")
	}
	if findings[0].Line != 5 {
		t.Errorf("line = %d, want 5 (the status property)", findings[0].Line)
	}
	if !strings.Contains(findings[0].String(), "ACME-1 A task.md:5") {
		t.Errorf("String() = %q, want a file:line prefix", findings[0].String())
	}
}

func TestEveryBrokenFileIsReported(t *testing.T) {
	// A validator that stops at the first bad file makes fixing a batch of
	// them a game of whack-a-mole.
	root := newVault(t)
	for _, n := range []string{"1", "2", "3"} {
		put(t, root, "ACME-"+n+" A task.md", strings.Replace(
			strings.Replace(validTask, "key: ACME-1", "key: ACME-"+n, 1),
			"status: Backlog", "status: Pending", 1))
	}

	if got := len(run(t, root)); got != 3 {
		t.Errorf("got %d findings, want 3 — one per broken file", got)
	}
}

func TestRunOutsideAVault(t *testing.T) {
	if _, err := Run(t.TempDir()); err == nil {
		t.Error("Run accepted a directory that is not a vault")
	}
}

func fired(findings []Finding, rule int) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestAProjectFolderNobodyDeclaredIsReported(t *testing.T) {
	// A folder full of tasks that docket.yaml does not mention is work nobody
	// can see, which is the one thing a tracker must not allow.
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)

	stray := filepath.Join(root, "GHOST")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "GHOST-1 A task.md"),
		[]byte(strings.Replace(validTask, "key: ACME-1", "key: GHOST/1", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if !fired(run(t, root), RuleProjects) {
		t.Error("an undeclared project folder was not reported")
	}
}

func TestAProjectNoBoardShowsIsReported(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddProject("BETA", "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	// The boards were not regenerated, so BETA is invisible on them.

	if !fired(run(t, root), RuleProjects) {
		t.Error("a project no board mentions was not reported")
	}
}
