package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
)

const peopleUsage = `docket people — who the work is on.

Usage:
  docket people [flags] [directory]

An assignee is a handle, and a handle should be somebody: a page in people/ that
the task links to. Then Obsidian answers "what is Marina on" with its own
backlinks pane, and a typo is a finding rather than a colleague.

With no flags this lists what the vault has and what it is missing. --write
writes a page for every handle the tasks name and nobody has written down;
--from-git does the same for everybody who has committed here, which is the list
a project already has and nobody had to type.

Nothing is invented: a page holds the handle, the name if one is known, and
nothing else. Flags:
`

func runPeople(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("people", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, peopleUsage)
		flags.PrintDefaults()
	}
	write := flags.Bool("write", false,
		"write a page for every handle the tasks name that has none")
	fromGit := flags.Bool("from-git", false,
		"also write a page for everybody who has committed to this repository")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "people", stderr)
	if code != exitOK {
		return code
	}

	root, err := project.FindRoot(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket people: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket people: %v\n", err)
		return exitError
	}
	entries, err := vault.List(root, c)
	if err != nil {
		fmt.Fprintf(stderr, "docket people: %v\n", err)
		return exitError
	}
	people, err := vault.People(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket people: %v\n", err)
		return exitError
	}

	carried := vault.HandlesIn(entries)

	if *write || *fromGit {
		written, err := writePeople(root, people, carried, *fromGit)
		if err != nil {
			fmt.Fprintf(stderr, "docket people: %v\n", err)
			return exitError
		}
		if len(written) == 0 {
			fmt.Fprint(stdout, "Everybody already has a page.\n")
		}
		for _, path := range written {
			fmt.Fprintf(stdout, "wrote %s\n", path)
		}
		if len(written) > 0 {
			fmt.Fprint(stdout, "\nThe tasks still name them as handles. "+
				"Write the link — assignee: \"[[handle]]\" — and Obsidian draws the edge.\n")
		}
		// Read again, so the listing below is of what is now there.
		if people, err = vault.People(root); err != nil {
			fmt.Fprintf(stderr, "docket people: %v\n", err)
			return exitError
		}
	}

	reportPeople(stdout, people, carried)
	return exitOK
}

// writePeople writes a page for everybody who has none.
func writePeople(root string, people []vault.Person, carried map[string]int, fromGit bool) ([]string, error) {
	wanted := map[string]string{} // handle -> name, when one is known
	for handle := range carried {
		wanted[handle] = ""
	}

	if fromGit {
		repo, err := gitvcs.Open(root)
		if err != nil {
			return nil, err
		}
		authors, err := repo.Contributors()
		if err != nil {
			return nil, err
		}
		for _, who := range authors {
			handle := vault.Handle(who.Name)
			if handle == "" {
				continue
			}
			// Somebody already writing tasks under a handle keeps it: the name
			// git records and the handle a board uses are different spellings
			// of the same person, and the board's is the one in the files.
			if _, ok := carried[handle]; !ok {
				wanted[handle] = who.Name
			}
		}
	}

	var handles []string
	for handle := range wanted {
		if _, known := vault.PersonOf(people, handle); !known {
			handles = append(handles, handle)
		}
	}
	sort.Strings(handles)

	dir := filepath.Join(root, vault.PeopleDir)
	if len(handles) > 0 {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	var written []string
	for _, handle := range handles {
		at := filepath.Join(dir, handle+".md")
		if _, err := os.Stat(at); err == nil {
			continue // a page that is not a person page: leave it alone
		}
		if err := os.WriteFile(at, []byte(vault.PersonPage(handle, wanted[handle])), 0o644); err != nil {
			return nil, err
		}
		written = append(written, filepath.ToSlash(filepath.Join(vault.PeopleDir, handle+".md")))
	}
	return written, nil
}

// report prints who is here and who is missing.
func reportPeople(stdout io.Writer, people []vault.Person, carried map[string]int) {
	if len(people) == 0 && len(carried) == 0 {
		fmt.Fprint(stdout, "Nothing is assigned to anybody, and nobody is written down.\n")
		return
	}

	fmt.Fprintf(stdout, "%-24s %6s  %s\n", "HANDLE", "TASKS", "PAGE")
	seen := map[string]bool{}

	var handles []string
	for handle := range carried {
		handles = append(handles, handle)
	}
	for _, p := range people {
		if _, ok := carried[p.Handle]; !ok {
			handles = append(handles, p.Handle)
		}
	}
	sort.Slice(handles, func(a, b int) bool {
		if carried[handles[a]] != carried[handles[b]] {
			return carried[handles[a]] > carried[handles[b]]
		}
		return handles[a] < handles[b]
	})

	missing := 0
	for _, handle := range handles {
		if seen[strings.ToLower(handle)] {
			continue
		}
		seen[strings.ToLower(handle)] = true

		page := "—"
		if p, ok := vault.PersonOf(people, handle); ok {
			page = p.Path
			if p.Name != "" && p.Name != handle {
				page += "  (" + p.Name + ")"
			}
		} else {
			missing++
		}
		fmt.Fprintf(stdout, "%-24s %6d  %s\n", handle, carried[handle], page)
	}

	if missing > 0 {
		fmt.Fprintf(stdout, "\n%d with no page. `docket people --write` writes them, "+
			"or fix the handle if it is a typo.\n", missing)
	}
}
