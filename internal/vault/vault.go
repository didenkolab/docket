// Package vault creates docket vaults and reads the tasks in them.
//
// The templates ship inside the binary rather than being fetched from a
// template repository: one fewer thing to keep in sync, and init works offline.
package vault

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/vadymdidenkolab/docket/internal/project"
)

//go:embed all:template
var templates embed.FS

const templateRoot = "template"

// gitignoreSource is stored without its leading dot. A real .gitignore inside
// the module would be a gitignore for this repository, which is not what it is
// meant to be — it is content we hand to a new vault.
const gitignoreSource = "gitignore"

// Directories a vault keeps its content in. Everything else at the root that is
// listed in docket.yaml is a project.
const (
	DocsDir      = "docs"
	BoardsDir    = "boards"
	TemplatesDir = "templates"
	Attachments  = "attachments"
	HistoryDir   = "_history"
	TaskTemplate = "templates/task.md"
)

// Options are the values a new vault is stamped with.
type Options struct {
	// Key is the first project's key, which is also its folder.
	Key string
	// Name is what people call the vault. Defaults to Key.
	Name string
}

func (o *Options) normalize() error {
	o.Key = strings.TrimSpace(o.Key)
	o.Name = strings.TrimSpace(o.Name)

	if o.Key == "" {
		return fmt.Errorf("a project key is required")
	}
	if err := project.ValidKey(o.Key); err != nil {
		return err
	}
	if o.Name == "" {
		o.Name = o.Key
	}
	return nil
}

// Init writes a new vault into dir, creating dir if it does not exist, and
// returns the paths it created, relative to dir.
//
// It refuses to write into a directory that already holds anything other than
// a .git directory. Scaffolding over an existing tree is how people lose work,
// and the caller who really means it can pick an empty directory.
func Init(dir string, opts Options) ([]string, error) {
	if err := opts.normalize(); err != nil {
		return nil, err
	}
	if err := ensureEmpty(dir); err != nil {
		return nil, err
	}

	data := struct{ Key, Name string }{opts.Key, opts.Name}

	var written []string
	err := fs.WalkDir(templates, templateRoot, func(src string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(templateRoot, filepath.FromSlash(src))
		if err != nil {
			return err
		}
		if path.Base(src) == gitignoreSource && path.Dir(src) == templateRoot {
			rel = ".gitignore"
		}

		content, err := render(src, data)
		if err != nil {
			return err
		}

		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}

		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	config := DefaultConfig(opts.Key, opts.Name)
	if err := config.Save(dir); err != nil {
		return nil, err
	}
	written = append(written, project.FileName)

	if err := os.MkdirAll(filepath.Join(dir, opts.Key), 0o755); err != nil {
		return nil, err
	}
	keep := filepath.Join(opts.Key, ".gitkeep")
	if err := os.WriteFile(filepath.Join(dir, keep), nil, 0o644); err != nil {
		return nil, err
	}
	written = append(written, filepath.ToSlash(keep))

	boards, err := WriteBoards(dir, config)
	if err != nil {
		return nil, err
	}
	written = append(written, boards...)

	sort.Strings(written)
	return written, nil
}

// DefaultConfig is the vocabulary a new vault starts with.
func DefaultConfig(key, name string) *project.Config {
	return &project.Config{
		Name:     name,
		Projects: []project.Project{{Key: key, Name: name}},
		Statuses: []project.Status{
			{Name: "Backlog", Category: project.CategoryTodo},
			{Name: "Ready", Category: project.CategoryTodo},
			{Name: "In progress", Category: project.CategoryDoing},
			{Name: "In review", Category: project.CategoryDoing},
			{Name: "Done", Category: project.CategoryDone},
			{Name: "Dropped", Category: project.CategoryDone},
		},
		Types:      []string{"task", "bug", "story", "epic"},
		Priorities: []string{"low", "normal", "high", "urgent"},
	}
}

func render(src string, data any) ([]byte, error) {
	raw, err := templates.ReadFile(src)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New(path.Base(src)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", src, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		return nil, fmt.Errorf("template %s: %w", src, err)
	}
	return out.Bytes(), nil
}

// ensureEmpty creates dir when it is missing and otherwise checks that it holds
// nothing but .git — the common case of running init inside a freshly cloned
// empty repository.
func ensureEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		return fmt.Errorf(
			"%s is not empty (it holds %q): docket init will not write over an existing tree",
			dir, entry.Name())
	}
	return nil
}
