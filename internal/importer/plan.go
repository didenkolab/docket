package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/didenkolab/docket/internal/project"
	"gopkg.in/yaml.v3"
)

// MapsFile is the single file a person edits between plan and apply.
const MapsFile = "maps.yaml"

// StatusMap is what a source status becomes.
type StatusMap struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
}

// TypeMap is what a source type becomes: its name in the vault, and the level
// that decides what may contain what.
//
// The level used to be left for a person to write, on the reasoning that
// guessing which of somebody's types is an epic is a guess an import should not
// make. That reasoning was right and the premise was wrong: Jira states the
// hierarchy. Every issue type carries a hierarchyLevel — 1 for an epic, 0 for
// ordinary work, -1 for a sub-task — and it is the same numbering docket uses.
// Reading it is not guessing.
//
// A source that says nothing leaves the level at nought, which is the honest
// answer: everything is ordinary work until somebody says otherwise.
type TypeMap struct {
	Name  string `yaml:"name"`
	Level int    `yaml:"level"`
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
	Types      map[string]TypeMap   `yaml:"types"`
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
	Name string `json:"name"`
	// HierarchyLevel is what Jira says about an issue type: 1 for an epic, 0 for
	// ordinary work, -1 for a sub-task. The same numbering docket uses, which is
	// why it can be read rather than guessed.
	HierarchyLevel int `json:"hierarchyLevel"`
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
		Types:      map[string]TypeMap{},
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
				maps.Types[t.Name] = TypeMap{Name: mapType(t.Name), Level: t.HierarchyLevel}
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

// declaredPriorities is the priorities the source lists, in its own order. An
// empty result is the ordinary case for a snapshot taken before this was
// captured, and the caller falls back to alphabetical.
func declaredPriorities(snap Reader) []string {
	var priorities []struct {
		Name string `json:"name"`
	}
	if err := snap.ReadJSON("meta/priorities.json", &priorities); err != nil {
		return nil
	}
	out := make([]string, 0, len(priorities))
	for _, p := range priorities {
		if p.Name != "" {
			out = append(out, p.Name)
		}
	}
	return out
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
// mapType is what a source type is called in the vault.
//
// An English name recognised as one of the four the template ships is mapped to
// it, so an ordinary Jira arrives speaking the vocabulary the boards already
// use. Anything else keeps its own name, exactly as a status does — a team's
// word for its work is worth more than a tidy set, and slugging it was how
// every Cyrillic type in a real project became the empty string.
func mapType(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "task", "sub-task", "subtask", "technical task":
		return "task"
	case "bug", "defect", "incident":
		return "bug"
	case "story", "user story", "improvement", "new feature", "feature":
		return "story"
	case "epic", "initiative":
		return "epic"
	default:
		return strings.TrimSpace(name)
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

// notWord is everything that is not a letter or a digit, in any script.
//
// It was `[^a-z0-9]+`, which is every character of a name written in anything
// but Latin — so slugging a Cyrillic type name replaced all of it and trimmed
// what was left, returning nothing at all. Found by importing a real project
// whose vocabulary is Russian.
var notWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

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
# types:      mapped onto the vault's vocabulary where the English name is
#             recognised, and kept as written otherwise — a team's word for its
#             work is worth more than a tidy set. The level comes from the
#             source, which states it: 1 contains 0, and 0 contains -1.
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
		if strings.TrimSpace(mapped.Name) == "" {
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

// PriorityOrder is the priorities in the order the source declares them, most
// important first.
//
// Alphabetical order is what Values gives, and for a priority it is nonsense:
// a real project came out as high, highest, low, lowest, medium. The vault then
// takes the middle of the list as its default, which made "low" the default,
// and the board's colouring means whatever position implies — so every one of
// them was wrong.
//
// Jira states the order and the extract keeps it. A priority the order does not
// mention follows the ones it does, alphabetically, so a value that appeared on
// an issue but not in the metadata is still offered rather than dropped.
func (m *Maps) PriorityOrder(declared []string) []string {
	rank := make(map[string]int, len(declared))
	for i, name := range declared {
		if mapped, known := m.Priorities[name]; known && mapped != "" {
			if _, seen := rank[mapped]; !seen {
				rank[mapped] = i
			}
		}
	}

	out := Values(m.Priorities)
	sort.SliceStable(out, func(i, j int) bool {
		a, knownA := rank[out[i]]
		b, knownB := rank[out[j]]
		switch {
		case knownA && knownB:
			return a < b
		case knownA:
			return true
		case knownB:
			return false
		}
		return out[i] < out[j]
	})
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
