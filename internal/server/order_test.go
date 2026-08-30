package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// withColumn adds tasks to the vault's one project, all in Backlog, and returns
// their keys in the order the board would draw them.
func withColumn(t *testing.T, root string, titles ...string) []string {
	t.Helper()

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"ACME-1"} // newServer already made one
	for _, title := range titles {
		_, made, err := vault.Create(root, c, vault.NewOptions{Title: title, Now: noon})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, made.Key)
	}
	return keys
}

// drag sends the PATCH the board sends when a card is dropped, and fails on
// anything but 200.
func drag(t *testing.T, h http.Handler, key string, body map[string]any) taskJSON {
	t.Helper()

	raw, _ := json.Marshal(body)
	r := httptest.NewRequest("PATCH", "/api/tasks/"+key, strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH %s = %d: %s", key, w.Code, w.Body)
	}
	var out taskJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// columnOrder is the keys of one status, in the order the board draws them.
func columnOrder(t *testing.T, root, status string) []string {
	t.Helper()

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := vault.List(root, c)
	if err != nil {
		t.Fatal(err)
	}
	var cards []card
	for _, e := range entries {
		if e.Task != nil && e.Task.Status == status {
			cards = append(cards, card{Key: e.Key, order: e.Task.Order})
		}
	}
	sortCards(cards)

	keys := make([]string, len(cards))
	for i, c := range cards {
		keys[i] = c.Key
	}
	return keys
}

// A column nobody has arranged reads oldest first.
func TestAColumnWithNoOrderIsInKeyOrder(t *testing.T) {
	_, _, root := newServer(t)
	withColumn(t, root, "Second", "Third")

	if got := columnOrder(t, root, "Backlog"); !equal(got, []string{"ACME-1", "ACME-2", "ACME-3"}) {
		t.Errorf("order = %v", got)
	}
}

func TestDraggingACardToTheTopOfItsOwnColumn(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second", "Third")

	// "" is the top of the column.
	drag(t, h, "ACME-3", map[string]any{"after": ""})

	if got := columnOrder(t, root, "Backlog"); !equal(got, []string{"ACME-3", "ACME-1", "ACME-2"}) {
		t.Errorf("order = %v", got)
	}
	if author := lastCommit(t, root); !strings.Contains(author, "ACME-3: order") {
		t.Errorf("commit = %q", author)
	}
}

func TestDraggingACardIntoTheMiddle(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second", "Third", "Fourth")

	drag(t, h, "ACME-4", map[string]any{"after": "ACME-1"})

	if got := columnOrder(t, root, "Backlog"); !equal(got, []string{"ACME-1", "ACME-4", "ACME-2", "ACME-3"}) {
		t.Errorf("order = %v", got)
	}
}

// The order and the status move together, in one commit.
func TestDraggingACardIntoAnotherColumnAtAPosition(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second", "Third")

	drag(t, h, "ACME-2", map[string]any{"status": "Ready"})
	drag(t, h, "ACME-3", map[string]any{"status": "Ready", "after": ""})

	if got := columnOrder(t, root, "Ready"); !equal(got, []string{"ACME-3", "ACME-2"}) {
		t.Errorf("Ready = %v", got)
	}
	if got := columnOrder(t, root, "Backlog"); !equal(got, []string{"ACME-1"}) {
		t.Errorf("Backlog = %v", got)
	}
	if got := lastCommit(t, root); !strings.Contains(got, "Backlog → Ready") {
		t.Errorf("commit = %q", got)
	}
}

// Placing after a task that is not in the column is a client that has the wrong
// board on screen. Saying so beats silently putting the card somewhere else.
func TestPlacingAfterATaskInAnotherColumnIsRefused(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second")
	drag(t, h, "ACME-2", map[string]any{"status": "Ready"})

	raw, _ := json.Marshal(map[string]any{"after": "ACME-2"})
	r := httptest.NewRequest("PATCH", "/api/tasks/ACME-1", strings.NewReader(string(raw)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "not in Backlog") {
		t.Errorf("the refusal does not say why: %s", w.Body)
	}
}

// Repeatedly inserting into the same gap eventually exhausts it. The column is
// renumbered then, and the order on screen is still the order on disk.
func TestRunningOutOfRoomRenumbersTheColumn(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second", "Third")

	// Force the tightest possible gap: two neighbours one apart.
	setOrder(t, root, "ACME-1", 100)
	setOrder(t, root, "ACME-2", 101)
	setOrder(t, root, "ACME-3", 200)

	drag(t, h, "ACME-3", map[string]any{"after": "ACME-1"})

	if got := columnOrder(t, root, "Backlog"); !equal(got, []string{"ACME-1", "ACME-3", "ACME-2"}) {
		t.Errorf("order = %v", got)
	}
	for i, key := range []string{"ACME-1", "ACME-3", "ACME-2"} {
		if got := orderOf(t, root, key); got == nil || *got != (i+1)*task.Step {
			t.Errorf("%s has order %v, want %d", key, got, (i+1)*task.Step)
		}
	}
	// One commit, carrying every file it had to touch.
	if got := lastCommit(t, root); !strings.Contains(got, "ACME-3: order") {
		t.Errorf("commit = %q", got)
	}
	if dirty := gitStatus(t, root); dirty != "" {
		t.Errorf("the renumbering left files uncommitted:\n%s", dirty)
	}
}

// A drag that changes nothing else must not leave the vault half-written.
func TestOrderSurvivesAReadWriteRoundTrip(t *testing.T) {
	_, h, root := newServer(t)
	withColumn(t, root, "Second")

	got := drag(t, h, "ACME-2", map[string]any{"after": ""})
	if got.Order == nil {
		t.Fatal("the API did not report the new order")
	}
	raw, err := os.ReadFile(filepath.Join(root, "ACME", "ACME-2 Second.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "order: "+strconv.Itoa(*got.Order)) {
		t.Errorf("the file does not carry the order:\n%s", raw)
	}
	// It has to survive a parse, or the next drag reads nothing.
	parsed, err := task.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Order == nil || *parsed.Order != *got.Order {
		t.Errorf("order came back as %v, want %d", parsed.Order, *got.Order)
	}
}

/* ---------- helpers ---------- */

func setOrder(t *testing.T, root, key string, order int) {
	t.Helper()

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := vault.Find(root, c, key)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := task.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.SetOrder(order)
	content, err := parsed.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-q", "-m", "order")
}

func orderOf(t *testing.T, root, key string) *int {
	t.Helper()

	c, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := vault.List(root, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Key == key && e.Task != nil {
			return e.Task.Order
		}
	}
	t.Fatalf("%s is not in the vault", key)
	return nil
}

func gitStatus(t *testing.T, root string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
