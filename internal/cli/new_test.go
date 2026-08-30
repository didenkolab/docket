package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFlags() *flag.FlagSet {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.String("type", "", "")
	flags.Bool("verbose", false, "")
	return flags
}

// vaultDir scaffolds a vault and returns its path.
func vaultDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "vault")
	if code, _, stderr := run(t, "init", "--key", "ACME", dir); code != exitOK {
		t.Fatalf("init failed: %s", stderr)
	}
	return dir
}

func TestNewCreatesATask(t *testing.T) {
	dir := vaultDir(t)

	code, stdout, stderr := run(t, "new", "-C", dir, "Fix login redirect loop")
	if code != exitOK {
		t.Fatalf("exit code = %d; stderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "ACME-1") {
		t.Errorf("stdout does not report the key:\n%s", stdout)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "tasks", "ACME-1.md"))
	if err != nil {
		t.Fatalf("no task on disk: %v", err)
	}
	if !strings.Contains(string(raw), "title: Fix login redirect loop") {
		t.Errorf("the title was not written:\n%s", raw)
	}
}

func TestNewAcceptsFlagsAfterTheTitle(t *testing.T) {
	// Go's flag package stops at the first positional argument, so without the
	// argument permutation these flags would be swallowed as extra positionals.
	dir := vaultDir(t)

	code, _, stderr := run(t, "new", "-C", dir, "Fix login redirect loop",
		"--type", "bug", "--priority", "high", "--labels", "auth, regression")
	if code != exitOK {
		t.Fatalf("exit code = %d; stderr:\n%s", code, stderr)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, "tasks", "ACME-1.md"))
	for _, want := range []string{"type: bug", "priority: high", "labels: [auth, regression]"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in:\n%s", want, raw)
		}
	}
}

func TestNewFindsTheVaultFromASubdirectory(t *testing.T) {
	dir := vaultDir(t)
	sub := filepath.Join(dir, "docs", "spec")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	if code, _, stderr := run(t, "new", "A task from deep inside"); code != exitOK {
		t.Fatalf("exit code = %d; stderr:\n%s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "tasks", "ACME-1.md")); err != nil {
		t.Errorf("the task did not land in the vault root: %v", err)
	}
}

func TestNewOutsideAVaultFails(t *testing.T) {
	dir := t.TempDir()
	code, _, stderr := run(t, "new", "-C", dir, "Nowhere to put this")
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "vault") {
		t.Errorf("stderr does not explain the problem:\n%s", stderr)
	}
}

func TestNewNeedsExactlyOneTitle(t *testing.T) {
	dir := vaultDir(t)
	for _, args := range [][]string{
		{"new", "-C", dir},
		{"new", "-C", dir, "one", "two"},
	} {
		if code, _, _ := run(t, args...); code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, exitUsage)
		}
	}
}

func TestNewRejectsValuesTheProjectDoesNotKnow(t *testing.T) {
	dir := vaultDir(t)

	code, _, stderr := run(t, "new", "-C", dir, "A task", "--type", "saga")
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "saga") {
		t.Errorf("stderr does not name the bad value:\n%s", stderr)
	}
}

func TestPermuteKeepsFlagValuesTogether(t *testing.T) {
	flags := newTestFlags()
	got := permute(flags, []string{"Title here", "--type", "bug", "--flag=value", "second"})
	want := []string{"--type", "bug", "--flag=value", "Title here", "second"}

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permute = %v, want %v", got, want)
	}
}

func TestPermuteLeavesBooleanFlagsAlone(t *testing.T) {
	// A boolean flag takes no value, so the argument after it is positional.
	flags := newTestFlags()
	got := permute(flags, []string{"--verbose", "Title here"})
	want := []string{"--verbose", "Title here"}

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permute = %v, want %v", got, want)
	}
}

func TestPermuteStopsAtADoubleDash(t *testing.T) {
	flags := newTestFlags()
	got := permute(flags, []string{"--type", "bug", "--", "--not-a-flag"})
	want := []string{"--type", "bug", "--not-a-flag"}

	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("permute = %v, want %v", got, want)
	}
}
