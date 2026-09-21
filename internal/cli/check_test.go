package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckOnACleanVault(t *testing.T) {
	dir := vaultDir(t)
	if code, _, stderr := run(t, "new", "-C", dir, "A task"); code != exitOK {
		t.Fatalf("new failed: %s", stderr)
	}

	code, stdout, stderr := run(t, "check", dir)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d; stdout:\n%s", code, exitOK, stdout)
	}
	if stderr != "" {
		t.Errorf("wrote %q to stderr", stderr)
	}
	if !strings.Contains(stdout, "No findings") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestCheckExitsNonZeroOnFindings(t *testing.T) {
	// This is what makes it usable as a pre-commit hook.
	dir := vaultDir(t)
	broken := "---\nkey: ACME-1\ntitle: T\ntype: task\nstatus: Pending\n" +
		"status_category: todo\npriority: normal\nassignee:\nlabels: []\n" +
		"created: 2026-08-30T12:00:00Z\nupdated: 2026-08-30T12:00:00Z\naliases: []\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "ACME", "ACME-1 T.md"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, _ := run(t, "check", dir)
	if code == exitOK {
		t.Errorf("exit code = 0 on a broken vault; stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ACME-1 T.md:5") {
		t.Errorf("the finding has no file:line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 finding.") {
		t.Errorf("no summary:\n%s", stdout)
	}
}

func TestCheckQuietPrintsOnlyFindings(t *testing.T) {
	dir := vaultDir(t)

	code, stdout, _ := run(t, "check", "--quiet", dir)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d", code, exitOK)
	}
	if stdout != "" {
		t.Errorf("quiet printed %q on a clean vault", stdout)
	}
}

func TestCheckOutsideAVault(t *testing.T) {
	code, _, stderr := run(t, "check", t.TempDir())
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "vault") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestCheckTakesAtMostOneDirectory(t *testing.T) {
	if code, _, _ := run(t, "check", "one", "two"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}

// check --fix renames a retitled task and puts every link to it back on the
// note, in that order.
//
// The order is the whole of DKT-61. Relinking used to run first, so it wrote
// links naming the title the task was about to stop having, the rename then
// made those names wrong, and the second pass reported the vault clean —
// because the rule that reads a relation matches the key inside the link, and
// the key had not changed. Valid here, and drawing no edges in Obsidian.
func TestFixRenamesThenPutsTheLinksBackOnTheNote(t *testing.T) {
	dir := vaultDir(t)
	for _, title := range []string{"Fix login redirect loop", "Session model"} {
		if code, _, stderr := run(t, "new", "-C", dir, title); code != exitOK {
			t.Fatalf("new %q failed: %s", title, stderr)
		}
	}
	if code, _, stderr := run(t, "set", "ACME-1", "blocked_by=ACME-2", dir); code != exitOK {
		t.Fatalf("set failed: %s", stderr)
	}

	// Retitled by hand, the way somebody does in Obsidian: the frontmatter
	// changes and the file name does not.
	second := filepath.Join(dir, "ACME", "ACME-2 Session model.md")
	raw, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(strings.Replace(string(raw),
		"title: Session model", "title: The session model, rewritten", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := run(t, "check", "--fix", dir); code != exitOK {
		t.Fatalf("check --fix exited %d: %s%s", code, stdout, stderr)
	}

	if _, err := os.Stat(filepath.Join(dir, "ACME",
		"ACME-2 The session model, rewritten.md")); err != nil {
		t.Fatalf("the retitled task was not renamed: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, "ACME", "ACME-1 Fix login redirect loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "[[ACME-2 The session model, rewritten]]") {
		t.Errorf("the link still names a note that no longer exists:\n%s", first)
	}

	code, stdout, _ := run(t, "check", dir)
	if code != exitOK || !strings.Contains(stdout, "No findings") {
		t.Errorf("the vault is not clean after --fix: %d\n%s", code, stdout)
	}
}

// A link in a body is repointed by a rename too, not only one in frontmatter.
//
// This is what using vault.Retitle buys: the same operation the server performs
// when somebody retitles a task in the interface, rather than a second
// implementation that moved the file and stopped there. A reference in prose is
// the commonest kind there is — it is how one task explains itself by naming
// another.
func TestFixRepointsALinkInABody(t *testing.T) {
	dir := vaultDir(t)
	for _, title := range []string{"Fix login redirect loop", "Session model"} {
		if code, _, stderr := run(t, "new", "-C", dir, title); code != exitOK {
			t.Fatalf("new %q failed: %s", title, stderr)
		}
	}

	first := filepath.Join(dir, "ACME", "ACME-1 Fix login redirect loop.md")
	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(first, append(raw,
		[]byte("\nThe cause is in [[ACME-2 Session model]], which sets the cookie.\n")...),
		0o644); err != nil {
		t.Fatal(err)
	}

	second := filepath.Join(dir, "ACME", "ACME-2 Session model.md")
	was, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(strings.Replace(string(was),
		"title: Session model", "title: The session model, rewritten", 1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, stdout, stderr := run(t, "check", "--fix", dir); code != exitOK {
		t.Fatalf("check --fix exited %d: %s%s", code, stdout, stderr)
	}

	body, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "[[ACME-2 Session model]]") {
		t.Errorf("a link in the body still names the old note:\n%s", body)
	}
	if !strings.Contains(string(body), "[[ACME-2 The session model, rewritten]]") {
		t.Errorf("the link in the body was not repointed:\n%s", body)
	}
}
