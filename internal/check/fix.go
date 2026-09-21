package check

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Boards rewrites every generated board that has drifted from docket.yaml, and
// reports which ones it rewrote.
//
// The third finding with a right answer: a board is generated from the
// configuration, so a stale one is not a disagreement between two things a
// person meant — it is the tool's own output, out of date. A board that has had
// vault.Marker removed is left alone.
func Boards(root string) ([]string, error) {
	c, err := project.Load(root)
	if err != nil {
		return nil, err
	}

	want := generatedFor(root, c)

	var written []string
	for _, f := range checkGeneratedBoards(root, c) {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.WriteFile(full, []byte(want[f.Path]), 0o644); err != nil {
			return written, err
		}
		written = append(written, f.Path)
	}
	return written, nil
}

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

// Relink rewrites the relationships that are still strings.
//
// The second finding a machine can settle on its own, for the same reason as a
// name that drifted from its title: there is a right answer. A parent named
// `ACME-4` means the task whose key is ACME-4, and the note name for that task
// is a lookup, not a guess. A label named `auth` means the note `auth`, whether
// or not that note exists yet — an unresolved link is still an edge in the
// graph, and writing the page later is what turns it into a page.
//
// It returns the paths it rewrote, so a caller can commit them.
func Relink(root string) ([]string, error) {
	c, err := project.Load(root)
	if err != nil {
		return nil, err
	}
	entries, err := vault.List(root, c)
	if err != nil {
		return nil, err
	}

	notes := map[string]string{}
	for _, e := range entries {
		if e.Task != nil {
			notes[e.Key] = e.Note()
		}
	}

	var written []string
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		t := e.Task

		changed := false
		if raw := t.RawParent(); raw != "" {
			if note, ok := rename(raw, notes); ok {
				t.SetParent(note)
				changed = true
			}
		}
		// A link whose note name is out of date is rewritten here too, which is
		// what makes a rename safe: Renames moves the file, and this puts every
		// link that named the old title back on the note. Doing only the first
		// left a vault that passed every rule and drew no edges in Obsidian
		// (DKT-61).
		for _, r := range c.Relations() {
			raws := t.RawRelated(r.Name)
			if len(raws) == 0 {
				continue
			}
			names := make([]string, 0, len(raws))
			touched := false
			for _, raw := range raws {
				note, ok := rename(raw, notes)
				if !ok {
					note = task.NoteOf(raw)
				}
				touched = touched || ok
				names = append(names, note)
			}
			if touched {
				t.SetRelated(r.Name, names)
				changed = true
			}
		}
		if needsLinking(t.RawLabels()) {
			t.SetLabels(t.Labels)
			changed = true
		}
		if !changed {
			continue
		}

		if err := t.Sync(); err != nil {
			return written, err
		}
		content, err := t.Bytes()
		if err != nil {
			return written, err
		}
		full := filepath.Join(root, filepath.FromSlash(e.Path))
		if err := os.WriteFile(full, content, 0o644); err != nil {
			return written, err
		}
		written = append(written, e.Path)
	}
	return written, nil
}

// rename is the note name a value should carry, and whether writing it would
// change anything.
//
// Two findings with one right answer, so one function: a value that is a bare
// key becomes a link, and a link naming a title its task no longer has gets the
// name the task has now. Both are lookups rather than guesses, because the key
// says which task is meant.
//
// A key nothing in the vault has is left as it is. Rule 5 already reports it,
// and keeping the intent visible is better than dropping it.
func rename(raw string, notes map[string]string) (string, bool) {
	note := task.NoteOf(raw)
	want, ok := notes[task.KeyOf(note)]
	switch {
	case ok && want != note:
		return want, true
	case !task.IsLink(raw):
		if ok {
			return want, true
		}
		return note, true
	}
	return note, false
}

func needsLinking(raw []string) bool {
	for _, value := range raw {
		if !task.IsLink(value) {
			return true
		}
	}
	return false
}

// Resolvable is every name a wikilink in this vault can point at, for a caller
// assembling the names of a whole workspace. See RunIn.
func Resolvable(root string) (map[string]bool, error) { return resolvable(root) }
