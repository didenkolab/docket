package gitvcs

import (
	"sort"
	"strconv"
	"strings"
)

// Who has actually worked in this repository.
//
// The list a project already has and nobody had to type: git records an author
// on every commit. It is not the same list as "who may work here" — that one
// lives on the host and needs a token — but it is the one that is true, offline,
// and available before anybody signs in to anything.

// Contributor is somebody who has committed here.
type Contributor struct {
	Name    string
	Email   string
	Commits int
}

// Contributors reads the authors of every commit, most first.
//
// Merged on email rather than on name: the same person commits as "Vadim" from
// one machine and "Vadym Didenko" from another, and merging on the name would
// make them two people while merging on the address makes them one. Where a
// name differs between commits, the one they used most recently wins — that is
// the spelling they chose last.
func (r *Repo) Contributors() ([]Contributor, error) {
	out, err := r.output("log", "--no-merges", "--pretty=format:%aN\t%aE")
	if err != nil {
		return nil, err
	}

	seen := map[string]*Contributor{}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		name, email, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || strings.TrimSpace(email) == "" {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(email))
		if known, ok := seen[key]; ok {
			known.Commits++
			continue
		}
		seen[key] = &Contributor{Name: strings.TrimSpace(name), Email: strings.TrimSpace(email), Commits: 1}
		order = append(order, key)
	}

	people := make([]Contributor, 0, len(order))
	for _, key := range order {
		people = append(people, *seen[key])
	}
	sort.SliceStable(people, func(a, b int) bool {
		if people[a].Commits != people[b].Commits {
			return people[a].Commits > people[b].Commits
		}
		return people[a].Name < people[b].Name
	})
	return people, nil
}

// String is the contributor as a line a person reads.
func (c Contributor) String() string {
	return c.Name + " — " + strconv.Itoa(c.Commits) + " commits"
}

// Dirty reports whether anything in the working tree is uncommitted.
//
// Asked before anything is thrown away. The board commits everything it writes,
// so a dirty tree is a person editing in the folder — which is exactly whose
// work must not disappear because somebody clicked remove in a browser.
func (r *Repo) Dirty() (bool, error) {
	out, err := r.output("status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}
