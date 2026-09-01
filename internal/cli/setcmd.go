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

	"github.com/vadymdidenkolab/docket/internal/check"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/task"
)

const setUsage = `docket set — write properties on a task, from a script.

Usage:
  docket set KEY name=value [name=value ...] [directory]

The way an importer or a reaction writes to the vault. Everything it sets is
checked against what the vault declared: a choice must be on the list, a number
must parse, a relation must be a key that exists. A script reaching for sed on
frontmatter gets none of that and breaks the file on the first title with a
colon in it.

  docket set TEST-5 result=failed ran_at=2026-09-01T14:20:00Z
  docket set TEST-5 runs=TEST-3 found=ACME-88      # relations, by key
  docket set ACME-12 status="In review"            # status moves it, and checks the workflow

Flags:
`

func runSet(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("set", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, setUsage)
		flags.PrintDefaults()
	}
	quiet := flags.Bool("quiet", false, "say nothing when it worked")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if flags.NArg() < 2 {
		fmt.Fprint(stderr, "docket set: a key and at least one name=value\n\n")
		fmt.Fprint(stderr, setUsage)
		return exitUsage
	}

	key := flags.Arg(0)
	var pairs [][2]string
	dir := "."
	for _, arg := range flags.Args()[1:] {
		name, value, ok := strings.Cut(arg, "=")
		if !ok {
			// Not a pair: the last argument may be the directory.
			dir = arg
			continue
		}
		pairs = append(pairs, [2]string{strings.TrimSpace(name), value})
	}
	if len(pairs) == 0 {
		fmt.Fprint(stderr, "docket set: nothing to set\n")
		return exitUsage
	}

	sp, err := space.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}
	owner, rel, _, err := sp.Locate(key)
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}
	c, err := project.Load(owner.Root)
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}

	at := filepath.Join(owner.Root, filepath.FromSlash(rel))
	raw, err := os.ReadFile(at)
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}
	t, err := task.Parse(raw)
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}

	var said []string
	for _, pair := range pairs {
		how, err := setOne(sp, c, t, pair[0], pair[1])
		if err != nil {
			fmt.Fprintf(stderr, "docket set: %v\n", err)
			return exitError
		}
		said = append(said, how)
	}

	body, err := t.Bytes()
	if err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}
	if err := os.WriteFile(at, body, 0o644); err != nil {
		fmt.Fprintf(stderr, "docket set: %v\n", err)
		return exitError
	}
	if !*quiet {
		fmt.Fprintf(stdout, "%s: %s\n", key, strings.Join(said, ", "))
	}
	return exitOK
}

// setOne writes one property, checked against what the vault declared.
func setOne(sp *space.Space, c *project.Config, t *task.Task, name, value string) (string, error) {
	value = strings.TrimSpace(value)

	switch name {
	case "status":
		category, known := c.CategoryOf(value)
		if !known {
			return "", fmt.Errorf("%q is not a status: it is one of %s",
				value, strings.Join(c.StatusNames(), ", "))
		}
		if !c.CanMove(t.Status, value) {
			return "", fmt.Errorf("the workflow does not allow %s → %s", t.Status, value)
		}
		was := t.Status
		t.SetStatus(value, category)
		return was + " → " + value, nil

	case "assignee":
		t.SetAssignee(value)
		return "assignee " + orNobody(value), nil

	case "estimate":
		if value == "" {
			t.ClearEstimate()
			return "estimate cleared", nil
		}
		size, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return "", fmt.Errorf("estimate %q is not a number", value)
		}
		t.SetEstimate(size)
		return "estimate " + value, nil

	case "tags":
		// Obsidian's own tags, comma or space separated. A property of the
		// format rather than a declared field, and it was missing here — which
		// an importer found the first time it tried to carry a scenario's tags
		// across.
		t.SetTags(listOf(value))
		return "tags " + orNobody(value), nil

	case "labels":
		t.SetLabels(listOf(value))
		return "labels " + orNobody(value), nil

	case "sprint":
		t.SetSprint(value)
		return "sprint " + orNobody(value), nil

	case "parent":
		if value != "" {
			if _, _, _, err := sp.Locate(value); err != nil {
				return "", fmt.Errorf("parent %s: %w", value, err)
			}
		}
		t.SetParent(noteFor(sp, value))
		return "parent " + orNobody(value), nil
	}

	// A relation: the value is one or more keys, and each has to exist.
	if c.IsRelation(name) {
		var notes []string
		for _, key := range strings.Fields(strings.ReplaceAll(value, ",", " ")) {
			if _, _, _, err := sp.Locate(key); err != nil {
				return "", fmt.Errorf("%s %s: %w", name, key, err)
			}
			notes = append(notes, noteFor(sp, key))
		}
		t.SetRelated(name, notes)
		return name + " " + value, nil
	}

	// A field the vault declared: checked the way `docket check` checks it, so
	// that a script cannot write what a person would be refused.
	for _, f := range c.Fields {
		if f.Name != name {
			continue
		}
		if wrong := check.WrongFor(f, value); wrong != "" {
			return "", fmt.Errorf("%s", wrong)
		}
		t.SetProperty(f.Name, value, f.Kind != project.FieldText)
		return name + " " + value, nil
	}

	var known []string
	for _, f := range c.Fields {
		known = append(known, f.Name)
	}
	sort.Strings(known)
	return "", fmt.Errorf("%q is not a property this vault declared: it has %s, "+
		"plus status, assignee, estimate, sprint, parent and its relations",
		name, strings.Join(known, ", "))
}

func orNobody(value string) string {
	if strings.TrimSpace(value) == "" {
		return "cleared"
	}
	return value
}

// noteFor is what a wikilink to that key should say.
func noteFor(sp *space.Space, key string) string {
	if strings.TrimSpace(key) == "" {
		return ""
	}
	_, rel, _, err := sp.Locate(key)
	if err != nil {
		return key
	}
	return strings.TrimSuffix(filepath.Base(rel), ".md")
}

// listOf reads a comma or space separated list.
func listOf(value string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
