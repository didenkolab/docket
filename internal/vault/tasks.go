package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Entry is one task file found in a vault.
type Entry struct {
	Key     string // ACME-12, taken from the head of the file name
	Project string // the folder it sits in
	Number  int
	Path    string // path relative to the vault root
	Task    *task.Task
	Raw     []byte // the file as read, so a caller can fingerprint it
	Err     error  // set when the file could not be parsed; Task is then nil
}

// Note is the file's name without .md — what a wikilink to this task says.
func (e Entry) Note() string {
	return strings.TrimSuffix(filepath.Base(e.Path), ".md")
}

// builtinTaskTemplate is used when a vault has no templates/task.md. A vault
// that lost its template should still be able to take a new task.
const builtinTaskTemplate = `---
key:
title:
type: task
status:
status_category:
priority:
assignee:
labels: []
created:
updated:
aliases: []
---

## Comments
`

// FileName is what a task's file is called: the key, a space, and the title.
//
// The name carries the title because that is what Obsidian shows — in the
// graph, in the file explorer, in search. A file called 12.md tells nobody
// anything. See ADR-0005.
//
// The title goes in as written, in whatever language it was written in. Only
// the characters a file name or a wikilink genuinely cannot hold are replaced,
// and the replacements are listed in sanitise so that a reader can see exactly
// how much of the original survives: all of it, except those.
func FileName(key, title string) string {
	title = strings.TrimSpace(sanitise(title))
	if title == "" {
		return key + ".md"
	}

	// Most file systems cap one path component at 255 bytes. A title is only
	// shortened when it would not otherwise fit, and never otherwise.
	const limit = 240
	if len(key)+1+len(title)+3 > limit {
		title = strings.TrimSpace(truncate(title, limit-len(key)-4))
	}
	return key + " " + title + ".md"
}

// sanitise replaces only what cannot be in a file name that Obsidian can also
// link to.
//
//	/ \  path separators — would make folders
//	: * ? " < > |  refused by one file system or another
//	# ^ [ ]        wikilink syntax; a note holding these cannot be linked to
//
// Everything else — every alphabet, every accent, punctuation, emoji — is kept
// exactly as it was typed.
func sanitise(title string) string {
	return strings.NewReplacer(
		"/", "-", "\\", "-", ":", " -", "*", "", "?", "", `"`, "'",
		"<", "(", ">", ")", "|", "-", "#", "", "^", "", "[", "(", "]", ")",
	).Replace(title)
}

// truncate cuts to a byte budget without splitting a character in half.
func truncate(s string, budget int) string {
	if len(s) <= budget {
		return s
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// Find locates a task's file by its key, and returns the path relative to the
// vault root.
//
// The key is no longer the path, so this is a glob rather than a join. It is
// confined here, which is the point: everything else still asks for a task by
// its key.
func Find(root string, c *project.Config, key string) (string, error) {
	projectKey, _, err := project.SplitKey(key)
	if err != nil {
		return "", err
	}
	if !c.HasProject(projectKey) {
		return "", fmt.Errorf("project %s is not in this vault", projectKey)
	}

	matches, err := filepath.Glob(filepath.Join(root, projectKey, key+"*.md"))
	if err != nil {
		return "", err
	}
	for _, match := range matches {
		// ACME-1* also matches ACME-12; only a space or the extension ends a key.
		if keyOfFile(filepath.Base(match)) == key {
			rel, err := filepath.Rel(root, match)
			if err != nil {
				return "", err
			}
			return filepath.ToSlash(rel), nil
		}
	}
	return "", fmt.Errorf("%s is not in this vault", key)
}

// keyOfFile reads the key off the head of a file name.
func keyOfFile(name string) string {
	name = strings.TrimSuffix(name, ".md")
	if space := strings.Index(name, " "); space >= 0 {
		return name[:space]
	}
	return name
}

// List reads every task in the vault, project by project in the order
// docket.yaml gives them, and by number within each.
//
// A file that fails to parse is reported as an Entry carrying the error rather
// than aborting the walk: a validator has to see all the broken files, not just
// the first.
func List(root string, c *project.Config) ([]Entry, error) {
	var entries []Entry

	for _, key := range c.ProjectKeys() {
		found, err := listProject(root, key)
		if err != nil {
			return nil, err
		}
		entries = append(entries, found...)
	}
	return entries, nil
}

func listProject(root, projectKey string) ([]Entry, error) {
	dir := filepath.Join(root, projectKey)
	names, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for _, name := range names {
		if name.IsDir() || !strings.HasSuffix(name.Name(), ".md") {
			continue
		}

		key := keyOfFile(name.Name())
		entry := Entry{
			Key:     key,
			Project: projectKey,
			Path:    filepath.ToSlash(filepath.Join(projectKey, name.Name())),
		}

		owner, number, keyErr := project.SplitKey(key)
		switch {
		case keyErr != nil:
			entry.Err = fmt.Errorf("file name %q does not start with a task key: "+
				"a task is named %q", name.Name(), "KEY-1 Its title.md")
		case owner != projectKey:
			entry.Err = fmt.Errorf("%s sits in the %s folder", key, projectKey)
		default:
			entry.Number = number
		}
		if entry.Err != nil {
			entries = append(entries, entry)
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, name.Name()))
		if err != nil {
			entry.Err = err
		} else {
			entry.Raw = raw
			if entry.Task, err = task.Parse(raw); err != nil {
				entry.Err = err
			}
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Number < entries[j].Number })
	return entries, nil
}

// NextKey is the highest task number in a project plus one.
//
// There is no counter file on purpose: a counter is a single line that every
// task creation has to touch, which turns routine parallel work into merge
// conflicts on that line. Two agents on two branches can still pick the same
// number here — that surfaces as a git add/add conflict, which is loud and
// fixable, and never a silent overwrite.
func NextKey(root, projectKey string) (string, error) {
	entries, err := listProject(root, projectKey)
	if err != nil {
		return "", err
	}

	highest := 0
	for _, e := range entries {
		if e.Number > highest {
			highest = e.Number
		}
	}
	return project.Key(projectKey, highest+1), nil
}

// NewOptions describes the task to create. Empty fields fall back to the
// vault's defaults.
type NewOptions struct {
	Project     string
	Title       string
	Description string
	Type        string
	Status      string
	Priority    string
	Assignee    string
	Parent      string
	Labels      []string
	Tags        []string
	Now         time.Time
}

// Create writes a new task and returns its path relative to the vault root.
//
// It fills the vault's own templates/task.md when there is one, so that a vault
// which added properties to its template keeps them.
func Create(root string, c *project.Config, opts NewOptions) (string, *task.Task, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return "", nil, fmt.Errorf("a task needs a title")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	if opts.Project == "" {
		opts.Project = c.Projects[0].Key
	}
	if !c.HasProject(opts.Project) {
		return "", nil, fmt.Errorf("project %q is not in this vault: it holds %s",
			opts.Project, strings.Join(c.ProjectKeys(), ", "))
	}

	if opts.Type == "" {
		opts.Type = c.Types[0]
	}
	if !c.HasType(opts.Type) {
		return "", nil, fmt.Errorf("type %q is not one of %s", opts.Type, strings.Join(c.Types, ", "))
	}

	if opts.Priority == "" {
		opts.Priority = c.DefaultPriority()
	}
	if !c.HasPriority(opts.Priority) {
		return "", nil, fmt.Errorf("priority %q is not one of %s",
			opts.Priority, strings.Join(c.Priorities, ", "))
	}

	if opts.Status == "" {
		opts.Status = c.FirstStatus().Name
	}
	category, known := c.CategoryOf(opts.Status)
	if !known {
		return "", nil, fmt.Errorf("status %q is not one of %s",
			opts.Status, strings.Join(c.StatusNames(), ", "))
	}

	parentNote := ""
	if opts.Parent != "" {
		rel, err := Find(root, c, opts.Parent)
		if err != nil {
			return "", nil, fmt.Errorf("parent %s does not exist", opts.Parent)
		}
		// A parent is written as a link, and a link resolves by note name.
		parentNote = noteName(rel)
	}

	key, err := NextKey(root, opts.Project)
	if err != nil {
		return "", nil, err
	}

	t, err := task.Parse(taskTemplate(root))
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", TaskTemplate, err)
	}

	stamp := opts.Now.UTC().Format(task.TimeFormat)
	t.Set("key", key)
	t.Set("title", opts.Title)
	t.Set("type", opts.Type)
	t.SetStatus(opts.Status, category)
	t.Set("priority", opts.Priority)
	t.Set("assignee", opts.Assignee)
	t.SetPlain("created", stamp)
	t.SetPlain("updated", stamp)
	t.SetLabels(opts.Labels)
	t.SetTags(opts.Tags)
	t.SetList("aliases", nil)
	if opts.Description != "" {
		t.SetDescription(opts.Description)
	}
	t.SetParent(parentNote)
	if err := t.Sync(); err != nil {
		return "", nil, err
	}

	content, err := t.Bytes()
	if err != nil {
		return "", nil, err
	}

	rel := filepath.ToSlash(filepath.Join(opts.Project, FileName(key, opts.Title)))
	if err := writeNew(filepath.Join(root, filepath.FromSlash(rel)), content); err != nil {
		return "", nil, err
	}
	return rel, t, nil
}

// writeNew writes a file that must not exist yet.
//
// The scan in NextKey can be beaten by another process picking the same number
// between the scan and the write. O_EXCL turns that race into an error on the
// second writer instead of a task quietly replacing the one that got there
// first.
func writeNew(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(content)
	return err
}

func taskTemplate(root string) []byte {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(TaskTemplate)))
	if err != nil {
		return []byte(builtinTaskTemplate)
	}
	return raw
}

// Rename moves a task's file, which is what a change of title amounts to now
// that the name carries it. Both paths come back so the caller can commit the
// rename as a rename rather than as a delete and an add.
func Rename(root, from, to string) error {
	if from == to {
		return nil
	}
	target := filepath.Join(root, filepath.FromSlash(to))
	if _, err := os.Stat(target); err == nil {
		return fmt.Errorf("%s already exists", to)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.Rename(filepath.Join(root, filepath.FromSlash(from)), target)
}

// PathFor is where a task with this key and title belongs.
func PathFor(projectKey, key, title string) string {
	return filepath.ToSlash(filepath.Join(projectKey, FileName(key, title)))
}

// Note is the name a wikilink to this task uses: the file name without the
// folder and without the extension.
//
// A link resolves by note name, and a note is named after its task, so this is
// the one place that knows how to turn a key into something linkable. Obsidian
// does not consult aliases, so `[[ACME-12]]` on its own points at nothing —
// see docs/decisions/0005-a-file-is-named-after-its-task.md.
func Note(root string, c *project.Config, key string) (string, error) {
	rel, err := Find(root, c, key)
	if err != nil {
		return "", err
	}
	return noteName(rel), nil
}
