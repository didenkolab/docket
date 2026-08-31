package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vadymdidenkolab/docket/internal/check"
	"github.com/vadymdidenkolab/docket/internal/snapshot"
)

var noon = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

// issue builds one Jira issue as the API returns it.
func issue(key, summary, status, category, issueType, priority string, extra map[string]any) map[string]any {
	fields := map[string]any{
		"summary":   summary,
		"issuetype": map[string]any{"name": issueType},
		"priority":  map[string]any{"name": priority},
		"project":   map[string]any{"key": strings.Split(key, "-")[0], "name": "Acme Platform"},
		"status": map[string]any{
			"name":           status,
			"statusCategory": map[string]any{"key": category},
		},
		"created": "2026-01-02T03:04:05.000+0000",
		"updated": "2026-02-03T04:05:06.000+0000",
		"labels":  []string{"auth"},
		"assignee": map[string]any{
			"accountId": "acc-1", "displayName": "Dana Example",
		},
	}
	for k, v := range extra {
		fields[k] = v
	}
	return map[string]any{"key": key, "fields": fields}
}

// buildSnapshot writes a small but complete snapshot to a temp directory.
func buildSnapshot(t *testing.T) *snapshot.Snapshot {
	t.Helper()

	snap, err := snapshot.Create(filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.SaveManifest(&snapshot.Manifest{
		Instance: "example.atlassian.net",
		Projects: []string{"ACME"},
		Spaces:   []string{"ENG"},
	}); err != nil {
		t.Fatal(err)
	}

	description := map[string]any{
		"type": "doc", "version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": "It loops."}},
		}},
	}

	issues := []map[string]any{
		issue("ACME-1", "Fix login redirect loop", "In Progress", "indeterminate", "Bug", "High",
			map[string]any{
				"description":       description,
				"customfield_10001": "Sprint 4",
				"customfield_10002": nil,
				"customfield_10003": []any{map[string]any{"value": "Payments"}, map[string]any{"value": "Auth"}},
			}),
		issue("ACME-2", "Session model", "Cancelled", "done", "Story", "Low", nil),
		issue("ACME-3", "Sub work", "To Do", "new", "Sub-task", "Low",
			map[string]any{"parent": map[string]any{"key": "ACME-1"}}),
	}
	for _, i := range issues {
		if err := snap.Append("issues/ACME.jsonl", i); err != nil {
			t.Fatal(err)
		}
	}

	comment := map[string]any{
		"author":  map[string]any{"accountId": "acc-1", "displayName": "Dana Example"},
		"created": "2026-03-04T05:06:07.000+0000",
		"body": map[string]any{
			"type": "doc", "version": 1,
			"content": []any{map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": "Reproduced."}},
			}},
		},
	}
	if err := snap.Append("comments/ACME.jsonl",
		map[string]any{"key": "ACME-1", "entry": comment}); err != nil {
		t.Fatal(err)
	}

	history := map[string]any{
		"id": "1", "created": "2026-01-05T00:00:00.000+0000",
		"items": []any{map[string]any{"field": "status", "fromString": "To Do", "toString": "In Progress"}},
	}
	if err := snap.Append("changelog/ACME.jsonl",
		map[string]any{"key": "ACME-1", "entry": history}); err != nil {
		t.Fatal(err)
	}

	if err := snap.WriteJSON("meta/fields.json", []map[string]any{
		{"id": "customfield_10001", "name": "Sprint", "custom": true},
		{"id": "customfield_10002", "name": "Story Points", "custom": true},
		{"id": "customfield_10003", "name": "Team", "custom": true},
		{"id": "summary", "name": "Summary", "custom": false},
	}); err != nil {
		t.Fatal(err)
	}

	pages := []map[string]any{
		{
			"id": "100", "title": "Engineering", "status": "current",
			"version": map[string]any{"number": 3},
			"body":    map[string]any{"storage": map[string]any{"value": "<p>The space home.</p>"}},
		},
		{
			"id": "101", "title": "Session model", "status": "current", "parentId": "100",
			"version": map[string]any{"number": 7},
			"body": map[string]any{"storage": map[string]any{
				"value": `<h2>How</h2><p>See <ac:link><ri:page ri:content-title="Engineering"/></ac:link>.</p>`,
			}},
		},
		{
			"id": "102", "title": "A draft", "status": "draft",
			"body": map[string]any{"storage": map[string]any{"value": "<p>nope</p>"}},
		},
	}
	for _, p := range pages {
		if err := snap.Append("pages/ENG.jsonl", p); err != nil {
			t.Fatal(err)
		}
	}
	return snap
}

func TestPlanProposesAWholeMapping(t *testing.T) {
	maps, report, err := Plan(buildSnapshot(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if report.Issues != 3 {
		t.Errorf("read %d issues, want 3", report.Issues)
	}

	// A cancelled status belongs to the done category, which is the whole
	// reason a status is a pair rather than a name.
	if got := maps.Statuses["Cancelled"]; got.Category != "done" || got.Name != "Cancelled" {
		t.Errorf("Cancelled maps to %+v, want name Cancelled in category done", got)
	}
	if got := maps.Statuses["In Progress"].Category; got != "doing" {
		t.Errorf("In Progress is in category %q, want doing", got)
	}
	if got := maps.Types["Sub-task"]; got.Name != "task" {
		t.Errorf("Sub-task maps to %+v, want task", got)
	}
	if got := maps.Types["Bug"]; got.Name != "bug" {
		t.Errorf("Bug maps to %+v", got)
	}
	if got := maps.People["acc-1"]; got != "dana_example" {
		t.Errorf("the person maps to %q", got)
	}
}

func TestPlanDropsFieldsNobodyFilledIn(t *testing.T) {
	maps, report, err := Plan(buildSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}

	if sprint := maps.Fields["customfield_10001"]; sprint.Drop || sprint.Property != "x_sprint" {
		t.Errorf("Sprint maps to %+v, want x_sprint and not dropped", sprint)
	}
	if points := maps.Fields["customfield_10002"]; !points.Drop {
		t.Errorf("Story Points has no values anywhere but was not proposed for dropping: %+v", points)
	}
	if report.UnusedFields != 1 {
		t.Errorf("UnusedFields = %d, want 1", report.UnusedFields)
	}
}

func TestMapsRoundTripThroughTheFile(t *testing.T) {
	maps, _, err := Plan(buildSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := maps.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, MapsFile))
	if !strings.Contains(string(raw), "personal data") {
		t.Error("the file does not warn that the people section is personal data")
	}

	loaded, err := LoadMaps(dir)
	if err != nil {
		t.Fatalf("LoadMaps: %v", err)
	}
	if loaded.Statuses["Cancelled"].Category != "done" {
		t.Error("a mapping was lost on the way through the file")
	}
}

func TestLoadMapsRejectsWhatCannotBeApplied(t *testing.T) {
	cases := map[string]string{
		"no statuses":         "statuses: {}\n",
		"bad category":        "statuses:\n  Open: {name: Open, category: pending}\n",
		"status with no name": "statuses:\n  Open: {name: \"\", category: todo}\n",
		"field neither mapped nor dropped": "statuses:\n  Open: {name: Open, category: todo}\n" +
			"fields:\n  customfield_1: {}\n",
		"unusable property": "statuses:\n  Open: {name: Open, category: todo}\n" +
			"fields:\n  customfield_1: {property: \"has spaces\"}\n",
	}
	for name, body := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, MapsFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadMaps(dir); err == nil {
			t.Errorf("%s: LoadMaps accepted it", name)
		}
	}
}

func TestStatusOrderIsTodoThenDoingThenDone(t *testing.T) {
	maps, _, err := Plan(buildSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}

	var categories []string
	for _, s := range maps.StatusOrder() {
		categories = append(categories, s.Category)
	}
	want := []string{"todo", "doing", "done"}
	if strings.Join(categories, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", categories, want)
	}
}

func TestApplyWritesAValidVault(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, err := Plan(snap)
	if err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(t.TempDir(), "vault")
	report, err := Apply(snap, maps, ApplyOptions{
		Root: root, Project: "ACME", Spaces: []string{"ENG"}, Now: noon,
	}, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if report.Tasks != 3 {
		t.Errorf("wrote %d tasks, want 3", report.Tasks)
	}
	if report.Pages != 2 {
		t.Errorf("wrote %d pages, want 2 — the draft should not be one", report.Pages)
	}

	findings, err := check.Run(root)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("the imported vault does not validate:\n%v", findings)
	}
}

func TestApplyCarriesTheDetailAcross(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := Apply(snap, maps, ApplyOptions{
		Root: root, Project: "ACME", Spaces: []string{"ENG"}, Now: noon,
	}, nil); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	for _, want := range []string{
		"key: ACME-1",
		"title: Fix login redirect loop",
		"type: bug",
		"status: In Progress",
		"status_category: doing",
		"x_sprint: Sprint 4",       // a custom field on an x_ property
		"x_team: [Payments, Auth]", // a multi-value field, flattened to scalars
		"assignee: dana_example",   // an account id turned into a handle
		"created: 2026-01-02T03:04:05Z",
		"It loops.", // the ADF description
		"## Comments",
		"**dana_example · 2026-03-04 05:06** — Reproduced.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "customfield_") {
		t.Errorf("a raw field id leaked into the task:\n%s", text)
	}
}

func TestApplyKeepsTheHistoryGitNeverSaw(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := Apply(snap, maps, ApplyOptions{Root: root, Project: "ACME", Now: noon}, nil); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "ACME", "_history", "1.jsonl"))
	if err != nil {
		t.Fatalf("no imported history: %v", err)
	}
	if !strings.Contains(string(raw), "In Progress") {
		t.Errorf("the history is empty of what happened:\n%s", raw)
	}
}

func TestApplyBuildsTheProjectVocabularyFromTheMaps(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := Apply(snap, maps, ApplyOptions{Root: root, Project: "ACME", Now: noon}, nil); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "docket.yaml"))
	text := string(raw)
	for _, want := range []string{"key: ACME", "name: Acme Platform", "Cancelled", "In Progress", "bug", "story"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in docket.yaml:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Backlog") {
		t.Errorf("the scaffold's default statuses survived the import:\n%s", text)
	}
}

func TestApplyPlacesPagesUnderTheirParents(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := Apply(snap, maps, ApplyOptions{
		Root: root, Project: "ACME", Spaces: []string{"ENG"}, Now: noon,
	}, nil); err != nil {
		t.Fatal(err)
	}

	nested := filepath.Join(root, "docs", "ENG", "Engineering", "Session model.md")
	raw, err := err2(os.ReadFile(nested))
	if err != nil {
		t.Fatalf("the page is not under its parent: %v", err)
	}
	if !strings.Contains(string(raw), "[[Engineering]]") {
		t.Errorf("a page-to-page link did not become a wikilink:\n%s", raw)
	}
}

func TestApplyRefusesAProjectKeyAVaultCannotUse(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)

	if _, err := Apply(snap, maps, ApplyOptions{
		Root: t.TempDir(), Project: "not-a-key", Now: noon,
	}, nil); err == nil {
		t.Error("Apply accepted a key a vault cannot use")
	}
}

func TestApplyStopsOnAStatusTheMapsDoNotCover(t *testing.T) {
	snap := buildSnapshot(t)
	maps, _, _ := Plan(snap)
	delete(maps.Statuses, "Cancelled")

	if _, err := Apply(snap, maps, ApplyOptions{
		Root: filepath.Join(t.TempDir(), "v"), Project: "ACME", Now: noon,
	}, nil); err == nil {
		t.Error("Apply silently invented a status")
	}
}

// ---- extract, against a stub instance ----

func TestExtractPagesThroughAndResumes(t *testing.T) {
	var searches int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case strings.HasPrefix(r.URL.Path, "/rest/api/3/search/jql"):
			searches++
			if r.URL.Query().Get("nextPageToken") == "" {
				fmt.Fprint(w, `{"issues":[`+string(mustJSON(t,
					issue("ACME-1", "One", "To Do", "new", "Task", "Low", nil)))+
					`],"nextPageToken":"page2","isLast":false}`)
				return
			}
			fmt.Fprint(w, `{"issues":[`+string(mustJSON(t,
				issue("ACME-2", "Two", "To Do", "new", "Task", "Low", nil)))+
				`],"isLast":true}`)

		case strings.Contains(r.URL.Path, "/changelog"):
			fmt.Fprint(w, `{"values":[],"isLast":true,"total":0}`)
		case strings.Contains(r.URL.Path, "/comment"):
			fmt.Fprint(w, `{"comments":[],"total":0}`)
		case r.URL.Path == "/rest/api/3/field":
			fmt.Fprint(w, `[{"id":"customfield_1","name":"Sprint","custom":true}]`)
		default:
			fmt.Fprint(w, `[]`)
		}
	}))
	defer server.Close()

	dir := filepath.Join(t.TempDir(), "snap")
	snap, err := snapshot.Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.URL, "me@example.com", "token")

	opts := ExtractOptions{Projects: []string{"ACME"}, Now: noon}
	if err := Extract(context.Background(), client, snap, opts, nil); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	count := 0
	if err := snap.Each("issues/ACME.jsonl", func(json.RawMessage) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("extracted %d issues, want 2 — pagination did not follow the token", count)
	}

	// A second run must skip work the manifest already records as done.
	before := searches
	if err := Extract(context.Background(), client, snap, opts, nil); err != nil {
		t.Fatalf("second Extract: %v", err)
	}
	if searches != before {
		t.Errorf("the second run searched again: %d calls, want %d", searches, before)
	}
}

func TestExtractRetriesAThrottledRequest(t *testing.T) {
	var attempts int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/rest/api/3/search/jql") {
			attempts++
			if attempts == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			fmt.Fprint(w, `{"issues":[],"isLast":true}`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()

	snap, err := snapshot.Create(filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(server.URL, "me@example.com", "token")
	client.Backoff = time.Millisecond

	if err := Extract(context.Background(), client, snap,
		ExtractOptions{Projects: []string{"ACME"}, Now: noon}, nil); err != nil {
		t.Fatalf("a rate limit was not survived: %v", err)
	}
	if attempts < 2 {
		t.Errorf("attempts = %d, want the throttled request retried", attempts)
	}
}

func TestBadCredentialsAreNotRetriedForever(t *testing.T) {
	var attempts int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClient(server.URL, "me@example.com", "wrong")
	client.Backoff = time.Millisecond

	err := client.Get(context.Background(), "/rest/api/3/field", nil, new(any))
	if err == nil {
		t.Fatal("bad credentials were accepted")
	}
	if !strings.Contains(err.Error(), "API token") {
		t.Errorf("the error does not say what to check: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 — retrying a rejected token cannot help", attempts)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func err2[T any](v T, err error) (T, error) { return v, err }

// A vocabulary that is not English.
//
// Every type of a real project came out of the mapping as the empty string,
// because slugging was `[^a-z0-9]+` — which is every character of a name
// written in anything but Latin. And every epic came out at level nought,
// because the level was left for a person to write on the grounds that guessing
// is wrong. Jira states the hierarchy on each type, so it was never a guess.
func TestPlanKeepsANonLatinVocabularyAndItsLevels(t *testing.T) {
	snap, err := snapshot.Create(filepath.Join(t.TempDir(), "snap"))
	if err != nil {
		t.Fatal(err)
	}
	if err := snap.SaveManifest(&snapshot.Manifest{
		Instance: "example.atlassian.net", Projects: []string{"PIER"},
	}); err != nil {
		t.Fatal(err)
	}

	// The source states its hierarchy on every type, which is why the level can
	// be read rather than guessed.
	levelled := func(key, kind string, level int, status, category string) map[string]any {
		one := issue(key, "Что-то", status, category, kind, "Обычный", nil)
		one["fields"].(map[string]any)["issuetype"] = map[string]any{
			"name": kind, "hierarchyLevel": level,
		}
		return one
	}
	for _, one := range []map[string]any{
		levelled("PIER-1", "Эпик", 1, "В работе", "indeterminate"),
		levelled("PIER-2", "История", 0, "Готово", "done"),
		levelled("PIER-3", "Подзадача", -1, "К выполнению", "new"),
		// An English name still maps onto the vocabulary the boards use.
		levelled("PIER-4", "Bug", 0, "Готово", "done"),
	} {
		if err := snap.Append("issues/PIER.jsonl", one); err != nil {
			t.Fatal(err)
		}
	}

	maps, _, err := Plan(snap)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	for _, c := range []struct {
		source, name string
		level        int
	}{
		{"Эпик", "Эпик", 1},
		{"История", "История", 0},
		{"Подзадача", "Подзадача", -1},
		{"Bug", "bug", 0},
	} {
		got := maps.Types[c.source]
		if got.Name != c.name {
			t.Errorf("%s maps to %q, want %q — a name in another script must survive",
				c.source, got.Name, c.name)
		}
		if got.Level != c.level {
			t.Errorf("%s is at level %d, want %d — the source states it", c.source, got.Level, c.level)
		}
	}

	if got := maps.Statuses["К выполнению"]; got.Name != "К выполнению" || got.Category != "todo" {
		t.Errorf("К выполнению maps to %+v", got)
	}
	// And a priority in another script is no longer slugged away to nothing.
	if got := maps.Priorities["Обычный"]; got == "" {
		t.Error("a priority written in Cyrillic became the empty string")
	}
}

// A page name is cut at a character, not at a byte.
//
// The limit was applied with a plain slice, which is the same thing in English
// and is not in anything else: a Cyrillic letter is two bytes, so a long title
// was cut inside one and the file system refused the name outright — "illegal
// byte sequence", on a real Confluence space whose pages are titled in Russian.
func TestAPageNameIsCutAtACharacter(t *testing.T) {
	// One ASCII character first, so the hundred-and-twentieth byte lands inside
	// a letter rather than between two. Without the offset every cut is on a
	// boundary by luck and a byte-wise slice looks correct — which is how the
	// first version of this test passed against the bug it was written for.
	long := "x" + strings.Repeat("я", 200)
	got := fileSlug(long)

	if !utf8.ValidString(got) {
		t.Errorf("the name is not valid UTF-8: %q", got)
	}
	if len(got) > 120 {
		t.Errorf("the name is %d bytes, and a file system counts bytes", len(got))
	}
	if got == "" {
		t.Error("the name is empty, so every long title would collide")
	}

	// The characters that a path cannot hold are replaced rather than dropped,
	// and a title that is nothing but those still gets a name.
	for _, c := range []struct{ what, title, want string }{
		{"a slash would make a folder", "one/two", "one-two"},
		{"a colon is refused by one file system or another", "a: b", "a - b"},
		{"wikilink syntax cannot be linked to", "a [b] #c", "a (b) c"},
		{"nothing usable at all", "###", "untitled"},
		{"nothing at all", "   ", "untitled"},
	} {
		if got := fileSlug(c.title); got != c.want {
			t.Errorf("%s: %q became %q, want %q", c.what, c.title, got, c.want)
		}
	}
}
