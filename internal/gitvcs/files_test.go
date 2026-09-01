package gitvcs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A whole folder at one commit, in one call. Reading it a file at a time is a
// git process a file, and on a four thousand file board that was thirty six
// seconds to step back one commit — slow enough that the history reads as
// broken. What it returns has to be exactly what reading them one by one did.
func TestFilesReadsAWholeFolderAtACommit(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")

	for _, folder := range []string{"ACME", "docs"} {
		if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, dir, "ACME/ACME-1 первая задача.md", "one\n")
	write(t, dir, "ACME/ACME-2 a second.md", "two\n")
	write(t, dir, "docs/elsewhere.md", "not in the folder\n")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	who := Author{Name: "Dana", Email: "dana@example.com"}
	if err := repo.Commit([]string{"ACME/ACME-1 первая задача.md",
		"ACME/ACME-2 a second.md", "docs/elsewhere.md"}, "two tasks", who); err != nil {
		t.Fatal(err)
	}
	first, err := repo.output("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	first = strings.TrimSpace(first)

	// A later commit, so the read is of history rather than of the tree.
	write(t, dir, "ACME/ACME-2 a second.md", "two, changed\n")
	write(t, dir, "ACME/ACME-3 a third.md", "three\n")
	if err := repo.Commit([]string{"ACME/ACME-2 a second.md",
		"ACME/ACME-3 a third.md"}, "a third", who); err != nil {
		t.Fatal(err)
	}

	files, err := repo.Files(first, "ACME")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("read %d files at the first commit, want 2: %v", len(files), keysOf(files))
	}
	// The name with Cyrillic in it is the one that breaks: git quotes such paths
	// unless told not to, and a quoted name matches nothing.
	if got := string(files["ACME/ACME-1 первая задача.md"]); got != "one\n" {
		t.Errorf("ACME-1 = %q, want %q", got, "one\n")
	}
	if got := string(files["ACME/ACME-2 a second.md"]); got != "two\n" {
		t.Errorf("ACME-2 at the first commit = %q, want what it was then", got)
	}

	// Every file, and the same bytes, as reading them one at a time.
	for name, body := range files {
		blob, err := repo.Blob(first, name)
		if err != nil {
			t.Fatal(err)
		}
		if string(blob) != string(body) {
			t.Errorf("%s: whole-tree read %q, one-at-a-time read %q", name, body, blob)
		}
	}

	// A folder that does not exist at that commit is an empty tree, not an
	// error: that is what a project added later looks like from before.
	missing, err := repo.Files(first, "LATER")
	if err != nil {
		t.Fatalf("a folder absent at that commit should read as empty: %v", err)
	}
	if len(missing) != 0 {
		t.Errorf("read %d files from a folder that was not there", len(missing))
	}
}

func keysOf(files map[string][]byte) []string {
	var out []string
	for name := range files {
		out = append(out, name)
	}
	return out
}
