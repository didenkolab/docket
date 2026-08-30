// Package cli dispatches docket subcommands.
package cli

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/vadymdidenkolab/docket/internal/vault"
)

// version is stamped at build time:
//
//	go build -ldflags "-X github.com/vadymdidenkolab/docket/internal/cli.version=v0.1.0"
//
// Left empty in ordinary builds, where Version falls back to Go's build info.
var version string

// Exit codes. Anything else a command returns is its own.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = `docket — a task tracker and knowledge base kept as Markdown in git.

Usage:
  docket <command> [flags]

Commands:
  init        Scaffold a new project vault
  version     Print the version
  help        Print this help

Planned:
  new         Create a task with a valid key
  check       Validate a vault against the specification
  workspace   Assemble several project repositories into one Obsidian vault
  serve       Web UI and HTTP API over a repository
  import      Import from Jira and Confluence

The format and the roadmap live in
https://github.com/vadymdidenkolab/docket-board
`

const initUsage = `docket init — scaffold a new project vault.

Usage:
  docket init --key KEY [--name NAME] [directory]

The directory defaults to the current one and must be empty, apart from a .git
directory. Flags:
`

// Version reports the build's version. A release build carries the tag stamped
// in via ldflags. A `go install` build has no such stamp, so the module version
// Go recorded is used instead. Only a build with neither reports "dev", which is
// then the truth rather than a placeholder.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// Run executes one invocation and returns the process exit code. It writes
// nothing to the real stdout or stderr, so tests can read what it produced.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, Version())
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "docket: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
}

func runInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, initUsage)
		flags.PrintDefaults()
	}

	key := flags.String("key", "", "project key: the prefix of every task, such as ACME")
	name := flags.String("name", "", "project name (defaults to the key)")

	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprintf(stderr, "docket init: one directory at most, got %d\n\n", flags.NArg())
		flags.Usage()
		return exitUsage
	}
	if *key == "" {
		fmt.Fprint(stderr, "docket init: --key is required\n\n")
		flags.Usage()
		return exitUsage
	}

	dir := "."
	if flags.NArg() == 1 {
		dir = flags.Arg(0)
	}

	written, err := vault.Init(dir, vault.Options{Key: *key, Name: *name})
	if err != nil {
		fmt.Fprintf(stderr, "docket init: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "Created a vault for %s in %s — %d files.\n\n", *key, dir, len(written))
	fmt.Fprint(stdout, "Next:\n")
	fmt.Fprintf(stdout, "  open %s in Obsidian, then open boards/board.base\n", dir)
	fmt.Fprintf(stdout, "  copy templates/task.md to tasks/%s-1.md to write the first task\n", *key)
	fmt.Fprint(stdout, "  read AGENTS.md before letting an agent loose in it\n")
	return exitOK
}
