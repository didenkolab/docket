package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A link naming a title its task no longer has is a finding.
//
// It is the one way a vault can be valid here and broken in Obsidian: the key
// inside the link still resolves, so every rule that matched on the key saw
// nothing wrong, while Obsidian resolves note names and drew no edge at all.
// `check --fix` renaming a retitled file used to leave every link to it in
// exactly this state and then report the vault clean (DKT-61).
func TestALinkNamingAnOldTitleIsAFinding(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-2 The session model, rewritten.md",
		strings.Replace(strings.Replace(validTask,
			"key: ACME-1", "key: ACME-2", 1),
			"title: A task", "title: The session model, rewritten", 1))
	put(t, root, "ACME-1 A task.md",
		strings.Replace(validTask, "labels: []",
			"labels: []\nblocked_by: [\"[[ACME-2 Session model]]\"]", 1))

	var found *Finding
	for i, f := range run(t, root) {
		if strings.Contains(f.Message, "names a title that task no longer has") {
			found = &run(t, root)[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("a link naming a title nothing carries was not reported:\n%v", run(t, root))
	}
	if !strings.Contains(found.Message, "[[ACME-2 The session model, rewritten]]") {
		t.Errorf("the finding does not say what to write instead: %s", found.Message)
	}
}

// A link that is already right is not a finding, and neither is a link to a
// page, which has no key to look up.
func TestALinkThatIsRightIsLeftAlone(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-2 Another task.md",
		strings.Replace(strings.Replace(validTask,
			"key: ACME-1", "key: ACME-2", 1),
			"title: A task", "title: Another task", 1))
	put(t, root, "ACME-1 A task.md",
		strings.Replace(validTask, "labels: []",
			"labels: [\"[[auth]]\"]\nblocked_by: [\"[[ACME-2 Another task]]\"]", 1))

	for _, f := range run(t, root) {
		if strings.Contains(f.Message, "names a title") {
			t.Errorf("a correct link was reported: %s", f.Message)
		}
	}
}

// Relink puts a link that names an old title back on the note.
//
// Run after the rename, which is the order that matters: a link names a note,
// so the names have to be right before the links are written.
func TestRelinkRewritesALinkNamingAnOldTitle(t *testing.T) {
	root := newVault(t)
	put(t, root, "ACME-2 The session model, rewritten.md",
		strings.Replace(strings.Replace(validTask,
			"key: ACME-1", "key: ACME-2", 1),
			"title: A task", "title: The session model, rewritten", 1))
	path := "ACME-1 A task.md"
	put(t, root, path,
		strings.Replace(validTask, "labels: []",
			"labels: []\nblocked_by: [\"[[ACME-2 Session model]]\"]", 1))

	written, err := Relink(root)
	if err != nil {
		t.Fatalf("Relink: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("rewrote %d files, want 1: %v", len(written), written)
	}

	raw, err := os.ReadFile(filepath.Join(root, "ACME", path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[[ACME-2 The session model, rewritten]]") {
		t.Errorf("the link was not put back on the note:\n%s", raw)
	}
	if got := run(t, root); len(got) != 0 {
		t.Errorf("the vault is still not clean: %v", got)
	}
}
