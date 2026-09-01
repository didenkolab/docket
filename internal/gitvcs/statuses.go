package gitvcs

import (
	"strconv"
	"strings"
	"time"
)

// When each task changed status, read out of the history in one pass.
//
// Four of the Jira marketplace's top hundred sell this one report — how long
// work sits in each column — because Jira keeps a changelog per issue and
// charges an app to aggregate it. Here it is not a feature at all: every move
// was a commit, so the answer is in `git log`, and the only work is reading it.
//
// One pass over the whole project rather than one `git log` per task. A board
// of eleven hundred tasks is eleven hundred processes done the other way, and a
// report nobody waits for is a report nobody runs.

// StatusChange is a task arriving in a status.
type StatusChange struct {
	// Path is the file, relative to the repository.
	Path   string
	Status string
	When   time.Time
	Who    string
}

// StatusChanges reads every status a file has held, oldest first, for every
// file under dir.
//
// Renames are followed by git itself; a task renamed when its title changed
// keeps one history, which is the whole reason the move is a commit rather than
// a row in a table.
func (r *Repo) StatusChanges(dir string) (map[string][]StatusChange, error) {
	// Where each file ended up. A task renamed when its title changed is one
	// task, and reading its two names as two lives would say every renamed task
	// arrived in its column on the day somebody fixed a typo in the title.
	//
	// Two passes because git will give either the names or the patch, not both:
	// --name-status suppresses -p. The first is cheap — no patch text at all.
	renamed, err := r.renames(dir)
	if err != nil {
		return nil, err
	}

	// %x00 starts a commit, and the fields are separated by %x1f so that a name
	// with a space or a tab in it cannot be mistaken for a field.
	out, err := r.output("-c", "core.quotePath=false", "log", "--reverse",
		"--format=%x00%aI%x1f%aN", "-p", "-M", "--", dir)
	if err != nil {
		return nil, err
	}

	changes := map[string][]StatusChange{}
	var when time.Time
	var who, path string

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\x00"):
			stamp, name, _ := strings.Cut(strings.TrimPrefix(line, "\x00"), "\x1f")
			when, _ = time.Parse(time.RFC3339, strings.TrimSpace(stamp))
			who, path = strings.TrimSpace(name), ""

		case strings.HasPrefix(line, "+++ b/"):
			// git puts a tab after the name when it holds a space, so that the
			// header stays unambiguous. Nearly every task file here has one.
			path = renamed.finally(unquote(
				strings.TrimRight(strings.TrimPrefix(line, "+++ b/"), "\t")))

		case strings.HasPrefix(line, "+status:") && path != "":
			status := strings.TrimSpace(strings.TrimPrefix(line, "+status:"))
			if status == "" {
				continue
			}
			// The same status written again is not a move: a title change
			// rewrites the file, and the whole frontmatter arrives as added
			// lines when the file is new to git.
			if held := changes[path]; len(held) > 0 && held[len(held)-1].Status == status {
				continue
			}
			changes[path] = append(changes[path], StatusChange{
				Path: path, Status: status, When: when, Who: who,
			})
		}
	}
	return changes, nil
}

// trail is where each file ended up, by the name it had at the time.
type trail map[string]string

// finally follows the chain of renames to the name a file has now.
func (t trail) finally(path string) string {
	for i := 0; i < 32; i++ {
		next, ok := t[path]
		if !ok || next == path {
			return path
		}
		path = next
	}
	return path
}

// renames reads what was renamed to what, oldest first.
func (r *Repo) renames(dir string) (trail, error) {
	out, err := r.output("-c", "core.quotePath=false", "log", "--reverse",
		"--format=", "--name-status", "-M", "--diff-filter=R", "--", dir)
	if err != nil {
		return nil, err
	}

	moved := trail{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "R") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 {
			continue
		}
		moved[unquote(parts[1])] = unquote(parts[2])
	}
	return moved, nil
}

// unquote undoes git's quoting of a path with something unusual in it. Paths
// here are Cyrillic more often than not, and core.quotePath=false covers that;
// a path with a quote or a tab in it still arrives quoted.
func unquote(path string) string {
	path = strings.TrimSpace(path)
	if len(path) < 2 || !strings.HasPrefix(path, `"`) || !strings.HasSuffix(path, `"`) {
		return path
	}
	if unquoted, err := strconv.Unquote(path); err == nil {
		return unquoted
	}
	return path
}
