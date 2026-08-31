package gitvcs

import (
	"fmt"
	"sort"
	"strconv"
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

/* ---------- releases ---------- */

// Release is a tag: a name, when it was made, and what it points at.
//
// A release is not a thing to model — it is a thing git already has. Jira keeps
// a version object with a name, dates and a released flag, and a `fixVersion`
// on every issue pointing at it, and then generates release notes by asking
// which issues carry that value. All of that is a second record of what a tag
// already says.
//
// Here the tag is the release. It exists or it does not; its date is its date;
// and what shipped in it is `git log v1.1.0..v1.2.0`, which is a question git
// answers without anybody maintaining an answer.
type Release struct {
	Name string
	When time.Time
	// Annotation is the tag message, when the tag has one. A lightweight tag
	// has none, and that is fine — it is still a release.
	Annotation string
	Hash       string
	// depth is how much history the tagged commit contains, used only to break
	// a tie between two commits made in the same second. A release that
	// contains another is later than it, whatever the clock says.
	depth int
}

// Releases are the repository's tags, newest first.
//
// Ordered and dated by the commit each tag points at, not by when somebody
// typed the tag command. Tagging is often retroactive — three releases labelled
// in one afternoon are three releases whose tag dates are minutes apart and
// whose order is meaningless. The commit is when the work existed, and that is
// what a release is.
//
// Not by name either: a version number sorts wrongly as a string, and every
// scheme for sorting one properly is a scheme somebody's version numbers break.
func (r *Repo) Releases() ([]Release, error) {
	// One field per line rather than a separator: for-each-ref has its own
	// format language and does not expand the %x1f that git log does, so a
	// separator asked for that way arrives as the literal text. None of these
	// four fields can contain a newline — a tag subject is its first line — so
	// lines are unambiguous.
	// Five fields, one per line. `*committerdate` dereferences an annotated tag
	// to the commit it points at and is empty for a lightweight one, which has
	// no tag object and whose `committerdate` is already the commit's.
	const fields = 5
	out, err := r.output("for-each-ref",
		"--format=%(refname:short)%0a%(*committerdate:iso-strict)%0a"+
			"%(committerdate:iso-strict)%0a%(objectname)%0a%(contents:subject)",
		"refs/tags")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var releases []Release
	for i := 0; i+fields <= len(lines); i += fields {
		if lines[i] == "" {
			continue
		}
		stamp := lines[i+1]
		if stamp == "" {
			stamp = lines[i+2]
		}
		when, _ := time.Parse(time.RFC3339, stamp)
		releases = append(releases, Release{
			Name: lines[i], When: when, Hash: lines[i+3], Annotation: lines[i+4],
			depth: r.depthOf(lines[i]),
		})
	}

	sort.SliceStable(releases, func(a, b int) bool {
		if !releases[a].When.Equal(releases[b].When) {
			return releases[a].When.After(releases[b].When)
		}
		// Two commits in the same second. The one containing more history is
		// the later one, which is what a chain of releases always is.
		return releases[a].depth > releases[b].depth
	})
	return releases, nil
}

// depthOf is how many commits a ref contains. Zero when git cannot say, which
// leaves the order to the dates.
func (r *Repo) depthOf(ref string) int {
	out, err := r.output("rev-list", "--count", ref)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0
	}
	return n
}

// Shipped is every file that changed between two points, which for two tags is
// what went into a release.
//
// An empty `from` means everything up to `to` — the first release, which
// contains the whole history before it.
func (r *Repo) Shipped(from, to string) ([]string, error) {
	span := to
	if from != "" {
		span = from + ".." + to
	}
	out, err := r.output("-c", "core.quotePath=false",
		"diff", "--name-only", "--diff-filter=ACMR", span)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// At is a file's content at a point in history — a tag, a branch, a commit.
// Empty with no error means the file was not there.
func (r *Repo) At(ref, path string) ([]byte, error) { return r.Blob(ref, path) }

/* ---------- branches ---------- */

// Branch is one line of work in the repository.
type Branch struct {
	Name string
	// Current is the branch the working tree is on. A board reading files from
	// disk is reading this one.
	Current bool
	// Remote says this branch is only on the remote — somebody else's proposal,
	// which is the usual case for one you are reviewing rather than writing.
	Remote  bool
	Subject string
	When    time.Time
}

// Branches are the repository's local branches, the current one first and the
// rest by how recently they moved.
//
// A branch is a proposal about the plan: re-scoping a release, splitting an
// epic, dropping a quarter's work. This is what lets a board show one without
// applying it — see docs/design/git-as-the-database.md.
// Branches is every branch worth reading a proposal from: the local ones, and
// the remote-tracking ones nobody here has checked out.
//
// The remote ones matter more than they look. A proposal you are reviewing is
// usually somebody else's, which means it is on the remote and not in this
// clone — a branch list that showed only local branches showed only your own
// proposals, which is the half you did not need to review.
func (r *Repo) Branches() ([]Branch, error) {
	local, err := r.branchesIn("refs/heads")
	if err != nil {
		return nil, err
	}
	remote, err := r.branchesIn("refs/remotes")
	if err != nil {
		return local, nil // a repository with no remote is not a problem
	}

	// A remote branch that has a local one of the same name is the same
	// proposal, and the local one is the one to read.
	here := map[string]bool{}
	for _, b := range local {
		here[b.Name] = true
	}
	for _, b := range remote {
		// A remote ref is <remote>/<branch>, and the branch may itself hold
		// slashes: origin/предложение/сроки. What has no slash at all is the
		// remote's symbolic HEAD, which git shortens to just "origin" — not a
		// proposal, and it looked like one until a board showed it.
		remoteName, branch, ok := strings.Cut(b.Name, "/")
		if !ok || branch == "HEAD" || remoteName == "" || here[branch] {
			continue
		}
		b.Remote = true
		local = append(local, b)
	}

	sort.SliceStable(local, func(a, b int) bool { return local[a].When.After(local[b].When) })
	sort.SliceStable(local, func(a, b int) bool { return local[a].Current && !local[b].Current })
	return local, nil
}

func (r *Repo) branchesIn(where string) ([]Branch, error) {
	const fields = 4
	out, err := r.output("-c", "core.quotePath=false", "for-each-ref",
		"--sort=-committerdate",
		"--format=%(refname:short)%0a%(HEAD)%0a%(committerdate:iso-strict)%0a%(contents:subject)",
		where)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var branches []Branch
	for i := 0; i+fields <= len(lines); i += fields {
		if lines[i] == "" {
			continue
		}
		when, _ := time.Parse(time.RFC3339, lines[i+2])
		branches = append(branches, Branch{
			Name: lines[i], Current: strings.TrimSpace(lines[i+1]) == "*",
			When: when, Subject: lines[i+3],
		})
	}
	return branches, nil
}

// Fetch brings a ref down from the remote without touching the working tree.
//
// For a proposal somebody else pushed: reviewing it should not need a checkout,
// and it must not disturb whoever is working in the tree.
func (r *Repo) Fetch(cred Credential, ref string) error {
	args := append(PushArgs(cred), "fetch", "--quiet", "origin", ref)
	_, err := r.outputWithEnv(PushEnv(cred), args...)
	return err
}

// Current is the branch the working tree is on, or "" in a detached head.
func (r *Repo) Current() string {
	out, err := r.output("symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Tree is every file under a directory at one point in history.
//
// This is what makes a board over a branch possible without touching the
// working tree: the files are read out of the object database, so looking at a
// proposal cannot disturb what somebody is editing.
func (r *Repo) Tree(ref, dir string) ([]string, error) {
	args := []string{"-c", "core.quotePath=false", "ls-tree", "-r", "--name-only", ref}
	if dir != "" {
		args = append(args, "--", dir)
	}
	out, err := r.output(args...)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}

// MergeBase is where two refs diverged.
//
// It is what a proposal has to be compared against. Comparing a branch with
// whatever the working tree is now would report everything that has happened
// on the working tree since as though the proposal were undoing it — the
// proposal did not touch those tasks, and saying it did is worse than saying
// nothing.
func (r *Repo) MergeBase(a, b string) (string, error) {
	out, err := r.output("merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Distance is how many commits each of two refs has that the other does not.
func (r *Repo) Distance(base, ref string) (behind, ahead int, err error) {
	out, err := r.output("rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("git rev-list said %q", strings.TrimSpace(out))
	}
	behind, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	return behind, ahead, err
}
