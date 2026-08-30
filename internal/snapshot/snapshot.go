// Package snapshot reads and writes a cold copy of a source tracker.
//
// The snapshot exists so that only one phase of an import touches the network.
// Everything downstream — proposing mappings, writing the vault, redoing either
// after the mapping turns out wrong — runs against these files. Mapping a large
// instance is an iterative business, and iterating against a live API is slow,
// rate-limited, and different every time you run it.
//
// Nothing is interpreted on the way in. Objects are stored as the API returned
// them, so a decision made later cannot be blocked by a field that seemed
// uninteresting at extract time.
package snapshot

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Layout inside a snapshot directory.
const (
	ManifestFile = "manifest.json"
	IssuesDir    = "issues"
	ChangelogDir = "changelog"
	CommentsDir  = "comments"
	PagesDir     = "pages"
	MetaDir      = "meta"
)

// Manifest describes what a snapshot holds and how far the extraction got.
type Manifest struct {
	Instance string         `json:"instance"`
	TakenAt  string         `json:"taken_at"`
	Projects []string       `json:"projects,omitempty"`
	Spaces   []string       `json:"spaces,omitempty"`
	Counts   map[string]int `json:"counts,omitempty"`

	// Done names the units that finished, so a run interrupted halfway can
	// pick up where it stopped instead of starting over.
	Done []string `json:"done,omitempty"`
}

// Snapshot is a directory holding extracted data.
type Snapshot struct{ Dir string }

// Create prepares a snapshot directory, which may already exist — that is how
// an interrupted extraction resumes.
func Create(dir string) (*Snapshot, error) {
	for _, sub := range []string{IssuesDir, ChangelogDir, CommentsDir, PagesDir, MetaDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Snapshot{Dir: dir}, nil
}

// Open reads an existing snapshot.
func Open(dir string) (*Snapshot, error) {
	if _, err := os.Stat(filepath.Join(dir, ManifestFile)); err != nil {
		return nil, fmt.Errorf("%s is not a snapshot: no %s", dir, ManifestFile)
	}
	return &Snapshot{Dir: dir}, nil
}

// Manifest reads the manifest.
func (s *Snapshot) Manifest() (*Manifest, error) {
	var m Manifest
	if err := s.ReadJSON(ManifestFile, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SaveManifest writes the manifest.
func (s *Snapshot) SaveManifest(m *Manifest) error {
	sort.Strings(m.Done)
	return s.WriteJSON(ManifestFile, m)
}

// MarkDone records that a unit of work finished.
func (s *Snapshot) MarkDone(m *Manifest, unit string) error {
	for _, existing := range m.Done {
		if existing == unit {
			return nil
		}
	}
	m.Done = append(m.Done, unit)
	return s.SaveManifest(m)
}

// IsDone reports whether a unit already finished in an earlier run.
func (m *Manifest) IsDone(unit string) bool {
	for _, existing := range m.Done {
		if existing == unit {
			return true
		}
	}
	return false
}

// WriteJSON writes one indented JSON file.
func (s *Snapshot) WriteJSON(rel string, v any) error {
	path := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// ReadJSON reads one JSON file.
func (s *Snapshot) ReadJSON(rel string, v any) error {
	raw, err := os.ReadFile(filepath.Join(s.Dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, v)
}

// Truncate empties a JSONL file, so that re-extracting a unit replaces it
// rather than appending a second copy of everything.
func (s *Snapshot) Truncate(rel string) error {
	path := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, nil, 0o644)
}

// Append adds one object to a JSONL file.
func (s *Snapshot) Append(rel string, v any) error {
	path := filepath.Join(s.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = f.Write(append(raw, '\n'))
	return err
}

// Each calls fn for every object in a JSONL file. A missing file is not an
// error: a project with no comments simply has none.
func (s *Snapshot) Each(rel string, fn func(json.RawMessage) error) error {
	f, err := os.Open(filepath.Join(s.Dir, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		if err := fn(json.RawMessage(text)); err != nil {
			return fmt.Errorf("%s line %d: %w", rel, line, err)
		}
	}
	return scanner.Err()
}

// Units lists the JSONL files in a directory, by their base name.
func (s *Snapshot) Units(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, dir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			names = append(names, strings.TrimSuffix(e.Name(), ".jsonl"))
		}
	}
	sort.Strings(names)
	return names, nil
}

// Path is where a unit's file lives inside the snapshot.
func Path(dir, unit string) string { return dir + "/" + unit + ".jsonl" }
