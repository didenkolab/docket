package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// newServer scaffolds a vault, puts it under git, adds a task, and serves it.
func newServer(t *testing.T) (*Server, http.Handler, string) {
	t.Helper()

	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Name: "Acme Platform", Template: vaulttest.Template(t)}); err != nil {
		t.Fatalf("vault.Init: %v", err)
	}
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "vault")

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Fix login redirect loop", Type: "bug", Priority: "high",
		Assignee: "agent/claude", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "ACME-1")

	s, err := New(root, Options{Author: gitvcs.Author{Name: "Server", Email: "server@example.com"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.now = func() time.Time { return noon.Add(time.Hour) }

	return s, s.Handler(), root
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func lastCommit(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "log", "-1", "--format=%an <%ae> | %s")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func postForm(t *testing.T, h http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// currentVersion reads the fingerprint the server would hand a client.
func currentVersion(t *testing.T, s *Server, key string) string {
	t.Helper()
	_, v, err := s.loadTask(key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBoardShowsEveryColumn(t *testing.T) {
	_, h, _ := newServer(t)

	w := get(t, h, "/")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"Backlog", "In progress", "Done", "ACME-1", "Fix login redirect loop"} {
		if !strings.Contains(body, want) {
			t.Errorf("the board does not mention %q", want)
		}
	}
}

func TestTaskPageCarriesTheVersion(t *testing.T) {
	s, h, _ := newServer(t)

	body := get(t, h, "/task/ACME-1").Body.String()
	if !strings.Contains(body, currentVersion(t, s, "ACME-1")) {
		t.Error("the page does not carry the version a write is checked against")
	}
	if !strings.Contains(body, "ACME-1 Fix login redirect loop.md") {
		t.Error("the page does not name the file behind it")
	}
}

func TestUnknownTaskIsNotFound(t *testing.T) {
	_, h, _ := newServer(t)
	if w := get(t, h, "/task/ACME-404"); w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestMoveWritesAndCommits(t *testing.T) {
	s, h, root := newServer(t)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{
		"version": {currentVersion(t, s, "ACME-1")},
		"status":  {"In progress"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303; body:\n%s", w.Code, w.Body)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if !strings.Contains(string(raw), "status: In progress") {
		t.Errorf("the status was not written:\n%s", raw)
	}
	if !strings.Contains(string(raw), "status_category: doing") {
		t.Errorf("the category did not move with the status:\n%s", raw)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "Server <server@example.com>") {
		t.Errorf("commit = %q, want it attributed to the server's author", got)
	}
}

func TestMoveToAnUnknownStatusIsRefused(t *testing.T) {
	s, h, root := newServer(t)
	before := lastCommit(t, root)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{
		"version": {currentVersion(t, s, "ACME-1")},
		"status":  {"Pending"},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if lastCommit(t, root) != before {
		t.Error("a refused move produced a commit")
	}
}

func TestAWriteOnStaleContentIsRefused(t *testing.T) {
	// This is the criterion that lets Obsidian, an agent and the server share
	// one repository: the server never lands an edit on top of a change it
	// never saw.
	s, h, root := newServer(t)
	stale := currentVersion(t, s, "ACME-1")

	path := filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md")
	raw, _ := os.ReadFile(path)
	edited := string(raw) + "\nAn edit made outside the server.\n"
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	before := lastCommit(t, root)

	w := postForm(t, h, "/task/ACME-1/status", url.Values{
		"version": {stale},
		"status":  {"Done"},
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", w.Code)
	}

	after, _ := os.ReadFile(path)
	if string(after) != edited {
		t.Error("the outside edit was overwritten")
	}
	if lastCommit(t, root) != before {
		t.Error("a refused write produced a commit")
	}
}

func TestCommentIsAppended(t *testing.T) {
	s, h, root := newServer(t)

	w := postForm(t, h, "/task/ACME-1/comment", url.Values{
		"version": {currentVersion(t, s, "ACME-1")},
		"text":    {"Reproduced on staging."},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "ACME", "ACME-1 Fix login redirect loop.md"))
	if !strings.Contains(string(raw), "Reproduced on staging.") {
		t.Errorf("the comment was not written:\n%s", raw)
	}
	if !strings.Contains(string(raw), "**Server · 2026-08-30 13:00**") {
		t.Errorf("the comment is not attributed and stamped:\n%s", raw)
	}
}

func TestCreatingATaskThroughTheForm(t *testing.T) {
	_, h, root := newServer(t)

	w := postForm(t, h, "/new", url.Values{
		"title":    {"Session model"},
		"type":     {"story"},
		"priority": {"normal"},
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}
	if got := w.Header().Get("Location"); got != "/task/ACME-2" {
		t.Errorf("redirected to %q, want /task/ACME-2", got)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "ACME-2: Session model") {
		t.Errorf("commit = %q", got)
	}
}

func TestCreatingATaskWithoutATitleIsRefused(t *testing.T) {
	_, h, _ := newServer(t)
	if w := postForm(t, h, "/new", url.Values{"title": {"   "}}); w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
}

// ---- API ----

func TestAPIListAndFilter(t *testing.T) {
	_, h, _ := newServer(t)

	var all []taskJSON
	decode(t, get(t, h, "/api/tasks"), &all)
	if len(all) != 1 {
		t.Fatalf("got %d tasks, want 1", len(all))
	}

	var done []taskJSON
	decode(t, get(t, h, "/api/tasks?category=done"), &done)
	if len(done) != 0 {
		t.Errorf("got %d done tasks, want 0", len(done))
	}
}

func TestAPIPatchIsAttributedToTheCaller(t *testing.T) {
	s, h, root := newServer(t)
	version := currentVersion(t, s, "ACME-1")

	body := `{"version":"` + version + `","status":"In progress","comment":"On it."}`
	r := httptest.NewRequest("PATCH", "/api/tasks/ACME-1", strings.NewReader(body))
	r.Header.Set("X-Docket-Author", "Agent <agent@example.com>")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d; body: %s", w.Code, w.Body)
	}
	var got taskJSON
	decode(t, w, &got)
	if got.Status != "In progress" || got.StatusCategory != "doing" {
		t.Errorf("status = %q/%q", got.Status, got.StatusCategory)
	}
	if commit := lastCommit(t, root); !strings.Contains(commit, "Agent <agent@example.com>") {
		t.Errorf("commit = %q, want it attributed to the caller", commit)
	}
}

func TestAPIPatchRefusesAStaleVersion(t *testing.T) {
	s, h, _ := newServer(t)
	stale := currentVersion(t, s, "ACME-1")

	first := `{"version":"` + stale + `","priority":"low"}`
	patch(t, h, "/api/tasks/ACME-1", first, http.StatusOK)

	second := `{"version":"` + stale + `","priority":"urgent"}`
	patch(t, h, "/api/tasks/ACME-1", second, http.StatusConflict)
}

func TestAPIPatchRejectsValuesTheProjectDoesNotKnow(t *testing.T) {
	s, h, _ := newServer(t)
	version := currentVersion(t, s, "ACME-1")

	patch(t, h, "/api/tasks/ACME-1",
		`{"version":"`+version+`","status":"Pending"}`, http.StatusBadRequest)
}

func TestAPICreate(t *testing.T) {
	_, h, root := newServer(t)

	r := httptest.NewRequest("POST", "/api/tasks",
		strings.NewReader(`{"title":"From the API","type":"task"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d; body: %s", w.Code, w.Body)
	}
	var got taskJSON
	decode(t, w, &got)
	if got.Key != "ACME-2" {
		t.Errorf("key = %q, want ACME-2", got.Key)
	}
	if got.Version == "" {
		t.Error("the response carries no version to write back with")
	}
	if commit := lastCommit(t, root); !strings.Contains(commit, "ACME-2: From the API") {
		t.Errorf("commit = %q", commit)
	}
}

// ---- pages and search ----

func TestPageRendersAndResolvesLinks(t *testing.T) {
	_, h, root := newServer(t)

	page := "---\ntitle: Session model\n---\n\nSee [[ACME-1 Fix login redirect loop]] and [[nowhere]].\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "session.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/page/docs/session").Body.String()
	if !strings.Contains(body, `href="/task/ACME-1"`) {
		t.Errorf("a resolvable link was not turned into a link:\n%s", body)
	}
	if !strings.Contains(body, "dead-link") {
		t.Errorf("a broken link was not marked as broken:\n%s", body)
	}
}

// A name with a space in it is the normal case here: a note is named after its
// task and an attachment after the task it belongs to. A space inside Markdown
// link parentheses ends the URL, so an unescaped href renders as literal text.
func TestALinkToANameWithSpacesIsStillALink(t *testing.T) {
	_, h, root := newServer(t)

	if err := os.MkdirAll(filepath.Join(root, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "attachments", "ACME-1 screenshot.png"),
		[]byte("not really a png"), 0o644); err != nil {
		t.Fatal(err)
	}

	page := "---\ntitle: Notes\n---\n\n" +
		"![[attachments/ACME-1 screenshot.png]]\n\n" +
		"and [[ACME-1 Fix login redirect loop]].\n"
	if err := os.WriteFile(filepath.Join(root, "docs", "notes.md"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/page/docs/notes").Body.String()
	if !strings.Contains(body, `src="/file/attachments/ACME-1%20screenshot.png"`) {
		t.Errorf("the attachment did not become an image:\n%s", body)
	}
	if !strings.Contains(body, `href="/task/ACME-1"`) {
		t.Errorf("the task link did not survive:\n%s", body)
	}
	// The literal remains of a link that failed to parse.
	if strings.Contains(body, "screenshot.png](") {
		t.Errorf("a link rendered as text:\n%s", body)
	}
}

func TestPagePathCannotEscapeTheVault(t *testing.T) {
	_, h, _ := newServer(t)
	if w := get(t, h, "/page/../../etc/passwd"); w.Code == http.StatusOK {
		t.Errorf("a path outside the vault was served: %d", w.Code)
	}
}

func TestSearchFindsTasksAndPages(t *testing.T) {
	_, h, root := newServer(t)
	if err := os.WriteFile(filepath.Join(root, "docs", "session.md"),
		[]byte("A page about redirect loops.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/search?q=redirect").Body.String()
	if !strings.Contains(body, "ACME-1") {
		t.Error("search did not find the task")
	}
	if !strings.Contains(body, "docs/session") {
		t.Error("search did not find the page")
	}
}

func TestSearchNarrows(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	// A second task, so a filter has something to exclude.
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Rotate the signing key", Type: "task", Priority: "low",
		Labels: []string{"security"}, Now: noon,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "session.md"),
		[]byte("A page about the signing key.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		query    string
		wants    []string
		excludes []string
	}{
		{"by assignee", "?assignee=agent%2Fclaude", []string{"ACME-1"}, []string{"ACME-2"}},
		{"unassigned", "?assignee=%21unassigned", []string{"ACME-2"}, []string{"ACME-1"}},
		{"by label", "?label=security", []string{"ACME-2"}, []string{"ACME-1"}},
		{"by priority", "?priority=high", []string{"ACME-1"}, []string{"ACME-2"}},
		{"by type", "?type=bug", []string{"ACME-1"}, []string{"ACME-2"}},
		{"by status", "?status=Backlog", []string{"ACME-1", "ACME-2"}, nil},
		{"by project", "?project=ACME", []string{"ACME-1", "ACME-2"}, nil},
		// Words and a filter together: both must hold.
		{"words and a filter", "?q=signing&label=security", []string{"ACME-2"}, []string{"ACME-1"}},
		// A page has no assignee, so narrowing by one is a question about tasks.
		{"narrowed skips pages", "?q=signing&type=task", []string{"ACME-2"}, []string{"docs/session"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := get(t, h, "/search"+tc.query).Body.String()
			results := body[strings.Index(body, `<ul class="hits">`):]
			for _, want := range tc.wants {
				if !strings.Contains(results, want) {
					t.Errorf("%s is missing from the results:\n%s", want, results)
				}
			}
			for _, unwanted := range tc.excludes {
				if strings.Contains(results, unwanted) {
					t.Errorf("%s should have been filtered out:\n%s", unwanted, results)
				}
			}
		})
	}
}

// An unfilled form asks nothing, so it should not answer with the whole vault.
func TestSearchWithNothingAskedShowsNoResults(t *testing.T) {
	_, h, _ := newServer(t)
	body := get(t, h, "/search").Body.String()
	if strings.Contains(body, `<ul class="hits">`) {
		t.Error("an empty form listed results anyway")
	}
	if !strings.Contains(body, "narrow by project") {
		t.Error("an empty form does not say what it can do")
	}
}

// The filter menus offer what the vault actually uses, not a fixed list.
func TestSearchFormOffersTheVaultsVocabulary(t *testing.T) {
	_, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Title: "Rotate the signing key", Assignee: "dana",
		Labels: []string{"security"}, Now: noon,
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/search").Body.String()
	for _, want := range []string{`value="dana"`, `value="security"`, `value="agent/claude"`,
		`value="Backlog"`, `value="bug"`, `value="ACME"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the form does not offer %s", want)
		}
	}
}

func TestServingADirectoryThatIsNotAGitRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	if _, err := vault.Init(root, vault.Options{Key: "ACME", Template: vaulttest.Template(t)}); err != nil {
		t.Fatal(err)
	}
	// Every write is a commit, so a vault outside git would lose its history
	// silently. Refusing at startup is the only honest option.
	if _, err := New(root, Options{Author: gitvcs.Author{Name: "T", Email: "t@example.com"}}); err == nil {
		t.Error("New accepted a vault that is not under git")
	}
}

func decode(t *testing.T, w *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), into); err != nil {
		t.Fatalf("decoding %s: %v", w.Body.String(), err)
	}
}

func patch(t *testing.T, h http.Handler, path, body string, wantCode int) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PATCH", path, strings.NewReader(body)))
	if w.Code != wantCode {
		t.Fatalf("code = %d, want %d; body: %s", w.Code, wantCode, w.Body)
	}
}

func TestTheBoardShowsEveryProjectAtOnce(t *testing.T) {
	// Work crosses projects constantly, so all of them is the default view.
	s, h, root := newServer(t)

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.AddProject("BETA", "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Project: "BETA", Title: "Something in Beta", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}
	_ = s

	body := get(t, h, "/").Body.String()
	if !strings.Contains(body, "ACME-1") || !strings.Contains(body, "BETA-1") {
		t.Errorf("the board does not show both projects:\n%s", body)
	}
}

func TestTheBoardCanBeNarrowedToOneProject(t *testing.T) {
	_, h, root := newServer(t)

	c, _ := project.Load(root)
	if err := c.AddProject("BETA", "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Project: "BETA", Title: "Something in Beta", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}

	body := get(t, h, "/?project=BETA").Body.String()
	if !strings.Contains(body, "BETA-1") {
		t.Error("the selected project is missing from its own board")
	}
	if strings.Contains(body, "ACME-1") {
		t.Error("a task from another project appeared on a narrowed board")
	}
}

func TestNarrowingToAProjectThatDoesNotExist(t *testing.T) {
	_, h, _ := newServer(t)
	if w := get(t, h, "/?project=GHOST"); w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func TestTheAPICanFilterByProject(t *testing.T) {
	_, h, root := newServer(t)

	c, _ := project.Load(root)
	if err := c.AddProject("BETA", "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, _, err := vault.Create(root, c, vault.NewOptions{
		Project: "BETA", Title: "Something in Beta", Now: noon,
	}); err != nil {
		t.Fatal(err)
	}

	var all, beta []taskJSON
	decode(t, get(t, h, "/api/tasks"), &all)
	decode(t, get(t, h, "/api/tasks?project=BETA"), &beta)

	if len(all) != 2 {
		t.Errorf("got %d tasks across the vault, want 2", len(all))
	}
	if len(beta) != 1 || beta[0].Key != "BETA-1" {
		t.Errorf("filtering by project gave %v", beta)
	}
}
