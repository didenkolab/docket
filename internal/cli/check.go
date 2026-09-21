package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/didenkolab/docket/internal/check"
	"github.com/didenkolab/docket/internal/space"
)

const checkUsage = `docket check — validate a vault against the specification.

Usage:
  docket check [flags] [directory]

Reports every problem it finds, with a file and a line, and exits non-zero when
there is at least one — so it works as a pre-commit hook.

--fix settles the three findings that have a right answer: a file whose name no
longer matches its title, a relationship still written as a string rather than
as a link, and a generated board that has drifted from docket.yaml. Everything
else is left alone, because picking a side between two things a person meant
would be guessing. Flags:
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
		"rename files whose name drifted from their title, rewrite relationships still\n"+
			"    \twritten as strings, regenerate stale boards, then check again")

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
	sp, err := space.Open(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket check: %v\n", err)
		return exitError
	}

	if *fix {
		renamed, err := repairAcross(sp, stdout)
		if err != nil {
			fmt.Fprintf(stderr, "docket check: %v\n", err)
			return exitError
		}
		if renamed > 0 && !*quiet {
			fmt.Fprintln(stdout)
		}
	}

	findings, err := runAcross(sp)
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

// runAcross checks every repository in the space.
//
// Each is a project and is valid or not by itself, so each is checked on its
// own and its findings are reported under its own path. Links are the one thing
// that crosses: a link from one project to a note in another resolves when the
// workspace is opened in Obsidian, so every repository's names are known to
// every check. A repository taken away on its own would report those, which is
// true — the note really is not there any more.
func runAcross(sp *space.Space) ([]check.Finding, error) {
	vaults := sp.Vaults()

	known := map[string]bool{}
	if len(vaults) > 1 {
		for _, v := range vaults {
			names, err := check.Resolvable(v.Root)
			if err != nil {
				return nil, err
			}
			for name := range names {
				known[name] = true
			}
		}
	}

	var all []check.Finding
	for _, v := range vaults {
		findings, err := check.RunIn(v.Root, known)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", v.Prefix, err)
		}
		for _, f := range findings {
			f.Path = v.PathIn(f.Path)
			all = append(all, f)
		}
	}
	return all, nil
}

// repairAcross fixes what can be fixed, in every repository.
func repairAcross(sp *space.Space, stdout io.Writer) (int, error) {
	total := 0
	for _, v := range sp.Vaults() {
		n, err := repair(v.Root, stdout)
		if err != nil {
			return total, fmt.Errorf("%s: %w", v.Prefix, err)
		}
		total += n
	}
	return total, nil
}

// repair renames what can be renamed and says what it did. The renames are
// printed before they happen, because a file moving under an editor is
// something to be told about rather than to discover.
func repair(root string, stdout io.Writer) (int, error) {
	// Renaming first, then relinking. The order is the whole of DKT-61: a link
	// names a note, so the names have to be right before the links are written,
	// or every link to a retitled task is rewritten to the name it is about to
	// stop having. Relink re-reads the vault, so nothing is moved out from
	// under it — Apply has finished by then.
	renames, err := check.Renames(root)
	if err != nil {
		return 0, err
	}
	for _, r := range renames {
		fmt.Fprintf(stdout, "renamed %s\n     to %s\n", r.From, r.To)
	}
	repointed, err := check.Apply(root, renames)
	if err != nil {
		return 0, err
	}
	for _, path := range repointed {
		fmt.Fprintf(stdout, "linked  %s\n", path)
	}

	relinked, err := check.Relink(root)
	if err != nil {
		return 0, err
	}
	for _, path := range relinked {
		fmt.Fprintf(stdout, "linked  %s\n", path)
	}

	boards, err := check.Boards(root)
	if err != nil {
		return 0, err
	}
	for _, path := range boards {
		fmt.Fprintf(stdout, "wrote   %s\n", path)
	}

	// Counted as distinct paths: a file whose links a rename repointed can be
	// the same file relinking then rewrote, and saying "3 files changed" about
	// two files is the kind of small lie that makes people stop reading output.
	seen := map[string]bool{}
	for _, group := range [][]string{repointed, relinked, boards} {
		for _, path := range group {
			seen[path] = true
		}
	}
	touched := len(seen) + len(renames)
	if touched > 0 {
		fmt.Fprintf(stdout, "\n%s. Commit them together with whatever changed.\n",
			plural(touched, "file changed", "files changed"))
	}
	return touched, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
