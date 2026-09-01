package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
)

// An app's second version changes its own type, and that is not a conflict with
// the vault — it is what an upgrade is. Without this the installer could only
// refuse, and the only way forward would be editing docket.yaml by hand.
func TestAnAppMayReplaceWhatItItselfBrought(t *testing.T) {
	root, c := vaultAt(t)

	first := pack(t, `name: tests
version: "1"
description: Test management
vocabulary:
  types:
    - name: test_run
  fields:
    - name: result
      kind: text
`, map[string]string{"templates/run.md": "---\ntype: test_run\n---\n\nfirst\n"})

	p, err := Read(first, first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, c, p); err != nil {
		t.Fatal(err)
	}

	after, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Apps) != 1 || len(after.Apps[0].Brought.Types) != 1 {
		t.Fatalf("the vault did not record what the app brought: %+v", after.Apps)
	}

	// The second version moves its own type below the line and changes the kind
	// of its own field — both refusals if they were somebody else's.
	second := pack(t, `name: tests
version: "2"
description: Test management
vocabulary:
  types:
    - name: test_run
      level: -1
  fields:
    - name: result
      kind: choice
      choices: [passed, failed]
`, map[string]string{"templates/run.md": "---\ntype: test_run\n---\n\nsecond\n"})

	next, err := Read(second, second)
	if err != nil {
		t.Fatal(err)
	}
	if conflicts := Check(root, after, next); len(conflicts) > 0 {
		t.Fatalf("an app was refused its own upgrade: %v", reasons(conflicts))
	}
	if _, err := Install(root, after, next); err != nil {
		t.Fatal(err)
	}

	upgraded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ty := range upgraded.Types {
		if ty.Name == "test_run" && ty.Level != -1 {
			t.Errorf("the type is still at level %d", ty.Level)
		}
	}
	for _, f := range upgraded.Fields {
		if f.Name == "result" && f.Kind != "choice" {
			t.Errorf("the field is still %s", f.Kind)
		}
	}
	if upgraded.Apps[0].Version != "2" {
		t.Errorf("the vault still records version %s", upgraded.Apps[0].Version)
	}
	body, err := os.ReadFile(filepath.Join(root, "templates", "run.md"))
	if err != nil || !strings.Contains(string(body), "second") {
		t.Errorf("the app's own file was not replaced: %q %v", body, err)
	}
}

// Somebody else's is still somebody else's.
func TestAnAppMayNotReplaceWhatAnotherAppBrought(t *testing.T) {
	root, c := vaultAt(t)

	theirs := pack(t, "name: theirs\nvocabulary:\n  fields:\n    - name: result\n      kind: text\n", nil)
	p, err := Read(theirs, theirs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, c, p); err != nil {
		t.Fatal(err)
	}
	after, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	mine := pack(t, "name: mine\nvocabulary:\n  fields:\n    - name: result\n      kind: number\n", nil)
	next, err := Read(mine, mine)
	if err != nil {
		t.Fatal(err)
	}
	conflicts := Check(root, after, next)
	if len(conflicts) == 0 {
		t.Fatal("one app took over another's field")
	}
	if !strings.Contains(strings.Join(reasons(conflicts), " "), "result") {
		t.Errorf("refused with %v", reasons(conflicts))
	}
}
