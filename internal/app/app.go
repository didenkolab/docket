// Package app installs a pack: vocabulary and files a vault takes on.
//
// The Jira marketplace's top hundred, sorted by what they actually are, is
// ninety per cent four mechanisms — vocabulary, reactions, surfaces, and a way
// out to the network. This is the first, and it is the one that needs no code
// at all: a test-management app is four types, six fields, a verb and three
// views, and every one of those is a line in a file.
//
// So a pack is a git repository holding a manifest and some files. Installing
// it merges the vocabulary into docket.yaml and copies the files in. Nothing is
// executed, nothing is hidden: what an installation did is a diff, and it is
// reviewed and reverted like any other diff.
//
// What it refuses matters more than what it does. A pack that would redefine a
// status, take a property that already means something else, or write over a
// file somebody has edited is refused by name, with the conflict said out loud.
// An installer that "merges" those is an installer that loses work quietly.
package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"gopkg.in/yaml.v3"
)

// FileName is the manifest, and the file that marks a directory as a pack.
const FileName = "docket-app.yaml"

// Copyable are the folders a pack may put files in.
//
// Deliberately four. A pack that could write anywhere could write docket.yaml,
// a task, or a .git hook, and then installing one would be running one.
//
// hooks/ is where a program lives, and a file there arrives executable if it
// was executable in the pack. That is not consent to run it: nothing in a
// repository runs unless the server was started with --programs. Installing
// writes the file; running it is a separate decision made on the machine.
var Copyable = []string{"templates", "boards", "docs", "hooks"}

// Manifest is what a pack says it is.
type Manifest struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Version     string `yaml:"version,omitempty"`
	// Vocabulary is what it adds to docket.yaml.
	Vocabulary Vocabulary `yaml:"vocabulary,omitempty"`
	// Surfaces are the pages and panels it draws, each from a program it
	// carries. An app declaring one is an app that brings code, and installing
	// it says so out loud.
	Surfaces Surfaces `yaml:"surfaces,omitempty"`
}

// Surfaces are the pages and panels a pack declares.
type Surfaces struct {
	Pages  []project.Surface `yaml:"pages,omitempty"`
	Panels []project.Surface `yaml:"panels,omitempty"`
}

// BringsPrograms reports whether installing this would put a program in the
// repository — the fact that decides whether anybody has to think before
// installing it.
func (p *Pack) BringsPrograms() bool {
	if len(p.Surfaces.Pages) > 0 || len(p.Surfaces.Panels) > 0 {
		return true
	}
	for _, file := range p.Files {
		if strings.HasPrefix(file, "hooks/") {
			return true
		}
	}
	return false
}

// Vocabulary is the part of a configuration a pack may contribute.
//
// No statuses and no workflow: a column is what a team agreed to, and an
// installer that added one would change how work moves through a project
// because somebody wanted a report. A pack that needs a status says so in its
// description and lets a person add it.
type Vocabulary struct {
	Types     []project.Type     `yaml:"types,omitempty"`
	Fields    []project.Field    `yaml:"fields,omitempty"`
	Relations []project.Relation `yaml:"relations,omitempty"`
}

// Pack is a manifest and where it was read from.
type Pack struct {
	Manifest
	// Dir is the directory holding it, which is a temporary clone for a remote.
	Dir string
	// Source is what was asked for: a URL or a path.
	Source string
	// Files are the paths it would copy, relative to the vault root.
	Files []string
}

// ErrNotAPack is returned for a directory holding no manifest.
var ErrNotAPack = errors.New("not a docket app: no " + FileName)

// Read opens a pack from a directory.
func Read(dir, source string) (*Pack, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", source, ErrNotAPack)
	}
	if err != nil {
		return nil, err
	}

	p := &Pack{Dir: dir, Source: source}
	if err := yaml.Unmarshal(raw, &p.Manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("%s: the app has no name", FileName)
	}
	if !project.FieldName.MatchString(p.Name) {
		return nil, fmt.Errorf("%s: %q is not a name a vault can record — "+
			"a letter, then letters, digits or underscores", FileName, p.Name)
	}

	for _, folder := range Copyable {
		at := filepath.Join(dir, folder)
		err := filepath.WalkDir(at, func(where string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			// Regular files only. A symlink in a pack is a path out of it —
			// `templates/x -> /etc/hosts` reads as a file the installer would
			// copy, and `-> ../../../.git/hooks/pre-commit` is worse: it turns
			// installing an app into running one.
			if !d.Type().IsRegular() {
				return fmt.Errorf("%s is not a regular file, and an app may only carry files",
					filepath.ToSlash(where[len(dir)+1:]))
			}
			rel, err := filepath.Rel(dir, where)
			if err != nil {
				return err
			}
			if slashed := filepath.ToSlash(rel); strings.HasPrefix(slashed, "../") {
				return fmt.Errorf("%s leads outside the app", slashed)
			}
			p.Files = append(p.Files, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(p.Files)
	return p, nil
}

// Fetch reads a pack from a path or clones it from a git remote.
//
// A `#folder` on the end names a pack inside the repository, because a library
// of them is one repository: six apps in six repositories is six things to
// clone, six histories to follow and six places for the same fix.
//
// The caller removes the returned directory when it is done with it; for a
// local path it is the path itself and removing it would be removing somebody's
// work, so the cleanup is returned rather than assumed.
func Fetch(source string) (*Pack, func(), error) {
	nothing := func() {}

	// The whole string is what gets recorded, so that installing again from the
	// same place finds the same app: a source that lost its #folder points at a
	// repository with no manifest at its root.
	asked := source
	remote, inside := source, ""
	if at := strings.LastIndex(source, "#"); at >= 0 {
		remote, inside = source[:at], strings.Trim(source[at+1:], "/")
		if strings.HasPrefix(filepath.ToSlash(filepath.Clean(inside)), "../") ||
			filepath.IsAbs(inside) {
			return nil, nothing, fmt.Errorf("%q leads outside the repository", inside)
		}
	}

	if info, err := os.Stat(remote); err == nil && info.IsDir() {
		at := filepath.Join(remote, filepath.FromSlash(inside))
		pack, err := Read(at, asked)
		return pack, nothing, err
	}
	source = remote

	staging, err := os.MkdirTemp("", "docket-app-")
	if err != nil {
		return nil, nothing, err
	}
	cleanup := func() { _ = os.RemoveAll(staging) }

	at := filepath.Join(staging, "app")
	clone := exec.Command("git", "clone", "--depth", "1", "--quiet", source, at)
	// Nothing may prompt: an installer waiting for a password nobody is there
	// to type is an installer that hangs.
	clone.Env = append(clone.Environ(),
		"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	if out, err := clone.CombinedOutput(); err != nil {
		cleanup()
		return nil, nothing, fmt.Errorf("cannot read the app at %s: %s",
			source, strings.TrimSpace(string(out)))
	}

	pack, err := Read(filepath.Join(at, filepath.FromSlash(inside)), asked)
	if err != nil {
		cleanup()
		return nil, nothing, err
	}
	return pack, cleanup, nil
}

// Conflict is a reason a pack cannot be installed as it is.
type Conflict struct {
	What string // "type", "field", "relation", "file"
	Name string
	Why  string
}

func (c Conflict) Error() string { return c.What + " " + c.Name + ": " + c.Why }

// Check reports everything that stands in the way, rather than the first thing.
//
// All of them, because installing an app is a decision somebody makes once with
// the whole picture: told one conflict at a time, they fix it, run it again,
// and are told the next.
func Check(root string, c *project.Config, p *Pack) []Conflict {
	var out []Conflict

	known := map[string]project.Type{}
	for _, t := range c.Types {
		known[t.Name] = t
	}
	for _, t := range p.Vocabulary.Types {
		if existing, ok := known[t.Name]; ok && existing.Level != t.Level {
			out = append(out, Conflict{"type", t.Name, fmt.Sprintf(
				"this vault already has it at level %d and the app wants level %d",
				existing.Level, t.Level)})
		}
	}

	fields := map[string]project.Field{}
	for _, f := range c.Fields {
		fields[f.Name] = f
	}
	for _, f := range p.Vocabulary.Fields {
		if existing, ok := fields[f.Name]; ok && existing.Kind != f.Kind {
			out = append(out, Conflict{"field", f.Name, fmt.Sprintf(
				"this vault already has it as %s and the app wants %s",
				existing.Kind, f.Kind)})
		}
		if project.OwnedProperties[f.Name] {
			out = append(out, Conflict{"field", f.Name,
				"that is a property the format owns"})
		}
		if c.IsRelation(f.Name) {
			out = append(out, Conflict{"field", f.Name,
				"that is already a relation here, and one property cannot be both"})
		}
	}

	for _, r := range p.Vocabulary.Relations {
		if existing, ok := c.RelationOf(r.Name); ok && existing.Inverse != r.Inverse {
			out = append(out, Conflict{"relation", r.Name, fmt.Sprintf(
				"this vault already has it with inverse %q and the app wants %q",
				existing.Inverse, r.Inverse)})
		}
		if _, ok := fields[r.Name]; ok {
			out = append(out, Conflict{"relation", r.Name,
				"that is already a field here, and one property cannot be both"})
		}
	}

	for _, page := range append(append([]project.Surface{}, p.Surfaces.Pages...), p.Surfaces.Panels...) {
		for _, existing := range append(append([]project.Surface{}, c.Pages...), c.Panels...) {
			if strings.EqualFold(existing.Name, page.Name) && existing.Run != page.Run {
				out = append(out, Conflict{"page", page.Name, fmt.Sprintf(
					"this vault already draws it with %s and the app wants %s",
					existing.Run, page.Run)})
			}
		}
	}

	// A file that is already there and differs is somebody's edit. Identical is
	// fine — installing the same app twice should be quiet.
	for _, rel := range p.Files {
		at := filepath.Join(root, filepath.FromSlash(rel))
		theirs, err := os.ReadFile(at)
		if err != nil {
			continue // not there: nothing to overwrite
		}
		ours, err := os.ReadFile(filepath.Join(p.Dir, filepath.FromSlash(rel)))
		if err != nil {
			out = append(out, Conflict{"file", rel, err.Error()})
			continue
		}
		if string(theirs) != string(ours) {
			out = append(out, Conflict{"file", rel,
				"already here and different — the app would write over it"})
		}
	}
	return out
}

// Installed is what a vault records about an app it has taken on.
type Installed struct {
	Name    string `yaml:"name"`
	Source  string `yaml:"source"`
	Version string `yaml:"version,omitempty"`
}

// Install writes the pack into the vault and returns the files it changed.
//
// Not committed here. `docket check --fix` does not commit either, and for the
// same reason: what an installer did should be looked at before it is history.
func Install(root string, c *project.Config, p *Pack) ([]string, error) {
	if conflicts := Check(root, c, p); len(conflicts) > 0 {
		return nil, fmt.Errorf("%s cannot be installed as it is: %s",
			p.Name, conflicts[0].Error())
	}

	var changed []string
	for _, rel := range p.Files {
		from := filepath.Join(p.Dir, filepath.FromSlash(rel))
		to := filepath.Join(root, filepath.FromSlash(rel))

		body, err := os.ReadFile(from)
		if err != nil {
			return changed, err
		}
		if existing, err := os.ReadFile(to); err == nil && string(existing) == string(body) {
			continue // the same file: installing twice says nothing new
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return changed, err
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(from); err == nil && info.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := os.WriteFile(to, body, mode); err != nil {
			return changed, err
		}
		changed = append(changed, rel)
	}

	wrote, err := merge(root, c, p)
	if err != nil {
		return changed, err
	}
	if wrote {
		changed = append(changed, project.FileName)
	}

	// The generated boards name the vault's types, so a pack that brought a
	// type has just made them stale — and `docket check` would say so on the
	// next run. Regenerating is deterministic and only touches the files that
	// say they are generated, so it belongs here rather than in a note telling
	// somebody to go and do it.
	if wrote {
		boards, err := vault.WriteBoards(root, c)
		if err != nil {
			return changed, err
		}
		for _, board := range boards {
			if !slices.Contains(changed, board) {
				changed = append(changed, board)
			}
		}
	}
	sort.Strings(changed)
	return changed, nil
}

// merge adds the pack's vocabulary to docket.yaml and records the app.
func merge(root string, c *project.Config, p *Pack) (bool, error) {
	before := len(c.Types) + len(c.Fields) + len(c.Declared) + len(c.Apps) +
		len(c.Pages) + len(c.Panels)
	known := func(name string, in []string) bool {
		for _, got := range in {
			if got == name {
				return true
			}
		}
		return false
	}

	var typeNames, fieldNames []string
	for _, t := range c.Types {
		typeNames = append(typeNames, t.Name)
	}
	for _, f := range c.Fields {
		fieldNames = append(fieldNames, f.Name)
	}

	for _, t := range p.Vocabulary.Types {
		if !known(t.Name, typeNames) {
			c.Types = append(c.Types, t)
		}
	}
	for _, f := range p.Vocabulary.Fields {
		if !known(f.Name, fieldNames) {
			c.Fields = append(c.Fields, f)
		}
	}
	for _, r := range p.Vocabulary.Relations {
		if !c.IsRelation(r.Name) {
			c.Declared = append(c.Declared, r)
		}
	}
	for _, page := range p.Surfaces.Pages {
		if !hasSurface(c.Pages, page.Name) {
			c.Pages = append(c.Pages, page)
		}
	}
	for _, shown := range p.Surfaces.Panels {
		if !hasSurface(c.Panels, shown.Name) {
			c.Panels = append(c.Panels, shown)
		}
	}

	// Recorded so `docket app list` can say what is installed, and so a later
	// version of the same app can tell what it is replacing.
	replaced := false
	for i, was := range c.Apps {
		if was.Name == p.Name {
			c.Apps[i] = project.App{Name: p.Name, Source: p.Source, Version: p.Version}
			replaced = true
		}
	}
	if !replaced {
		c.Apps = append(c.Apps, project.App{Name: p.Name, Source: p.Source, Version: p.Version})
	}

	if before == len(c.Types)+len(c.Fields)+len(c.Declared)+len(c.Apps)+
		len(c.Pages)+len(c.Panels) && replaced {
		// Nothing new: the same app at the same version, installed twice.
		return false, nil
	}
	return true, c.Save(root)
}

// Named is the pack directory's own name, used when a manifest is being written
// rather than read.
func Named(dir string) string { return path.Base(filepath.ToSlash(dir)) }

// hasSurface reports whether a list already declares that name.
func hasSurface(surfaces []project.Surface, name string) bool {
	for _, s := range surfaces {
		if strings.EqualFold(s.Name, name) {
			return true
		}
	}
	return false
}
