package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/vault/vaulttest"
)

var noon = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// newVault scaffolds a vault under git with one task in it.
func newVault(t *testing.T) (*Server, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Name: "Acme Platform", Template: vaulttest.Template(t)}); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	git(t, root, "init", "-q", "-b", "main")
	commit(t, root, "vault")

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Fix login redirect loop", Type: "bug", Priority: "high",
		Assignee: "agent/claude", Description: "The redirect never settles.", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}
	commit(t, root, "ACME-1")

	s, err := New(root, gitvcs.Author{Name: "Agent", Email: "agent@example.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.Now = func() time.Time { return noon.Add(time.Hour) }
	return s, root
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", message)
}

/* ---------- driving the server the way a client would ---------- */

// exchange sends the lines to a fresh server and returns the answers, so a test
// reads as the conversation a client would have.
func exchange(t *testing.T, s *Server, requests ...string) []response {
	t.Helper()

	var out bytes.Buffer
	if err := s.Serve(strings.NewReader(strings.Join(requests, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var answers []response
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r response
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("cannot parse answer %q: %v", line, err)
		}
		answers = append(answers, r)
	}
	return answers
}

func rpc(id int, method string, params any) string {
	raw, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	return string(raw)
}

func callTool(id int, name string, args map[string]any) string {
	return rpc(id, "tools/call", map[string]any{"name": name, "arguments": args})
}

// answer reads the text a tool put in its result.
func answer(t *testing.T, r response) string {
	t.Helper()
	if r.Error != nil {
		t.Fatalf("call failed: %s", r.Error.Message)
	}
	raw, err := json.Marshal(r.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("cannot read result %s: %v", raw, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected one piece of content, got %d in %s", len(result.Content), raw)
	}
	return result.Content[0].Text
}

/* ---------- the handshake ---------- */

func TestInitialiseAndList(t *testing.T) {
	s, _ := newVault(t)

	answers := exchange(t, s,
		rpc(1, "initialize", map[string]any{}),
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		rpc(2, "tools/list", map[string]any{}),
	)

	// The notification carries no id, so it is answered with silence.
	if len(answers) != 2 {
		t.Fatalf("expected two answers, got %d", len(answers))
	}

	raw, _ := json.Marshal(answers[0].Result)
	if !strings.Contains(string(raw), Protocol) {
		t.Errorf("initialize did not state the protocol: %s", raw)
	}

	raw, _ = json.Marshal(answers[1].Result)
	var listed struct {
		Tools []struct {
			Name        string
			Description string
			InputSchema map[string]any `json:"inputSchema"`
		}
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatal(err)
	}
	wanted := []string{"list_tasks", "get_task", "create_task", "update_task",
		"search", "read_page", "write_page", "check"}
	if len(listed.Tools) != len(wanted) {
		t.Fatalf("expected %d tools, got %d", len(wanted), len(listed.Tools))
	}
	for i, tool := range listed.Tools {
		if tool.Name != wanted[i] {
			t.Errorf("tool %d is %s, expected %s", i, tool.Name, wanted[i])
		}
		if tool.Description == "" || tool.InputSchema["type"] != "object" {
			t.Errorf("%s is not usable: %+v", tool.Name, tool)
		}
	}
}

func TestUnknownMethodIsAnError(t *testing.T) {
	s, _ := newVault(t)
	answers := exchange(t, s, rpc(1, "sing", map[string]any{}))
	if answers[0].Error == nil {
		t.Fatal("an unknown method should be refused")
	}
}

func TestGarbageDoesNotStopTheServer(t *testing.T) {
	s, _ := newVault(t)
	answers := exchange(t, s, "{not json", rpc(1, "ping", map[string]any{}))
	if len(answers) != 2 {
		t.Fatalf("expected a parse error and then the ping, got %d answers", len(answers))
	}
	if answers[0].Error == nil {
		t.Error("garbage should produce a parse error")
	}
	if answers[1].Error != nil {
		t.Errorf("the server stopped working after garbage: %s", answers[1].Error.Message)
	}
}

/* ---------- reading ---------- */

func TestListTasksNarrows(t *testing.T) {
	s, _ := newVault(t)

	answers := exchange(t, s,
		callTool(1, "list_tasks", map[string]any{}),
		callTool(2, "list_tasks", map[string]any{"assignee": "nobody"}),
	)

	var all []taskView
	if err := json.Unmarshal([]byte(answer(t, answers[0])), &all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Key != "ACME-1" {
		t.Fatalf("expected ACME-1, got %+v", all)
	}
	if all[0].Note != "ACME-1 Fix login redirect loop" {
		t.Errorf("a wikilink to the task would say %q", all[0].Note)
	}

	var none []taskView
	if err := json.Unmarshal([]byte(answer(t, answers[1])), &none); err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("nobody has no tasks, got %+v", none)
	}
}

func TestGetTaskCarriesEverythingNeededToWriteItBack(t *testing.T) {
	s, _ := newVault(t)

	var got struct {
		taskView
		Version     string
		Description string
		Reachable   []string `json:"reachable_statuses"`
	}
	answers := exchange(t, s, callTool(1, "get_task", map[string]any{"key": "ACME-1"}))
	if err := json.Unmarshal([]byte(answer(t, answers[0])), &got); err != nil {
		t.Fatal(err)
	}

	if got.Version == "" {
		t.Error("without a version an agent cannot write safely")
	}
	if !strings.Contains(got.Description, "redirect never settles") {
		t.Errorf("description is %q", got.Description)
	}
	if len(got.Reachable) == 0 {
		t.Error("an agent needs to know where the task can go")
	}
}

func TestSearchFindsTasksAndPages(t *testing.T) {
	s, root := newVault(t)

	if err := os.WriteFile(filepath.Join(root, "docs", "note.md"),
		[]byte("---\ntitle: Note\n---\n\nA redirect loop, once more.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	answers := exchange(t, s, callTool(1, "search", map[string]any{"query": "redirect loop"}))
	var hits []struct{ Key, Path string }
	if err := json.Unmarshal([]byte(answer(t, answers[0])), &hits); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected the task and the page, got %+v", hits)
	}
	if hits[0].Key != "ACME-1" || hits[1].Path != "docs/note.md" {
		t.Errorf("hits are %+v", hits)
	}
}

/* ---------- writing ---------- */

func TestCreateTaskCommits(t *testing.T) {
	s, root := newVault(t)

	answers := exchange(t, s, callTool(1, "create_task", map[string]any{
		"title": "Rotate the signing key", "type": "task", "priority": "normal",
	}))
	if !strings.Contains(answer(t, answers[0]), "ACME-2") {
		t.Fatalf("expected ACME-2 back, got %q", answer(t, answers[0]))
	}

	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-2 Rotate the signing key.md")); err != nil {
		t.Errorf("the file is not where the answer said: %v", err)
	}
	if author := lastAuthor(t, root); author != "Agent <agent@example.com>" {
		t.Errorf("the commit is by %q, not the agent", author)
	}
}

func TestUpdateTaskMovesStatusAndRenamesOnTitle(t *testing.T) {
	s, root := newVault(t)

	answers := exchange(t, s, callTool(1, "get_task", map[string]any{"key": "ACME-1"}))
	var got struct{ Version string }
	if err := json.Unmarshal([]byte(answer(t, answers[0])), &got); err != nil {
		t.Fatal(err)
	}

	answers = exchange(t, s, callTool(2, "update_task", map[string]any{
		"key": "ACME-1", "version": got.Version,
		"title": "Fix the login redirect", "status": "In progress",
		"comment": "Reproduced it.",
	}))
	if reply := answer(t, answers[0]); !strings.Contains(reply, "In progress") {
		t.Fatalf("update said %q", reply)
	}

	renamed := filepath.Join(root, "ACME", "ACME-1 Fix the login redirect.md")
	content, err := os.ReadFile(renamed)
	if err != nil {
		t.Fatalf("the file did not follow the title: %v", err)
	}
	if !strings.Contains(string(content), "Reproduced it.") {
		t.Error("the comment was not appended")
	}
	if _, err := os.Stat(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md")); !os.IsNotExist(err) {
		t.Error("the old name is still there")
	}
}

func TestUpdateTaskRefusesAStaleWrite(t *testing.T) {
	s, root := newVault(t)

	answers := exchange(t, s, callTool(1, "get_task", map[string]any{"key": "ACME-1"}))
	var got struct{ Version string }
	if err := json.Unmarshal([]byte(answer(t, answers[0])), &got); err != nil {
		t.Fatal(err)
	}

	// Somebody edits the same task in Obsidian.
	file := filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md")
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, []byte("\nA line from Obsidian.\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	answers = exchange(t, s, callTool(2, "update_task", map[string]any{
		"key": "ACME-1", "version": got.Version, "priority": "low",
	}))
	if answers[0].Error == nil {
		t.Fatal("a stale write should be refused")
	}

	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "A line from Obsidian.") {
		t.Error("the refused write landed anyway")
	}
}

// A vault with no transitions block lets a task go anywhere, which is what a
// vault should do before anybody has said otherwise.
func TestUpdateTaskAllowsAnyMoveWithoutAWorkflow(t *testing.T) {
	s, _ := newVault(t)

	answers := exchange(t, s, callTool(1, "update_task", map[string]any{
		"key": "ACME-1", "status": "Done",
	}))
	if reply := answer(t, answers[0]); !strings.Contains(reply, "Backlog → Done") {
		t.Errorf("update said %q", reply)
	}
}

func TestUpdateTaskRefusesAMoveTheWorkflowForbids(t *testing.T) {
	s, root := newVault(t)
	restrict(t, root, "transitions:\n  Backlog: [Ready]\n  Ready: [In progress]\n"+
		"  In progress: [In review]\n  In review: [Done]\n")

	answers := exchange(t, s, callTool(1, "update_task", map[string]any{
		"key": "ACME-1", "status": "Done",
	}))
	if answers[0].Error == nil {
		t.Fatal("Backlog → Done is not a transition this workflow allows")
	}
	if !strings.Contains(answers[0].Error.Message, "workflow") {
		t.Errorf("the refusal does not explain itself: %s", answers[0].Error.Message)
	}

	// And the move the workflow does allow still goes through.
	answers = exchange(t, s, callTool(2, "update_task", map[string]any{
		"key": "ACME-1", "status": "Ready",
	}))
	if reply := answer(t, answers[0]); !strings.Contains(reply, "Ready") {
		t.Errorf("update said %q", reply)
	}
}

// restrict appends a workflow to the vault's configuration.
func restrict(t *testing.T, root, workflow string) {
	t.Helper()
	file := filepath.Join(root, project.FileName)
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, append(content, []byte("\n"+workflow)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWritePageStaysUnderDocs(t *testing.T) {
	s, root := newVault(t)

	answers := exchange(t, s,
		callTool(1, "write_page", map[string]any{
			"path": "docs/decisions/keys", "title": "How keys are allocated", "body": "One per project.",
		}),
		callTool(2, "write_page", map[string]any{
			"path": "../escape", "title": "Nope", "body": "x",
		}),
		callTool(3, "read_page", map[string]any{"path": "docs/decisions/keys"}),
	)

	if !strings.Contains(answer(t, answers[0]), "docs/decisions/keys.md") {
		t.Errorf("write said %q", answer(t, answers[0]))
	}
	if answers[1].Error == nil {
		t.Error("a path outside the vault should be refused")
	}
	if page := answer(t, answers[2]); !strings.Contains(page, "One per project.") {
		t.Errorf("the page reads %q", page)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "decisions", "keys.md")); err != nil {
		t.Errorf("the page is not on disk: %v", err)
	}
}

func TestCheckReportsAVaultItCanRead(t *testing.T) {
	s, _ := newVault(t)
	answers := exchange(t, s, callTool(1, "check", map[string]any{}))
	if reply := answer(t, answers[0]); reply != "No findings." {
		t.Errorf("a freshly scaffolded vault should be clean, got:\n%s", reply)
	}
}

func lastAuthor(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%an <%ae>")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}
