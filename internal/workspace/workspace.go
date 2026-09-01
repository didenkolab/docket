// Package workspace assembles several project repositories into one Obsidian
// vault without merging them.
//
// The workspace repository tracks the Obsidian config and a manifest. The
// projects inside it stay separate repositories and are ignored by it. This is
// deliberately not git submodules: a submodule pins a commit, so every task
// edit would need a second commit here to move the pointer, and the projects
// would sit permanently in detached HEAD. Submodules fit dependencies that
// change rarely; a tracker changes constantly.
package workspace

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"gopkg.in/yaml.v3"
)

//go:embed all:template
var templates embed.FS

const templateRoot = "template"

// FileName is the manifest, and the file that marks a workspace root.
const FileName = "workspace.yaml"

// Markers around the generated part of .gitignore, so that anything a person
// added by hand survives regeneration.
const (
	ignoreBegin = "# --- docket workspace: project repositories (generated) ---"
	ignoreEnd   = "# --- end ---"
)

// ErrNotAWorkspace is returned for a directory holding no manifest.
var ErrNotAWorkspace = errors.New("not a docket workspace: no " + FileName)

// Project is one entry in the manifest.
type Project struct {
	Key    string `yaml:"key"`
	Path   string `yaml:"path"`
	Remote string `yaml:"remote"`
}

// Manifest is the content of workspace.yaml.
type Manifest struct {
	Projects []Project `yaml:"projects"`
}

// Load reads and validates the manifest in dir.
func Load(dir string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", dir, ErrNotAWorkspace)
	}
	if err != nil {
		return nil, err
	}

	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return &m, nil
}

// FindRoot walks up from start looking for a manifest.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s and its parents: %w", start, ErrNotAWorkspace)
		}
		dir = parent
	}
}

func (m *Manifest) validate() error {
	keys := map[string]bool{}
	paths := map[string]bool{}

	for i := range m.Projects {
		p := &m.Projects[i]
		p.Key = strings.TrimSpace(p.Key)
		p.Remote = strings.TrimSpace(p.Remote)

		if !project.KeyPattern.MatchString(p.Key) {
			return fmt.Errorf("project key %q is not usable", p.Key)
		}
		if p.Remote == "" {
			return fmt.Errorf("project %s has no remote", p.Key)
		}

		// Check the path as written. Tidying it first would turn "/etc" into a
		// relative "etc" and quietly accept what it should refuse.
		raw := strings.TrimSpace(p.Path)
		if raw == "" {
			raw = strings.ToLower(p.Key)
		}
		if filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
			return fmt.Errorf("project %s has path %q: it must be relative to the workspace",
				p.Key, p.Path)
		}
		cleaned := path.Clean(filepath.ToSlash(raw))
		if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
			return fmt.Errorf("project %s has path %q: it must stay inside the workspace",
				p.Key, p.Path)
		}
		p.Path = cleaned
		if keys[p.Key] {
			return fmt.Errorf("project %s is listed twice", p.Key)
		}
		if paths[p.Path] {
			return fmt.Errorf("path %q is used by two projects", p.Path)
		}
		keys[p.Key], paths[p.Path] = true, true
	}
	return nil
}

// Save writes the manifest and refreshes the generated part of .gitignore, so
// that the two never disagree about which folders are project repositories.
func (m *Manifest) Save(dir string) error {
	if err := m.validate(); err != nil {
		return err
	}

	var buf bytes.Buffer
	buf.WriteString("# Projects assembled into this workspace. Each is its own git repository.\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}

	if err := os.WriteFile(filepath.Join(dir, FileName), buf.Bytes(), 0o644); err != nil {
		return err
	}
	return m.writeGitignore(dir)
}

// writeGitignore rewrites the generated block, leaving anything else in the
// file alone.
func (m *Manifest) writeGitignore(dir string) error {
	generated := []string{ignoreBegin}
	for _, p := range m.Projects {
		generated = append(generated, "/"+p.Path+"/")
	}
	generated = append(generated, ignoreEnd)
	block := strings.Join(generated, "\n") + "\n"

	path := filepath.Join(dir, ".gitignore")
	existing, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return os.WriteFile(path, []byte(defaultIgnore+"\n"+block), 0o644)
	}
	if err != nil {
		return err
	}

	text := string(existing)
	begin := strings.Index(text, ignoreBegin)
	if begin < 0 {
		return os.WriteFile(path, []byte(strings.TrimRight(text, "\n")+"\n\n"+block), 0o644)
	}
	end := strings.Index(text[begin:], ignoreEnd)
	if end < 0 {
		return os.WriteFile(path, []byte(text[:begin]+block), 0o644)
	}
	tail := text[begin+end+len(ignoreEnd):]
	return os.WriteFile(path, []byte(text[:begin]+strings.TrimSuffix(block, "\n")+tail), 0o644)
}

const defaultIgnore = `# Obsidian per-machine state — the layout of open panes, not content
.obsidian/workspace.json
.obsidian/workspace-mobile.json

# Obsidian local trash
.trash/

# Editors. Which panes somebody had open is not a fact about the project, and
# an .idea/ committed once follows every clone around for good.
.idea/
.vscode/

# macOS
.DS_Store
`

// Add puts a project in the manifest.
func (m *Manifest) Add(p Project) error {
	for _, existing := range m.Projects {
		if existing.Key == strings.TrimSpace(p.Key) {
			return fmt.Errorf("project %s is already in the workspace", existing.Key)
		}
	}
	m.Projects = append(m.Projects, p)
	return m.validate()
}

// Init creates a workspace in dir, which must be empty apart from .git.
func Init(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	switch {
	case os.IsNotExist(err):
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		for _, e := range entries {
			if e.Name() == ".git" {
				continue
			}
			return nil, fmt.Errorf("%s is not empty (it holds %q): docket workspace init will "+
				"not write over an existing tree", dir, e.Name())
		}
	}

	var written []string
	err = fs.WalkDir(templates, templateRoot, func(src string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(templateRoot, filepath.FromSlash(src))
		if err != nil {
			return err
		}
		content, err := templates.ReadFile(src)
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

	empty := &Manifest{}
	if err := empty.Save(dir); err != nil {
		return nil, err
	}
	return append(written, FileName, ".gitignore"), nil
}
