package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesAVault(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")

	code, stdout, stderr := run(t, "init", "--key", "ACME", "--name", "Acme Platform", dir)
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if !strings.Contains(stdout, "ACME") {
		t.Errorf("stdout does not report what was created:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "docket.yaml")); err != nil {
		t.Errorf("no vault on disk: %v", err)
	}
}

func TestInitDefaultsToTheCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if code, _, stderr := run(t, "init", "--key", "ACME"); code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "docket.yaml")); err != nil {
		t.Errorf("no vault in the current directory: %v", err)
	}
}

func TestInitWithoutAKeyIsAUsageError(t *testing.T) {
	code, stdout, stderr := run(t, "init", t.TempDir())
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("wrote %q to stdout, want nothing", stdout)
	}
	if !strings.Contains(stderr, "--key") {
		t.Errorf("stderr does not say what is missing:\n%s", stderr)
	}
}

func TestInitWithABadKeyFails(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")

	code, _, stderr := run(t, "init", "--key", "acme", dir)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "acme") {
		t.Errorf("stderr does not name the bad key:\n%s", stderr)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a directory was created for a rejected key")
	}
}

func TestInitIntoANonEmptyDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, "init", "--key", "ACME", dir)
	if code != exitError {
		t.Errorf("exit code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "notes.md") {
		t.Errorf("stderr does not name what was in the way:\n%s", stderr)
	}
}

func TestInitTakesAtMostOneDirectory(t *testing.T) {
	code, _, stderr := run(t, "init", "--key", "ACME", "one", "two")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "directory") {
		t.Errorf("stderr does not explain the problem:\n%s", stderr)
	}
}

func TestInitRejectsUnknownFlags(t *testing.T) {
	if code, _, _ := run(t, "init", "--key", "ACME", "--colour", "blue"); code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
}
