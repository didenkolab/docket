// Package vault reads and writes the files a vault is made of.
package vault

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/didenkolab/docket/internal/project"
)

// Folders a vault has, named once.
const (
	DocsDir      = "docs"
	BoardsDir    = "boards"
	TemplatesDir = "templates"
	Attachments  = "attachments"
	HistoryDir   = "_history"
	TaskTemplate = "templates/task.md"
)

// DefaultTemplate is what a new project starts as.
//
// A repository rather than something embedded in this binary, so that it can be
// changed without releasing one. The scaffold is exactly the part a team wants
// to make its own — its own AGENTS.md, its own conventions, its own CI — and a
// team should not have to wait for us to make it.
//
// The cost is honest: init needs to reach it. --template names another one, and
// any git remote will do, including a path on disk.
const DefaultTemplate = "https://github.com/didenkolab/docket-template.git"

// TemplateOnly are the paths that belong to a template rather than to what it
// makes. They let a template repository explain itself without every project
// inheriting the explanation — and its licence: the terms a template is
// published under are the template's, not the terms of every board made from
// it, which is a decision for the team whose board it is.
var TemplateOnly = []string{"TEMPLATE.md", ".template", "LICENSE"}

// Options say what a new vault is for.
type Options struct {
	// Key is the project key: the prefix of every task, and its folder.
	Key string
	// Name is what to call the vault for people.
	Name string
	// Template is the git remote to scaffold from. Empty means DefaultTemplate.
	Template string
}

func (o *Options) normalize() error {
	o.Key = strings.TrimSpace(o.Key)
	o.Name = strings.TrimSpace(o.Name)

	if o.Key == "" {
		return fmt.Errorf("a project key is required")
	}
	// Not upper-cased for the caller: a key is what every task, every folder
	// and every link is named after, so it is taken as given or refused.
	if err := project.ValidKey(o.Key); err != nil {
		return err
	}
	if o.Name == "" {
		o.Name = o.Key
	}
	if o.Template == "" {
		o.Template = DefaultTemplate
	}
	return nil
}

// ErrNotEmpty means the directory already holds something.
var ErrNotEmpty = errors.New("directory is not empty")

// Init writes a new vault into dir from a template repository, creating dir if
// it does not exist, and returns the paths it created, relative to dir.
//
// The template is read at arm's length: cloned to a depth of one into a
// temporary directory, its .git removed, and the files copied in. What lands in
// the new vault is content and not somebody else's history, and nothing
// connects the result to where the template lives. That is a copy rather than a
// fork on purpose — a fork keeps a relationship to the upstream, shows itself
// as one, and carries settings across.
//
// It refuses to write into a directory that already holds anything other than a
// .git directory. Scaffolding over an existing tree is how people lose work, and
// the caller who really means it can pick an empty directory.
func Init(dir string, opts Options) ([]string, error) {
	if err := opts.normalize(); err != nil {
		return nil, err
	}
	if err := ensureEmpty(dir); err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp("", "docket-template-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)

	source := filepath.Join(staging, "template")
	clone := exec.Command("git", "clone", "--depth", "1", "--quiet", opts.Template, source)
	// Nothing may prompt. A server scaffolding a project would hang forever
	// waiting for a password nobody is there to type, and a person at a
	// terminal is better told the template is unreachable than asked to
	// authenticate to it.
	clone.Env = append(clone.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if out, err := clone.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cannot read the template at %s: %s",
			opts.Template, strings.TrimSpace(string(out)))
	}

	// The template's history is not this project's history.
	for _, drop := range append([]string{".git"}, TemplateOnly...) {
		if err := os.RemoveAll(filepath.Join(source, drop)); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(filepath.Join(source, project.FileName)); err != nil {
		return nil, fmt.Errorf("%s holds no %s at its root, so it is not a vault template",
			opts.Template, project.FileName)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	written, err := copyTree(source, dir)
	if err != nil {
		return nil, err
	}
	stamped, err := stamp(dir, opts)
	if err != nil {
		return nil, err
	}
	return onDisk(dir, merge(written, stamped)), nil
}

// stamp makes the copy this project's own: the key and the name the caller
// asked for, a folder for its tasks, and boards that agree with the
// vocabulary.
//
// The configuration is rewritten rather than search-and-replaced, because it is
// structured and rewriting it is the honest way to change it: substituting text
// in YAML is how a template quietly produces a vault that will not parse.
func stamp(dir string, opts Options) ([]string, error) {
	c, err := project.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("the template's %s cannot be read: %w", project.FileName, err)
	}

	// Whatever projects the template shipped were placeholders. A template
	// cannot know the key, and carrying its placeholder into the new vault
	// would leave a project nobody asked for and no folder for the one they
	// did.
	//
	// The placeholder key is also the token the template's prose is written
	// against, so the substitution below can find it. That is what lets a
	// template be a valid vault and still have an AGENTS.md that speaks about
	// this project: it says PROJ-12 because PROJ is a real project in the
	// template, and PROJ becomes ACME on the way out.
	for _, p := range c.Projects {
		if p.Key == opts.Key {
			continue
		}
		if err := rename(dir, p.Key, opts.Key, c.Name, opts.Name); err != nil {
			return nil, err
		}
		if err := removeIfEmpty(filepath.Join(dir, p.Key)); err != nil {
			return nil, err
		}
	}
	c.Name = opts.Name
	c.Projects = []project.Project{{Key: opts.Key, Name: opts.Name}}
	if err := c.Save(dir); err != nil {
		return nil, err
	}
	written := []string{project.FileName}

	if err := os.MkdirAll(filepath.Join(dir, opts.Key), 0o755); err != nil {
		return nil, err
	}
	keep := filepath.ToSlash(filepath.Join(opts.Key, ".gitkeep"))
	if err := os.WriteFile(filepath.Join(dir, keep), nil, 0o644); err != nil {
		return nil, err
	}
	written = append(written, keep)

	// The boards name the project folders, so they cannot come from a template
	// that did not know the key.
	boards, err := WriteBoards(dir, c)
	if err != nil {
		return nil, err
	}
	written = append(written, boards...)

	// And the graph, for the same reason: the colours are the vault's own
	// words for its containers and its statuses, so a template that did not
	// know them could not carry it. Without this the graph opens as one grey
	// dot per note, which is the picture purpose §3 promises and does not keep.
	if err := WriteGraph(dir, c); err != nil {
		return nil, err
	}
	return append(written, GraphFile), nil
}

// removeIfEmpty drops a placeholder project folder, and leaves one that holds
// anything: a template may ship an example task, and losing it silently would
// be worse than an extra folder.
func removeIfEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			return nil
		}
	}
	return os.RemoveAll(dir)
}

// copyTree copies everything under src into dst and reports what it wrote,
// relative to dst and in slash form.
func copyTree(src, dst string) ([]string, error) {
	var written []string
	err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil || rel == "." {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, content, info.Mode().Perm()); err != nil {
			return err
		}
		written = append(written, filepath.ToSlash(rel))
		return nil
	})
	return written, err
}

// merge is the union of two lists of paths, in order, without duplicates: the
// template may already have shipped a file that stamping then rewrote.
func merge(first, second []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{first, second} {
		for _, p := range list {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// ensureEmpty refuses a directory that already holds anything but .git.
func ensureEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".git" {
			continue
		}
		return fmt.Errorf("%s: %w (%s)", dir, ErrNotEmpty, entry.Name())
	}
	return nil
}

// rename replaces the template's placeholder key and name with this project's,
// in every text file it wrote.
//
// A search and replace, which is the wrong tool for structured data and the
// right one here: the token is an upper-case project key, matched only where it
// stands as a word, and what it appears in is prose written on purpose against
// it. The configuration itself is not touched this way — it is rewritten from a
// parsed value, because substituting text in YAML is how a template quietly
// produces a vault that will not parse.
func rename(dir, fromKey, toKey, fromName, toName string) error {
	token := regexp.MustCompile(`\b` + regexp.QuoteMeta(fromKey) + `\b`)

	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		case filepath.Base(path) == project.FileName:
			return nil // rewritten from a parsed value, not substituted
		case !substitutable(path):
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := token.ReplaceAllString(string(raw), toKey)
		if fromName != "" && fromName != toName {
			text = strings.ReplaceAll(text, fromName, toName)
		}
		if text == string(raw) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(path, []byte(text), info.Mode().Perm())
	})
}

// substitutable reports whether a file holds prose rather than bytes. Anything
// else a template ships — an image, a font — is copied and left alone.
func substitutable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".yaml", ".yml", ".base", ".json", ".txt", ".toml", ".gitignore", "":
		return true
	}
	return false
}

// onDisk keeps the paths that survived stamping. copyTree reports what it wrote
// before the placeholder project was removed, and a list naming a file that is
// not there would be a lie in the one place somebody checks.
func onDisk(dir string, paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			kept = append(kept, p)
		}
	}
	return kept
}
