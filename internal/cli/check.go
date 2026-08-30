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
there is at least one — so it works as a pre-commit hook. Flags:
`

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, checkUsage)
		flags.PrintDefaults()
	}
	quiet := flags.Bool("quiet", false, "print findings only, without the summary")

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

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
