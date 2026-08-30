// Package space presents one or more vaults as one.
//
// A repository is a project, the way a Space is a project in Jira — so a board
// showing several projects is a board reading several repositories. This is
// what lets everything above it go on asking "what tasks are there" and "write
// this one" without knowing how many repositories the answer came from.
//
// A plain vault is a space of exactly one, so nothing has a single-repository
// case and a several-repository case. There is one case.
package space

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
	"github.com/vadymdidenkolab/docket/internal/workspace"
)

// Space is what a command was pointed at: a vault, or a workspace of them.
type Space struct {
	// Root is the directory the interface's paths are relative to.
	Root string
	// Workspace says whether Root is a workspace rather than a vault itself.
	// It changes nothing about how a space is used and everything about what
	// the interface should say when a path looks odd.
	Workspace bool
	// Missing lists projects the manifest names that nobody has cloned. Not an
	// error: a workspace is a list of repositories somebody may not have all
	// of, and refusing to open the ones that are there would help nobody.
	Missing []string

	vaults []*Vault
}

// Vault is one repository in the space.
type Vault struct {
	// Prefix is the path from the space root to this vault: "" for a vault
	// opened directly, "acme" for one inside a workspace. It is what makes a
	// path in the interface unambiguous when two repositories both have docs/.
	Prefix string
	Root   string
	Repo   *gitvcs.Repo
}

// Open finds what dir is and reads it.
//
// A workspace root first, because a workspace contains vaults and looking for a
// vault first would find whichever one happens to sit beside the manifest.
func Open(dir string) (*Space, error) {
	if root, err := workspace.FindRoot(dir); err == nil {
		return openWorkspace(root)
	}

	root, err := project.FindRoot(dir)
	if err != nil {
		return nil, err
	}
	// A repository that is not under git can still be read, and `docket check`
	// only reads. Writing is what needs git — see RequireGit.
	repo, _ := gitvcs.Open(root)
	return &Space{Root: root, vaults: []*Vault{{Root: root, Repo: repo}}}, nil
}

func openWorkspace(root string) (*Space, error) {
	manifest, err := workspace.Load(root)
	if err != nil {
		return nil, err
	}

	s := &Space{Root: root, Workspace: true}
	var missing []string

	for _, p := range manifest.Projects {
		at := filepath.Join(root, filepath.FromSlash(p.Path))
		if _, err := project.Load(at); err != nil {
			// A project nobody has cloned yet is not an error in the workspace,
			// it is a project nobody has cloned yet. Say which, once, rather
			// than refusing to open anything.
			missing = append(missing, p.Key)
			continue
		}
		repo, _ := gitvcs.Open(at)
		s.vaults = append(s.vaults, &Vault{Prefix: p.Path, Root: at, Repo: repo})
	}

	if len(s.vaults) == 0 {
		return nil, fmt.Errorf("%s holds no cloned projects: run docket workspace sync", root)
	}
	if len(missing) > 0 {
		s.Missing = missing
	}
	return s, nil
}

// RequireGit refuses a space any part of which is not under git.
//
// Every change is a commit, so a vault outside git would lose its history
// silently. Reading does not need it, which is why opening a space does not
// demand it and anything that writes does.
func (s *Space) RequireGit() error {
	for _, v := range s.vaults {
		if v.Repo == nil {
			where := v.Root
			if v.Prefix != "" {
				where = v.Prefix
			}
			return fmt.Errorf("%s is not a git repository: the history of every change lives "+
				"in git, so serving a vault that is not under git would quietly lose it", where)
		}
	}
	return nil
}

// Vaults is every repository in the space, in manifest order.
func (s *Space) Vaults() []*Vault { return s.vaults }

// Single is the only vault, for the things that genuinely have one — a page
// written where the space itself was opened, an attachment with nowhere else to
// go. It is nil for a workspace, and a caller that gets nil has to ask which
// project it meant.
func (s *Space) Single() *Vault {
	if len(s.vaults) == 1 && !s.Workspace {
		return s.vaults[0]
	}
	return nil
}

// Config is the vocabulary of the whole space.
//
// One vault answers with its own configuration and nothing is merged. Several
// answer with the union: every status any of them uses, in the order they first
// appear, then the same for types and priorities.
//
// The union rather than a shared file, because a repository is a project and a
// project is authoritative about itself. A workspace holding a second copy of
// the vocabulary would be a second source of truth, and the first thing to go
// out of step with the repositories it describes.
//
// A move is still checked against the card's own project — see ConfigOf. A
// column a project does not have simply never holds its cards.
func (s *Space) Config() (*project.Config, error) {
	if len(s.vaults) == 1 {
		return project.Load(s.vaults[0].Root)
	}

	merged := &project.Config{Name: filepath.Base(s.Root)}
	seenStatus := map[string]bool{}
	seenText := map[string]bool{}

	for _, v := range s.vaults {
		c, err := project.Load(v.Root)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", v.Prefix, err)
		}
		merged.Projects = append(merged.Projects, c.Projects...)

		for _, st := range c.Statuses {
			if !seenStatus[st.Name] {
				seenStatus[st.Name] = true
				merged.Statuses = append(merged.Statuses, st)
			}
		}
		merged.Types = union(merged.Types, c.Types, seenText, "type:")
		merged.Priorities = union(merged.Priorities, c.Priorities, seenText, "priority:")
	}
	return merged, nil
}

func union(into, from []string, seen map[string]bool, kind string) []string {
	for _, value := range from {
		if key := kind + value; !seen[key] {
			seen[key] = true
			into = append(into, value)
		}
	}
	return into
}

// ConfigOf is the configuration of the repository that owns a project — the one
// whose statuses, priorities and workflow decide what may happen to its tasks.
func (s *Space) ConfigOf(projectKey string) (*project.Config, *Vault, error) {
	for _, v := range s.vaults {
		c, err := project.Load(v.Root)
		if err != nil {
			continue
		}
		if c.HasProject(projectKey) {
			return c, v, nil
		}
	}
	return nil, nil, fmt.Errorf("no repository in this space holds project %s", projectKey)
}

// ErrNoSuchTask is what a lookup returns when no repository has the key.
var ErrNoSuchTask = errors.New("no such task")

// Entries is every task in the space, with paths relative to the space root so
// that two repositories can both have a docs/ and an ACME/ without colliding.
func (s *Space) Entries() ([]vault.Entry, error) {
	var all []vault.Entry
	for _, v := range s.vaults {
		c, err := project.Load(v.Root)
		if err != nil {
			return nil, err
		}
		entries, err := vault.List(v.Root, c)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			e.Path = v.pathIn(e.Path)
			all = append(all, e)
		}
	}
	return all, nil
}

// Locate finds a task: the vault that holds it, its path inside that vault, and
// its path in the space.
func (s *Space) Locate(key string) (v *Vault, inVault, inSpace string, err error) {
	for _, candidate := range s.vaults {
		c, err := project.Load(candidate.Root)
		if err != nil {
			continue
		}
		rel, err := vault.Find(candidate.Root, c, key)
		if err != nil {
			continue
		}
		return candidate, rel, candidate.pathIn(rel), nil
	}
	return nil, "", "", fmt.Errorf("%s: %w", key, ErrNoSuchTask)
}

// Resolve turns a path in the space into the vault that owns it and the path
// inside that vault. It is how a page or an attachment is found when several
// repositories each have their own docs/.
func (s *Space) Resolve(inSpace string) (*Vault, string, error) {
	inSpace = strings.TrimPrefix(filepath.ToSlash(filepath.Clean("/"+inSpace)), "/")

	best := (*Vault)(nil)
	for _, v := range s.vaults {
		switch {
		case v.Prefix == "":
			best = v
		case inSpace == v.Prefix:
			return v, "", nil
		case strings.HasPrefix(inSpace, v.Prefix+"/"):
			return v, strings.TrimPrefix(inSpace, v.Prefix+"/"), nil
		}
	}
	if best != nil {
		return best, inSpace, nil
	}
	return nil, "", fmt.Errorf("%s is not in any repository of this space", inSpace)
}

// Path is the absolute path of something in the space.
func (s *Space) Path(inSpace string) (string, error) {
	v, rel, err := s.Resolve(inSpace)
	if err != nil {
		return "", err
	}
	return filepath.Join(v.Root, filepath.FromSlash(rel)), nil
}

// PathIn is a path inside this vault, said from the space root.
func (v *Vault) PathIn(rel string) string { return v.pathIn(rel) }

// pathIn is a path inside this vault, said from the space root.
func (v *Vault) pathIn(rel string) string {
	if v.Prefix == "" {
		return rel
	}
	return v.Prefix + "/" + rel
}

// Abs is the absolute path of something in this vault.
func (v *Vault) Abs(rel string) string {
	return filepath.Join(v.Root, filepath.FromSlash(rel))
}

// Exists says whether a path in this vault is there.
func (v *Vault) Exists(rel string) bool {
	_, err := os.Stat(v.Abs(rel))
	return err == nil
}
