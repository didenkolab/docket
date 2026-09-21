package cli

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/vault"
)

const graphUsage = `docket graph — what shape the vault's links are in.

Usage:
  docket graph [flags] [directory]

Opening the graph in Obsidian tells you it is busy. It does not tell you whether
the busyness is structure or a hairball, and twice on this project the picture
looked the same before and after a change that was measurably wrong: navigation
pages that held eighteen per cent of every edge, and sprint pages that reached
twenty-eight. Both were found by counting.

So this counts. The four questions are the ones docs/design/How things connect.md
opens with — what moves with what, what is this part of, where does work pile
up, and what is nobody looking after.

--colours rewrites .obsidian/graph.json so the graph is drawn in this vault's own
words: its containers, its statuses, its sprints. Your zoom and which panels you
had folded are left as you set them. Flags:
`

// thresholds worth a sentence rather than a number.
//
// Neither is a rule. A vault where nearly every note is in one cluster has no
// structure to read, and a note holding a fifth of every edge is doing what an
// index does — but both are judgements, and the command says what it sees and
// leaves the judgement where it belongs.
const (
	blobShare = 0.9  // one cluster holding this much of the vault
	hubShare  = 15.0 // per cent of every edge on one note
)

func runGraph(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("graph", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, graphUsage)
		flags.PrintDefaults()
	}
	colours := flags.Bool("colours", false,
		"write .obsidian/graph.json: colour by kind, and forces that let clusters sit apart")
	colors := flags.Bool("colors", false, "the same, spelled the other way")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	if flags.NArg() > 1 {
		fmt.Fprint(stderr, graphUsage)
		return exitUsage
	}

	dir := "."
	if flags.NArg() == 1 {
		dir = flags.Arg(0)
	}
	root, err := project.FindRoot(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket: %v\n", err)
		return exitError
	}
	c, err := project.Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket: %v\n", err)
		return exitError
	}

	if *colours || *colors {
		if err := vault.WriteGraph(root, c); err != nil {
			fmt.Fprintf(stderr, "docket: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "Wrote %s. Reopen the graph in Obsidian to see it.\n\n", vault.GraphFile)
	}

	shape, err := vault.Measure(root)
	if err != nil {
		fmt.Fprintf(stderr, "docket: %v\n", err)
		return exitError
	}
	describeShape(stdout, shape)
	return exitOK
}

// describeShape writes the shape in sentences, because a table of numbers is a
// thing somebody has to already know how to read.
func describeShape(w io.Writer, s vault.Shape) {
	if s.Notes == 0 {
		fmt.Fprintln(w, "No notes.")
		return
	}
	fmt.Fprintf(w, "%s, %s.\n\n", count(s.Notes, "note", "notes"), count(s.Edges, "link", "links"))

	if s.Edges == 0 {
		fmt.Fprintln(w, "Nothing links to anything, so there is no graph to read yet.")
		return
	}

	// Clusters.
	switch {
	case len(s.Clusters) == 0:
		fmt.Fprintln(w, "Every note stands alone.")
	case len(s.Clusters) == 1:
		fmt.Fprintf(w, "One cluster, %s of %d, around %s.\n",
			count(s.Clusters[0].Size, "note", "notes"), s.Notes, s.Clusters[0].Named)
	default:
		fmt.Fprintf(w, "%s. The largest holds %d of %d notes, around %s",
			count(len(s.Clusters), "1 cluster", "clusters"),
			s.Clusters[0].Size, s.Notes, s.Clusters[0].Named)
		if len(s.Clusters) > 1 {
			var rest []string
			for _, c := range s.Clusters[1:] {
				if len(rest) == 3 {
					rest = append(rest, "…")
					break
				}
				rest = append(rest, fmt.Sprintf("%d around %s", c.Size, c.Named))
			}
			fmt.Fprintf(w, "; then %s", strings.Join(rest, ", "))
		}
		fmt.Fprintln(w, ".")
	}
	if float64(s.Largest()) >= blobShare*float64(s.Notes) && s.Notes > 8 {
		fmt.Fprintf(w, "  Nearly everything is in one cluster, which is the same as no "+
			"structure: every note a few hops from every other.\n")
	}

	// Hubs.
	fmt.Fprintln(w, "\nMost connected:")
	for i, h := range s.Hubs {
		if i == 6 {
			break
		}
		fmt.Fprintf(w, "  %-44s %2d  %4.1f%% of every link\n", trim(h.Note, 44), h.Edges, h.Share)
	}
	if s.Concentration() >= hubShare {
		fmt.Fprintf(w, "  %s holds %.0f%% of every link on its own. A page that gathers work "+
			"has to give something back for it — see how-things-connect §5.\n",
			trim(s.Hubs[0].Note, 44), s.Concentration())
	}

	// Islands.
	if len(s.Islands) == 0 {
		fmt.Fprintln(w, "\nNothing is on its own.")
		return
	}
	fmt.Fprintf(w, "\n%s nothing links to, which is the question a list cannot answer:\n",
		count(len(s.Islands), "1 note", "notes"))
	for i, name := range s.Islands {
		if i == 8 {
			fmt.Fprintf(w, "  … and %d more\n", len(s.Islands)-8)
			break
		}
		fmt.Fprintf(w, "  %s\n", name)
	}
}

// count writes a number with the right word. `one` is the whole phrase, so a
// caller can pass "1 cluster" and not have the number printed twice.
func count(n int, one, many string) string {
	if n == 1 {
		if strings.ContainsAny(one, "0123456789") {
			return one
		}
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func trim(s string, to int) string {
	if len([]rune(s)) <= to {
		return s
	}
	return string([]rune(s)[:to-1]) + "…"
}
