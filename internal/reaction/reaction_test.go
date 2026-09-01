package reaction

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A reaction is a program in the repository, told what happened on stdin, and
// what it writes is a file like any other.
func TestAReactionIsRunAndToldWhatHappened(t *testing.T) {
	root := t.TempDir()
	script(t, root, "hooks/note.sh", `#!/bin/sh
cat > "$DOCKET_ROOT/what-it-was-told.json"
echo "wrote it"
`)

	results := Run(context.Background(), root,
		[]Declared{{On: OnMoved, Run: "hooks/note.sh"}},
		Event{Event: OnMoved, Key: "ACME-1", To: "Done", Who: "marina"})

	if len(results) != 1 {
		t.Fatalf("%d reactions ran", len(results))
	}
	if results[0].Err != nil {
		t.Fatalf("it failed: %v — %s", results[0].Err, results[0].Output)
	}
	if results[0].Output != "wrote it" {
		t.Errorf("it said %q", results[0].Output)
	}

	raw, err := os.ReadFile(filepath.Join(root, "what-it-was-told.json"))
	if err != nil {
		t.Fatal(err)
	}
	var told Event
	if err := json.Unmarshal(raw, &told); err != nil {
		t.Fatalf("what it was told is not JSON: %v — %s", err, raw)
	}
	if told.Key != "ACME-1" || told.To != "Done" || told.Who != "marina" {
		t.Errorf("it was told %+v", told)
	}
}

// Which event, which status, which project — a reaction that ran on everything
// would be a reaction nobody could aim.
func TestAReactionRunsOnlyForWhatItAsksFor(t *testing.T) {
	for _, c := range []struct {
		what     string
		declared Declared
		event    Event
		wants    bool
	}{
		{"its event", Declared{On: OnMoved}, Event{Event: OnMoved}, true},
		{"another event", Declared{On: OnMoved}, Event{Event: OnCreated}, false},
		{"the status it named", Declared{On: OnMoved, Status: "Done"},
			Event{Event: OnMoved, To: "Done"}, true},
		{"another status", Declared{On: OnMoved, Status: "Done"},
			Event{Event: OnMoved, To: "In review"}, false},
		{"the project it named", Declared{On: OnMoved, Project: "ACME"},
			Event{Event: OnMoved, Project: "ACME"}, true},
		{"another project", Declared{On: OnMoved, Project: "ACME"},
			Event{Event: OnMoved, Project: "ACME"}, false},
	} {
		if got := c.declared.Wants(c.event); got != c.wants {
			t.Errorf("%s: %v, want %v", c.what, got, c.wants)
		}
	}
}

// What may not be declared. Every one of these is a way for a reaction to stop
// being the reviewed thing in the repository.
func TestAReactionCannotEscapeTheRepository(t *testing.T) {
	for _, c := range []struct {
		what     string
		declared Declared
		mentions string
	}{
		{"an absolute path", Declared{On: OnMoved, Run: "/usr/bin/curl"}, "absolute path"},
		{"a path out", Declared{On: OnMoved, Run: "../../evil.sh"}, "outside the repository"},
		{"a shell command", Declared{On: OnMoved, Run: "sh -c 'rm -rf /'"}, "no shell"},
		{"a pipeline", Declared{On: OnMoved, Run: "hooks/x.sh | tee /tmp/y"}, "no shell"},
		{"an event nobody has", Declared{On: "task.exploded", Run: "hooks/x.sh"}, "not an event"},
		{"nothing to run", Declared{On: OnMoved}, "nothing to run"},
	} {
		err := c.declared.Validate()
		if err == nil {
			t.Errorf("%s was accepted", c.what)
			continue
		}
		if !strings.Contains(err.Error(), c.mentions) {
			t.Errorf("%s: refused with %q, which does not say %q", c.what, err, c.mentions)
		}
	}
}

// A title is somebody's text. Run through a shell it would be a command; run
// directly it is a string in a JSON document and nothing else.
func TestATitleCannotBecomeACommand(t *testing.T) {
	root := t.TempDir()
	script(t, root, "hooks/echo.sh", `#!/bin/sh
cat > "$DOCKET_ROOT/told.json"
`)

	results := Run(context.Background(), root,
		[]Declared{{On: OnCreated, Run: "hooks/echo.sh"}},
		Event{Event: OnCreated, Key: "ACME-2",
			Title: `"; touch /tmp/docket-pwned; echo "`})

	if results[0].Err != nil {
		t.Fatalf("%v — %s", results[0].Err, results[0].Output)
	}
	raw, err := os.ReadFile(filepath.Join(root, "told.json"))
	if err != nil {
		t.Fatal(err)
	}
	var told Event
	if err := json.Unmarshal(raw, &told); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if told.Title != `"; touch /tmp/docket-pwned; echo "` {
		t.Errorf("the title arrived as %q", told.Title)
	}
	if _, err := os.Stat("/tmp/docket-pwned"); err == nil {
		os.Remove("/tmp/docket-pwned")
		t.Fatal("a title became a command")
	}
}

// A program that is not executable is a declaration somebody got wrong, and
// saying so is more use than a permission error from the kernel.
func TestANonExecutableIsSaidPlainly(t *testing.T) {
	root := t.TempDir()
	at := filepath.Join(root, "hooks")
	if err := os.MkdirAll(at, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(at, "plain.sh"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	results := Run(context.Background(), root,
		[]Declared{{On: OnMoved, Run: "hooks/plain.sh"}}, Event{Event: OnMoved})
	if results[0].Err == nil {
		t.Fatal("a file nobody can run was run")
	}
	if !strings.Contains(results[0].Err.Error(), "chmod +x") {
		t.Errorf("said %q", results[0].Err)
	}
}

func script(t *testing.T, root, rel, body string) {
	t.Helper()
	at := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}
