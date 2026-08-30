package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"gopkg.in/yaml.v3"
)

// MapsFile is the single file a person edits between plan and apply.
const MapsFile = "maps.yaml"

// StatusMap is what a source status becomes.
type StatusMap struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
}

// FieldMap is what a source custom field becomes. A dropped field is written
// down rather than omitted, so the file lists everything the source had and
// nothing disappears by not being mentioned.
type FieldMap struct {
	Property string `yaml:"property,omitempty"`
	Drop     bool   `yaml:"drop,omitempty"`
	Name     string `yaml:"name,omitempty"` // the source's own name, for the reader
	Used     int    `yaml:"used,omitempty"`
}

// Maps is the whole translation from the source's vocabulary to the vault's.
type Maps struct {
	Statuses   map[string]StatusMap `yaml:"statuses"`
	Types      map[string]string    `yaml:"types"`
	Priorities map[string]string    `yaml:"priorities"`
	People     map[string]string    `yaml:"people"`
	Fields     map[string]FieldMap  `yaml:"fields"`
}

// Report is what plan found, for printing.
type Report struct {
	Issues       int
	Statuses     int
	Types        int
	Priorities   int
	People       int
	Fields       int
	UnusedFields int
}

// sourceIssue is the part of a Jira issue plan and apply care about. The rest
// stays in the snapshot untouched.
type sourceIssue struct {
	Key    string                     `json:"key"`
	Fields map[string]json.RawMessage `json:"fields"`
}

type namedValue struct {
	Name           string `json:"name"`
	StatusCategory struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type person struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// Plan reads a snapshot and proposes how to map it.
//
// Everything it produces is a proposal. Statuses keep their names because a
// team's own words for its process are worth more than a tidy set; categories
// come from the source's own three-way grouping, which is the part machines
// act on. People are the one section that always needs a human: the source has
// display names and account ids, and a vault wants handles.
func Plan(snap Reader) (*Maps, *Report, error) {
	maps := &Maps{
		Statuses:   map[string]StatusMap{},
		Types:      map[string]string{},
		Priorities: map[string]string{},
		People:     map[string]string{},
		Fields:     map[string]FieldMap{},
	}
	report := &Report{}

	catalogue, err := fieldCatalogue(snap)
	if err != nil {
		return nil, nil, err
	}

	projects, err := snap.Units("issues")
	if err != nil {
		return nil, nil, err
	}

	used := map[string]int{}
	for _, p := range projects {
		err := snap.Each("issues/"+p+".jsonl", func(raw json.RawMessage) error {
			var issue sourceIssue
			if err := json.Unmarshal(raw, &issue); err != nil {
				return err
			}
			report.Issues++

			if status := named(issue.Fields["status"]); status.Name != "" {
				maps.Statuses[status.Name] = StatusMap{
					Name:     status.Name,
					Category: categoryOf(status.StatusCategory.Key),
				}
			}
			if t := named(issue.Fields["issuetype"]); t.Name != "" {
				maps.Types[t.Name] = mapType(t.Name)
			}
			if p := named(issue.Fields["priority"]); p.Name != "" {
				maps.Priorities[p.Name] = slug(p.Name)
			}
			for _, field := range []string{"assignee", "reporter", "creator"} {
				if who := people(issue.Fields[field]); who.AccountID != "" {
					maps.People[who.AccountID] = slug(who.DisplayName)
				}
			}
			for id, value := range issue.Fields {
				if strings.HasPrefix(id, "customfield_") && !isEmpty(value) {
					used[id]++
				}
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}

	for id, name := range catalogue {
		count := used[id]
		entry := FieldMap{Name: name, Used: count}
		if count == 0 {
			entry.Drop = true
			report.UnusedFields++
		} else {
			entry.Property = "x_" + slug(name)
		}
		maps.Fields[id] = entry
	}
	// A field with values but no catalogue entry still has to go somewhere.
	for id, count := range used {
		if _, known := maps.Fields[id]; !known {
			maps.Fields[id] = FieldMap{Property: "x_" + slug(id), Used: count}
		}
	}

	report.Statuses = len(maps.Statuses)
	report.Types = len(maps.Types)
	report.Priorities = len(maps.Priorities)
	report.People = len(maps.People)
	report.Fields = len(maps.Fields)
	return maps, report, nil
}

// Reader is the part of a snapshot plan and apply need. An interface so both
// can be tested against a snapshot built in a temp directory.
type Reader interface {
	Each(rel string, fn func(json.RawMessage) error) error
	ReadJSON(rel string, into any) error
	Units(dir string) ([]string, error)
}

func fieldCatalogue(snap Reader) (map[string]string, error) {
	var fields []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Custom bool   `json:"custom"`
	}
	if err := snap.ReadJSON("meta/fields.json", &fields); err != nil {
		return map[string]string{}, nil // a snapshot without metadata still maps
	}

	catalogue := map[string]string{}
	for _, f := range fields {
		if strings.HasPrefix(f.ID, "customfield_") {
			catalogue[f.ID] = f.Name
		}
	}
	return catalogue, nil
}

// categoryOf translates the source's three-way grouping. This is the part that
// carries a cancelled-in-done workflow across intact: a status people read as
// "abandoned" is filed under done, and every machine question — is this in
// flight, is this closed — gets the right answer.
func categoryOf(key string) string {
	switch key {
	case "done":
		return project.CategoryDone
	case "indeterminate":
		return project.CategoryDoing
	default:
		return project.CategoryTodo
	}
}

// mapType lands the common source names on the vault's default vocabulary and
// slugs anything else, so an instance with its own types keeps them.
func mapType(name string) string {
	switch strings.ToLower(name) {
	case "task", "sub-task", "subtask", "technical task":
		return "task"
	case "bug", "defect", "incident":
		return "bug"
	case "story", "user story", "improvement", "new feature", "feature":
		return "story"
	case "epic", "initiative":
		return "epic"
	default:
		return slug(name)
	}
}

func named(raw json.RawMessage) namedValue {
	var v namedValue
	_ = json.Unmarshal(raw, &v)
	return v
}

func people(raw json.RawMessage) person {
	var v person
	_ = json.Unmarshal(raw, &v)
	return v
}

func isEmpty(raw json.RawMessage) bool {
	text := strings.TrimSpace(string(raw))
	return text == "" || text == "null" || text == "[]" || text == "{}" || text == `""`
}

var notWord = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = notWord.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

const mapsHeader = `# How this import translates the source's vocabulary into the vault's.
#
# Everything here is a proposal. Edit it, then run apply — apply never touches
# the network, so you can redo this as many times as it takes.
#
# statuses:   the source's own names are kept, because a team's words for its
#             process are worth more than a tidy set. The category is what
#             machines act on and must be todo, doing or done.
# types:      mapped onto the vault's vocabulary where the name is recognised,
#             slugged otherwise. Every value here ends up in project.yaml.
# priorities: likewise.
# people:     account ids on the left, handles on the right. This section is
#             personal data and always wants a human eye.
# fields:     custom fields. A field with no values anywhere is proposed for
#             dropping; the rest land on x_ prefixed properties, which is what
#             keeps a foreign schema from colliding with the core one.

`

// Save writes the maps for a person to edit.
func (m *Maps) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var body strings.Builder
	body.WriteString(mapsHeader)

	encoded, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	body.Write(encoded)

	return os.WriteFile(filepath.Join(dir, MapsFile), []byte(body.String()), 0o644)
}

// LoadMaps reads an edited maps file and checks it can be applied.
func LoadMaps(dir string) (*Maps, error) {
	raw, err := os.ReadFile(filepath.Join(dir, MapsFile))
	if err != nil {
		return nil, err
	}

	var m Maps
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", MapsFile, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", MapsFile, err)
	}
	return &m, nil
}

func (m *Maps) validate() error {
	if len(m.Statuses) == 0 {
		return fmt.Errorf("no statuses: nothing could be placed on a board")
	}

	for source, mapped := range m.Statuses {
		if mapped.Name == "" {
			return fmt.Errorf("status %q maps to no name", source)
		}
		switch mapped.Category {
		case project.CategoryTodo, project.CategoryDoing, project.CategoryDone:
		default:
			return fmt.Errorf("status %q maps to category %q, want todo, doing or done",
				source, mapped.Category)
		}
	}
	for source, mapped := range m.Types {
		if mapped == "" {
			return fmt.Errorf("type %q maps to nothing", source)
		}
	}
	for id, field := range m.Fields {
		if field.Drop {
			continue
		}
		if field.Property == "" {
			return fmt.Errorf("field %s is neither dropped nor mapped to a property", id)
		}
		if strings.ContainsAny(field.Property, " :\t") {
			return fmt.Errorf("field %s maps to %q, which is not a usable property name",
				id, field.Property)
		}
	}
	return nil
}

// StatusOrder lists the mapped statuses in board order: not started, then in
// flight, then finished. The source has no order worth keeping — a workflow
// graph is not a column layout — so this is the one place the import decides
// something the source did not say.
func (m *Maps) StatusOrder() []StatusMap {
	byCategory := map[string][]StatusMap{}
	seen := map[string]bool{}

	for _, mapped := range m.Statuses {
		if seen[mapped.Name] {
			continue
		}
		seen[mapped.Name] = true
		byCategory[mapped.Category] = append(byCategory[mapped.Category], mapped)
	}

	var out []StatusMap
	for _, category := range []string{project.CategoryTodo, project.CategoryDoing, project.CategoryDone} {
		group := byCategory[category]
		sort.Slice(group, func(i, j int) bool { return group[i].Name < group[j].Name })
		out = append(out, group...)
	}
	return out
}

// Values lists the distinct right-hand sides of a mapping, sorted.
func Values(m map[string]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range m {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
