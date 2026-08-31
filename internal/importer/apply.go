package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/adf"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// ApplyOptions says what to write and where.
type ApplyOptions struct {
	Root    string // the vault to write into
	Project string // which project in the snapshot
	Spaces  []string
	Now     time.Time
}

// ApplyReport is what was written.
type ApplyReport struct {
	Tasks     int
	Comments  int
	Histories int
	Pages     int
	Dropped   int // custom field values dropped by the maps
}

// Apply writes a vault from a snapshot and a set of maps.
//
// Nothing here touches the network: everything comes from the snapshot, so a
// mapping can be redone as many times as it takes without going back to the
// source. The caller commits the result in one go, which is what makes an
// import reviewable and revertable as a unit.
func Apply(snap Reader, maps *Maps, opts ApplyOptions, log Logf) (*ApplyReport, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}
	if !project.KeyPattern.MatchString(opts.Project) {
		return nil, fmt.Errorf("project key %q cannot be used as a vault key: "+
			"2 to 10 upper-case letters or digits, starting with a letter", opts.Project)
	}

	report := &ApplyReport{}

	issues, err := readIssues(snap, opts.Project)
	if err != nil {
		return nil, err
	}
	if len(issues) == 0 {
		return nil, fmt.Errorf("the snapshot holds no issues for project %s", opts.Project)
	}

	if err := writeConfig(opts.Root, opts.Project, projectName(issues, opts.Project), maps); err != nil {
		return nil, err
	}

	comments, err := groupByKey(snap, "comments/"+opts.Project+".jsonl")
	if err != nil {
		return nil, err
	}
	histories, err := groupByKey(snap, "changelog/"+opts.Project+".jsonl")
	if err != nil {
		return nil, err
	}

	// Every task's note name, worked out before any of them is written: a
	// parent is a link, a link resolves by note name, and a task may name a
	// parent that has not been written yet.
	notes := noteNames(opts.Project, issues)

	for _, issue := range issues {
		written, err := writeTask(opts, maps, issue, comments[issue.Key], notes, report)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", issue.Key, err)
		}
		if written {
			report.Tasks++
		}
		if entries := histories[issue.Key]; len(entries) > 0 {
			if err := writeHistory(opts.Root, opts.Project, numberOf(issue.Key), entries); err != nil {
				return nil, fmt.Errorf("%s history: %w", issue.Key, err)
			}
			report.Histories++
		}
	}
	log("%d tasks, %d with imported history", report.Tasks, report.Histories)

	for _, space := range opts.Spaces {
		count, err := writePages(snap, opts.Root, space)
		if err != nil {
			return nil, fmt.Errorf("space %s: %w", space, err)
		}
		report.Pages += count
		log("%s: %d pages", space, count)
	}

	return report, nil
}

func readIssues(snap Reader, key string) ([]sourceIssue, error) {
	var issues []sourceIssue
	err := snap.Each("issues/"+key+".jsonl", func(raw json.RawMessage) error {
		var issue sourceIssue
		if err := json.Unmarshal(raw, &issue); err != nil {
			return err
		}
		issues = append(issues, issue)
		return nil
	})
	sort.Slice(issues, func(i, j int) bool {
		return numberOf(issues[i].Key) < numberOf(issues[j].Key)
	})
	return issues, err
}

func numberOf(key string) int {
	if dash := strings.LastIndex(key, "-"); dash >= 0 {
		n, _ := strconv.Atoi(key[dash+1:])
		return n
	}
	return 0
}

func projectName(issues []sourceIssue, fallback string) string {
	for _, issue := range issues {
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(issue.Fields["project"], &p); err == nil && p.Name != "" {
			return p.Name
		}
	}
	return fallback
}

// writeConfig builds docket.yaml from the maps, so the vault's vocabulary is
// exactly what the import decided rather than the scaffold's defaults.
func writeConfig(root, key, name string, maps *Maps) error {
	if _, err := vault.Init(root, vault.Options{Key: key, Name: name}); err != nil {
		return err
	}

	c := &project.Config{
		Name:       name,
		Projects:   []project.Project{{Key: key, Name: name}},
		Types:      typesOf(maps.Types),
		Priorities: Values(maps.Priorities),
	}
	for _, mapped := range maps.StatusOrder() {
		c.Statuses = append(c.Statuses, project.Status{Name: mapped.Name, Category: mapped.Category})
	}
	if len(c.Priorities) == 0 {
		c.Priorities = []string{"normal"}
	}

	if err := c.Save(root); err != nil {
		return err
	}
	// The boards name their projects, so they have to be rebuilt for this one.
	_, err := vault.WriteBoards(root, c)
	return err
}

func writeTask(opts ApplyOptions, maps *Maps, issue sourceIssue,
	comments []json.RawMessage, notes map[string]string, report *ApplyReport) (bool, error) {

	fields := issue.Fields

	status := named(fields["status"])
	mappedStatus, known := maps.Statuses[status.Name]
	if !known {
		return false, fmt.Errorf("status %q is not in maps.yaml", status.Name)
	}
	issueType := named(fields["issuetype"])
	mappedType, known := maps.Types[issueType.Name]
	if !known {
		return false, fmt.Errorf("type %q is not in maps.yaml", issueType.Name)
	}

	priority := "normal"
	if p := named(fields["priority"]); p.Name != "" {
		if mapped, ok := maps.Priorities[p.Name]; ok {
			priority = mapped
		} else {
			priority = slug(p.Name)
		}
	}

	body := &strings.Builder{}
	description, err := adf.ConvertJSON(fields["description"])
	if err != nil {
		return false, err
	}
	body.WriteString(strings.TrimRight(description, "\n"))
	body.WriteString("\n")

	if len(comments) > 0 {
		body.WriteString("\n" + task.CommentsHeading + "\n")
		for _, raw := range comments {
			text, author, when, err := renderComment(raw, maps)
			if err != nil {
				return false, err
			}
			fmt.Fprintf(body, "\n**%s · %s** — %s\n", author, when, text)
			report.Comments++
		}
	}

	t, err := task.Parse([]byte("---\nkey:\n---\n" + body.String()))
	if err != nil {
		return false, err
	}

	key := project.Key(opts.Project, numberOf(issue.Key))
	t.Set("key", key)
	t.Set("title", stringField(fields["summary"]))
	t.Set("type", mappedType.Name)
	t.SetStatus(mappedStatus.Name, mappedStatus.Category)
	t.Set("priority", priority)
	t.Set("assignee", handle(maps, fields["assignee"]))

	if parent := parentKey(fields["parent"]); parent != "" {
		t.SetParent(notes[project.Key(opts.Project, numberOf(parent))])
	}

	t.SetLabels(stringList(fields["labels"]))
	t.SetPlain("created", timestamp(fields["created"], opts.Now))
	t.SetPlain("updated", timestamp(fields["updated"], opts.Now))
	// The source key outlives its system: it sits in commit messages, branch
	// names and years of conversation. As an alias it keeps resolving.
	t.SetList("aliases", []string{issue.Key})

	for id, mapped := range maps.Fields {
		raw, present := fields[id]
		if !present || isEmpty(raw) {
			continue
		}
		if mapped.Drop || mapped.Property == "" {
			report.Dropped++
			continue
		}
		if values := scalarList(raw); len(values) > 1 {
			t.SetList(mapped.Property, values)
		} else if len(values) == 1 {
			t.Set(mapped.Property, values[0])
		}
	}

	content, err := t.Bytes()
	if err != nil {
		return false, err
	}
	path := filepath.Join(opts.Root, opts.Project, vault.FileName(key, stringField(fields["summary"])))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, content, 0o644)
}

// writeHistory keeps the change history the source had, because git never saw
// it. Files under _history are read-only after an import.
func writeHistory(root, projectKey string, number int, entries []json.RawMessage) error {
	dir := filepath.Join(root, projectKey, vault.HistoryDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var body strings.Builder
	for _, raw := range entries {
		body.Write(raw)
		body.WriteString("\n")
	}
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(number)+".jsonl"), []byte(body.String()), 0o644)
}

func groupByKey(snap Reader, rel string) (map[string][]json.RawMessage, error) {
	grouped := map[string][]json.RawMessage{}
	err := snap.Each(rel, func(raw json.RawMessage) error {
		var record changelogRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return err
		}
		grouped[record.Key] = append(grouped[record.Key], record.Entry)
		return nil
	})
	return grouped, err
}

func renderComment(raw json.RawMessage, maps *Maps) (text, author, when string, err error) {
	var comment struct {
		Author  person          `json:"author"`
		Body    json.RawMessage `json:"body"`
		Created string          `json:"created"`
	}
	if err := json.Unmarshal(raw, &comment); err != nil {
		return "", "", "", err
	}

	converted, err := adf.ConvertJSON(comment.Body)
	if err != nil {
		return "", "", "", err
	}

	author = maps.People[comment.Author.AccountID]
	if author == "" {
		author = comment.Author.DisplayName
	}
	if author == "" {
		author = "unknown"
	}

	when = comment.Created
	if parsed, err := parseJiraTime(comment.Created); err == nil {
		when = parsed.UTC().Format("2006-01-02 15:04")
	}

	// A comment lives on one logical line in the task body; its own paragraphs
	// are kept with hard breaks so nothing is lost.
	text = strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(converted), "\n\n", "\n"))
	return text, author, when, nil
}

func handle(maps *Maps, raw json.RawMessage) string {
	who := people(raw)
	if who.AccountID == "" {
		return ""
	}
	if mapped := maps.People[who.AccountID]; mapped != "" {
		return mapped
	}
	return slug(who.DisplayName)
}

func parentKey(raw json.RawMessage) string {
	var parent struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(raw, &parent)
	return parent.Key
}

func stringField(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return ""
}

func stringList(raw json.RawMessage) []string {
	var list []string
	_ = json.Unmarshal(raw, &list)
	return list
}

// scalarList flattens a custom field value into scalars. The format forbids
// nested frontmatter, so an object becomes the one field a reader would have
// looked at anyway.
func scalarList(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return flatten(value)
}

func flatten(value any) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case bool:
		return []string{strconv.FormatBool(v)}
	case float64:
		return []string{strconv.FormatFloat(v, 'f', -1, 64)}
	case []any:
		var out []string
		for _, item := range v {
			out = append(out, flatten(item)...)
		}
		return out
	case map[string]any:
		for _, key := range []string{"value", "name", "displayName", "key", "id"} {
			if inner, ok := v[key]; ok {
				return flatten(inner)
			}
		}
		return nil
	default:
		return nil
	}
}

// jiraTimeLayout is the offset format Jira uses, which is RFC 3339 without the
// colon in the zone.
const jiraTimeLayout = "2006-01-02T15:04:05.000-0700"

func parseJiraTime(s string) (time.Time, error) {
	if t, err := time.Parse(jiraTimeLayout, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func timestamp(raw json.RawMessage, fallback time.Time) string {
	if parsed, err := parseJiraTime(stringField(raw)); err == nil {
		return parsed.UTC().Format(task.TimeFormat)
	}
	return fallback.UTC().Format(task.TimeFormat)
}

// noteNames is what a wikilink to each imported task will say. A parent is a
// link and a link resolves by note name, so every name has to be known before
// the first file is written — a task can name a parent that comes later in the
// snapshot.
func noteNames(projectKey string, issues []sourceIssue) map[string]string {
	notes := make(map[string]string, len(issues))
	for _, issue := range issues {
		key := project.Key(projectKey, numberOf(issue.Key))
		title := stringField(issue.Fields["summary"])
		notes[key] = strings.TrimSuffix(vault.FileName(key, title), ".md")
	}
	return notes
}

// typesOf turns the mapping into the vault's types, levels and all.
//
// The levels used to be dropped, on the reasoning that guessing which of
// somebody's types is an epic is a guess an import should not make silently.
// The reasoning was right and the premise was wrong: Jira states the hierarchy
// on every issue type, in the same numbering docket uses. Reading it is not
// guessing, and dropping it left every imported epic as ordinary work — a
// container that contains nothing, which is the one thing an epic is for.
//
// Deduplicated by name and ordered from the top down, so a board's vocabulary
// reads epic, story, task, sub-task rather than in whatever order the issues
// happened to arrive.
func typesOf(mapped map[string]TypeMap) []project.Type {
	byName := map[string]project.Type{}
	for _, m := range mapped {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		// Two source types with one vault name — Sub-task and Подзадача both
		// becoming "task" — keep the level furthest from ordinary, because a
		// sub-task wrongly called ordinary work lands in the backlog.
		if had, seen := byName[name]; seen && abs(had.Level) >= abs(m.Level) {
			continue
		}
		byName[name] = project.Type{Name: name, Level: m.Level}
	}

	types := make([]project.Type, 0, len(byName))
	for _, t := range byName {
		types = append(types, t)
	}
	sort.SliceStable(types, func(a, b int) bool {
		if types[a].Level != types[b].Level {
			return types[a].Level > types[b].Level
		}
		return types[a].Name < types[b].Name
	})
	return types
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
