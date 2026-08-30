package gitvcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAuthor(t *testing.T) {
	got, err := ParseAuthor("  Vadym Didenko <v@example.com>  ")
	if err != nil {
		t.Fatalf("ParseAuthor: %v", err)
	}
	if got.Name != "Vadym Didenko" || got.Email != "v@example.com" {
		t.Errorf("got %q / %q", got.Name, got.Email)
	}
	if got.String() != "Vadym Didenko <v@example.com>" {
		t.Errorf("String() = %q", got.String())
	}
}

func TestParseAuthorRejectsWhatItCannotUse(t *testing.T) {
	for _, s := range []string{"", "nobody", "<only@example.com>", "Name <>", "Name <a@b.com> trailing"} {
		if _, err := ParseAuthor(s); err == nil {
			t.Errorf("ParseAuthor(%q) was accepted", s)
		}
	}
}

func TestOpenRefusesADirectoryWithoutGit(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("Open accepted a directory that is not a repository")
	}
}

func TestCommit(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")

	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	author := Author{Name: "Agent", Email: "agent@example.com"}

	write(t, dir, "a.md", "one")
	if err := repo.Commit([]string{"a.md"}, "add a", author); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if got := log(t, dir); !strings.Contains(got, "Agent <agent@example.com> | add a") {
		t.Errorf("log = %q", got)
	}

	// Committing again with nothing changed must be a no-op, not an error:
	// a write that turns out to change nothing is not a failure.
	if err := repo.Commit([]string{"a.md"}, "add a again", author); err != nil {
		t.Fatalf("a no-op commit failed: %v", err)
	}
	if got := log(t, dir); strings.Contains(got, "again") {
		t.Error("an empty commit was created")
	}
}

func TestCommitTouchesOnlyTheGivenPaths(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	repo, _ := Open(dir)
	author := Author{Name: "Agent", Email: "agent@example.com"}

	write(t, dir, "wanted.md", "one")
	write(t, dir, "unrelated.md", "two")

	if err := repo.Commit([]string{"wanted.md"}, "only this one", author); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "unrelated.md") {
		t.Errorf("the unrelated file was swept into the commit; status:\n%s", out)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func log(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%an <%ae> | %s")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	return string(out)
}
