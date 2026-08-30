// Package cli dispatches docket subcommands.
package cli

import (
	"fmt"
	"io"
	"runtime/debug"
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
	exitUsage = 2
)

const usage = `docket — a task tracker and knowledge base kept as Markdown in git.

Usage:
  docket <command> [flags]

Commands:
  version     Print the version
  help        Print this help

Planned:
  init        Scaffold a new project vault
  new         Create a task with a valid key
  check       Validate a vault against the specification
  workspace   Assemble several project repositories into one Obsidian vault
  serve       Web UI and HTTP API over a repository
  import      Import from Jira and Confluence

The format and the roadmap live in
https://github.com/vadymdidenkolab/docket-board
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
