package gitvcs

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Two people write different lines of the same task, and blame has to say which
// is which — that is the whole point of it.
func TestBlameSaysWhoWroteEachLine(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")

	write(t, dir, "task.md", "A description.\n\n- [ ] the first criterion\n")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit([]string{"task.md"}, "ACME-1: a task",
		Author{Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}

	write(t, dir, "task.md", "A description.\n\n- [ ] the first criterion\n- [ ] and a second\n")
	if err := repo.Commit([]string{"task.md"}, "ACME-1: a criterion added",
		Author{Name: "Sam", Email: "sam@example.com"}); err != nil {
		t.Fatal(err)
	}

	lines, err := repo.Blame("task.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 {
		t.Fatalf("blamed %d lines, want 4: %+v", len(lines), lines)
	}

	for i, want := range []struct{ author, text string }{
		{"Dana", "A description."},
		{"Dana", ""},
		{"Dana", "- [ ] the first criterion"},
		{"Sam", "- [ ] and a second"},
	} {
		got := lines[i]
		if got.Number != i+1 {
			t.Errorf("line %d is numbered %d", i+1, got.Number)
		}
		if got.Author != want.author {
			t.Errorf("line %d is by %q, want %q", i+1, got.Author, want.author)
		}
		if got.Text != want.text {
			t.Errorf("line %d reads %q, want %q", i+1, got.Text, want.text)
		}
	}

	// The commit's subject comes along, which in this vault is the change said
	// in words.
	if !strings.Contains(lines[3].Summary, "criterion added") {
		t.Errorf("the last line's commit says %q", lines[3].Summary)
	}
	if lines[0].Commit == lines[3].Commit {
		t.Error("two commits were reported as one")
	}
	if lines[3].When.IsZero() {
		t.Error("no date")
	}
}

// Retitling a task moves its file. A blame that stopped at the rename would say
// the whole task was written by whoever renamed it, which is the opposite of
// useful.
func TestBlameFollowsARename(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")

	write(t, dir, "ACME-1 Old title.md", "Written first.\n")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit([]string{"ACME-1 Old title.md"}, "ACME-1: a task",
		Author{Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "mv", "ACME-1 Old title.md", "ACME-1 A better title.md")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git mv: %v\n%s", err, out)
	}
	if err := repo.Commit([]string{"."}, "ACME-1: title",
		Author{Name: "Sam", Email: "sam@example.com"}); err != nil {
		t.Fatal(err)
	}

	lines, err := repo.Blame("ACME-1 A better title.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("blamed %d lines", len(lines))
	}
	if lines[0].Author != "Dana" {
		t.Errorf("the line is credited to %q — the rename was followed badly", lines[0].Author)
	}
}

// A path with non-ASCII in it, because git escapes those by default and half
// this project's fixtures are in Russian.
func TestBlameReadsANonAsciiPath(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")

	const name = "PIER-7 Дубли вебхуков.md"
	write(t, dir, name, "Магазин получает один и тот же вебхук дважды.\n")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Commit([]string{name}, "PIER-7: a bug",
		Author{Name: "Олег", Email: "oleg@example.com"}); err != nil {
		t.Fatal(err)
	}

	lines, err := repo.Blame(name)
	if err != nil {
		t.Fatalf("Blame on a non-ASCII path: %v", err)
	}
	if len(lines) != 1 || lines[0].Author != "Олег" {
		t.Errorf("blamed %+v", lines)
	}
	_ = filepath.Base(name)
}
