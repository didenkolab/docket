package check

import (
	"fmt"
	"path/filepath"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Rename is one file whose name no longer says what the task is.
type Rename struct {
	From string // relative to the vault root
	To   string
}

// Renames is every file whose name has drifted from its title.
//
// This is the one finding a machine can settle on its own. Every other rule
// reports a disagreement between two things a person meant — a status that is
// not in the vocabulary, a parent that does not exist — and picking a side
// would be guessing. A name that no longer matches its title has a right
// answer: the frontmatter is what the task says about itself, and the name is
// derived from it.
//
// Nothing is renamed here; the caller decides. A rename moves a file somebody
// may have open, and doing it as a side effect of asking a question would be a
// surprise.
func Renames(root string) ([]Rename, error) {
	c, err := project.Load(root)
	if err != nil {
		return nil, err
	}
	entries, err := vault.List(root, c)
	if err != nil {
		return nil, err
	}

	var out []Rename
	for _, e := range entries {
		if e.Task == nil || e.Task.Title == "" || e.Task.Key != e.Key {
			// A key that disagrees with the file name is not a naming drift —
			// it is two claims about which task this is, and only a person
			// knows which is right.
			continue
		}
		want := vault.FileName(e.Task.Key, e.Task.Title)
		if want == filepath.Base(e.Path) {
			continue
		}
		out = append(out, Rename{
			From: e.Path,
			To:   filepath.ToSlash(filepath.Join(filepath.Dir(e.Path), want)),
		})
	}
	return out, nil
}

// Apply performs the renames, stopping at the first that fails.
//
// It goes through vault.Rename, which refuses to write over a file that is
// already there — two tasks given the same title would otherwise leave one of
// them silently gone.
func Apply(root string, renames []Rename) error {
	for _, r := range renames {
		if err := vault.Rename(root, r.From, r.To); err != nil {
			return fmt.Errorf("%s: %w", r.From, err)
		}
	}
	return nil
}
