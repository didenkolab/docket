package server

import (
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Backlinks are what Obsidian calls linked mentions: everything that points at
// this note.
//
// Obsidian shows them in a pane beside every note, and for a tracker they are
// the answer to a question Jira needs a whole feature for — what else refers to
// this task. A task that blocks another, a decision page that cites it, a
// retrospective that mentions it: all of them are `[[ACME-12 …]]` in some file,
// and none of them had to be declared as a relationship in advance.
//
// That is the difference between a link and a field. A field has to be invented
// before it can be used; a link is written in a sentence and is a relationship
// the moment it is saved.

// mention is one file that links here.
type mention struct {
	Href  string
	Label string
	// Task is set when the thing linking is a task rather than a page, so a
	// list of mentions can show what state that work is in.
	Task *hitTask
	// Context is the sentence the link sits in — what Obsidian shows under each
	// linked mention, and the thing that makes the list worth reading.
	Context string
}

// backlinks is every file that links to a note, newest-looking first: tasks
// before pages, then by key.
//
// Read on every request, like everything else. Obsidian keeps an index and
// warns that it can fall out of step with the files; there is no index here to
// fall out of step.
func (s *Server) backlinks(r *http.Request, note, selfPath string, relations []string) []mention {
	entries, err := s.entries(r)
	if err != nil {
		return nil
	}

	var tasks, pages []mention

	for _, e := range entries {
		if e.Task == nil || e.Path == selfPath {
			continue
		}
		body := e.Task.Body()
		// A relation is a link in frontmatter, and Obsidian counts one as a
		// backlink — which is the whole reason a relationship here is a link
		// rather than a field. Missing them meant a story never learned that
		// eleven tests point at it: the link existed, on one side, and the
		// other side of the page said "no backlinks".
		linked := false
		for _, name := range relations {
			if mentions(strings.Join(e.Task.RawRelated(name), " "), note) {
				linked = true
				break
			}
		}
		if !linked && !mentions(body, note) &&
			!mentions(strings.Join(e.Task.RawLabels(), " "), note) &&
			!mentions(e.Task.RawParent(), note) {
			continue
		}
		tasks = append(tasks, mention{
			Href:  "/task/" + e.Key,
			Label: e.Key + " " + e.Task.Title,
			Task: &hitTask{
				Key: e.Key, Title: e.Task.Title,
				Status: e.Task.Status, Category: e.Task.StatusCategory,
			},
			Context: around(body, note),
		})
	}

	for _, page := range s.pages() {
		if page+".md" == selfPath {
			continue
		}
		full, err := s.abs(page + ".md")
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(full)
		if err != nil || !mentions(string(raw), note) {
			continue
		}
		pages = append(pages, mention{
			Href:    "/page/" + page,
			Label:   page,
			Context: around(string(raw), note),
		})
	}

	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].Label < tasks[j].Label })
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Label < pages[j].Label })
	return append(tasks, pages...)
}

// mentions says whether text carries a wikilink to a note.
//
// The link has to be a link: the note's name appearing in a sentence is what
// Obsidian calls an unlinked mention and shows separately, because it is a
// coincidence until somebody makes it a link.
func mentions(text, note string) bool {
	for _, target := range task.Links(text) {
		if strings.EqualFold(target, note) || strings.EqualFold(path.Base(target), note) {
			return true
		}
	}
	return false
}

// around is the sentence a link sits in, which is what makes a list of
// backlinks worth reading rather than a list of file names.
func around(text, note string) string {
	needle := "[[" + note
	at := strings.Index(strings.ToLower(text), strings.ToLower(needle))
	if at < 0 {
		return ""
	}
	return clip(text, at-70, at+len(needle)+70)
}

// linkProperties is every relation the vault declares, so a backlink scan knows
// which properties hold links rather than words.
func linkProperties(c *project.Config) []string {
	relations := c.Relations()
	names := make([]string, 0, len(relations))
	for _, r := range relations {
		names = append(names, r.Name)
	}
	return names
}
