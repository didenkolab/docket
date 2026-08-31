package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// A vault with no remote commits every change and publishes none of them, and
// the board used to report neither.
//
// The repository was skipped before the panel was drawn, so a person moving
// cards saw no badge, no warning and no explanation — which is the silence
// IGL-37 exists to prevent, one step earlier in the chain. That rule catches a
// push that failed; this is a push that was never attempted.
func TestAVaultWithNoRemoteSaysSo(t *testing.T) {
	s, handler, root := newServer(t)
	_ = root

	// Nothing written yet: a local-only vault nobody has touched is a normal
	// thing to have, and a badge that is always there is a badge nobody reads.
	if notes := s.pushNotes(); len(notes) != 0 {
		t.Errorf("an untouched local vault is nagging about its remote: %+v", notes)
	}

	// Move a card, which is a commit.
	w := as(t, handler, nil, http.MethodPost, "/task/ACME-1/status",
		url.Values{"status": {"In review"}})
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("the move was refused: %d %s", w.Code, w.Body)
	}

	notes := s.pushNotes()
	if len(notes) != 1 {
		t.Fatalf("after a write, the board says %d things about its remote, want 1", len(notes))
	}
	if !notes[0].NoRemote {
		t.Errorf("it does not say there is no remote: %+v", notes[0])
	}
	if notes[0].Wrote < 1 {
		t.Errorf("it does not say how much is stranded: %+v", notes[0])
	}

	// And the page says it in words, with what to do.
	page := as(t, handler, nil, http.MethodGet, "/", nil).Body.String()
	for _, want := range []string{"no remote", "nothing has left this folder", "git remote add origin"} {
		if !strings.Contains(page, want) {
			t.Errorf("the board does not say %q", want)
		}
	}
}
