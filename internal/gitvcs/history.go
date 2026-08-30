package gitvcs

import (
	"fmt"
	"strings"
	"time"
)

// Change is one commit that touched a file, and what it did to it.
type Change struct {
	Hash    string
	Author  Author
	When    time.Time
	Subject string

	// Path is the file's name after this commit. On the commit that renamed
	// it — which is how a retitle looks — Was is the name it had before.
	Path string
	Was  string

	// Added and Deleted say whether the file came into or went out of
	// existence here, so a history can open with "created" and end with
	// "deleted" rather than with a silent gap.
	Added   bool
	Deleted bool
}

// unit separates fields inside a log record: ASCII unit separator, which a
// name, an address, a date or a one-line subject cannot contain.
//
// git writes the byte; the format string asks for it by name. A NUL would be
// the obvious choice and cannot be used, because the format reaches git as a
// command-line argument and an argument ends at the first NUL.
const (
	unitFormat = "%x1f"
	unit       = "\x1f"
)

// History is every commit that touched the files matching a pathspec, newest
// first.
//
// A task's history is found by pathspec rather than by following one file,
// because a task's name changes when its title does. Git can follow a rename,
// but only by guessing from how similar the two versions look — and a retitle
// that also rewrites the body falls under the threshold, at which point the
// history silently stops at the rename and the task appears to have been
// created the day somebody reworded it. The key is in the file name, so
// `ACME/ACME-12 *.md` is the whole life of ACME-12 and nothing else, decided by
// the format rather than by a heuristic.
func (r *Repo) History(pathspec string, limit int) ([]Change, error) {
	args := []string{
		// git escapes any byte outside ASCII in the paths it prints, and wraps
		// the result in quotes: `"BETA/BETA-2 \320\237….md"`. A title may be
		// written in any script, so almost every path in a real vault comes
		// back mangled and nothing can be read from it afterwards.
		"-c", "core.quotePath=false",
		"log",
		"--format=" + strings.Join([]string{"%H", "%an", "%ae", "%aI", "%s"}, unitFormat),
		"--name-status",
		"--no-renames", // a rename shows as a delete and an add, which is what a retitle is
	}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-%d", limit))
	}
	args = append(args, "--", pathspec)

	out, err := r.output(args...)
	if err != nil {
		return nil, err
	}
	return parseLog(out), nil
}

// parseLog reads `git log --name-status` output: a record line, then a blank
// line, then one line per file it touched, then the next record.
func parseLog(out string) []Change {
	var changes []Change
	var current *Change
	var touched [][2]string // status, path — for the commit being read

	flush := func() {
		if current == nil {
			return
		}
		current.apply(touched)
		if current.Path != "" || current.Was != "" {
			changes = append(changes, *current)
		}
		current, touched = nil, nil
	}

	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if fields := strings.Split(line, unit); len(fields) == 5 {
			flush()
			when, _ := time.Parse(time.RFC3339, fields[3])
			current = &Change{
				Hash:    fields[0],
				Author:  Author{Name: fields[1], Email: fields[2]},
				When:    when,
				Subject: fields[4],
			}
			continue
		}
		if current == nil {
			continue
		}
		if status, path, found := strings.Cut(line, "\t"); found && status != "" {
			touched = append(touched, [2]string{status[:1], path})
		}
	}
	flush()
	return changes
}

// apply works out what one commit did to the task, from the files it touched.
//
// With renames turned off, a retitle arrives as a delete and an add in the same
// commit — the same task under two names. Deciding once, from all of a commit's
// files together, avoids depending on which order git lists them in.
func (c *Change) apply(touched [][2]string) {
	var added, deleted string
	for _, t := range touched {
		switch t[0] {
		case "A":
			added = t[1]
		case "D":
			deleted = t[1]
		default: // M, and anything else that leaves the file where it was
			c.Path = t[1]
		}
	}

	switch {
	case added != "" && deleted != "": // a retitle
		c.Path, c.Was = added, deleted
	case added != "":
		c.Path, c.Added = added, true
	case deleted != "":
		c.Was, c.Deleted = deleted, true
	}
}

// Blob is a file's content at one commit. An empty result with no error means
// the file did not exist there, which is how a history knows where to stop
// comparing.
func (r *Repo) Blob(hash, path string) ([]byte, error) {
	out, err := r.output("show", hash+":"+path)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") ||
			strings.Contains(err.Error(), "exists on disk, but not in") {
			return nil, nil
		}
		return nil, err
	}
	return []byte(out), nil
}
