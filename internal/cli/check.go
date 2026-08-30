package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/vadymdidenkolab/docket/internal/check"
	"github.com/vadymdidenkolab/docket/internal/project"
)

const checkUsage = `docket check — validate a vault against the specification.

Usage:
  docket check [flags] [directory]

Reports every problem it finds, with a file and a line, and exits non-zero when
there is at least one — so it works as a pre-commit hook.

--fix renames files whose name no longer matches their title, which is the one
finding with a right answer: the frontmatter is what a task says about itself,
and the name is derived from it. Everything else is left alone, because picking
a side between two things a person meant would be guessing. Flags:
`

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, checkUsage)
		flags.PrintDefaults()
	}
	quiet := flags.Bool("quiet", false, "print findings only, without the summary")
	fix := flags.Bool("fix", false,
		"rename files whose name no longer matches their title, then check again")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprintf(stderr, "docket check: one directory at most, got %d\n\n", flags.NArg())
		flags.Usage()
		return exitUsage
	}

	start := "."
	if flags.NArg() == 1 {
		start = flags.Arg(0)
	}
	root, err := project.FindRoot(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket check: %v\n", err)
		return exitError
	}

	if *fix {
		renamed, err := repair(root, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "docket check: %v\n", err)
			return exitError
		}
		if renamed > 0 && !*quiet {
			fmt.Fprintln(stdout)
		}
	}

	findings, err := check.Run(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket check: %v\n", err)
		return exitError
	}

	for _, f := range findings {
		fmt.Fprintln(stdout, f)
	}
	if len(findings) == 0 {
		if !*quiet {
			fmt.Fprintln(stdout, "No findings.")
		}
		return exitOK
	}
	if !*quiet {
		fmt.Fprintf(stdout, "\n%s.\n", plural(len(findings), "finding", "findings"))
	}
	return exitError
}

// repair renames what can be renamed and says what it did. The renames are
// printed before they happen, because a file moving under an editor is
// something to be told about rather than to discover.
func repair(root string, stdout io.Writer) (int, error) {
	renames, err := check.Renames(root)
	if err != nil {
		return 0, err
	}
	for _, r := range renames {
		fmt.Fprintf(stdout, "renamed %s\n     to %s\n", r.From, r.To)
	}
	if err := check.Apply(root, renames); err != nil {
		return 0, err
	}
	if len(renames) > 0 {
		fmt.Fprintf(stdout, "\n%s. Commit them together with whatever changed the titles.\n",
			plural(len(renames), "file renamed", "files renamed"))
	}
	return len(renames), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
