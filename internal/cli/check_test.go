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
	broken := "---\nkey: ACME/1\ntitle: T\ntype: task\nstatus: Pending\n" +
		"status_category: todo\npriority: normal\nassignee:\nlabels: []\n" +
		"created: 2026-08-30T12:00:00Z\nupdated: 2026-08-30T12:00:00Z\naliases: []\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "ACME", "1.md"), []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, _ := run(t, "check", dir)
	if code == exitOK {
		t.Errorf("exit code = 0 on a broken vault; stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "ACME/1.md:5") {
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
