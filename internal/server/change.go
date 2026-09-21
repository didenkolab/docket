package server

import (
	"net/http"
	"strings"

	"github.com/didenkolab/docket/internal/space"
	"github.com/didenkolab/docket/internal/vault"
)

// The page that reads a branch as a change to the plan. See plan.go for why.

type changeView struct {
	Ref string
	// Beside is a second proposal, shown next to the first.
	//
	// A choice between two plans was two tabs, and comparing two things in two
	// tabs is comparing one thing twice. Named in
	// docs/design/Git as the database.md.
	Beside string
	// Others are the branches that could be put beside this one.
	Others []string
	// Besides is the second proposal's rows, one per repository.
	Besides []repoChange
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
	// Ref is which proposal this row is about, so a page showing two can label
	// them.
	Ref string
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
		row.Ref = ref
		view.Missing = false
		view.Repos = append(view.Repos, row)
	}

	// A second proposal, side by side. Refused unless it is a real ref, for the
	// same reason as the first: a ref becomes an argument to a git command.
	if beside := strings.TrimSpace(r.FormValue("with")); beside != "" && beside != ref {
		if s.known(beside) {
			view.Beside = beside
			for _, v := range s.sp().Vaults() {
				if row, ok := s.changeIn(r, v, beside); ok {
					row.Ref = beside
					view.Besides = append(view.Besides, row)
				}
			}
		}
	}
	view.Others = s.otherBranches(ref, view.Beside)

	title := "What " + ref + " would do"
	if view.Beside != "" {
		title = ref + " beside " + view.Beside
	}
	s.render(w, r, "change.html", c, title, view)
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

// otherBranches is what else could be put beside this proposal.
func (s *Server) otherBranches(ref, beside string) []string {
	vaults := s.sp().Vaults()
	if len(vaults) == 0 || vaults[0].Repo == nil {
		return nil
	}
	branches, err := vaults[0].Repo.Branches()
	if err != nil {
		return nil
	}
	var out []string
	for _, b := range branches {
		if b.Name != ref && b.Name != beside {
			out = append(out, b.Name)
		}
	}
	return out
}
