package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/vadymdidenkolab/docket/internal/workspace"
)

const workspaceUsage = `docket workspace — assemble several project repositories into one Obsidian vault.

Usage:
  docket workspace init [directory]
  docket workspace add --key KEY --remote URL [--path PATH] [directory]
  docket workspace sync [directory]

Each project stays its own git repository. The workspace tracks only the
Obsidian config and the manifest, and ignores the project folders.
`

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, workspaceUsage)
		return exitUsage
	}

	switch args[0] {
	case "init":
		return runWorkspaceInit(args[1:], stdout, stderr)
	case "add":
		return runWorkspaceAdd(args[1:], stdout, stderr)
	case "sync":
		return runWorkspaceSync(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, workspaceUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "docket workspace: unknown subcommand %q\n\n", args[0])
		fmt.Fprint(stderr, workspaceUsage)
		return exitUsage
	}
}

func runWorkspaceInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("workspace init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "workspace init", stderr)
	if code != exitOK {
		return code
	}

	written, err := workspace.Init(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket workspace init: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "Created a workspace in %s — %d files.\n\n", dir, len(written))
	fmt.Fprint(stdout, "Next:\n")
	fmt.Fprint(stdout, "  docket workspace add --key ACME --remote git@github.com:example/acme.git\n")
	fmt.Fprint(stdout, "  docket workspace sync\n")
	return exitOK
}

func runWorkspaceAdd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("workspace add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	key := flags.String("key", "", "project key, such as ACME")
	remote := flags.String("remote", "", "git remote to clone the project from")
	path := flags.String("path", "", "folder inside the workspace (default: the key, lower-case)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *key == "" || *remote == "" {
		fmt.Fprint(stderr, "docket workspace add: --key and --remote are required\n\n")
		fmt.Fprint(stderr, workspaceUsage)
		return exitUsage
	}
	start, code := oneDirectory(flags, "workspace add", stderr)
	if code != exitOK {
		return code
	}

	root, err := workspace.FindRoot(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket workspace add: %v\n", err)
		return exitError
	}
	m, err := workspace.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket workspace add: %v\n", err)
		return exitError
	}
	if err := m.Add(workspace.Project{Key: *key, Path: *path, Remote: *remote}); err != nil {
		fmt.Fprintf(stderr, "docket workspace add: %v\n", err)
		return exitError
	}
	if err := m.Save(root); err != nil {
		fmt.Fprintf(stderr, "docket workspace add: %v\n", err)
		return exitError
	}

	added := m.Projects[len(m.Projects)-1]
	fmt.Fprintf(stdout, "%s  %s  %s\n", added.Key, added.Path, added.Remote)
	fmt.Fprint(stdout, "Run docket workspace sync to clone it.\n")
	return exitOK
}

func runWorkspaceSync(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("workspace sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	start, code := oneDirectory(flags, "workspace sync", stderr)
	if code != exitOK {
		return code
	}

	root, err := workspace.FindRoot(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket workspace sync: %v\n", err)
		return exitError
	}
	m, err := workspace.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket workspace sync: %v\n", err)
		return exitError
	}
	if len(m.Projects) == 0 {
		fmt.Fprint(stdout, "The workspace has no projects yet.\n")
		return exitOK
	}

	results := workspace.Sync(root, m, workspace.ExecGit{})
	for _, r := range results {
		fmt.Fprintln(stdout, r)
	}
	if workspace.Failed(results) {
		return exitError
	}
	return exitOK
}

// oneDirectory reads the optional trailing directory argument.
func oneDirectory(flags *flag.FlagSet, command string, stderr io.Writer) (string, int) {
	switch flags.NArg() {
	case 0:
		return ".", exitOK
	case 1:
		return flags.Arg(0), exitOK
	default:
		fmt.Fprintf(stderr, "docket %s: one directory at most, got %d\n", command, flags.NArg())
		return "", exitUsage
	}
}
