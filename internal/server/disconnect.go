package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// Taking a project out of a workspace.
//
// Two sentences that must not be confused. "I do not want this here" is a
// manifest edit and costs nothing — the repository is untouched, and adding it
// back is one paste of its URL. "Destroy this" is a different sentence, and
// nothing here says it: the clone is only removed when asked for separately,
// and never when it holds anything that exists nowhere else.
//
// What this will never do is delete a repository on the host. A board is a
// client of a git repository; a client that can destroy the thing it is a view
// of is a client nobody should point at anything they care about.

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	sp := s.sp()
	fail := func(message string) {
		http.Redirect(w, r, "/projects?trouble="+urlEscape(message), http.StatusSeeOther)
	}

	if !sp.Workspace {
		fail("This is a single vault, not a workspace, so there is no manifest to take a " +
			"project out of.")
		return
	}
	if !s.mayConfigureAnything(r) {
		s.refuse(w, r, "Taking a project out changes the workspace for everybody, so it needs "+
			"administrator access to a repository already in it.")
		return
	}

	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		fail("Nothing said which project.")
		return
	}
	alsoDelete := r.FormValue("delete_folder") == "1"

	s.writes.Lock()
	defer s.writes.Unlock()

	m, err := workspace.Load(sp.Root)
	if err != nil {
		fail(err.Error())
		return
	}
	gone, err := m.Remove(key)
	if err != nil {
		fail(err.Error())
		return
	}

	// Refused before the manifest is touched, so a project is never half
	// removed: still on disk, no longer in the list, and nobody told why.
	at := filepath.Join(sp.Root, filepath.FromSlash(gone.Path))
	if alsoDelete {
		if why := keeping(at); why != "" {
			fail(gone.Key + " was left alone: " + why)
			return
		}
	}

	if err := m.Save(sp.Root); err != nil {
		fail(err.Error())
		return
	}

	said := gone.Key + " is out of the workspace. Its repository is untouched — " +
		"paste " + gone.Remote + " on this page to bring it back."
	if alsoDelete {
		if err := os.RemoveAll(at); err != nil {
			said = gone.Key + " is out of the workspace, but its folder could not be " +
				"removed: " + err.Error()
		} else {
			said = gone.Key + " is out of the workspace and its clone is deleted. " +
				"Everything in it is on " + gone.Remote + "."
		}
	}

	if err := s.reload(); err != nil {
		fail("Removed it, but the workspace cannot be reopened: " + err.Error())
		return
	}
	http.Redirect(w, r, "/projects?saved="+urlEscape(said), http.StatusSeeOther)
}

// keeping is why a clone must not be deleted, or "" when it may be.
//
// The question is not "is this repository important" — they all are — but
// "does this folder hold anything that exists nowhere else". Uncommitted work
// and commits that were never sent are exactly that.
func keeping(at string) string {
	repo, err := gitvcs.Open(at)
	if err != nil {
		return "it is not a git repository, so nothing here can say whether its contents " +
			"exist anywhere else. Delete the folder by hand if you are sure."
	}
	dirty, err := repo.Dirty()
	if err != nil {
		return "git could not be asked whether it holds uncommitted work: " + err.Error()
	}
	if dirty {
		return "it holds uncommitted changes, which exist nowhere else. Commit or discard " +
			"them first."
	}
	unsent, err := repo.Unpushed()
	if err != nil {
		return "git could not be asked whether everything has been sent: " + err.Error()
	}
	if unsent > 0 {
		return fmt.Sprintf("%d commits here have never been sent to its remote. "+
			"Send them first, or delete the folder by hand.", unsent)
	}
	return ""
}
