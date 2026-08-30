package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// historyDepth is how far back one page goes. A task with more than this many
// changes is a task nobody is going to read to the bottom of, and git has the
// rest.
const historyDepth = 100

type historyView struct {
	Key     string
	Title   string
	Entries []entry
	Path    string
}

// entry is one commit, said in the vocabulary of the tracker rather than of
// git: who, when, and which fields moved.
type entry struct {
	Hash    string
	Short   string
	Author  string
	Initial string
	When    string
	Subject string
	Changes []fieldChange
	Created bool
	Deleted bool
}

type fieldChange struct {
	Field string
	From  string
	To    string
	// Note carries a change that has no before and after worth printing — a
	// comment, a rewritten description.
	Note string
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	key := keyOf(r)
	c, err := project.Load(s.root)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the vault", err.Error())
		return
	}

	t, _, err := s.loadTask(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}
	rel, _, err := s.locate(key)
	if err != nil {
		s.fail(w, r, http.StatusNotFound, "No such task", key+" is not in this vault")
		return
	}

	projectKey, _, err := project.SplitKey(key)
	if err != nil {
		s.fail(w, r, http.StatusBadRequest, "Not a key", key+" is not a task key")
		return
	}

	// Every file this task has ever had. The key is in the name, so this is the
	// task's whole life and nothing else — see gitvcs.History.
	changes, err := s.repo.History(projectKey+"/"+key+" *.md", historyDepth)
	if err != nil {
		s.fail(w, r, http.StatusInternalServerError, "Cannot read the history", err.Error())
		return
	}

	s.render(w, r, "history.html", c, key+" — history", historyView{
		Key:     key,
		Title:   t.Title,
		Path:    rel,
		Entries: s.describe(changes),
	})
}

// describe turns commits into what changed, by comparing each version of the
// task with the one before it.
//
// The versions compared are consecutive entries in this task's own history
// rather than a commit and its git parent. They are the same thing whenever the
// history is a line, and when it is not — a merge — the older of the two is
// still the last state anybody saw, which is the useful comparison.
func (s *Server) describe(changes []gitvcs.Change) []entry {
	entries := make([]entry, 0, len(changes))

	for i, ch := range changes {
		e := entry{
			Hash:    ch.Hash,
			Short:   short(ch.Hash),
			Author:  ch.Author.Name,
			Initial: initialOf(ch.Author.Name),
			When:    ch.When.UTC().Format("2006-01-02 15:04"),
			Subject: ch.Subject,
			Created: ch.Added,
			Deleted: ch.Deleted,
		}

		// The state after this commit, and the state after the previous one —
		// which is this task's previous version, whatever it was called then.
		after := s.versionAt(ch.Hash, ch.Path)
		var before *task.Task
		if i+1 < len(changes) {
			previous := changes[i+1]
			before = s.versionAt(previous.Hash, previous.Path)
		}

		switch {
		case ch.Deleted:
			e.Changes = []fieldChange{{Note: "deleted the task"}}
		case before == nil && after != nil:
			e.Changes = []fieldChange{{Note: "created the task"}}
			e.Created = true
		case before != nil && after != nil:
			e.Changes = compare(before, after)
			if ch.Was != "" && ch.Path != ch.Was {
				e.Changes = append(e.Changes, fieldChange{Note: "renamed the file to match"})
			}
		}
		entries = append(entries, e)
	}
	return entries
}

// versionAt parses the task as it stood at one commit. A version that will not
// parse is not an error worth stopping a history for — it simply cannot be
// compared, and the commit still shows with its message.
func (s *Server) versionAt(hash, path string) *task.Task {
	if path == "" {
		return nil
	}
	raw, err := s.repo.Blob(hash, path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	t, err := task.Parse(raw)
	if err != nil {
		return nil
	}
	return t
}

// tracked is the frontmatter a history reports on, in the order a person reads
// them. Anything else in the frontmatter is somebody's own field and is left to
// the diff in git.
var tracked = []struct {
	field string
	of    func(*task.Task) string
}{
	{"title", func(t *task.Task) string { return t.Title }},
	{"status", func(t *task.Task) string { return t.Status }},
	{"type", func(t *task.Task) string { return t.Type }},
	{"priority", func(t *task.Task) string { return t.Priority }},
	{"assignee", func(t *task.Task) string { return t.Assignee }},
	{"parent", func(t *task.Task) string { return t.Parent }},
	{"labels", func(t *task.Task) string { return strings.Join(t.Labels, ", ") }},
}

// compare says what moved between two versions of a task.
//
// `updated` is deliberately not reported: it changes on every edit and saying
// so on every entry would bury the change somebody is looking for. Neither is
// `order`, which is where a card sits on a board and not something that
// happened to the work.
func compare(before, after *task.Task) []fieldChange {
	var changes []fieldChange

	for _, f := range tracked {
		was, now := f.of(before), f.of(after)
		if was == now {
			continue
		}
		changes = append(changes, fieldChange{Field: f.field, From: blank(was), To: blank(now)})
	}

	if n := len(after.Comments()) - len(before.Comments()); n != 0 {
		changes = append(changes, fieldChange{Note: countOf(n, "comment", "comments")})
	}
	if before.Description() != after.Description() {
		changes = append(changes, fieldChange{Note: "edited the description"})
	}
	if n := len(after.Attachments()) - len(before.Attachments()); n != 0 {
		changes = append(changes, fieldChange{Note: countOf(n, "attachment", "attachments")})
	}
	// Where a card sits in its column is not a field, but it is something
	// somebody did, and saying nothing about it leaves an entry that claims
	// nothing happened.
	if placeOf(before) != placeOf(after) {
		changes = append(changes, fieldChange{Note: "moved it in its column"})
	}
	return changes
}

// countOf says how many of something were added or taken away.
func countOf(n int, one, many string) string {
	verb := "added"
	if n < 0 {
		verb, n = "removed", -n
	}
	word := many
	if n == 1 {
		word = one
	}
	return verb + " " + strconv.Itoa(n) + " " + word
}

func placeOf(t *task.Task) int {
	if t.Order == nil {
		return -1 // "no order" is a state of its own, and no order is a real one
	}
	return *t.Order
}

// blank is what an empty value reads as. "assignee — → dana" says less than
// "assignee nobody → dana".
func blank(v string) string {
	if strings.TrimSpace(v) == "" {
		return "—"
	}
	return v
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}

func initialOf(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}
