package cli

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/space"
)

const exportUsage = `docket export — the tasks, as data.

Usage:
  docket export [--format json|csv] [--open] [directory]

Four of the marketplace's top hundred sell exporting issues to a spreadsheet.
This is that, and it is also the interface an app computes from: a program that
wants to draw something asks for JSON rather than parsing the vault again, so
the awkward reading stays in one place that is tested. Flags:
`

type exported struct {
	Key      string   `json:"key"`
	Project  string   `json:"project"`
	Title    string   `json:"title"`
	Type     string   `json:"type"`
	Status   string   `json:"status"`
	Category string   `json:"category"`
	Priority string   `json:"priority"`
	Assignee string   `json:"assignee,omitempty"`
	Parent   string   `json:"parent,omitempty"`
	Sprint   string   `json:"sprint,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Estimate *float64 `json:"estimate,omitempty"`
	Created  string   `json:"created,omitempty"`
	Updated  string   `json:"updated,omitempty"`
	Path     string   `json:"path"`
	// Checked and Boxes are the acceptance list: how many of its boxes are
	// ticked. Counted here because every app that wants progress on a card
	// would otherwise count them again.
	Checked int `json:"checked"`
	Boxes   int `json:"boxes"`
}

func runExport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, exportUsage)
		flags.PrintDefaults()
	}
	format := flags.String("format", "json", "json or csv")
	onlyOpen := flags.Bool("open", false, "leave out what is finished")
	fields := flags.String("fields", "",
		"csv only: the columns to print, in order, comma separated\n"+
			"    \t(a program reading this with awk should name them: a title may hold\n"+
			"    \ta comma, and counting columns from the left is how an app comes to\n"+
			"    \tread the wrong one)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	dir, code := oneDirectory(flags, "export", stderr)
	if code != exitOK {
		return code
	}

	sp, err := space.Open(dir)
	if err != nil {
		fmt.Fprintf(stderr, "docket export: %v\n", err)
		return exitError
	}
	entries, err := sp.Entries()
	if err != nil {
		fmt.Fprintf(stderr, "docket export: %v\n", err)
		return exitError
	}

	out := []exported{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if *onlyOpen && e.Task.StatusCategory == "done" {
			continue
		}
		row := exported{
			Key: e.Key, Project: e.Project, Title: e.Task.Title, Type: e.Task.Type,
			Status: e.Task.Status, Category: e.Task.StatusCategory,
			Priority: e.Task.Priority, Assignee: e.Task.Assignee, Parent: e.Task.Parent,
			Sprint: e.Task.Sprint, Labels: e.Task.Labels, Tags: e.Task.Tags,
			Created: e.Task.Created, Updated: e.Task.Updated, Path: e.Path,
			Estimate: e.Task.Estimate,
		}
		row.Checked, row.Boxes = boxes(e.Task.Body())
		out = append(out, row)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Key < out[b].Key })

	switch *format {
	case "json":
		body, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "docket export: %v\n", err)
			return exitError
		}
		fmt.Fprintf(stdout, "%s\n", body)
	case "csv":
		wanted := everyColumn
		if strings.TrimSpace(*fields) != "" {
			wanted = nil
			for _, name := range strings.Split(*fields, ",") {
				name = strings.TrimSpace(name)
				if _, ok := column[name]; !ok {
					fmt.Fprintf(stderr, "docket export: %q is not a column: %s\n",
						name, strings.Join(everyColumn, ", "))
					return exitUsage
				}
				wanted = append(wanted, name)
			}
		}

		w := csv.NewWriter(stdout)
		_ = w.Write(wanted)
		for _, row := range out {
			line := make([]string, 0, len(wanted))
			for _, name := range wanted {
				line = append(line, column[name](row))
			}
			_ = w.Write(line)
		}
		w.Flush()
		if err := w.Error(); err != nil {
			fmt.Fprintf(stderr, "docket export: %v\n", err)
			return exitError
		}
	default:
		fmt.Fprintf(stderr, "docket export: %q is not a format: json or csv\n", *format)
		return exitUsage
	}
	return exitOK
}

// everyColumn is what a full export prints, in order.
var everyColumn = []string{"key", "project", "title", "type", "status", "category",
	"priority", "assignee", "parent", "sprint", "labels", "estimate",
	"checked", "boxes", "created", "updated"}

// column reads one column out of a row.
//
// Named rather than positional so that an app can ask for the two it wants and
// read them with awk without counting: a title may hold a comma, and counting
// columns from the left is exactly how an app comes to read the wrong one.
var column = map[string]func(exported) string{
	"key":      func(r exported) string { return r.Key },
	"project":  func(r exported) string { return r.Project },
	"title":    func(r exported) string { return r.Title },
	"type":     func(r exported) string { return r.Type },
	"status":   func(r exported) string { return r.Status },
	"category": func(r exported) string { return r.Category },
	"priority": func(r exported) string { return r.Priority },
	"assignee": func(r exported) string { return r.Assignee },
	"parent":   func(r exported) string { return r.Parent },
	"sprint":   func(r exported) string { return r.Sprint },
	"labels":   func(r exported) string { return strings.Join(r.Labels, " ") },
	"estimate": func(r exported) string {
		if r.Estimate == nil {
			return ""
		}
		return fmt.Sprintf("%g", *r.Estimate)
	},
	"checked": func(r exported) string { return fmt.Sprint(r.Checked) },
	"boxes":   func(r exported) string { return fmt.Sprint(r.Boxes) },
	"created": func(r exported) string { return r.Created },
	"updated": func(r exported) string { return r.Updated },
	"path":    func(r exported) string { return r.Path },
}

// boxes counts an acceptance list: how many boxes it has and how many are
// ticked.
//
// Three of the marketplace's top hundred are checklists. The list is already
// Markdown in the body and Obsidian already draws it; what nobody had was the
// count, and every app that wanted progress on a card would otherwise write
// this loop again.
func boxes(body string) (checked, total int) {
	fenced := false
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if !strings.HasPrefix(trimmed, "- [") || len(trimmed) < 5 || trimmed[4] != ']' {
			continue
		}
		total++
		if trimmed[3] != ' ' {
			checked++
		}
	}
	return checked, total
}
