package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/didenkolab/docket/internal/app"
	"github.com/didenkolab/docket/internal/project"
)

const appUsage = `docket app — vocabulary and files a vault takes on.

Usage:
  docket app add <url-or-path> [directory]
  docket app list [directory]

An app is a git repository holding docket-app.yaml and some files. Installing it
adds its types, fields and relations to docket.yaml and copies its templates,
views and documents in. Nothing is executed: what an installation did is a diff,
and it is reviewed and reverted like any other diff.

A conflict is refused rather than merged. A field the vault already has as
another kind, a relation with a different inverse, a file somebody has edited —
all of them are named, and nothing is written.

Files are not committed. Look at them, then commit them yourself.
`

func runApp(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, appUsage)
		return exitUsage
	}
	switch args[0] {
	case "add":
		return runAppAdd(args[1:], stdout, stderr)
	case "list":
		return runAppList(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, appUsage)
		return exitOK
	}
	fmt.Fprintf(stderr, "docket app: unknown subcommand %q\n\n", args[0])
	fmt.Fprint(stderr, appUsage)
	return exitUsage
}

func runAppAdd(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("app add", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dry := flags.Bool("dry-run", false, "say what it would change, and change nothing")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if flags.NArg() == 0 {
		fmt.Fprint(stderr, "docket app add: say what to install — a git URL or a path\n\n")
		fmt.Fprint(stderr, appUsage)
		return exitUsage
	}
	source := flags.Arg(0)

	start := "."
	if flags.NArg() > 1 {
		start = flags.Arg(1)
	}
	root, err := project.FindRoot(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket app add: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket app add: %v\n", err)
		return exitError
	}

	pack, cleanup, err := app.Fetch(source)
	if err != nil {
		fmt.Fprintf(stderr, "docket app add: %v\n", err)
		return exitError
	}
	defer cleanup()

	fmt.Fprintf(stdout, "%s", describe(pack))

	if conflicts := app.Check(root, c, pack); len(conflicts) > 0 {
		fmt.Fprintf(stderr, "\n%s cannot be installed as it is:\n", pack.Name)
		for _, conflict := range conflicts {
			fmt.Fprintf(stderr, "  %s\n", conflict.Error())
		}
		fmt.Fprint(stderr, "\nNothing was written.\n")
		return exitError
	}
	if *dry {
		fmt.Fprint(stdout, "\nNothing was written: --dry-run.\n")
		return exitOK
	}

	changed, err := app.Install(root, c, pack)
	if err != nil {
		fmt.Fprintf(stderr, "docket app add: %v\n", err)
		return exitError
	}
	if len(changed) == 0 {
		fmt.Fprintf(stdout, "\n%s was already installed, unchanged.\n", pack.Name)
		return exitOK
	}
	fmt.Fprint(stdout, "\n")
	for _, path := range changed {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintf(stdout, "\n%d files changed. Look at them, then commit them.\n", len(changed))
	return exitOK
}

// describe is what the app says it is, before anything is done with it.
func describe(p *app.Pack) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", p.Name)
	if p.Version != "" {
		fmt.Fprintf(&b, " %s", p.Version)
	}
	fmt.Fprintf(&b, " — %s\n", p.Description)

	if n := len(p.Vocabulary.Types); n > 0 {
		fmt.Fprintf(&b, "  types      %s\n", names(typeNames(p)))
	}
	if n := len(p.Vocabulary.Fields); n > 0 {
		fmt.Fprintf(&b, "  fields     %s\n", names(fieldNames(p)))
	}
	if n := len(p.Vocabulary.Relations); n > 0 {
		fmt.Fprintf(&b, "  relations  %s\n", names(relationPairs(p)))
	}
	if n := len(p.Surfaces.Pages) + len(p.Surfaces.Panels); n > 0 {
		var shown []string
		for _, page := range p.Surfaces.Pages {
			shown = append(shown, page.Called()+" (page)")
		}
		for _, panel := range p.Surfaces.Panels {
			shown = append(shown, panel.Called()+" (panel)")
		}
		fmt.Fprintf(&b, "  draws      %s\n", names(shown))
	}
	if n := len(p.Files); n > 0 {
		fmt.Fprintf(&b, "  files      %s\n", names(p.Files))
	}
	if p.BringsPrograms() {
		fmt.Fprint(&b, "\n  This app brings a program. Installing it writes the file; nothing\n"+
			"  runs it unless a server is started with --programs, which is a\n"+
			"  decision made on the machine that would run it. Read the program.\n")
	}
	return b.String()
}

func typeNames(p *app.Pack) []string {
	var out []string
	for _, t := range p.Vocabulary.Types {
		out = append(out, t.Name)
	}
	return out
}

func fieldNames(p *app.Pack) []string {
	var out []string
	for _, f := range p.Vocabulary.Fields {
		out = append(out, f.Name+" ("+f.Kind+")")
	}
	return out
}

func relationPairs(p *app.Pack) []string {
	var out []string
	for _, r := range p.Vocabulary.Relations {
		if r.Inverse != "" {
			out = append(out, r.Name+" / "+r.Inverse)
			continue
		}
		out = append(out, r.Name)
	}
	return out
}

func names(values []string) string { return strings.Join(values, ", ") }

func runAppList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("app list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "app list", stderr)
	if code != exitOK {
		return code
	}
	root, err := project.FindRoot(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket app list: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket app list: %v\n", err)
		return exitError
	}

	if len(c.Apps) == 0 {
		fmt.Fprint(stdout, "No apps installed.\n")
		return exitOK
	}
	for _, installed := range c.Apps {
		fmt.Fprintf(stdout, "%-16s %-10s %s\n", installed.Name, installed.Version, installed.Source)
	}
	return exitOK
}
