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
		Labels                                        *[]string
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args.Key == "" {
		return nil, fmt.Errorf("which task?")
	}

	c, err := project.Load(s.Root)
	if err != nil {
		return nil, err
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	rel, err := vault.Find(s.Root, c, args.Key)
	if err != nil {
		return nil, err
	}
	full := filepath.Join(s.Root, filepath.FromSlash(rel))

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
	if args.Labels != nil {
		t.SetList("labels", *args.Labels)
		changed = append(changed, "labels")
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
	paths := []string{rel}
	if projectKey, _, err := project.SplitKey(t.Key); err == nil {
		if wanted := vault.PathFor(projectKey, t.Key, t.Title); wanted != rel {
			touched, err := vault.Retitle(s.Root, rel, wanted)
			if err != nil {
				return nil, err
			}
			paths = append(paths, touched...)
			rel = wanted
		}
	}

	message := args.Key + ": " + strings.Join(changed, ", ")
	if err := s.Repo.Commit(paths, message, s.Author); err != nil {
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

	c, err := project.Load(s.Root)
	if err != nil {
		return nil, err
	}
	entries, err := vault.List(s.Root, c)
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
		raw, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(page)))
		if err == nil && strings.Contains(strings.ToLower(string(raw)), needle) {
			hits = append(hits, hit{Path: page})
		}
	}
	return jsonText(hits)
}

func (s *Server) pages() []string {
	var out []string
	docs := filepath.Join(s.Root, vault.DocsDir)
	_ = filepath.Walk(docs, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		if rel, err := filepath.Rel(s.Root, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
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
	content, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, fmt.Errorf("%s is not in this vault", rel)
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

	full := filepath.Join(s.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return nil, err
	}

	content := fmt.Sprintf("---\ntitle: %s\ntype: page\nupdated: %s\n---\n\n%s\n",
		args.Title, s.Now().UTC().Format("2006-01-02"), strings.TrimRight(args.Body, "\n"))
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return nil, err
	}
	if err := s.Repo.Commit([]string{rel}, "wrote "+strings.TrimSuffix(rel, ".md"), s.Author); err != nil {
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
	findings, err := check.Run(s.Root)
	if err != nil {
		return nil, err
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
