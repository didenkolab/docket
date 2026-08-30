package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Directories a vault keeps its content in.
const (
	TasksDir     = "tasks"
	DocsDir      = "docs"
	TemplatesDir = "templates"
	TaskTemplate = "templates/task.md"
)

// Entry is one task file found in a vault.
type Entry struct {
	Key  string // the key taken from the file name
	Path string // path relative to the vault root
	Task *task.Task
	Err  error // set when the file could not be parsed; Task is then nil
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

// List reads every task in the vault, sorted by key number. A file that fails
// to parse is reported as an Entry carrying the error rather than aborting the
// walk: a validator has to see all the broken files, not just the first.
func List(root string) ([]Entry, error) {
	dir := filepath.Join(root, TasksDir)
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

		rel := filepath.ToSlash(filepath.Join(TasksDir, name.Name()))
		entry := Entry{
			Key:  strings.TrimSuffix(name.Name(), ".md"),
			Path: rel,
		}

		raw, err := os.ReadFile(filepath.Join(dir, name.Name()))
		if err != nil {
			entry.Err = err
		} else if entry.Task, err = task.Parse(raw); err != nil {
			entry.Err = err
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		ni, oki := number(entries[i].Key)
		nj, okj := number(entries[j].Key)
		if oki && okj && ni != nj {
			return ni < nj
		}
		return entries[i].Key < entries[j].Key
	})
	return entries, nil
}

var keySuffix = regexp.MustCompile(`-(\d+)$`)

func number(key string) (int, bool) {
	m := keySuffix.FindStringSubmatch(key)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// NextKey is the highest task number in the vault plus one.
//
// There is no counter file on purpose: a counter is a single line that every
// task creation has to touch, which turns routine parallel work into merge
// conflicts on that line. Two agents on two branches can still pick the same
// number here — that surfaces as a git add/add conflict, which is loud and
// fixable, and never a silent overwrite.
func NextKey(root string, p *project.Project) (string, error) {
	entries, err := List(root)
	if err != nil {
		return "", err
	}

	highest := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, p.Key+"-") {
			continue
		}
		if n, ok := number(e.Key); ok && n > highest {
			highest = n
		}
	}
	return fmt.Sprintf("%s-%d", p.Key, highest+1), nil
}

// NewOptions describes the task to create. Empty fields fall back to the
// project's defaults.
type NewOptions struct {
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
// It fills the vault's own templates/task.md when there is one, so that a
// project which added properties to its template keeps them.
func Create(root string, p *project.Project, opts NewOptions) (string, *task.Task, error) {
	if strings.TrimSpace(opts.Title) == "" {
		return "", nil, fmt.Errorf("a task needs a title")
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now()
	}

	if opts.Type == "" {
		opts.Type = p.Types[0]
	}
	if !p.HasType(opts.Type) {
		return "", nil, fmt.Errorf("type %q is not one of %s", opts.Type, strings.Join(p.Types, ", "))
	}

	if opts.Priority == "" {
		opts.Priority = p.DefaultPriority()
	}
	if !p.HasPriority(opts.Priority) {
		return "", nil, fmt.Errorf("priority %q is not one of %s",
			opts.Priority, strings.Join(p.Priorities, ", "))
	}

	if opts.Status == "" {
		opts.Status = p.FirstStatus().Name
	}
	category, known := p.CategoryOf(opts.Status)
	if !known {
		return "", nil, fmt.Errorf("status %q is not one of %s",
			opts.Status, strings.Join(p.StatusNames(), ", "))
	}

	if opts.Parent != "" {
		if _, err := os.Stat(filepath.Join(root, TasksDir, opts.Parent+".md")); err != nil {
			return "", nil, fmt.Errorf("parent %s does not exist", opts.Parent)
		}
	}

	key, err := NextKey(root, p)
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

	rel := filepath.Join(TasksDir, key+".md")
	if err := writeNew(filepath.Join(root, rel), content); err != nil {
		return "", nil, err
	}
	return filepath.ToSlash(rel), t, nil
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
