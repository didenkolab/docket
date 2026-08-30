package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
)

// newVault scaffolds a vault and loads its project, which is the state every
// task test starts from.
func newVault(t *testing.T) (root string, p *project.Project) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "vault")
	if _, err := Init(root, Options{Key: "ACME", Name: "Acme Platform"}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return root, p
}

var noon = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func TestCreateFillsTheDefaults(t *testing.T) {
	root, p := newVault(t)

	path, task, err := Create(root, p, NewOptions{Title: "Fix login redirect loop", Now: noon})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if path != "tasks/ACME-1.md" {
		t.Errorf("path = %q, want tasks/ACME-1.md", path)
	}
	if task.Key != "ACME-1" {
		t.Errorf("key = %q — the typed view was not refreshed after the edits", task.Key)
	}
	if task.Type != "task" {
		t.Errorf("type = %q, want the project's first type", task.Type)
	}
	if task.Status != "Backlog" || task.StatusCategory != "todo" {
		t.Errorf("status = %q/%q, want Backlog/todo", task.Status, task.StatusCategory)
	}
	if task.Priority != "normal" {
		t.Errorf("priority = %q, want normal", task.Priority)
	}
	if task.Created != "2026-08-30T12:00:00Z" || task.Updated != task.Created {
		t.Errorf("timestamps = %q/%q", task.Created, task.Updated)
	}
}

func TestCreateWritesWhatTheSpecShows(t *testing.T) {
	root, p := newVault(t)
	if _, _, err := Create(root, p, NewOptions{
		Title:    "Fix login redirect loop",
		Type:     "bug",
		Priority: "high",
		Assignee: "agent/claude",
		Labels:   []string{"auth", "regression"},
		Now:      noon,
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "tasks", "ACME-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"key: ACME-1\n",
		"title: Fix login redirect loop\n",
		"type: bug\n",
		"labels: [auth, regression]\n",
		"created: 2026-08-30T12:00:00Z\n",
		"aliases: []\n",
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "parent:") {
		t.Errorf("an empty parent was written:\n%s", raw)
	}
}

func TestCreateNumbersUpwards(t *testing.T) {
	root, p := newVault(t)
	for i := 1; i <= 3; i++ {
		path, _, err := Create(root, p, NewOptions{Title: "Task", Now: noon})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if want := filepath.ToSlash(filepath.Join("tasks", "ACME-"+itoa(i)+".md")); path != want {
			t.Errorf("path = %q, want %q", path, want)
		}
	}
}

func TestNextKeySkipsGaps(t *testing.T) {
	root, p := newVault(t)
	// A deleted ACME-2 must not be handed out again: keys are permanent.
	for _, key := range []string{"ACME-1", "ACME-7"} {
		write(t, root, key)
	}

	got, err := NextKey(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ACME-8" {
		t.Errorf("NextKey = %q, want ACME-8", got)
	}
}

func TestNextKeyIgnoresOtherProjects(t *testing.T) {
	root, p := newVault(t)
	write(t, root, "OTHER-99")

	got, err := NextKey(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ACME-1" {
		t.Errorf("NextKey = %q, want ACME-1", got)
	}
}

func TestCreateRefusesValuesTheProjectDoesNotDefine(t *testing.T) {
	root, p := newVault(t)

	for name, opts := range map[string]NewOptions{
		"no title":         {Title: "  "},
		"unknown type":     {Title: "T", Type: "saga"},
		"unknown status":   {Title: "T", Status: "Pending"},
		"unknown priority": {Title: "T", Priority: "screaming"},
		"missing parent":   {Title: "T", Parent: "ACME-404"},
	} {
		if _, _, err := Create(root, p, opts); err == nil {
			t.Errorf("%s: Create accepted it", name)
		}
	}
	if entries, _ := List(root); len(entries) != 0 {
		t.Errorf("a rejected task was written anyway: %v", entries)
	}
}

func TestWriteNewWillNotOverwrite(t *testing.T) {
	// This is the guard against another process picking the same key between
	// the scan in NextKey and the write.
	path := filepath.Join(t.TempDir(), "tasks", "ACME-2.md")
	if err := writeNew(path, []byte("first")); err != nil {
		t.Fatalf("the first write failed: %v", err)
	}

	if err := writeNew(path, []byte("second")); err == nil {
		t.Fatal("the second write replaced the first")
	}
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Errorf("the existing file was changed: %q", got)
	}
}

func TestCreateUsesTheVaultsOwnTemplate(t *testing.T) {
	root, p := newVault(t)
	custom := "---\nkey:\ntitle:\ntype:\nstatus:\nstatus_category:\npriority:\n" +
		"assignee:\nlabels: []\ncreated:\nupdated:\naliases: []\npoints: 0\n---\n\nCustom body.\n"
	if err := os.WriteFile(filepath.Join(root, "templates", "task.md"), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Create(root, p, NewOptions{Title: "T", Now: noon}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, "tasks", "ACME-1.md"))
	if !strings.Contains(string(raw), "points: 0") {
		t.Errorf("a project's own property was dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), "Custom body.") {
		t.Errorf("the template body was dropped:\n%s", raw)
	}
}

func TestCreateWorksWithoutATemplate(t *testing.T) {
	root, p := newVault(t)
	if err := os.Remove(filepath.Join(root, "templates", "task.md")); err != nil {
		t.Fatal(err)
	}

	if _, task, err := Create(root, p, NewOptions{Title: "T", Now: noon}); err != nil {
		t.Fatalf("Create without a template: %v", err)
	} else if task.Key != "ACME-1" {
		t.Errorf("key = %q", task.Key)
	}
}

func TestListSortsByNumberNotByName(t *testing.T) {
	root, _ := newVault(t)
	for _, key := range []string{"ACME-2", "ACME-10", "ACME-1"} {
		write(t, root, key)
	}

	entries, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ACME-1", "ACME-2", "ACME-10"}
	for i, key := range want {
		if entries[i].Key != key {
			t.Errorf("entry %d = %q, want %q (lexical order would say otherwise)",
				i, entries[i].Key, key)
		}
	}
}

func TestListReportsBrokenFilesInsteadOfStopping(t *testing.T) {
	root, _ := newVault(t)
	write(t, root, "ACME-1")
	if err := os.WriteFile(filepath.Join(root, "tasks", "ACME-2.md"),
		[]byte("no frontmatter here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, root, "ACME-3")

	entries, err := List(root)
	if err != nil {
		t.Fatalf("List stopped at a broken file: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	if entries[1].Err == nil {
		t.Error("the broken file was reported as fine")
	}
	if entries[2].Err != nil {
		t.Error("a good file after a broken one was not parsed")
	}
}

func TestListOnAVaultWithoutTasks(t *testing.T) {
	root, _ := newVault(t)
	if err := os.RemoveAll(filepath.Join(root, "tasks")); err != nil {
		t.Fatal(err)
	}
	entries, err := List(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("List = %v, %v; want no entries and no error", entries, err)
	}
}

// write puts a minimal valid task in the vault.
func write(t *testing.T, root, key string) {
	t.Helper()
	body := "---\nkey: " + key + "\ntitle: " + key + "\ntype: task\nstatus: Backlog\n" +
		"status_category: todo\npriority: normal\nassignee:\nlabels: []\n" +
		"created: 2026-08-30T12:00:00Z\nupdated: 2026-08-30T12:00:00Z\naliases: []\n---\n"
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", key+".md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
