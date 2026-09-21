package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/importer"
	"github.com/vadymdidenkolab/docket/internal/snapshot"
)

const importUsage = `docket import — bring an existing Jira and Confluence instance into a vault.

Usage:
  docket import extract --site URL --email ADDR --token TOKEN --snapshot DIR [flags]
  docket import plan    --snapshot DIR [--maps DIR]
  docket import apply   --snapshot DIR --project KEY --vault DIR [flags]

Only extract touches the network. Everything after it runs against the snapshot,
so a mapping can be redone as many times as it takes without pulling the source
again. An interrupted extract resumes where it stopped.
`

func runImport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, importUsage)
		return exitUsage
	}

	switch args[0] {
	case "extract":
		return runExtract(args[1:], stdout, stderr)
	case "plan":
		return runPlan(args[1:], stdout, stderr)
	case "apply":
		return runApply(args[1:], stdout, stderr)
	case "help", "--help", "-h":
		fmt.Fprint(stdout, importUsage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "docket import: unknown subcommand %q\n\n", args[0])
		fmt.Fprint(stderr, importUsage)
		return exitUsage
	}
}

func runExtract(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import extract", flag.ContinueOnError)
	flags.SetOutput(stderr)

	site := flags.String("site", "", "https://example.atlassian.net")
	email := flags.String("email", "", "the account the API token belongs to")
	token := flags.String("token", "", "API token (or set DOCKET_TOKEN)")
	dir := flags.String("snapshot", "", "directory to write the snapshot into")
	projects := flags.String("projects", "", "comma-separated Jira project keys")
	spaces := flags.String("spaces", "", "comma-separated Confluence space keys")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *token == "" {
		*token = os.Getenv("DOCKET_TOKEN")
	}

	missing := []string{}
	for name, value := range map[string]string{
		"--site": *site, "--email": *email, "--token": *token, "--snapshot": *dir,
	} {
		if value == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "docket import extract: missing %s\n\n", strings.Join(missing, ", "))
		fmt.Fprint(stderr, importUsage)
		return exitUsage
	}
	if *projects == "" && *spaces == "" {
		fmt.Fprint(stderr, "docket import extract: nothing to extract — give --projects or --spaces\n")
		return exitUsage
	}

	snap, err := snapshot.Create(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket import extract: %v\n", err)
		return exitError
	}

	log := func(format string, args ...any) {
		fmt.Fprintf(stdout, format+"\n", args...)
	}
	err = importer.Extract(context.Background(), importer.NewClient(*site, *email, *token), snap,
		importer.ExtractOptions{
			Projects: splitList(*projects),
			Spaces:   splitList(*spaces),
			Now:      time.Now(),
		}, log)
	if err != nil {
		fmt.Fprintf(stderr, "docket import extract: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "\nSnapshot written to %s. Next: docket import plan --snapshot %s\n", *dir, *dir)
	return exitOK
}

func runPlan(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import plan", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dir := flags.String("snapshot", "", "the snapshot to read")
	maps := flags.String("maps", "", "where to write maps.yaml (default: the snapshot)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *dir == "" {
		fmt.Fprint(stderr, "docket import plan: --snapshot is required\n")
		return exitUsage
	}
	if *maps == "" {
		*maps = *dir
	}

	snap, err := snapshot.Open(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket import plan: %v\n", err)
		return exitError
	}

	proposal, report, err := importer.Plan(snap)
	if err != nil {
		fmt.Fprintf(stderr, "docket import plan: %v\n", err)
		return exitError
	}
	if err := proposal.Save(*maps); err != nil {
		fmt.Fprintf(stderr, "docket import plan: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "%d issues read.\n", report.Issues)
	fmt.Fprintf(stdout, "  %d statuses, %d types, %d priorities\n",
		report.Statuses, report.Types, report.Priorities)
	fmt.Fprintf(stdout, "  %d people — handles are a guess and want a human eye\n", report.People)
	fmt.Fprintf(stdout, "  %d custom fields, %d of them never used and proposed for dropping\n",
		report.Fields, report.UnusedFields)
	fmt.Fprintf(stdout, "\nWritten to %s/%s. Edit it, then run docket import apply.\n",
		*maps, importer.MapsFile)
	return exitOK
}

func runApply(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import apply", flag.ContinueOnError)
	flags.SetOutput(stderr)

	dir := flags.String("snapshot", "", "the snapshot to read")
	mapsDir := flags.String("maps", "", "where maps.yaml is (default: the snapshot)")
	projectKey := flags.String("project", "", "which project in the snapshot to write")
	spaces := flags.String("spaces", "",
		"comma-separated spaces to write as pages; every space in the snapshot by default")
	vaultDir := flags.String("vault", "", "the vault to create")
	author := flags.String("author", "", `who the import commit is by, as "Name <email>"`)
	template := flags.String("template", "", "the repository the new vault is scaffolded from\n"+
		"\t\t(default https://github.com/vadymdidenkolab/docket-template.git; a path works too,\n"+
		"\t\tand is how an import runs with no route to the internet)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if *dir == "" || *projectKey == "" || *vaultDir == "" {
		fmt.Fprint(stderr, "docket import apply: --snapshot, --project and --vault are required\n\n")
		fmt.Fprint(stderr, importUsage)
		return exitUsage
	}
	if *mapsDir == "" {
		*mapsDir = *dir
	}

	who := gitvcs.Author{Name: "docket import", Email: "import@docket"}
	if *author != "" {
		parsed, err := gitvcs.ParseAuthor(*author)
		if err != nil {
			fmt.Fprintf(stderr, "docket import apply: %v\n", err)
			return exitUsage
		}
		who = parsed
	}

	snap, err := snapshot.Open(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket import apply: %v\n", err)
		return exitError
	}
	loaded, err := importer.LoadMaps(*mapsDir)
	if err != nil {
		fmt.Fprintf(stderr, "docket import apply: %v\n", err)
		return exitError
	}

	// The snapshot knows which spaces it holds — it says so in its manifest —
	// so the flag narrows rather than supplies. Requiring it was a second place
	// to say a fact the snapshot already had, and the failure was silent: an
	// import ran, reported "0 pages", and looked like it had worked.
	wanted := splitList(*spaces)
	if len(wanted) == 0 {
		if m, err := snap.Manifest(); err == nil {
			wanted = m.Spaces
		}
	}

	log := func(format string, args ...any) { fmt.Fprintf(stdout, format+"\n", args...) }
	report, err := importer.Apply(snap, loaded, importer.ApplyOptions{
		Root:     *vaultDir,
		Project:  *projectKey,
		Spaces:   wanted,
		Now:      time.Now(),
		Template: *template,
	}, log)
	if err != nil {
		fmt.Fprintf(stderr, "docket import apply: %v\n", err)
		return exitError
	}

	// One commit for the whole import. It is the only way an import can be
	// reviewed as a unit and reverted as one if the mapping turns out wrong.
	message := fmt.Sprintf("Import %s: %d tasks, %d comments, %d pages",
		*projectKey, report.Tasks, report.Comments, report.Pages)
	if err := commitVault(*vaultDir, message, who); err != nil {
		fmt.Fprintf(stderr, "docket import apply: written, but not committed: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "\n%s\n", message)
	if report.Dropped > 0 {
		fmt.Fprintf(stdout, "%d custom field values were dropped as maps.yaml asked.\n", report.Dropped)
	}
	fmt.Fprintf(stdout, "Review it with git show, and docket check %s.\n", *vaultDir)
	return exitOK
}

// commitVault initialises the vault's repository when it has none, then commits
// everything the import wrote.
func commitVault(dir, message string, author gitvcs.Author) error {
	if _, err := os.Stat(dir + "/.git"); err != nil {
		init := exec.Command("git", "init", "-q", "-b", "main")
		init.Dir = dir
		if out, err := init.CombinedOutput(); err != nil {
			return fmt.Errorf("git init: %s", strings.TrimSpace(string(out)))
		}
	}

	repo, err := gitvcs.Open(dir)
	if err != nil {
		return err
	}
	return repo.Commit([]string{"."}, message, author)
}

func splitList(raw string) []string {
	var out []string
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
