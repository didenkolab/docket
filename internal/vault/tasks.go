package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Entry is one task file found in a vault.
type Entry struct {
	Key     string // PROJECT/NUMBER, taken from the path
	Project string
	Number  int
	Path    string // path relative to the vault root
	Task    *task.Task
	Raw     []byte // the file as read, so a caller can fingerprint it
	Err     error  // set when the file could not be parsed; Task is then nil
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

// TaskPath is where a key's file lives, relative to the vault root. The path is
// the key with .md on the end, which is the whole point of the shape.
func TaskPath(key string) string { return key + ".md" }

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

		base := strings.TrimSuffix(name.Name(), ".md")
		number, convErr := strconv.Atoi(base)

		entry := Entry{
			Key:     projectKey + "/" + base,
			Project: projectKey,
			Number:  number,
			Path:    filepath.ToSlash(filepath.Join(projectKey, name.Name())),
		}
		if convErr != nil {
			entry.Err = fmt.Errorf("file name %q is not a task number: a task is PROJECT/NUMBER.md",
				name.Name())
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
	Project  string
	Title    string
	Type     string
	Status   string
	Priority string
	Assignee string
	Parent   string
	Labels   []string
	Now      time.Time
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

	if opts.Parent != "" {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(TaskPath(opts.Parent)))); err != nil {
			return "", nil, fmt.Errorf("parent %s does not exist", opts.Parent)
		}
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
	t.SetList("labels", opts.Labels)
	t.SetList("aliases", nil)
	if opts.Parent != "" {
		t.Set("parent", opts.Parent)
	} else {
		t.Remove("parent")
	}
	if err := t.Sync(); err != nil {
		return "", nil, err
	}

	content, err := t.Bytes()
	if err != nil {
		return "", nil, err
	}

	rel := TaskPath(key)
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
