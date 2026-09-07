package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/task"
)

// Retitle moves a note and repoints every link that pointed at it.
//
// A title lives in the file name, so changing a title renames the file — and
// every `[[ACME-12 Its old title]]` in the vault, in a body or in a `parent`
// property, would otherwise point at nothing. Obsidian repoints them when it
// renames a note. A tool writing to the same vault has to do the same, or the
// two disagree about what a rename is and `docket check` fills with dead links
// after every reworded title.
//
// It returns every path it touched, the new name first, so the caller can put
// them all in one commit. A rename recorded without the links it invalidated is
// a commit that leaves the vault broken at that point in its history.
func Retitle(root, from, to string) ([]string, error) {
	if from == to {
		return nil, nil
	}
	if err := Rename(root, from, to); err != nil {
		return nil, err
	}

	touched := []string{to, from}
	was, now := noteName(from), noteName(to)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if Hidden(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rewritten, changed := task.Retarget(string(content), was, now)
		if !changed {
			return nil
		}
		if err := os.WriteFile(path, []byte(rewritten), 0o644); err != nil {
			return err
		}
		if rel, err := filepath.Rel(root, path); err == nil {
			touched = append(touched, filepath.ToSlash(rel))
		}
		return nil
	})
	return touched, err
}

// Hidden says whether a directory is one no walk over the vault goes into:
// git's own storage, Obsidian's configuration, the trash, and anything else a
// tool keeps beside the content under a leading dot. A dot-directory is by
// convention somebody's working state, not a page — so the graph, the pages
// and the sprints are counted from what a reader would see, and a scratch
// directory left by an editor or an agent does not become forty notes.
func Hidden(name string) bool { return strings.HasPrefix(name, ".") }

// noteName is what a wikilink to a file says: its name without the folder and
// without the extension.
func noteName(path string) string {
	return strings.TrimSuffix(filepath.Base(filepath.FromSlash(path)), ".md")
}
