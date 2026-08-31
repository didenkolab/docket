package server

import (
	"strings"
	"testing"

	"github.com/vadymdidenkolab/docket/internal/access"
)

// A type carries a level now, so a template that ranges over the types gets
// structs rather than words. The form rendered "{epic 1}" as an option value,
// which would have created a task with a type no vault defines.
func TestTheFormsOfferTheVocabularyAsWords(t *testing.T) {
	_, h, _, _ := guardedServer(t)
	admin := signIn(t, h, access.RoleAdmin)

	for _, path := range []string{"/new", "/task/ACME-1/edit"} {
		body := as(t, h, admin, "GET", path, nil).Body.String()
		if strings.Contains(body, "{epic") || strings.Contains(body, "{task ") {
			t.Errorf("%s renders a type as a struct:\n%s", path, body)
		}
		if !strings.Contains(body, `value="epic"`) {
			t.Errorf("%s does not offer the types as words", path)
		}
	}
}

// What a new task gets when nobody chooses should be the middle of the road,
// and the form should say so rather than sitting on whatever is listed first.
func TestTheNewTaskFormStartsOnTheDefaults(t *testing.T) {
	_, h, _, _ := guardedServer(t)
	body := as(t, h, signIn(t, h, access.RoleAdmin), "GET", "/new", nil).Body.String()

	if !strings.Contains(body, `value="task" selected`) {
		t.Error("the form does not start on the default type")
	}
	if !strings.Contains(body, `value="normal" selected`) {
		t.Error("the form does not start on the default priority")
	}
}
