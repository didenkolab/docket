// Package cli dispatches docket subcommands.
package cli

import (
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
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
  init        Scaffold a new vault
  new         Create a task with a valid key
  project     List the projects a vault holds, or add one
  check       Validate a vault against the specification
  workspace   Assemble several project repositories into one Obsidian vault
  graph       What shape the vault's links are in
  people      Who the work is on, and who has no page yet
  app         Install a pack of vocabulary and files
  report      What the board cannot say by looking at today
  serve       A board and an API over a vault
  import      Bring in an existing Jira and Confluence instance
  mcp         Serve the vault to an agent over MCP
  version     Print the version
  help        Print this help

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
	case "new":
		return runNew(args[1:], stdout, stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "graph":
		return runGraph(args[1:], stdout, stderr)
	case "people":
		return runPeople(args[1:], stdout, stderr)
	case "app":
		return runApp(args[1:], stdout, stderr)
	case "report":
		return runReport(args[1:], stdout, stderr)
	case "project":
		return runProject(args[1:], stdout, stderr)
	case "workspace":
		return runWorkspace(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "import":
		return runImport(args[1:], stdout, stderr)
	case "mcp":
		return runMCP(args[1:], stdout, stderr)
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

// permute moves positional arguments behind flag arguments.
//
// Go's flag package stops parsing at the first non-flag argument, so
// `docket new "Title" --type bug` would quietly treat --type and bug as two more
// positionals and reject the command. That is how everyone writes it, so the
// arguments are reordered rather than the users.
func permute(flags *flag.FlagSet, args []string) []string {
	var flagArgs, positional []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			positional = append(positional, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if strings.Contains(name, "=") {
			continue
		}
		// A non-boolean flag takes the next argument as its value.
		if !isBoolFlag(flags, name) && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}

	return append(flagArgs, positional...)
}

func isBoolFlag(flags *flag.FlagSet, name string) bool {
	f := flags.Lookup(name)
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
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
	author := flags.String("author", "", `who to attribute the first commit to, as "Name <email>"`)
	template := flags.String("template", "",
		"the repository to scaffold from — any git remote, including a path on disk\n"+
			"    \t(default "+vault.DefaultTemplate+")")
	noCommit := flags.Bool("no-commit", false,
		"scaffold the files and leave them unstaged, rather than committing them")

	if err := flags.Parse(permute(flags, args)); err != nil {
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

	written, err := vault.Init(dir, vault.Options{Key: *key, Name: *name, Template: *template})
	if err != nil {
		fmt.Fprintf(stderr, "docket init: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "Created a vault for %s in %s — %d files.\n", *key, dir, len(written))

	// The scaffold is a commit, because the history is the record.
	//
	// A vault whose first state is uncommitted has a beginning nobody can read:
	// `git log` on a task starts at whatever commit somebody happened to make
	// first, and the AGENTS.md an agent is supposed to read is not in the
	// repository until then. A new project's first commit says what it is.
	if !*noCommit {
		switch committed, err := commitScaffold(dir, *key, *author); {
		case err != nil:
			fmt.Fprintf(stderr, "\ndocket init: the files are written but not committed: %v\n", err)
		case committed:
			fmt.Fprint(stdout, "Committed as the repository's first commit.\n")
		}
	}
	fmt.Fprint(stdout, "\nNext:\n")
	if dir != "." {
		fmt.Fprintf(stdout, "  cd %s\n", dir)
	}
	for _, step := range [][2]string{
		{`docket new "Its title"`, "the first task, as " + *key + "/" + *key + "-1 Its title.md"},
		{"docket serve", "a board in a browser"},
		{"open " + dir + " in Obsidian", "the same files, as a board and a wiki"},
	} {
		fmt.Fprintf(stdout, "  %-26s %s\n", step[0], step[1])
	}
	fmt.Fprint(stdout, "\nRead AGENTS.md before letting an agent loose in it.\n")
	return exitOK
}

// commitScaffold makes the vault's first commit, and reports whether it did.
//
// It does nothing rather than failing when there is no git repository or the
// repository already has commits: `docket init` into an existing repository is a
// normal thing to do, and taking over its history would be a surprise.
func commitScaffold(dir, key, author string) (bool, error) {
	repo, err := gitvcs.Open(dir)
	if err != nil {
		return false, nil // not a repository, which init does not require
	}
	// A repository with commits of its own has a history to respect.
	if past, err := repo.History(".", 1); err == nil && len(past) > 0 {
		return false, nil
	}

	who := gitvcs.Author{Name: "docket", Email: "docket@localhost"}
	if strings.TrimSpace(author) != "" {
		parsed, err := gitvcs.ParseAuthor(author)
		if err != nil {
			return false, err
		}
		who = parsed
	}

	message := key + ": a vault for tasks and pages, kept as files in git\n\n" +
		"Scaffolded by docket. AGENTS.md is how an agent works in here; docket.yaml is the\n" +
		"projects this vault holds and the vocabulary they share."
	if err := repo.Commit([]string{"."}, message, who); err != nil {
		return false, err
	}
	return true, nil
}
