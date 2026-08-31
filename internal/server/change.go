package server

import (
	"net/http"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// The page that reads a branch as a change to the plan. See plan.go for why.

type changeView struct {
	Ref string
	// Repos is one section per repository the branch exists in. A workspace may
	// hold the proposal in one repository and not the others, and saying which
	// is part of the answer.
	Repos []repoChange
	// Missing says the branch is in none of them.
	Missing bool
}

type repoChange struct {
	Name string
	// Base is the commit the proposal parted company with, short.
	Base string
	// Ahead and Behind are commits each side has that the other does not.
	Ahead  int
	Behind int
	Change planChange
	// Trouble is why this repository could not be read.
	Trouble string
}

// handlePlanChange says what a branch would do to the plan.
func (s *Server) handlePlanChange(w http.ResponseWriter, r *http.Request) {
	ref := strings.TrimSpace(r.PathValue("ref"))
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}
	// Checked rather than passed through: a ref becomes an argument to a git
	// command, and an unchecked one is an argument somebody else chose.
	if !s.known(ref) {
		s.fail(w, r, http.StatusNotFound, "No such branch",
			ref+" is not a branch or a tag of this repository.")
		return
	}

	view := changeView{Ref: ref, Missing: true}
	for _, v := range s.sp().Vaults() {
		row, ok := s.changeIn(r, v, ref)
		if !ok {
			continue
		}
		view.Missing = false
		view.Repos = append(view.Repos, row)
	}

	s.render(w, r, "change.html", c, "What "+ref+" would do", view)
}

// changeIn compares one repository at a ref with where the ref parted company
// from what is checked out.
//
// Against the merge base, not against the working tree: a proposal made a week
// ago has not undone everything that happened since, and reporting it as though
// it had is worse than reporting nothing.
func (s *Server) changeIn(r *http.Request, v *space.Vault, ref string) (repoChange, bool) {
	if v.Repo == nil {
		return repoChange{}, false
	}
	current := v.Repo.Current()
	if current == "" {
		return repoChange{}, false
	}
	base, err := v.Repo.MergeBase(current, ref)
	if err != nil || base == "" {
		// The ref is not in this repository, which in a workspace is ordinary.
		return repoChange{}, false
	}

	row := repoChange{Name: s.nameOf(v), Base: short(base)}
	row.Behind, row.Ahead, _ = v.Repo.Distance(base, ref)

	was, err := vault.ConfigAt(v.Repo, base)
	if err != nil {
		row.Trouble = "cannot read the configuration at " + short(base) + ": " + err.Error()
		return row, true
	}
	now, err := vault.ConfigAt(v.Repo, ref)
	if err != nil {
		row.Trouble = "cannot read the configuration on the branch: " + err.Error()
		return row, true
	}

	before, err := vault.ListAt(v.Repo, base, was)
	if err != nil {
		row.Trouble = err.Error()
		return row, true
	}
	after, err := vault.ListAt(v.Repo, ref, now)
	if err != nil {
		row.Trouble = err.Error()
		return row, true
	}

	// The filter that applies to a board applies to a review of one: a proposal
	// to a project somebody cannot read is not summarised for them.
	st := standingIn(r)
	row.Change = comparePlans(visible(st, before), visible(st, after), was, now)
	return row, true
}
