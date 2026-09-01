package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/vadymdidenkolab/docket/internal/anomaly"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

const anomalyUsage = `docket anomalies — what is odd about how the work is connected.

Usage:
  docket anomalies [--json] [--kind KIND] [directory]

Nothing in the Atlassian marketplace does this, because in Jira the links
between issues are rows in a table and nobody looks at their shape. Here the
vault is a graph, so questions become answerable that a board cannot ask: what
is adrift, what only one person can touch, which label has stopped meaning
anything, and where two tasks disagree about their own relationship.

Every finding is a question rather than a verdict. A task nobody links to may be
the most important thing in the project. What this can say is "unusual, and here
is why it noticed". Flags:
`

func runAnomalies(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("anomalies", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, anomalyUsage)
		flags.PrintDefaults()
	}
	asJSON := flags.Bool("json", false, "print them as JSON, for a program to format")
	kind := flags.String("kind", "", "only this kind: adrift, only-owner, overgrown, "+
		"one-sided, contained, unattended, concentrated")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "anomalies", stderr)
	if code != exitOK {
		return code
	}

	sp, err := space.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket anomalies: %v\n", err)
		return exitError
	}
	c, err := sp.Config()
	if err != nil {
		fmt.Fprintf(stderr, "docket anomalies: %v\n", err)
		return exitError
	}
	entries, err := sp.Entries()
	if err != nil {
		fmt.Fprintf(stderr, "docket anomalies: %v\n", err)
		return exitError
	}

	// The shape of the whole space: one vault or several, the links are the
	// links, and a hub in one repository is a hub.
	var shape vault.Shape
	for _, v := range sp.Vaults() {
		measured, err := vault.Measure(v.Root)
		if err != nil {
			continue
		}
		shape.Notes += measured.Notes
		shape.Edges += measured.Edges
		shape.Hubs = append(shape.Hubs, measured.Hubs...)
	}

	found := anomaly.Look(entries, shape, c)
	if *kind != "" {
		var kept []anomaly.Finding
		for _, f := range found {
			if f.Kind == *kind {
				kept = append(kept, f)
			}
		}
		found = kept
	}

	if *asJSON {
		body, err := json.MarshalIndent(found, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "docket anomalies: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "%s\n", body)
		return exitOK
	}

	if len(found) == 0 {
		fmt.Fprint(stdout, "Nothing unusual.\n")
		return exitOK
	}
	was := ""
	for _, f := range found {
		if f.Kind != was {
			fmt.Fprintf(stdout, "\n%s\n", f.Kind)
			was = f.Kind
		}
		fmt.Fprintf(stdout, "  %-24s %s\n", f.About, f.Says)
		fmt.Fprintf(stdout, "  %-24s   %s\n", "", f.Why)
	}
	fmt.Fprintf(stdout, "\n%d worth a look. None of them is a fault.\n", len(found))
	return exitOK
}
