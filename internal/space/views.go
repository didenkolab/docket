package space

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/base"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Saved views: the boards/*.base files, read as what they are.
//
// The vault already has a query language — Obsidian's Bases — and the files are
// in git beside the tasks they select. A second one on the web would be a
// second answer to the same question and a day when the two disagree. So a
// saved view here is that file, and the web draws what Obsidian draws.

// ViewIn is one .base file and the repository it belongs to.
type ViewIn struct {
	*base.Base
	Vault *Vault
	// Trouble is why the file could not be read, when it could not.
	//
	// Kept rather than dropped: a view somebody wrote and cannot find is worse
	// than a view that says why it will not draw.
	Trouble string
}

// Views is every saved view in the space, ordered by where it is.
func (s *Space) Views() []ViewIn {
	var all []ViewIn
	for _, v := range s.vaults {
		dir := filepath.Join(v.Root, vault.BoardsDir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a vault with no boards folder has no views
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".base") {
				continue
			}
			at := path.Join(v.Prefix, vault.BoardsDir, e.Name())
			note := strings.TrimSuffix(e.Name(), ".base")

			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				all = append(all, ViewIn{
					Base:  &base.Base{Note: note, Path: at},
					Vault: v, Trouble: err.Error(),
				})
				continue
			}
			b, err := base.Parse(raw)
			if err != nil {
				all = append(all, ViewIn{
					Base:  &base.Base{Note: note, Path: at},
					Vault: v, Trouble: err.Error(),
				})
				continue
			}
			b.Note, b.Path = note, at
			all = append(all, ViewIn{Base: b, Vault: v})
		}
	}
	sort.SliceStable(all, func(a, b int) bool { return all[a].Path < all[b].Path })
	return all
}

// ViewAt is the saved view at that path, relative to the space root.
func (s *Space) ViewAt(at string) (ViewIn, bool) {
	for _, v := range s.Views() {
		if v.Path == at {
			return v, true
		}
	}
	return ViewIn{}, false
}
