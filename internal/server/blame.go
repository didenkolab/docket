package server

import (
	"net/http"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
)

// Who wrote each line of a task.
//
// A tracker stores a description as one value and overwrites it, so the
// sentence that changed the scope of a task has no author and no date. The
// activity feed says "Dana edited the description", which is the least useful
// true thing that could be said about it.
//
// A task here is a file, so git already knows, and this page is a reading of
// something the storage was going to say anyway. Named in
// docs/design/git-as-the-database.md as one of the two small things worth doing.
//
// The frontmatter is shown as well as the body. It is where the status is, and
// "who moved this to Done, and when" is asked at least as often as "who wrote
// this criterion" — the difference is that the second has no other way of being
// answered.

type blameView struct {
	Key   string
	Title string
	Path  string
	Lines []blameLine
	// People are who has written any of it, most lines first, so the page opens
	// with an answer to "whose task is this really".
	People []blameShare
}

type blameLine struct {
	Number  int
	Text    string
	Author  string
	When    string
	Short   string
	Summary string
	// Same says this line came from the same commit as the one above, so the
	// gutter can stay quiet instead of repeating itself down a whole block.
	Same bool
	// Front says the line is frontmatter rather than body.
	Front bool
}

type blameShare struct {
	Author string
	Lines  int
}

func (s *Server) handleBlame(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := s.config()
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	t, _, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	owner, inVault, _, err := s.sp().Locate(key)
	if err != nil || owner.Repo == nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this space")
		return
	}

	lines, err := owner.Repo.Blame(inVault)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read who wrote it", err.Error())
		return
	}

	s.render(w, r, "blame.html", c, key+" — who wrote it", blameView{
		Key:    key,
		Title:  t.Title,
		Path:   owner.PathIn(inVault),
		Lines:  describeBlame(lines),
		People: sharesIn(lines),
	})
}

// describeBlame turns the lines into what the page shows: dates as dates, the
// gutter quiet where a block comes from one commit, and frontmatter marked.
func describeBlame(lines []gitvcs.BlameLine) []blameLine {
	out := make([]blameLine, 0, len(lines))
	previous := ""
	// The frontmatter is everything up to the second `---`, which is how a task
	// file is shaped — see task.Parse.
	delimiters := 0

	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "---" && delimiters < 2 {
			delimiters++
		}
		out = append(out, blameLine{
			Number:  l.Number,
			Text:    l.Text,
			Author:  l.Author,
			When:    l.When.Format("2006-01-02"),
			Short:   short(l.Commit),
			Summary: l.Summary,
			Same:    l.Commit == previous,
			Front:   delimiters < 2 || strings.TrimSpace(l.Text) == "---" && delimiters == 2,
		})
		previous = l.Commit
	}
	return out
}

// sharesIn is who wrote how much, most first.
func sharesIn(lines []gitvcs.BlameLine) []blameShare {
	count := map[string]int{}
	var order []string
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" {
			continue // a blank line is nobody's sentence
		}
		if _, seen := count[l.Author]; !seen {
			order = append(order, l.Author)
		}
		count[l.Author]++
	}

	shares := make([]blameShare, 0, len(order))
	for _, author := range order {
		shares = append(shares, blameShare{Author: author, Lines: count[author]})
	}
	for i := 1; i < len(shares); i++ {
		for j := i; j > 0 && shares[j].Lines > shares[j-1].Lines; j-- {
			shares[j], shares[j-1] = shares[j-1], shares[j]
		}
	}
	return shares
}
