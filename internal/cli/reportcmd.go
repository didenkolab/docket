package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/report"
	"github.com/vadymdidenkolab/docket/internal/space"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

const reportUsage = `docket report — what the board cannot say by looking at today.

Usage:
  docket report time-in-status [--json] [directory]

How long work sits in each column, and what has been waiting longest. Four of
the Jira marketplace's top hundred sell this one report, because Jira's
changelog is behind an API and aggregating it is somebody's product. Here every
move was a commit, so it is a read of git log and some arithmetic.

This is a command rather than a page on purpose: an app that wants to draw it
its own way runs this and formats the JSON, and the awkward part — following
renames through the history — stays in one place that is tested. Flags:
`

func runReport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, reportUsage)
		return exitUsage
	}
	switch args[0] {
	case "time-in-status":
		return runTimeInStatus(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, reportUsage)
		return exitOK
	}
	fmt.Fprintf(stderr, "docket report: unknown report %q\n\n", args[0])
	fmt.Fprint(stderr, reportUsage)
	return exitUsage
}

// columnJSON and rowJSON are the shapes an app reads. Durations in seconds,
// because a program formats them and a person does not read this.
type columnJSON struct {
	Status     string `json:"status"`
	Entered    int    `json:"entered"`
	Open       int    `json:"open"`
	Median     int64  `json:"median_seconds"`
	Longest    int64  `json:"longest_seconds"`
	LongestIn  string `json:"longest_in,omitempty"`
	Waiting    int64  `json:"waiting_seconds"`
	WaitingFor string `json:"waiting_for,omitempty"`
}

type rowJSON struct {
	Key     string `json:"key"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Waiting int64  `json:"waiting_seconds"`
	Age     int64  `json:"age_seconds"`
}

type reportJSON struct {
	Tasks   int          `json:"tasks"`
	Moves   int          `json:"moves"`
	Columns []columnJSON `json:"columns"`
	Stuck   []rowJSON    `json:"stuck"`
}

func runTimeInStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("report time-in-status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, reportUsage)
		flags.PrintDefaults()
	}
	asJSON := flags.Bool("json", false, "print it as JSON, for a program to format")
	most := flags.Int("stuck", 20, "how many of the longest waits to list")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "report time-in-status", stderr)
	if code != exitOK {
		return code
	}

	sp, err := space.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket report: %v\n", err)
		return exitError
	}
	c, err := sp.Config()
	if err != nil {
		fmt.Fprintf(stderr, "docket report: %v\n", err)
		return exitError
	}
	entries, err := sp.Entries()
	if err != nil {
		fmt.Fprintf(stderr, "docket report: %v\n", err)
		return exitError
	}

	known := map[string]vault.Entry{}
	for _, e := range entries {
		known[e.Path] = e
	}

	now := time.Now().UTC()
	lives := map[string]report.Life{}
	moves := 0

	for _, v := range sp.Vaults() {
		if v.Repo == nil {
			continue
		}
		changes, err := v.Repo.StatusChanges(".")
		if err != nil {
			fmt.Fprintf(stderr, "docket report: %s: %v\n", v.Prefix, err)
			continue
		}
		for at, list := range changes {
			moves += len(list)
			path := v.PathIn(at)
			// Only tasks: every document carries a status too — a decision is
			// `accepted`, a specification `normative` — and counting those puts
			// "accepted" on a report about a board.
			if e, ok := known[path]; !ok || e.Task == nil {
				continue
			}
			for _, life := range report.Read(map[string][]gitvcs.StatusChange{path: list}, now) {
				lives[path] = life
			}
		}
	}

	out := reportJSON{Tasks: len(lives), Moves: moves}
	named := func(at string) string {
		if e, ok := known[at]; ok {
			return e.Key
		}
		return at
	}
	for _, column := range report.Columns(lives, c.StatusNames(), named) {
		out.Columns = append(out.Columns, columnJSON{
			Status: column.Status, Entered: column.Entered, Open: column.Open,
			Median: int64(column.Median.Seconds()), Longest: int64(column.Longest.Seconds()),
			LongestIn: column.LongestIn,
			Waiting:   int64(column.Waiting.Seconds()), WaitingFor: column.WaitingFor,
		})
	}
	for _, life := range report.Stuck(lives, *most) {
		e, ok := known[life.Path]
		if !ok || e.Task == nil {
			continue
		}
		out.Stuck = append(out.Stuck, rowJSON{
			Key: e.Key, Title: e.Task.Title, Status: life.Now,
			Waiting: int64(life.Waiting.Seconds()), Age: int64(life.Age.Seconds()),
		})
	}

	if *asJSON {
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "docket report: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "%s\n", body)
		return exitOK
	}

	fmt.Fprintf(stdout, "%d tasks, %d moves\n\n", out.Tasks, out.Moves)
	fmt.Fprintf(stdout, "%-22s %8s %6s %14s %14s\n", "STATUS", "ENTERED", "NOW", "MEDIAN", "LONGEST")
	for _, column := range out.Columns {
		fmt.Fprintf(stdout, "%-22s %8d %6d %14s %14s\n", column.Status, column.Entered, column.Open,
			report.Said(time.Duration(column.Median)*time.Second),
			report.Said(time.Duration(column.Longest)*time.Second))
	}
	if len(out.Stuck) > 0 {
		fmt.Fprint(stdout, "\nWaiting longest\n")
		for _, row := range out.Stuck {
			fmt.Fprintf(stdout, "  %-10s %-18s %s  %s\n", row.Key,
				truncate(row.Status, 18),
				report.Said(time.Duration(row.Waiting)*time.Second), truncate(row.Title, 60))
		}
	}
	return exitOK
}

func truncate(s string, at int) string {
	runes := []rune(s)
	if len(runes) <= at {
		return s
	}
	return strings.TrimSpace(string(runes[:at-1])) + "…"
}
