package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
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
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Name: "Acme Platform",
		Template: vaulttest.Template(t)}); err != nil {
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

func TestAnEmbedByBareFileNameResolves(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)
	if err := os.MkdirAll(filepath.Join(root, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	svg := filepath.Join(root, "attachments", "architecture.svg")
	if err := os.WriteFile(svg, []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(root, "docs", "design.md")
	body := "---\ntitle: design\ntype: page\nupdated: 2026-09-06\n---\n\n![[architecture.svg]] and ![[attachments/architecture.svg]].\n"
	if err := os.WriteFile(page, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if findings := run(t, root); fired(findings, RuleLinks) {
		t.Errorf("an embed by bare file name was reported as broken: %v", findings)
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

/* ---------- --fix ---------- */

// A title edited by hand leaves the file name behind. That is the one finding
// with a right answer, so it is the one thing check can settle by itself.
func TestRenamesFollowTheTitle(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 Old name.md", strings.Replace(validTask, "title: A task", "title: A new name", 1))

	renames, err := Renames(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 {
		t.Fatalf("expected one rename, got %+v", renames)
	}
	if renames[0].To != "ACME/ACME-1 A new name.md" {
		t.Errorf("renaming to %q", renames[0].To)
	}

	if err := Apply(root, renames); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-1 A new name.md")); err != nil {
		t.Errorf("the file did not move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-1 Old name.md")); !os.IsNotExist(err) {
		t.Error("the old name is still there")
	}
	if findings := run(t, root); len(findings) != 0 {
		t.Errorf("the vault is still not clean:\n%v", findings)
	}
}

// A key that disagrees with the file name is two claims about which task this
// is, and renaming would pick one of them at random.
func TestAKeyThatDisagreesIsNotRenamedAway(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", strings.Replace(validTask, "key: ACME-1", "key: ACME-7", 1))

	renames, err := Renames(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 0 {
		t.Errorf("a key mismatch was renamed away: %+v", renames)
	}
}

// Two tasks may share a title. They cannot share a name, because the key is
// part of it — which is what makes renaming to match a title safe to do
// without asking.
func TestTwoTasksWithTheSameTitleDoNotCollide(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 A task.md", validTask)
	put(t, root, "ACME-2 Some other name.md", strings.Replace(validTask, "key: ACME-1", "key: ACME-2", 1))

	renames, err := Renames(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(renames) != 1 || renames[0].To != "ACME/ACME-2 A task.md" {
		t.Fatalf("renames = %+v", renames)
	}
	if err := Apply(root, renames); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, name := range []string{"ACME-1 A task.md", "ACME-2 A task.md"} {
		if _, err := os.Stat(filepath.Join(root, "ACME", name)); err != nil {
			t.Errorf("%s is gone: %v", name, err)
		}
	}
	if findings := run(t, root); len(findings) != 0 {
		t.Errorf("not clean:\n%v", findings)
	}
}

// A board is generated from docket.yaml, so one that has drifted is the tool's
// own output out of date rather than two things a person meant — the third
// finding --fix can settle.
func TestAStaleBoardIsReportedAndRegenerated(t *testing.T) {
	root := newVault(t)
	board := filepath.Join(root, filepath.FromSlash(vault.BoardFile))

	raw, err := os.ReadFile(board)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(raw), "property: formula.stage", "property: note.status", 1)
	if stale == string(raw) {
		t.Fatal("the scaffolded board does not group by the stage formula")
	}
	if err := os.WriteFile(board, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	findings := run(t, root)
	if len(findings) != 1 || findings[0].Path != vault.BoardFile {
		t.Fatalf("want one finding about %s, got %v", vault.BoardFile, findings)
	}

	written, err := Boards(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != vault.BoardFile {
		t.Fatalf("--fix wrote %v", written)
	}
	if got := run(t, root); len(got) != 0 {
		t.Errorf("still not clean after regenerating: %v", got)
	}
}

// Removing the first line is how a vault says a board is its own, and after
// that nothing has an opinion about what is in it.
func TestABoardWithoutTheMarkerIsLeftAlone(t *testing.T) {
	root := newVault(t)
	board := filepath.Join(root, filepath.FromSlash(vault.BoardFile))

	if err := os.WriteFile(board, []byte("filters:\n  and:\n    - file.inFolder(\"ACME\")\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := run(t, root); len(got) != 0 {
		t.Errorf("a board somebody took over was reported: %v", got)
	}
	if written, err := Boards(root); err != nil || len(written) != 0 {
		t.Errorf("--fix overwrote a board it does not own: %v, %v", written, err)
	}
}

// withFront is a valid task with its key, title and a line or two of extra
// frontmatter — for the rules that can only be broken across several files.
func withFront(t *testing.T, key, title, extra string) string {
	t.Helper()
	body := strings.Replace(validTask, "key: ACME-1", "key: "+key, 1)
	body = strings.Replace(body, "title: A task", "title: "+title, 1)
	// Two labels: keys would be a duplicate property, which is a different
	// finding from the one under test.
	if strings.Contains(extra, "labels:") {
		body = strings.Replace(body, "labels: []\n", "", 1)
	}
	return strings.Replace(body, "aliases: []", "aliases: []\n"+extra, 1)
}

// A tag's whole value is the list of what carries it, so the two ways of
// producing a tag nobody will ever ask for are both findings.
func TestATagThatIsNotASetIsReported(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 Alone.md", withFront(t, "ACME-1", "Alone",
		"tags: [only-here, area/auth]"))
	put(t, root, "ACME-2 Company.md", withFront(t, "ACME-2", "Company",
		"tags: [area/auth]"))
	// The label lives on a third task, so the clash is across files.
	put(t, root, "ACME-3 Labelled.md", withFront(t, "ACME-3", "Labelled",
		`labels: ["[[auth]]"]`))

	var about []string
	for _, f := range run(t, root) {
		if f.Rule == RuleTags {
			about = append(about, f.Message)
		}
	}
	joined := strings.Join(about, "\n")

	if !strings.Contains(joined, `"only-here"`) {
		t.Errorf("a tag on one task and nothing else was not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "set of one") {
		t.Errorf("the reason is not said:\n%s", joined)
	}
	// area/auth is on two tasks, so it is a set — but `auth` is also a label,
	// which makes it one fact in two places.
	if !strings.Contains(joined, `"area/auth"`) || !strings.Contains(joined, "two places") {
		t.Errorf("a label said again as a tag was not reported:\n%s", joined)
	}
}

// A tag several notes carry, that is not also a label, is exactly what a tag is
// for and must not be reported.
func TestATagThatIsASetIsLeftAlone(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-1 One.md", withFront(t, "ACME-1", "One", "tags: [risk/money]"))
	put(t, root, "ACME-2 Two.md", withFront(t, "ACME-2", "Two", "tags: [risk/money]"))

	for _, f := range run(t, root) {
		if f.Rule == RuleTags {
			t.Errorf("a real set was reported: %s", f.Message)
		}
	}
}
