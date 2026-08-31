package vault

import (
	"path"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
)

// Reading a vault at a point in history rather than from the working tree.
//
// A branch is a proposal about the plan — a release re-scoped, an epic split, a
// quarter dropped — and the only way to judge one is to see the board it would
// produce. That has to be possible without checking it out: somebody is
// working in the tree, and looking at a proposal must not disturb them.
//
// So these read the same vault out of the object database. `git show` and
// `git ls-tree` against a ref, parsed by the same code that parses a file, so a
// board over a branch is the same board.

// ConfigAt reads docket.yaml as it stands at a ref.
//
// The vocabulary can differ between branches — a proposal that adds a status is
// a proposal about the workflow — so a board over a branch has to read that
// branch's configuration, not the one on disk.
func ConfigAt(repo *gitvcs.Repo, ref string) (*project.Config, error) {
	raw, err := repo.At(ref, project.FileName)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, project.ErrNotAVault
	}
	return project.Parse(raw)
}

// ListAt is every task in the vault as it stands at a ref.
//
// The entries look exactly like the ones read from disk, so everything above
// this cannot tell the difference — which is the point. A board, a search and a
// task page work over a branch because they never learn they are.
func ListAt(repo *gitvcs.Repo, ref string, c *project.Config) ([]Entry, error) {
	var entries []Entry

	for _, key := range c.ProjectKeys() {
		paths, err := repo.Tree(ref, key)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			name := path.Base(p)
			if !strings.HasSuffix(name, ".md") {
				continue
			}
			taskKey := keyOfFile(name)
			if taskKey == "" {
				continue
			}
			_, number, err := project.SplitKey(taskKey)
			if err != nil {
				continue
			}

			entry := Entry{Key: taskKey, Project: key, Number: number, Path: p}
			raw, err := repo.At(ref, p)
			switch {
			case err != nil:
				entry.Err = err
			case len(raw) == 0:
				continue
			default:
				entry.Raw = raw
				if entry.Task, err = task.Parse(raw); err != nil {
					entry.Err = err
				}
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
