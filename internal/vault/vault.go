// Package vault creates docket vaults.
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

// Options are the values a new vault is stamped with.
type Options struct {
	// Key prefixes every task in the project: ACME-1, ACME-2.
	Key string
	// Name is what people call the project. Defaults to Key.
	Name string
}

func (o *Options) normalize() error {
	o.Key = strings.TrimSpace(o.Key)
	o.Name = strings.TrimSpace(o.Name)

	if o.Key == "" {
		return fmt.Errorf("a project key is required")
	}
	if !project.KeyPattern.MatchString(o.Key) {
		return fmt.Errorf(
			"project key %q is not usable: use 2 to 10 characters, upper-case letters and "+
				"digits, starting with a letter — the key is the head of every task file name",
			o.Key)
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

		content, err := render(src, opts)
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

	sort.Strings(written)
	return written, nil
}

func render(src string, opts Options) ([]byte, error) {
	raw, err := templates.ReadFile(src)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.New(path.Base(src)).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", src, err)
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, opts); err != nil {
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
