package cli

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
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
	Key      string `json:"key"`
	Project  string `json:"project"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Category string `json:"category"`
	Priority string `json:"priority"`
	Assignee string `json:"assignee,omitempty"`
	Parent   string `json:"parent,omitempty"`
	Sprint   string `json:"sprint,omitempty"`
	// Board is whether a task of this type belongs in a column, from the type's
	// own `board:` setting. Always written, never omitted: an app reading this
	// asks "is this somebody's work", and a missing key would answer "no" for
	// every ordinary task. A workload report that does not ask counts seven
	// hundred machine-written runs as somebody's backlog.
	Board    bool     `json:"board"`
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
	// Relations are what this task says about others, by verb. Present because
	// an app that draws coverage — which is what a test-management product
	// actually sells — needs the verb, and reading the files again to get it
	// would be a second implementation of the vault.
	Relations map[string][]string `json:"relations,omitempty"`
	// Body is the Markdown under the frontmatter, when asked for. Off by
	// default: a thousand tasks is a megabyte of prose, and most apps want the
	// properties.
	Body string `json:"body,omitempty"`
	// Fields are the properties this vault declared for itself, as written.
	// An app's own fields are the columns its own report is made of, and
	// leaving them out would send it back to reading the files.
	Fields map[string]string `json:"fields,omitempty"`
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
	withBody := flags.Bool("body", false, "json only: include each task's Markdown")
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
	c, err := sp.Config()
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
			Estimate: e.Task.Estimate, Board: c.OnBoard(e.Task.Type),
		}
		row.Checked, row.Boxes = boxes(e.Task.Body())
		row.Relations = e.Task.AllRelations(relationNames(c))
		for _, f := range c.Fields {
			if value := strings.TrimSpace(e.Task.Property(f.Name)); value != "" {
				if row.Fields == nil {
					row.Fields = map[string]string{}
				}
				row.Fields[f.Name] = value
			}
		}
		if *withBody {
			row.Body = e.Task.Body()
		}
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
				// A verb is a column too: `--fields key,tested_by` is how an app
				// asks what covers what.
				if _, ok := column[name]; !ok && c.IsRelation(name) {
					verb := name
					column[verb] = func(r exported) string {
						return strings.Join(r.Relations[verb], " ")
					}
				}
				// And a field the vault declared: an app reports on its own
				// properties, and it should not have to read the files for
				// them.
				if _, ok := column[name]; !ok && declaresField(c, name) {
					field := name
					column[field] = func(r exported) string { return r.Fields[field] }
				}
				if _, ok := column[name]; !ok {
					fmt.Fprintf(stderr, "docket export: %q is not a column: %s\n",
						name, strings.Join(append(append([]string{}, everyColumn...),
							alsoAColumn...), ", "))
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

// declaresField reports whether the vault declared a property by that name.
func declaresField(c *project.Config, name string) bool {
	for _, f := range c.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// relationNames is every verb the vault understands.
func relationNames(c *project.Config) []string {
	var names []string
	for _, r := range c.Relations() {
		names = append(names, r.Name)
	}
	return names
}

// everyColumn is what a full export prints, in order.
var everyColumn = []string{"key", "project", "title", "type", "status", "category",
	"priority", "assignee", "parent", "sprint", "labels", "estimate",
	"checked", "boxes", "created", "updated"}

// alsoAColumn is readable by name and left out of the full export, so that
// adding one does not move the columns under a program already reading it.
// They are named here so that a mistyped `--fields` still lists them.
var alsoAColumn = []string{"path", "board"}

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
	"board": func(r exported) string {
		if r.Board {
			return "true"
		}
		return "false"
	},
	"labels": func(r exported) string { return strings.Join(r.Labels, " ") },
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
