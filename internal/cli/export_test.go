package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Three of the marketplace's top hundred are checklists, and the list is
// already Markdown in the body. Counting it here means every app that wants
// progress on a card does not write this loop again — and gets the same answer
// Obsidian draws.
func TestAnAcceptanceListIsCounted(t *testing.T) {
	for _, c := range []struct {
		what           string
		body           string
		checked, total int
	}{
		{"nothing at all", "Just prose.\n", 0, 0},
		{"an empty list", "- [ ] one\n- [ ] two\n", 0, 2},
		{"half of it", "- [ ] one\n- [x] two\n", 1, 2},
		{"a capital X, which Obsidian also ticks", "- [X] one\n", 1, 1},
		{"an ordinary bullet is not a box", "- one\n- [ ] two\n", 0, 1},
		{"a box in an example is an example",
			"- [x] real\n```\n- [ ] not real\n- [x] nor this\n```\n", 1, 1},
		{"indented, as a nested list is", "  - [x] one\n", 1, 1},
	} {
		checked, total := boxes(c.body)
		if checked != c.checked || total != c.total {
			t.Errorf("%s: %d of %d, want %d of %d", c.what, checked, total, c.checked, c.total)
		}
	}
}

// An app reads columns by name because a title may hold a comma. Every name the
// flag accepts has to be a column that exists.
func TestEveryNamedColumnCanBeRead(t *testing.T) {
	for _, name := range everyColumn {
		if _, ok := column[name]; !ok {
			t.Errorf("%q is offered and cannot be read", name)
		}
	}
	for _, name := range alsoAColumn {
		if _, ok := column[name]; !ok {
			t.Errorf("%q is readable by name but not offered", name)
		}
	}
}

// `docket.yaml` can say a type is a record rather than work — `board: false` —
// and until now the export did not pass that on, so an app computing who is
// carrying what counted every machine-written run as somebody's backlog. The
// column answers it once, in the place that already reads the vocabulary.
func TestTheExportSaysWhetherATypeIsOnTheBoard(t *testing.T) {
	dir := vaultDir(t)

	config := filepath.Join(dir, "docket.yaml")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	// A type the vault keeps out of its columns, declared the way an app does.
	changed := strings.Replace(string(raw), "types:\n",
		"types:\n  - name: test_run\n    level: -1\n    board: false\n", 1)
	if changed == string(raw) {
		t.Fatalf("the scaffolded vault declares no types:\n%s", raw)
	}
	if err := os.WriteFile(config, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, _, stderr := run(t, "new", "-C", dir, "Ordinary work"); code != exitOK {
		t.Fatalf("new: %s", stderr)
	}
	if code, _, stderr := run(t, "new", "-C", dir, "A run", "--type", "test_run"); code != exitOK {
		t.Fatalf("new: %s", stderr)
	}

	code, stdout, stderr := run(t, "export", "--format", "csv",
		"--fields", "key,type,board", dir)
	if code != exitOK {
		t.Fatalf("export: exit %d; stderr:\n%s", code, stderr)
	}
	want := "key,type,board\nACME-1,task,true\nACME-2,test_run,false\n"
	if got := strings.ReplaceAll(stdout, "\r\n", "\n"); got != want {
		t.Errorf("csv is\n%q\nwant\n%q", got, want)
	}

	code, stdout, stderr = run(t, "export", "--format", "json", dir)
	if code != exitOK {
		t.Fatalf("export json: exit %d; stderr:\n%s", code, stderr)
	}
	// JSON carries the same answer as a boolean, and carries it for every task:
	// omitting false would read as "not on the board" for ordinary work.
	for _, want := range []string{`"type": "task"`, `"type": "test_run"`,
		`"board": true`, `"board": false`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("json does not say %s:\n%s", want, stdout)
		}
	}
}
