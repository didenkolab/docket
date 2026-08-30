package server

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// settingsForm builds the form the settings page submits, from a config plus
// whatever the caller wants to change.
func settingsForm(c *project.Config, change func(i int, s project.Status) (string, string)) url.Values {
	form := url.Values{
		"name":       {c.Name},
		"types":      {strings.Join(c.Types, ", ")},
		"priorities": {strings.Join(c.Priorities, ", ")},
	}
	for i, status := range c.Statuses {
		name, category := status.Name, status.Category
		if change != nil {
			name, category = change(i, status)
		}
		form.Add("status_was", status.Name)
		form.Add("status_name", name)
		form.Add("status_category", category)
		form.Add("status_position", itoa(i+1))
	}
	return form
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestSettingsShowsWhatIsInUse(t *testing.T) {
	_, h, _ := newServer(t)

	body := get(t, h, "/settings").Body.String()
	for _, want := range []string{"Backlog", "In progress", "docket.yaml", "ACME"} {
		if !strings.Contains(body, want) {
			t.Errorf("settings does not mention %q", want)
		}
	}
}

func TestRenamingAStatusTakesItsTasksWithIt(t *testing.T) {
	// A rename that left tasks behind would leave the vault failing its own
	// validation, which is a worse outcome than refusing the rename.
	_, h, root := newServer(t)
	c, _ := project.Load(root)

	form := settingsForm(c, func(i int, s project.Status) (string, string) {
		if s.Name == "Backlog" {
			return "Icebox", s.Category
		}
		return s.Name, s.Category
	})

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303; body:\n%s", w.Code, w.Body)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "ACME", "1.md"))
	if !strings.Contains(string(raw), "status: Icebox") {
		t.Errorf("the task did not follow the rename:\n%s", raw)
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatalf("the config no longer loads: %v", err)
	}
	if _, known := reloaded.CategoryOf("Icebox"); !known {
		t.Error("the renamed status is not in the config")
	}
	if got := lastCommit(t, root); !strings.Contains(got, "Settings") {
		t.Errorf("commit = %q", got)
	}
}

func TestRemovingAStatusStillInUseIsRefused(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)
	before := lastCommit(t, root)

	form := settingsForm(c, func(i int, s project.Status) (string, string) {
		if s.Name == "Backlog" {
			return "", s.Category // clearing a name removes the status
		}
		return s.Name, s.Category
	})

	w := postForm(t, h, "/settings", form)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "still holds") {
		t.Errorf("the refusal does not say why:\n%s", w.Body)
	}
	if lastCommit(t, root) != before {
		t.Error("a refused edit produced a commit")
	}
	if c2, _ := project.Load(root); len(c2.Statuses) != len(c.Statuses) {
		t.Error("the config was changed despite the refusal")
	}
}

func TestRemovingAnUnusedStatusIsAllowed(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)

	form := settingsForm(c, func(i int, s project.Status) (string, string) {
		if s.Name == "Dropped" {
			return "", s.Category
		}
		return s.Name, s.Category
	})

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d, want 303; body:\n%s", w.Code, w.Body)
	}
	if reloaded, _ := project.Load(root); reloaded.HasProject("") || len(reloaded.Statuses) != len(c.Statuses)-1 {
		t.Errorf("got %d statuses, want %d", len(reloaded.Statuses), len(c.Statuses)-1)
	}
}

func TestStatusOrderFollowsThePositionColumn(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)

	form := settingsForm(c, nil)
	// Reverse the positions: the first status should end up last.
	positions := form["status_position"]
	for i := range positions {
		positions[i] = itoa(len(positions) - i)
	}

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}

	reloaded, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Statuses[0].Name != c.Statuses[len(c.Statuses)-1].Name {
		t.Errorf("first status is %q, want %q", reloaded.Statuses[0].Name,
			c.Statuses[len(c.Statuses)-1].Name)
	}
}

func TestSettingsRejectsAConfigThatWouldNotLoad(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)

	form := settingsForm(c, nil)
	form.Set("types", "   ") // a vault with no task types is not a vault

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", w.Code)
	}
	if _, err := project.Load(root); err != nil {
		t.Errorf("the config on disk was damaged: %v", err)
	}
}

func TestSavingSettingsKeepsEveryProjectOnTheBoards(t *testing.T) {
	// Boards name their project folders, so a config change has to rewrite
	// them or a project silently stops being shown.
	_, h, root := newServer(t)

	c, _ := project.Load(root)
	if err := c.AddProject("BETA", "Beta"); err != nil {
		t.Fatal(err)
	}
	if err := c.Save(root); err != nil {
		t.Fatal(err)
	}

	if w := postForm(t, h, "/settings", settingsForm(c, nil)); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(vault.BoardFile)))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ACME", "BETA"} {
		if !strings.Contains(string(raw), `file.inFolder("`+key+`")`) {
			t.Errorf("the board does not name %s:\n%s", key, raw)
		}
	}
}

func TestAProjectsDisplayNameCanBeChanged(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)

	form := settingsForm(c, nil)
	form.Set("project_name_ACME", "Acme, renamed")

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}
	if reloaded, _ := project.Load(root); reloaded.ProjectName("ACME") != "Acme, renamed" {
		t.Errorf("name = %q", reloaded.ProjectName("ACME"))
	}
}

func TestTheBoardCarriesWhatADraggedCardNeeds(t *testing.T) {
	// Dragging a card PATCHes the API with the fingerprint the card was drawn
	// from, so a drop cannot land on a file that changed in the meantime.
	s, h, _ := newServer(t)

	body := get(t, h, "/").Body.String()
	for _, want := range []string{
		`data-key="ACME/1"`,
		`data-status="Backlog"`,
		`data-version="` + currentVersion(t, s, "ACME/1") + `"`,
		`data-status="In progress"`, // the column
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the board is missing %s", want)
		}
	}
}

// withWorkflow adds the matrix fields to a settings form.
func withWorkflow(form url.Values, allow map[string][]string) url.Values {
	form.Set("workflow", "on")
	for from, targets := range allow {
		for _, to := range targets {
			form.Add("transition_"+from, to)
		}
	}
	return form
}

func TestAWorkflowSavedInSettingsIsEnforcedEverywhere(t *testing.T) {
	s, h, root := newServer(t)
	c, _ := project.Load(root)

	form := withWorkflow(settingsForm(c, nil), map[string][]string{
		"Backlog": {"Ready"},
		"Ready":   {"In progress"},
	})
	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("saving the workflow: %d; body:\n%s", w.Code, w.Body)
	}

	saved, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CanMove("Backlog", "Ready") || saved.CanMove("Backlog", "Done") {
		t.Fatalf("the workflow was not stored: %v", saved.Transitions)
	}

	// The task page offers only what the workflow allows.
	page := get(t, h, "/task/ACME/1").Body.String()
	if strings.Contains(page, `<option value="Done"`) {
		t.Error("the task page offers a move the workflow forbids")
	}

	// And the move itself is refused, not merely hidden.
	w := postForm(t, h, "/task/ACME/1/status", url.Values{
		"version": {currentVersion(t, s, "ACME/1")},
		"status":  {"Done"},
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("a forbidden move was accepted: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "workflow") {
		t.Errorf("the refusal does not say what refused it:\n%s", w.Body)
	}
}

func TestTheAPIObeysTheWorkflowToo(t *testing.T) {
	s, h, root := newServer(t)
	c, _ := project.Load(root)
	postForm(t, h, "/settings", withWorkflow(settingsForm(c, nil), map[string][]string{
		"Backlog": {"Ready"},
	}))

	body := `{"version":"` + currentVersion(t, s, "ACME/1") + `","status":"Done"}`
	patch(t, h, "/api/tasks/ACME/1", body, http.StatusBadRequest)
}

func TestTheBoardTellsEachCardWhereItMayGo(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)
	postForm(t, h, "/settings", withWorkflow(settingsForm(c, nil), map[string][]string{
		"Backlog": {"Ready"},
	}))

	// The exact escaping of the separator is the template's business; what
	// matters is that the card names where it may go and only that.
	body := get(t, h, "/").Body.String()
	card := body[strings.Index(body, `data-key="ACME/1"`):]
	card = card[:strings.Index(card, ">")]

	if !strings.Contains(card, "data-reachable=") {
		t.Fatalf("the card carries no reachable statuses:\n%s", card)
	}
	if !strings.Contains(card, "Ready") {
		t.Errorf("the card does not name the status it may move to:\n%s", card)
	}
	if strings.Contains(card, "Done") {
		t.Errorf("the card names a status the workflow forbids:\n%s", card)
	}
}

func TestRenamingAStatusCarriesItsWorkflowAcross(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)
	postForm(t, h, "/settings", withWorkflow(settingsForm(c, nil), map[string][]string{
		"Backlog": {"Ready"},
		"Ready":   {"In progress"},
	}))

	current, _ := project.Load(root)
	form := withWorkflow(settingsForm(current, func(i int, s project.Status) (string, string) {
		if s.Name == "Ready" {
			return "Next up", s.Category
		}
		return s.Name, s.Category
	}), map[string][]string{
		"Backlog": {"Ready"},
		"Ready":   {"In progress"},
	})

	if w := postForm(t, h, "/settings", form); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d; body:\n%s", w.Code, w.Body)
	}

	saved, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.CanMove("Backlog", "Next up") {
		t.Errorf("a move pointing at the renamed status was lost: %v", saved.Transitions)
	}
	if !saved.CanMove("Next up", "In progress") {
		t.Errorf("the renamed status lost its own moves: %v", saved.Transitions)
	}
}

func TestTurningTheWorkflowOffAllowsEverythingAgain(t *testing.T) {
	_, h, root := newServer(t)
	c, _ := project.Load(root)
	postForm(t, h, "/settings", withWorkflow(settingsForm(c, nil), map[string][]string{
		"Backlog": {"Ready"},
	}))

	current, _ := project.Load(root)
	if w := postForm(t, h, "/settings", settingsForm(current, nil)); w.Code != http.StatusSeeOther {
		t.Fatalf("code = %d", w.Code)
	}

	saved, _ := project.Load(root)
	if len(saved.Transitions) != 0 {
		t.Errorf("the workflow survived being switched off: %v", saved.Transitions)
	}
	if !saved.CanMove("Backlog", "Done") {
		t.Error("moves are still restricted with no workflow")
	}
}
