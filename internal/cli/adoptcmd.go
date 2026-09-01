package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

const adoptUsage = `docket adopt — promote an imported property to what the format calls it.

Usage:
  docket adopt --sprint PROPERTY [--estimate PROPERTY] [--people] [--dry-run] [directory]

An import cannot know which of Jira's custom fields is the sprint: it arrives as
x_спринт or customfield_10020, one of a dozen properties nobody declared, and
the board's own sprint stays empty beside it. On a real imported project that is
ninety nine tasks in twelve sprints that no sprint page, no burndown and no
"what did we commit to" can see.

This says which is which, after the fact. --sprint writes a page per sprint and
points every task at it. --estimate promotes a number. --people writes a page
for each handle the tasks name and turns the assignees into links.

Sprint dates are inferred from the work: the earliest task created and the last
one changed. That is a guess, and every page says so in a line you should
correct — a guess written as a fact is worse than a blank. Flags:
`

func runAdopt(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("adopt", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, adoptUsage)
		flags.PrintDefaults()
	}
	sprintFrom := flags.String("sprint", "", "the property holding the sprint's name")
	estimateFrom := flags.String("estimate", "", "the property holding the size")
	people := flags.Bool("people", false,
		"write a page for each handle the tasks name, and link the assignees to it")
	dry := flags.Bool("dry-run", false, "say what it would change, and change nothing")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *sprintFrom == "" && *estimateFrom == "" && !*people {
		fmt.Fprint(stderr, "docket adopt: say what to adopt\n\n")
		fmt.Fprint(stderr, adoptUsage)
		return exitUsage
	}
	dir, code := oneDirectory(flags, "adopt", stderr)
	if code != exitOK {
		return code
	}

	root, err := project.FindRoot(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket adopt: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket adopt: %v\n", err)
		return exitError
	}
	entries, err := vault.List(root, c)
	if err != nil {
		fmt.Fprintf(stderr, "docket adopt: %v\n", err)
		return exitError
	}

	changed := map[string]bool{}
	var wrote []string

	if *sprintFrom != "" {
		pages, touched, err := adoptSprints(root, entries, *sprintFrom, *dry)
		if err != nil {
			fmt.Fprintf(stderr, "docket adopt: %v\n", err)
			return exitError
		}
		wrote = append(wrote, pages...)
		for _, key := range touched {
			changed[key] = true
		}
	}
	if *estimateFrom != "" {
		touched, err := adoptEstimates(root, entries, *estimateFrom, *dry)
		if err != nil {
			fmt.Fprintf(stderr, "docket adopt: %v\n", err)
			return exitError
		}
		for _, key := range touched {
			changed[key] = true
		}
	}
	if *people {
		pages, touched, err := adoptPeople(root, entries, *dry)
		if err != nil {
			fmt.Fprintf(stderr, "docket adopt: %v\n", err)
			return exitError
		}
		wrote = append(wrote, pages...)
		for _, key := range touched {
			changed[key] = true
		}
	}

	for _, page := range wrote {
		fmt.Fprintf(stdout, "wrote %s\n", page)
	}
	fmt.Fprintf(stdout, "%d tasks changed, %d pages written.\n", len(changed), len(wrote))
	if *dry {
		fmt.Fprint(stdout, "Nothing was written: --dry-run.\n")
		return exitOK
	}
	if len(changed) > 0 || len(wrote) > 0 {
		fmt.Fprint(stdout, "\nLook at the diff, then commit it. "+
			"Run docket check --fix to bring the boards along.\n")
	}
	return exitOK
}

// adoptSprints writes a page per sprint and points the tasks at it.
func adoptSprints(root string, entries []vault.Entry, property string, dry bool) (pages, touched []string, err error) {
	inSprint := map[string][]vault.Entry{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		// Jira's sprint field holds every sprint the work passed through, and
		// the import flattens that to "A, B, C". The task is in the last of
		// them, which is Jira's own reading and ours — a task carried into the
		// next fortnight names the one it is in now, and the one it came from
		// says so in its retrospective.
		//
		// The earlier names get no page: nothing would point at it, and a
		// sprint page nothing is in, dated from no work, would sit on the
		// sprints page looking like a fortnight that is running.
		names := sprintList(e.Task.Property(property))
		if len(names) == 0 {
			continue
		}
		last := names[len(names)-1]
		inSprint[last] = append(inSprint[last], e)
	}
	if len(inSprint) == 0 {
		return nil, nil, fmt.Errorf("no task carries %s", property)
	}

	var names []string
	for name := range inSprint {
		names = append(names, name)
	}
	sort.Strings(names)

	known, _ := vault.Sprints(root)
	for _, name := range names {
		note := sprintNote(name)
		if _, ok := sprintNamed(known, note); !ok {
			at := filepath.Join(root, filepath.FromSlash(vault.SprintDir), note+".md")
			if !dry {
				if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
					return pages, touched, err
				}
				starts, ends := span(inSprint[name])
				if err := os.WriteFile(at, []byte(sprintPage(name, starts, ends, property,
					len(inSprint[name]))), 0o644); err != nil {
					return pages, touched, err
				}
			}
			pages = append(pages, filepath.ToSlash(filepath.Join(vault.SprintDir, note+".md")))
		}

		for _, e := range inSprint[name] {
			if e.Task.Sprint == note {
				continue
			}
			if !dry {
				e.Task.SetSprint(note)
				if err := writeTask(root, e); err != nil {
					return pages, touched, err
				}
			}
			touched = append(touched, e.Key)
		}
	}
	return pages, touched, nil
}

// sprintList reads one or several sprint names out of a property.
func sprintList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// span is the fortnight the work suggests: the earliest task created and the
// last one changed.
func span(entries []vault.Entry) (starts, ends time.Time) {
	for _, e := range entries {
		if when, err := task.ParseTime(e.Task.Created); err == nil {
			if starts.IsZero() || when.Before(starts) {
				starts = when
			}
		}
		if when, err := task.ParseTime(e.Task.Updated); err == nil {
			if when.After(ends) {
				ends = when
			}
		}
	}
	if starts.IsZero() {
		starts = ends
	}
	if ends.IsZero() {
		ends = starts
	}
	return starts, ends
}

// sprintPage is what an adopted sprint starts as.
//
// The dates are a guess and the page says so in its first line. A sprint whose
// dates were inferred and presented as fact would make every report about it
// quietly wrong, and nobody would know which reports.
func sprintPage(name string, starts, ends time.Time, from string, count int) string {
	day := "2006-01-02"
	return "---\ntitle: " + name + "\ntype: sprint\n" +
		"starts: " + starts.Format(day) + "\nends: " + ends.Format(day) + "\n" +
		"inferred: true\n---\n\n" +
		"# " + name + "\n\n" +
		"Adopted from `" + from + "` when this vault came out of Jira, with " +
		strconv.Itoa(count) + " tasks in it.\n\n" +
		"**The dates above are a guess** — the earliest task created and the last one " +
		"changed. Correct them and remove `inferred: true`: everything measured " +
		"about this sprint is measured between those dates, and until the flag " +
		"goes nothing checks that they make sense beside the other sprints.\n\n" +
		"## What we said we would do\n\n" +
		"Nobody wrote this down at the time. If it still matters, write it now.\n"
}

// sprintNote is what the page will be called, which is what a task's `sprint:`
// will say.
func sprintNote(name string) string {
	note := strings.TrimSpace(name)
	// A file name cannot hold these, and a wikilink to one that does is a link
	// Obsidian will not resolve.
	for _, bad := range []string{"/", "\\", ":", "|", "#", "^", "[", "]"} {
		note = strings.ReplaceAll(note, bad, " ")
	}
	return strings.Join(strings.Fields(note), " ")
}

func sprintNamed(known []vault.Sprint, note string) (vault.Sprint, bool) {
	for _, s := range known {
		if strings.EqualFold(s.Note, note) {
			return s, true
		}
	}
	return vault.Sprint{}, false
}

// adoptEstimates promotes a number to the estimate the format knows.
func adoptEstimates(root string, entries []vault.Entry, property string, dry bool) ([]string, error) {
	var touched []string
	for _, e := range entries {
		if e.Task == nil || e.Task.Sized() {
			continue
		}
		raw := strings.TrimSpace(e.Task.Property(property))
		if raw == "" {
			continue
		}
		size, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			// Left alone rather than guessed at. "M" is a size to somebody, and
			// turning it into a number would invent the scale.
			continue
		}
		if !dry {
			e.Task.SetEstimate(size)
			if err := writeTask(root, e); err != nil {
				return touched, err
			}
		}
		touched = append(touched, e.Key)
	}
	return touched, nil
}

// adoptPeople writes a page per handle and turns the assignees into links.
func adoptPeople(root string, entries []vault.Entry, dry bool) (pages, touched []string, err error) {
	known, _ := vault.People(root)
	carrying := vault.HandlesIn(entries)

	var handles []string
	for handle := range carrying {
		handles = append(handles, handle)
	}
	sort.Strings(handles)

	for _, handle := range handles {
		if _, ok := vault.PersonOf(known, handle); ok {
			continue
		}
		at := filepath.Join(root, vault.PeopleDir, handle+".md")
		if !dry {
			if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
				return pages, touched, err
			}
			if err := os.WriteFile(at, []byte(vault.PersonPage(handle, "")), 0o644); err != nil {
				return pages, touched, err
			}
		}
		pages = append(pages, vault.PeopleDir+"/"+handle+".md")
	}

	// Now that everybody has a page, the handles can be links — which is what
	// makes a person's backlinks their work.
	for _, e := range entries {
		if e.Task == nil || strings.TrimSpace(e.Task.Assignee) == "" {
			continue
		}
		if task.IsLink(e.Task.RawAssignee()) {
			continue
		}
		if !dry {
			e.Task.SetAssignee(e.Task.Assignee)
			if err := writeTask(root, e); err != nil {
				return pages, touched, err
			}
		}
		touched = append(touched, e.Key)
	}
	return pages, touched, nil
}

// writeTask puts a task back where it came from.
func writeTask(root string, e vault.Entry) error {
	body, err := e.Task.Bytes()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, filepath.FromSlash(e.Path)), body, 0o644)
}
