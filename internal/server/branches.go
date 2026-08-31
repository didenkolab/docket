package server

import (
	"net/http"
	"net/url"
	"strings"
)

// The board over a branch.
//
// A branch is a proposal about the plan — a release re-scoped, an epic split, a
// quarter dropped. The only way to judge one is to see the board it would
// produce, and that has to be possible without checking it out, because
// somebody is working in the tree.
//
// It is a separate route rather than a `?ref=` on every page, and deliberately:
// reading a proposal and working in one are different activities, and a
// separate way in is a way that cannot be forgotten. Nothing here writes.

// branchView is one branch, ready to render.
type branchView struct {
	Name string
	Href string
	// Change is the same proposal read as what it would do.
	Change  string
	Current bool
	// Remote says nobody here has checked this one out — the usual shape of a
	// proposal you are being asked about rather than making.
	Remote  bool
	Subject string
	When    string
}

type branchesView struct {
	Branches []branchView
	Current  string
	None     bool
	// Asker is the host to paste a pull request address at, named so the box can
	// say which kind it means. Empty when there is nobody to ask.
	Asker string
	// Kind is what that host calls one: "pull request", "merge request".
	Kind string
}

func (s *Server) handleBranches(w http.ResponseWriter, r *http.Request) {
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	view := branchesView{}
	vaults := s.sp().Vaults()
	if len(vaults) > 0 && vaults[0].Repo != nil {
		found, err := vaults[0].Repo.Branches()
		if err != nil {
			s.fail(w, r, http.StatusInternalServerError, "Cannot read the branches", err.Error())
			return
		}
		for _, b := range found {
			if b.Current {
				view.Current = b.Name
			}
			view.Branches = append(view.Branches, branchView{
				Name: b.Name, Href: "/branch/" + url.PathEscape(b.Name),
				Change:  "/change/" + url.PathEscape(b.Name),
				Current: b.Current, Remote: b.Remote, Subject: b.Subject,
				When: b.When.UTC().Format("2006-01-02"),
			})
		}
	}
	view.None = len(view.Branches) < 2
	if _, host, ok := s.pullRequestHost(); ok {
		view.Asker, view.Kind = host.Name(), host.PullRequestName()
	}

	s.render(w, r, "branches.html", c, "Branches", view)
}

// handleBranch draws the board as it would be on one branch.
func (s *Server) handleBranch(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("ref"))

	// Checked rather than passed through: a ref goes into a git command, and an
	// unchecked one is an argument somebody else chose.
	if !s.known(ref) {
		s.fail(w, r, http.StatusNotFound, "No such branch",
			ref+" is not a branch or a tag of this repository.")
		return
	}
	s.board(w, r, s.sp().At(ref), ref)
}

// known reports whether a ref is a branch or a tag of this repository.
//
// A ref becomes an argument to a git command, so an unchecked one is an
// argument somebody else chose. Anything that is not a branch or a tag here is
// refused before it reaches git.
func (s *Server) known(ref string) bool {
	vaults := s.sp().Vaults()
	if ref == "" || len(vaults) == 0 || vaults[0].Repo == nil {
		return false
	}
	if branches, err := vaults[0].Repo.Branches(); err == nil {
		for _, b := range branches {
			if b.Name == ref {
				return true
			}
		}
	}
	if releases, err := vaults[0].Repo.Releases(); err == nil {
		for _, rel := range releases {
			if rel.Name == ref {
				return true
			}
		}
	}
	return false
}
