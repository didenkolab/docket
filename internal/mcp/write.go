package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/check"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// fingerprint identifies the exact bytes an agent was given, so a write can be
// refused rather than land on top of a change it never saw. The same value the
// web interface shows on a task page.
func fingerprint(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:8])
}

func (s *Server) updateTask(raw json.RawMessage) (any, error) {
	var args struct {
		Key, Version                                  string
		Title, Status, Priority, Description, Comment *string
		Assignee                                      *string
		// Parent moves the task under another, or out from under one when it
		// is the empty string. Hierarchy, not a relation — see task.Relations.
		Parent *string
		Labels *[]string
		Tags   *[]string
		// Estimate is how big the work is, in the vault's unit. A pointer to
		// nought is a claim that there is no work in it; nil is nobody having
		// said, and leaves whatever is there alone.
		Estimate *float64
		// Sprint is the sprint page to put it in, by title, or the empty string
		// to take it out of every one.
		Sprint *string
		// Relations is keyed by the property name — blocks, blocked_by,
		// relates — with task keys as the values.
		Relations map[string][]string
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Key == "" {
		return nil, fmt.Errorf("which task?")
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	owner, inVault, rel, err := s.Space.Locate(args.Key)
	if err != nil {
		return nil, err
	}
	// What may happen to a task is its own project's business, not the space's.
	projectKey, _, err := project.SplitKey(args.Key)
	if err != nil {
		return nil, err
	}
	c, _, err := s.Space.ConfigOf(projectKey)
	if err != nil {
		return nil, err
	}
	full := owner.Abs(inVault)

	content, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	if args.Version != "" && fingerprint(content) != args.Version {
		return nil, fmt.Errorf("%s changed since you read it — read it again and reapply. "+
			"Nothing was written", args.Key)
	}

	t, err := task.Parse(content)
	if err != nil {
		return nil, err
	}

	var changed []string
	if args.Title != nil && *args.Title != t.Title {
		if strings.TrimSpace(*args.Title) == "" {
			return nil, fmt.Errorf("a task needs a title")
		}
		t.Set("title", *args.Title)
		changed = append(changed, "title")
	}
	if args.Status != nil && *args.Status != t.Status {
		category, known := c.CategoryOf(*args.Status)
		if !known {
			return nil, fmt.Errorf("status %q is not one of %s",
				*args.Status, strings.Join(c.StatusNames(), ", "))
		}
		if !c.CanMove(t.Status, *args.Status) {
			return nil, fmt.Errorf("the workflow does not allow %s → %s; from %s a task can go to %s",
				t.Status, *args.Status, t.Status, strings.Join(statusNames(c.Reachable(t.Status)), ", "))
		}
		changed = append(changed, t.Status+" → "+*args.Status)
		t.SetStatus(*args.Status, category)
	}
	if args.Priority != nil && *args.Priority != t.Priority {
		if !c.HasPriority(*args.Priority) {
			return nil, fmt.Errorf("priority %q is not one of %s",
				*args.Priority, strings.Join(c.Priorities, ", "))
		}
		t.Set("priority", *args.Priority)
		changed = append(changed, "priority")
	}
	if args.Assignee != nil && *args.Assignee != t.Assignee {
		t.Set("assignee", *args.Assignee)
		changed = append(changed, "assignee")
	}
	if args.Estimate != nil {
		if said, err := s.setEstimate(c, t, *args.Estimate, args.Key); err != nil {
			return nil, err
		} else if said != "" {
			changed = append(changed, said)
		}
	}
	if args.Sprint != nil {
		if said, err := s.setSprint(t, *args.Sprint); err != nil {
			return nil, err
		} else if said != "" {
			changed = append(changed, said)
		}
	}
	if args.Parent != nil && *args.Parent != t.Parent {
		note := ""
		if *args.Parent != "" {
			owner, inVault, _, err := s.Space.Locate(*args.Parent)
			if err != nil {
				return nil, fmt.Errorf("parent %s is not in this space", *args.Parent)
			}
			_ = owner
			note = strings.TrimSuffix(path.Base(inVault), ".md")

			// A parent sits above its child when the vault says what its levels
			// are. Refusing here beats writing it and reporting it later.
			if parent := s.taskAt(*args.Parent); parent != nil && !c.CanParent(parent.Type, t.Type) {
				return nil, fmt.Errorf("a %q cannot hold a %q: a parent sits above its child, "+
					"and they are at levels %d and %d",
					parent.Type, t.Type, c.LevelOf(parent.Type), c.LevelOf(t.Type))
			}
		}
		t.SetParent(note)
		changed = append(changed, "parent")
	}
	if args.Labels != nil {
		t.SetLabels(*args.Labels)
		changed = append(changed, "labels")
	}
	if args.Tags != nil {
		t.SetTags(*args.Tags)
		changed = append(changed, "tags")
	}
	for field, keys := range args.Relations {
		if !task.IsRelation(field) {
			return nil, fmt.Errorf("%s is not a relation: it is one of %s",
				field, relationNames())
		}
		notes, err := s.notesFor(keys)
		if err != nil {
			return nil, err
		}
		t.SetRelated(field, notes)
		changed = append(changed, field)
	}
	if args.Description != nil && *args.Description != t.Description() {
		t.SetDescription(*args.Description)
		changed = append(changed, "description")
	}
	if args.Comment != nil && strings.TrimSpace(*args.Comment) != "" {
		t.AppendComment(s.Author.Name, s.Now(), *args.Comment)
		changed = append(changed, "comment")
	}

	if len(changed) == 0 {
		return textResult("Nothing to change on %s.", args.Key)
	}

	t.Touch(s.Now())
	if err := t.Sync(); err != nil {
		return nil, err
	}
	written, err := t.Bytes()
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, written, 0o644); err != nil {
		return nil, err
	}

	// A title lives in the file name, so changing it moves the file, and every
	// link that pointed at the old name moves with it. One commit for all of
	// them, so no point in the history has the vault pointing at nothing.
	paths := []string{inVault}
	if wanted := vault.PathFor(projectKey, t.Key, t.Title); wanted != inVault {
		touched, err := vault.Retitle(owner.Root, inVault, wanted)
		if err != nil {
			return nil, err
		}
		paths = append(paths, touched...)
		rel = owner.PathIn(wanted)
	}

	message := args.Key + ": " + strings.Join(changed, ", ")
	if err := owner.Repo.Commit(paths, message, s.Author); err != nil {
		return nil, err
	}
	return textResult("%s. Now at %s.", message, rel)
}

func (s *Server) search(raw json.RawMessage) (any, error) {
	var args struct{ Query string }
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(args.Query))
	if needle == "" {
		return nil, fmt.Errorf("search for what?")
	}

	entries, err := s.Space.Entries()
	if err != nil {
		return nil, err
	}

	type hit struct {
		Key   string `json:"key,omitempty"`
		Path  string `json:"path"`
		Title string `json:"title,omitempty"`
	}
	hits := []hit{}

	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if strings.Contains(strings.ToLower(e.Task.Title+"\n"+e.Task.Body()), needle) {
			hits = append(hits, hit{Key: e.Key, Path: e.Path, Title: e.Task.Title})
		}
	}
	for _, page := range s.pages() {
		full, err := s.Space.Path(page)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(full)
		if err == nil && strings.Contains(strings.ToLower(string(raw)), needle) {
			hits = append(hits, hit{Path: page})
		}
	}
	return jsonText(hits)
}

// pages is every page in the space, said from the space root — so two
// repositories can each have a docs/ without one hiding the other.
func (s *Server) pages() []string {
	var out []string
	for _, v := range s.Space.Vaults() {
		docs := filepath.Join(v.Root, vault.DocsDir)
		_ = filepath.Walk(docs, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
				return nil
			}
			if rel, err := filepath.Rel(v.Root, p); err == nil {
				out = append(out, v.PathIn(filepath.ToSlash(rel)))
			}
			return nil
		})
	}
	return out
}

func (s *Server) readPage(raw json.RawMessage) (any, error) {
	var args struct{ Path string }
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	rel, err := pagePath(args.Path)
	if err != nil {
		return nil, err
	}
	full, err := s.Space.Path(rel)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("%s is not in this space", rel)
	}
	return textResult("%s", content)
}

func (s *Server) writePage(raw json.RawMessage) (any, error) {
	var args struct{ Path, Title, Body string }
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	rel, err := pagePath(args.Path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Title) == "" {
		return nil, fmt.Errorf("a page needs a title")
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	v, inVault, err := s.Space.Resolve(rel)
	if err != nil {
		return nil, err
	}
	full := v.Abs(inVault)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}

	content := fmt.Sprintf("---\ntitle: %s\ntype: page\nupdated: %s\n---\n\n%s\n",
		args.Title, s.Now().UTC().Format("2006-01-02"), strings.TrimRight(args.Body, "\n"))
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if err := v.Repo.Commit([]string{inVault}, "wrote "+strings.TrimSuffix(inVault, ".md"), s.Author); err != nil {
		return nil, err
	}
	return textResult("Wrote %s. Link to it with [[%s]].", rel, strings.TrimSuffix(rel, ".md"))
}

// pagePath keeps a page inside docs/, where a file is a page rather than a task.
func pagePath(raw string) (string, error) {
	raw = strings.TrimSuffix(strings.Trim(strings.TrimSpace(raw), "/"), ".md")
	if raw == "" {
		return "", fmt.Errorf("a page needs a path")
	}
	cleaned := path.Clean(raw)
	if strings.HasPrefix(cleaned, "..") || cleaned == "." {
		return "", fmt.Errorf("that path leads outside the vault")
	}
	if !strings.HasPrefix(cleaned, vault.DocsDir+"/") {
		return "", fmt.Errorf("pages live under %s/", vault.DocsDir)
	}
	return cleaned + ".md", nil
}

func (s *Server) check() (any, error) {
	var findings []check.Finding
	for _, v := range s.Space.Vaults() {
		found, err := check.Run(v.Root)
		if err != nil {
			return nil, err
		}
		for _, f := range found {
			f.Path = v.PathIn(f.Path)
			findings = append(findings, f)
		}
	}
	if len(findings) == 0 {
		return textResult("No findings.")
	}

	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintln(&b, f)
	}
	fmt.Fprintf(&b, "\n%d finding(s).", len(findings))
	return textResult("%s", b.String())
}

// notesFor turns task keys into the note names a link resolves by, refusing a
// key nothing in the space has.
func (s *Server) notesFor(keys []string) ([]string, error) {
	notes := make([]string, 0, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		_, inVault, _, err := s.Space.Locate(key)
		if err != nil {
			return nil, fmt.Errorf("%s is not in this space", key)
		}
		notes = append(notes, strings.TrimSuffix(path.Base(inVault), ".md"))
	}
	return notes, nil
}

func relationNames() string {
	var names []string
	for _, r := range task.Relations {
		names = append(names, r.Field)
	}
	return strings.Join(names, ", ")
}

// taskAt reads one task, or nil when it cannot be read. Used where a missing
// task is already being reported for another reason.
func (s *Server) taskAt(key string) *task.Task {
	owner, inVault, _, err := s.Space.Locate(key)
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(owner.Abs(inVault))
	if err != nil {
		return nil
	}
	t, err := task.Parse(raw)
	if err != nil {
		return nil
	}
	return t
}

// setEstimate sizes a task, refusing what `docket check` would report.
//
// Refused here rather than written and reported later: an agent that gets a
// clear "4 is not on the scale" fixes it in the same turn, and an agent that
// gets a silent success leaves a vault that fails validation.
func (s *Server) setEstimate(c *project.Config, t *task.Task, size float64, key string) (string, error) {
	if !c.Sizes() {
		return "", fmt.Errorf("this vault does not size work: %s has no estimates block",
			project.FileName)
	}
	if size < 0 {
		return "", fmt.Errorf("work cannot be smaller than nothing")
	}
	if !c.OnScale(size) {
		return "", fmt.Errorf("estimate %s is not on the scale %s",
			project.Amount(size), strings.Join(scaleNames(c.EstimateScale()), ", "))
	}
	// A container's size is what its children add to — see rule 12.
	if s.hasChildren(key) {
		return "", fmt.Errorf("%s has children, so its size is what they add up to; "+
			"size the children instead", key)
	}
	if t.Sized() && t.Size() == size {
		return "", nil
	}
	t.SetEstimate(size)
	return "estimate " + project.Amount(size), nil
}

// setSprint puts a task in a sprint, or takes it out of every one.
func (s *Server) setSprint(t *task.Task, note string) (string, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		if t.Sprint == "" {
			return "", nil
		}
		was := t.Sprint
		t.SetSprint("")
		return "out of " + was, nil
	}
	if strings.EqualFold(note, t.Sprint) {
		return "", nil
	}

	var known []string
	for _, sp := range s.Space.Sprints() {
		if strings.EqualFold(sp.Note, note) {
			t.SetSprint(sp.Note)
			return "into " + sp.Note, nil
		}
		known = append(known, sp.Note)
	}
	if len(known) == 0 {
		return "", fmt.Errorf("this vault has no sprint pages: write one at %s/%s.md with "+
			"type: %s, starts and ends", vault.SprintDir, note, vault.SprintType)
	}
	return "", fmt.Errorf("%q is not a sprint page in this vault; it has %s",
		note, strings.Join(known, ", "))
}

// hasChildren reports whether any task names this one as its parent.
func (s *Server) hasChildren(key string) bool {
	entries, err := s.Space.Entries()
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Task != nil && e.Task.Parent == key {
			return true
		}
	}
	return false
}

func scaleNames(scale []float64) []string {
	out := make([]string, 0, len(scale))
	for _, v := range scale {
		out = append(out, project.Amount(v))
	}
	return out
}
