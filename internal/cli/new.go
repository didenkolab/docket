package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
)

const newUsage = `docket new — create a task with a valid key.

Usage:
  docket new [flags] "Title of the task"

Run from anywhere inside a vault; the vault root is found the way git finds it.
Anything not given falls back to the project's own defaults. Flags:
`

func runNew(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, newUsage)
		flags.PrintDefaults()
	}

	var (
		dir        = flags.String("C", ".", "run as if started in this directory")
		projectKey = flags.String("project", "", "which project (default: the vault's first)")
		taskType   = flags.String("type", "", "task type (default: the vault's first)")
		status     = flags.String("status", "", "status (default: the vault's first)")
		priority   = flags.String("priority", "", "priority (default: normal)")
		assignee   = flags.String("assignee", "", "who it is on, such as agent/claude")
		parent     = flags.String("parent", "", "key of the parent task")
		labels     = flags.String("labels", "", "comma-separated labels")
	)

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		fmt.Fprint(stderr, "docket new: one title, quoted\n\n")
		flags.Usage()
		return exitUsage
	}

	root, err := project.FindRoot(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket new: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket new: %v\n", err)
		return exitError
	}

	path, t, err := vault.Create(root, c, vault.NewOptions{
		Project:  *projectKey,
		Title:    flags.Arg(0),
		Type:     *taskType,
		Status:   *status,
		Priority: *priority,
		Assignee: *assignee,
		Parent:   *parent,
		Labels:   splitLabels(*labels),
		Now:      time.Now(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "docket new: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "%s  %s\n", t.Key, path)
	return exitOK
}

func splitLabels(raw string) []string {
	var labels []string
	for _, l := range strings.Split(raw, ",") {
		if l = strings.TrimSpace(l); l != "" {
			labels = append(labels, l)
		}
	}
	return labels
}
