package server

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The board as it was.
//
// A board is the present tense, and every question about last week is answered
// by somebody's memory. Here the past is not a changelog behind an API — it is
// the repository, and the board at any commit is the board that commit
// describes. Reading a ref is already how a proposal is looked at; this walks
// the commits instead of naming a branch.
//
// What it will not do is let anybody change the past. Everything that writes
// refuses when a ref is set — see space.Writable — so a board being looked at
// backwards is exactly that: looked at.

// commitName is what a commit may be called on the way in. Read rather than
// trusted: it goes into a git command, and an unchecked one is an argument
// somebody else chose.
var commitName = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// steps is how far back the walk can see in one page. A board being read
// backwards is being read a few steps at a time, not paged through a year.
const steps = 400

// travel is where the board is in the history, and where it can go from there.
type travel struct {
	// At is the commit being read, empty for the working tree.
	At      string
	Short   string
	Subject string
	Who     string
	When    string
	// Earlier and Later are the commits either side, empty at each end.
	Earlier string
	Later   string
	// Now is where to go back to the present.
	Now string
	// Position is "3 of 128", so a person knows how far back they are.
	Position string
	// Trouble says why the board cannot be walked, when it cannot.
	Trouble string
}

// timeTravel reads the walk for this request.
//
// Only a single vault. In a workspace a commit belongs to one repository, and
// the same hash means nothing in the other three — a board of four projects
// wound back would be one project's past beside three projects' present, which
// is a picture of a moment that never existed.
func (s *Server) timeTravel(r *http.Request) travel {
	at := strings.TrimSpace(r.URL.Query().Get("at"))
	if at == "" && !s.wantsTravel(r) {
		return travel{}
	}

	single := s.sp().Single()
	if single == nil || single.Repo == nil {
		return travel{Trouble: "A commit belongs to one repository, and this is a workspace " +
			"of several. Open a project on its own to read its board backwards."}
	}
	if at != "" && !commitName.MatchString(at) {
		return travel{Trouble: "That is not a commit."}
	}

	changes, err := single.Repo.History(".", steps)
	if err != nil || len(changes) == 0 {
		return travel{Trouble: "This repository has no history to walk."}
	}

	out := travel{Now: r.URL.Path}
	where := 0
	if at != "" {
		where = -1
		for i, change := range changes {
			if strings.HasPrefix(change.Hash, at) {
				where = i
				break
			}
		}
		if where < 0 {
			return travel{Trouble: "No commit here starts with " + at + "."}
		}
	}

	here := changes[where]
	if at != "" {
		out.At, out.Short = here.Hash, short(here.Hash)
		out.Subject, out.Who = here.Subject, here.Author.Name
		out.When = here.When.Format(time.RFC3339)
	}
	if where+1 < len(changes) {
		out.Earlier = elsewhere(r.URL, "at", short(changes[where+1].Hash))
	}
	if where > 0 {
		out.Later = elsewhere(r.URL, "at", short(changes[where-1].Hash))
	}
	out.Position = position(where, len(changes))
	return out
}

// wantsTravel says whether the board should offer the walk at all.
//
// Offered on a vault with a history and nothing else: a workspace has no single
// past to walk, and a repository with one commit has nowhere to go.
func (s *Server) wantsTravel(r *http.Request) bool {
	single := s.sp().Single()
	return single != nil && single.Repo != nil
}

func position(where, of int) string {
	if where == 0 {
		return "now"
	}
	return strconv.Itoa(where) + " back of " + strconv.Itoa(of)
}
