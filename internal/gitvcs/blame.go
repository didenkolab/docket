package gitvcs

import (
	"strconv"
	"strings"
	"time"
)

// Who wrote each line.
//
// A tracker stores a description as one value and overwrites it, so the
// sentence that changed the scope of a task has no author and no date — the
// activity feed says "Dana edited the description", which is the least useful
// true thing that could be said about it.
//
// A task here is a file, so git already knows. `git blame` on it says who wrote
// each acceptance criterion and when, and that is not a feature anybody had to
// build: it is what the storage was going to say anyway.

// BlameLine is one line of a file and the commit it came from.
type BlameLine struct {
	// Number is the line's place in the file as it stands.
	Number int
	Text   string
	// Commit is the commit that last touched the line.
	Commit string
	Author string
	When   time.Time
	// Summary is that commit's subject, which for this vault is usually the
	// change said in words — "ACME-1: Backlog → Ready".
	Summary string
}

// Blame says who wrote each line of a file.
//
// Renames are followed, which matters here more than anywhere: retitling a task
// moves its file, and a blame that stopped at the rename would say the whole
// task was written by whoever renamed it.
func (r *Repo) Blame(path string) ([]BlameLine, error) {
	// --porcelain, because the human format is aligned for reading and its
	// columns move with the longest author name in the file.
	//
	// -C follows content across renames; core.quotePath=false because git
	// escapes non-ASCII paths and this vault is full of them.
	out, err := r.output("-c", "core.quotePath=false", "blame", "--porcelain", "-C", "--", path)
	if err != nil {
		return nil, err
	}
	return parseBlame(out), nil
}

// parseBlame reads git's porcelain blame.
//
// The format states a commit's details once and then refers back to it by hash,
// so the metadata is remembered per commit rather than expected on every line.
func parseBlame(out string) []BlameLine {
	type about struct {
		author  string
		when    time.Time
		summary string
	}
	known := map[string]about{}

	var lines []BlameLine
	var commit string
	var current about
	number := 0

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "\t"):
			// The line's own content, which ends the group's header.
			if seen, ok := known[commit]; ok && current.author == "" {
				current = seen
			}
			known[commit] = current
			lines = append(lines, BlameLine{
				Number: number, Text: strings.TrimPrefix(line, "\t"),
				Commit: commit, Author: current.author,
				When: current.when, Summary: current.summary,
			})
			current = about{}

		case strings.HasPrefix(line, "author "):
			current.author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			if seconds, err := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64); err == nil {
				current.when = time.Unix(seconds, 0)
			}
		case strings.HasPrefix(line, "summary "):
			current.summary = strings.TrimPrefix(line, "summary ")

		case len(line) >= 40 && !strings.HasPrefix(line, "\t"):
			// A header line: hash, original line, final line, and sometimes a
			// group size. Anything else with a space is a field we ignore.
			fields := strings.Fields(line)
			if len(fields) >= 3 && len(fields[0]) == 40 {
				commit = fields[0]
				if n, err := strconv.Atoi(fields[2]); err == nil {
					number = n
				}
				if seen, ok := known[commit]; ok {
					current = seen
				}
			}
		}
	}
	return lines
}
