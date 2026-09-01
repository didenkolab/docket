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
	// Actions are the buttons it puts on its own pages. The one kind of
	// surface that does something rather than drawing, so an app declaring one
	// is unmistakably an app that brings code.
	Actions []project.Action `yaml:"actions,omitempty"`
	// Inbox is what it accepts from outside — an address a CI job posts to.
	Inbox []project.Inbound `yaml:"inbox,omitempty"`
}

// BringsPrograms reports whether installing this would put a program in the
// repository — the fact that decides whether anybody has to think before
// installing it.
func (p *Pack) BringsPrograms() bool {
	if len(p.Surfaces.Actions) > 0 || len(p.Surfaces.Inbox) > 0 {
		return true
	}
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

	// What this app itself brought last time is not a conflict: a new version
	// changing its own type is what an upgrade is.
	var mine project.Brought
	installedBefore := false
	for _, installed := range c.Apps {
		if strings.EqualFold(installed.Name, p.Name) {
			mine, installedBefore = installed.Brought, true
		}
	}
	// An app installed before the vault recorded what each one brought has an
	// empty list, and every one of its own names would read as somebody else's.
	// The vault does say the app is installed, and a name this same pack
	// declares is its own far more often than it is a coincidence.
	blind := installedBefore && mine.Empty()

	add := func(c Conflict) {
		if mine.Owns(c.What, c.Name) || blind {
			return
		}
		out = append(out, c)
	}

	known := map[string]project.Type{}
	for _, t := range c.Types {
		known[t.Name] = t
	}
	for _, t := range p.Vocabulary.Types {
		if existing, ok := known[t.Name]; ok && existing.Level != t.Level {
			add(Conflict{"type", t.Name, fmt.Sprintf(
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
			add(Conflict{"field", f.Name, fmt.Sprintf(
				"this vault already has it as %s and the app wants %s",
				existing.Kind, f.Kind)})
		}
		if project.OwnedProperties[f.Name] {
			add(Conflict{"field", f.Name, "that is a property the format owns"})
		}
		if c.IsRelation(f.Name) && !ownsRelation(p, f.Name) {
			add(Conflict{"field", f.Name,
				"that is already a relation here, and one property cannot be both"})
		}
	}

	for _, r := range p.Vocabulary.Relations {
		if existing, ok := c.RelationOf(r.Name); ok && existing.Inverse != r.Inverse {
			add(Conflict{"relation", r.Name, fmt.Sprintf(
				"this vault already has it with inverse %q and the app wants %q",
				existing.Inverse, r.Inverse)})
		}
		if _, ok := fields[r.Name]; ok {
			add(Conflict{"relation", r.Name,
				"that is already a field here, and one property cannot be both"})
		}
	}

	for _, page := range append(append([]project.Surface{}, p.Surfaces.Pages...), p.Surfaces.Panels...) {
		for _, existing := range append(append([]project.Surface{}, c.Pages...), c.Panels...) {
			if strings.EqualFold(existing.Name, page.Name) && existing.Run != page.Run {
				add(Conflict{"page", page.Name, fmt.Sprintf(
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
			// A file this app wrote last time is its own to replace.
			if blind || mine.Owns("file", rel) || carriedBy(mine, rel) {
				continue
			}
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
	var mine project.Brought
	for _, installed := range c.Apps {
		if strings.EqualFold(installed.Name, p.Name) {
			mine = installed.Brought
			if mine.Empty() {
				// Installed before the vault recorded ownership: everything
				// this pack declares is treated as its own, which is the same
				// reading Check made when it let the install through.
				mine = declaredBy(p)
			}
		}
	}

	// What the file says now, so that "nothing changed" is decided by comparing
	// the configuration rather than by counting its entries: an upgrade that
	// replaces a type in place leaves every count identical and every meaning
	// different.
	before, err := yaml.Marshal(c)
	if err != nil {
		return false, err
	}
	// Replaced where it is this app's own, added where it is new. An upgrade
	// that could only add would leave the old definition in place and the new
	// one nowhere — which is how a type ends up at a level its own app no
	// longer believes in.
	for _, t := range p.Vocabulary.Types {
		if at := indexOfType(c.Types, t.Name); at >= 0 {
			if mine.Owns("type", t.Name) {
				c.Types[at] = t
			}
			continue
		}
		c.Types = append(c.Types, t)
	}
	for _, f := range p.Vocabulary.Fields {
		if at := indexOfField(c.Fields, f.Name); at >= 0 {
			if mine.Owns("field", f.Name) {
				c.Fields[at] = f
			}
			continue
		}
		c.Fields = append(c.Fields, f)
	}
	for _, r := range p.Vocabulary.Relations {
		if at := indexOfRelation(c.Declared, r.Name); at >= 0 {
			if mine.Owns("relation", r.Name) {
				c.Declared[at] = r
			}
			continue
		}
		if !c.IsRelation(r.Name) {
			c.Declared = append(c.Declared, r)
		}
	}
	c.Pages = mergeSurfaces(c.Pages, p.Surfaces.Pages, mine)
	c.Panels = mergeSurfaces(c.Panels, p.Surfaces.Panels, mine)

	for _, action := range p.Surfaces.Actions {
		if at := indexOfAction(c.Actions, action.Name); at >= 0 {
			if mine.Owns("action", action.Name) {
				c.Actions[at] = action
			}
			continue
		}
		c.Actions = append(c.Actions, action)
	}
	for _, in := range p.Surfaces.Inbox {
		if at := indexOfInbound(c.Inbox, in.Name); at >= 0 {
			if mine.Owns("action", in.Name) {
				c.Inbox[at] = in
			}
			continue
		}
		c.Inbox = append(c.Inbox, in)
	}

	// Recorded so `docket app list` can say what is installed, and so a later
	// version of the same app can tell what it is replacing.
	recorded := false
	brought := project.Brought{}
	for _, t := range p.Vocabulary.Types {
		brought.Types = append(brought.Types, t.Name)
	}
	for _, f := range p.Vocabulary.Fields {
		brought.Fields = append(brought.Fields, f.Name)
	}
	for _, r := range p.Vocabulary.Relations {
		brought.Relations = append(brought.Relations, r.Name)
		if r.Inverse != "" {
			brought.Relations = append(brought.Relations, r.Inverse)
		}
	}
	for _, page := range p.Surfaces.Pages {
		brought.Pages = append(brought.Pages, page.Name)
	}
	for _, shown := range p.Surfaces.Panels {
		brought.Panels = append(brought.Panels, shown.Name)
	}
	for _, action := range p.Surfaces.Actions {
		brought.Actions = append(brought.Actions, action.Name)
	}
	for _, in := range p.Surfaces.Inbox {
		brought.Actions = append(brought.Actions, in.Name)
	}

	for i, was := range c.Apps {
		if was.Name == p.Name {
			c.Apps[i] = project.App{Name: p.Name, Source: p.Source,
				Version: p.Version, Brought: brought}
			recorded = true
		}
	}
	if !recorded {
		c.Apps = append(c.Apps, project.App{Name: p.Name, Source: p.Source,
			Version: p.Version, Brought: brought})
	}

	after, err := yaml.Marshal(c)
	if err != nil {
		return false, err
	}
	if string(before) == string(after) {
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

// ownsRelation reports whether this same pack declares that name as a relation,
// which is how a field and a relation of the same name inside one app is caught
// rather than mistaken for a clash with the vault.
func ownsRelation(p *Pack, name string) bool {
	for _, r := range p.Vocabulary.Relations {
		if strings.EqualFold(r.Name, name) || strings.EqualFold(r.Inverse, name) {
			return true
		}
	}
	return false
}

// carriedBy reports whether a path is one an app brings, which makes replacing
// it an upgrade rather than writing over somebody's edit.
//
// Files are matched by folder rather than recorded one by one: an app's second
// version renames its own scripts, and a list of names from the first version
// would call the new ones somebody else's.
func carriedBy(mine project.Brought, path string) bool {
	if len(mine.Pages) == 0 && len(mine.Panels) == 0 && len(mine.Types) == 0 &&
		len(mine.Fields) == 0 && len(mine.Relations) == 0 {
		return false // nothing recorded: an app installed before this was kept
	}
	return strings.HasPrefix(path, "hooks/") || strings.HasPrefix(path, "templates/") ||
		strings.HasPrefix(path, "boards/")
}

func indexOfType(types []project.Type, name string) int {
	for i, t := range types {
		if strings.EqualFold(t.Name, name) {
			return i
		}
	}
	return -1
}

func indexOfField(fields []project.Field, name string) int {
	for i, f := range fields {
		if strings.EqualFold(f.Name, name) {
			return i
		}
	}
	return -1
}

func indexOfRelation(relations []project.Relation, name string) int {
	for i, r := range relations {
		if strings.EqualFold(r.Name, name) {
			return i
		}
	}
	return -1
}

// mergeSurfaces replaces what this app drew before and adds what is new.
func mergeSurfaces(have, wanted []project.Surface, mine project.Brought) []project.Surface {
	for _, surface := range wanted {
		if at := indexOfSurface(have, surface.Name); at >= 0 {
			if mine.Owns("page", surface.Name) {
				have[at] = surface
			}
			continue
		}
		have = append(have, surface)
	}
	return have
}

func indexOfSurface(surfaces []project.Surface, name string) int {
	for i, s := range surfaces {
		if strings.EqualFold(s.Name, name) {
			return i
		}
	}
	return -1
}

// declaredBy is everything a pack declares, as ownership.
func declaredBy(p *Pack) project.Brought {
	var out project.Brought
	for _, t := range p.Vocabulary.Types {
		out.Types = append(out.Types, t.Name)
	}
	for _, f := range p.Vocabulary.Fields {
		out.Fields = append(out.Fields, f.Name)
	}
	for _, r := range p.Vocabulary.Relations {
		out.Relations = append(out.Relations, r.Name, r.Inverse)
	}
	for _, page := range p.Surfaces.Pages {
		out.Pages = append(out.Pages, page.Name)
	}
	for _, shown := range p.Surfaces.Panels {
		out.Panels = append(out.Panels, shown.Name)
	}
	for _, action := range p.Surfaces.Actions {
		out.Actions = append(out.Actions, action.Name)
	}
	for _, in := range p.Surfaces.Inbox {
		out.Actions = append(out.Actions, in.Name)
	}
	return out
}

func indexOfAction(actions []project.Action, name string) int {
	for i, a := range actions {
		if strings.EqualFold(a.Name, name) {
			return i
		}
	}
	return -1
}

func indexOfInbound(inbox []project.Inbound, name string) int {
	for i, in := range inbox {
		if strings.EqualFold(in.Name, name) {
			return i
		}
	}
	return -1
}
