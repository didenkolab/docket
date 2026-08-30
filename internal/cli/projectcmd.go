package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

const projectUsage = `docket project — the projects a vault holds.

Usage:
  docket project list [directory]
  docket project add --key KEY [--name NAME] [directory]

A project is a folder at the vault root, and a key is PROJECT/NUMBER. Adding one
also regenerates the boards, because a board selects tasks by naming the project
folders — a project no board mentions is a project whose work is invisible.
`

func runProject(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, projectUsage)
		return exitUsage
	}

	switch args[0] {
	case "list":
		return runProjectList(args[1:], stdout, stderr)
	case "add":
		return runProjectAdd(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, projectUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "docket project: unknown subcommand %q\n\n", args[0])
		fmt.Fprint(stderr, projectUsage)
		return exitUsage
	}
}

func runProjectList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("project list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	start, code := oneDirectory(flags, "project list", stderr)
	if code != exitOK {
		return code
	}

	root, c, err := openVault(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket project list: %v\n", err)
		return exitError
	}

	entries, err := vault.List(root, c)
	if err != nil {
		fmt.Fprintf(stderr, "docket project list: %v\n", err)
		return exitError
	}
	counts := map[string]int{}
	open := map[string]int{}
	for _, e := range entries {
		counts[e.Project]++
		if e.Task != nil && e.Task.StatusCategory != project.CategoryDone {
			open[e.Project]++
		}
	}

	for _, p := range c.Projects {
		fmt.Fprintf(stdout, "%-10s %-28s %d task(s), %d open\n",
			p.Key, p.Name, counts[p.Key], open[p.Key])
	}
	return exitOK
}

func runProjectAdd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("project add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	key := flags.String("key", "", "the project key, which is also its folder")
	name := flags.String("name", "", "what people call it (defaults to the key)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *key == "" {
		fmt.Fprint(stderr, "docket project add: --key is required\n\n")
		fmt.Fprint(stderr, projectUsage)
		return exitUsage
	}
	start, code := oneDirectory(flags, "project add", stderr)
	if code != exitOK {
		return code
	}

	root, c, err := openVault(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}
	if err := c.AddProject(*key, *name); err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}
	if err := c.Save(root); err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}
	if err := os.MkdirAll(filepath.Join(root, *key), 0o755); err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}
	if err := os.WriteFile(filepath.Join(root, *key, ".gitkeep"), nil, 0o644); err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}
	boards, err := vault.WriteBoards(root, c)
	if err != nil {
		fmt.Fprintf(stderr, "docket project add: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "%s added. %d board(s) regenerated so its tasks show up.\n", *key, len(boards))
	fmt.Fprintf(stdout, "First task: docket new --project %s \"Something to do\"\n", *key)
	return exitOK
}

func openVault(start string) (string, *project.Config, error) {
	root, err := project.FindRoot(start)
	if err != nil {
		return "", nil, err
	}
	c, err := project.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, c, nil
}
