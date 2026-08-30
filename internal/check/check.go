// Package check validates a vault against the eight rules in the format
// specification.
//
// It is what keeps the format honest once agents write most of the files, so it
// reports every finding rather than stopping at the first, and each one carries
// a file and a line an editor can jump to.
package check

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Rules, numbered as in the specification.
const (
	RuleFrontmatter = 1 // frontmatter exists and key equals the file name
	RuleUniqueKeys  = 2 // no key twice
	RuleStatus      = 3 // status is known and its category agrees
	RuleVocabulary  = 4 // type and priority are known
	RuleParent      = 5 // parent exists and the graph has no cycles
	RuleFlat        = 6 // no nested frontmatter values
	RuleTimestamps  = 7 // timestamps parse and are in order
	RuleLinks       = 8 // every wikilink resolves
)

// Finding is one problem, located.
type Finding struct {
	Path    string // relative to the vault root
	Line    int    // 0 when the problem has no single line
	Rule    int
	Message string
}

func (f Finding) String() string {
	where := f.Path
	if f.Line > 0 {
		where = fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	return fmt.Sprintf("%s: [rule %d] %s", where, f.Rule, f.Message)
}

// directories that hold no vault content
var skipDirs = map[string]bool{
	".git":      true,
	".obsidian": true,
	".trash":    true,
}

// Run validates the vault rooted at root.
//
// The returned error means the vault could not be read at all. Problems inside
// a readable vault come back as findings, because a validator that stops at the
// first broken file makes fixing a batch of them a game of whack-a-mole.
func Run(root string) ([]Finding, error) {
	p, err := project.Load(root)
	if err != nil {
		return nil, err
	}

	entries, err := vault.List(root)
	if err != nil {
		return nil, err
	}

	names, err := resolvable(root)
	if err != nil {
		return nil, err
	}

	var findings []Finding
	add := func(f Finding) { findings = append(findings, f) }

	seen := map[string]string{} // key -> path that claimed it
	parents := map[string]string{}
	exists := map[string]bool{}

	for _, e := range entries {
		exists[e.Key] = true
	}

	for _, e := range entries {
		if e.Err != nil {
			add(Finding{e.Path, 0, RuleFrontmatter, e.Err.Error()})
			continue
		}
		t := e.Task

		if t.Key != e.Key {
			add(Finding{e.Path, t.PropertyLine("key"), RuleFrontmatter,
				fmt.Sprintf("key is %q but the file is named %q — the path is a function of the key",
					t.Key, e.Key+".md")})
		}
		if where, taken := seen[t.Key]; taken {
			add(Finding{e.Path, t.PropertyLine("key"), RuleUniqueKeys,
				fmt.Sprintf("key %q is already used by %s", t.Key, where)})
		} else {
			seen[t.Key] = e.Path
		}

		checkStatus(add, e, p)
		checkVocabulary(add, e, p)

		if t.Parent != "" {
			parents[e.Key] = t.Parent
			if !exists[t.Parent] {
				add(Finding{e.Path, t.PropertyLine("parent"), RuleParent,
					fmt.Sprintf("parent %q does not exist", t.Parent)})
			}
		}

		for _, name := range t.NestedProperties() {
			add(Finding{e.Path, t.PropertyLine(name), RuleFlat,
				fmt.Sprintf("property %q holds a nested value: Obsidian cannot edit it and "+
					"Bases cannot filter on it", name)})
		}

		checkTimestamps(add, e)
		checkLinks(add, e, names)
	}

	for _, key := range cycles(parents) {
		e := entryFor(entries, key)
		add(Finding{e.Path, 0, RuleParent,
			fmt.Sprintf("%s is part of a parent cycle", key)})
	}

	findings = append(findings, checkPageLinks(root, names)...)

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func checkStatus(add func(Finding), e vault.Entry, p *project.Project) {
	t := e.Task
	category, known := p.CategoryOf(t.Status)
	switch {
	case t.Status == "":
		add(Finding{e.Path, t.PropertyLine("status"), RuleStatus, "no status"})
	case !known:
		add(Finding{e.Path, t.PropertyLine("status"), RuleStatus,
			fmt.Sprintf("status %q is not one of %s", t.Status, strings.Join(p.StatusNames(), ", "))})
	case t.StatusCategory != category:
		add(Finding{e.Path, t.PropertyLine("status_category"), RuleStatus,
			fmt.Sprintf("status %q is in category %q, but the task says %q — they move together",
				t.Status, category, t.StatusCategory)})
	}
}

func checkVocabulary(add func(Finding), e vault.Entry, p *project.Project) {
	t := e.Task
	if !p.HasType(t.Type) {
		add(Finding{e.Path, t.PropertyLine("type"), RuleVocabulary,
			fmt.Sprintf("type %q is not one of %s", t.Type, strings.Join(p.Types, ", "))})
	}
	if !p.HasPriority(t.Priority) {
		add(Finding{e.Path, t.PropertyLine("priority"), RuleVocabulary,
			fmt.Sprintf("priority %q is not one of %s", t.Priority, strings.Join(p.Priorities, ", "))})
	}
}

func checkTimestamps(add func(Finding), e vault.Entry) {
	t := e.Task

	created, errCreated := task.ParseTime(t.Created)
	if errCreated != nil {
		add(Finding{e.Path, t.PropertyLine("created"), RuleTimestamps,
			fmt.Sprintf("created %q is not an RFC 3339 timestamp", t.Created)})
	}
	updated, errUpdated := task.ParseTime(t.Updated)
	if errUpdated != nil {
		add(Finding{e.Path, t.PropertyLine("updated"), RuleTimestamps,
			fmt.Sprintf("updated %q is not an RFC 3339 timestamp", t.Updated)})
	}
	if errCreated == nil && errUpdated == nil && updated.Before(created) {
		add(Finding{e.Path, t.PropertyLine("updated"), RuleTimestamps,
			"updated is earlier than created"})
	}
}

func checkLinks(add func(Finding), e vault.Entry, names map[string]bool) {
	for _, target := range e.Task.Links() {
		if !names[strings.ToLower(target)] {
			add(Finding{e.Path, e.Task.LineOf(target), RuleLinks,
				fmt.Sprintf("[[%s]] resolves to nothing in this vault", target)})
		}
	}
}

// checkPageLinks applies rule 8 to the knowledge base as well: a broken link in
// a page is as dead as one in a task.
func checkPageLinks(root string, names map[string]bool) []Finding {
	var findings []Finding

	docs := filepath.Join(root, vault.DocsDir)
	_ = filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)

		lines := strings.Split(string(raw), "\n")
		for _, target := range task.Links(string(raw)) {
			if names[strings.ToLower(target)] {
				continue
			}
			findings = append(findings, Finding{rel, lineOf(lines, target), RuleLinks,
				fmt.Sprintf("[[%s]] resolves to nothing in this vault", target)})
		}
		return nil
	})
	return findings
}

func lineOf(lines []string, target string) int {
	for i, line := range lines {
		if strings.Contains(line, "[["+target) {
			return i + 1
		}
	}
	return 0
}

// resolvable collects everything a wikilink can point at: file names without
// their extension, full vault-relative paths with and without it, and the
// aliases declared in frontmatter. Matching is case-insensitive, the way
// Obsidian behaves on the file systems people actually use.
func resolvable(root string) (map[string]bool, error) {
	names := map[string]bool{}
	addName := func(s string) { names[strings.ToLower(s)] = true }

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		addName(rel)
		addName(strings.TrimSuffix(rel, filepath.Ext(rel)))
		addName(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))

		if strings.HasSuffix(path, ".md") {
			for _, alias := range aliasesOf(path) {
				addName(alias)
			}
		}
		return nil
	})
	return names, err
}

func aliasesOf(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	t, err := task.Parse(raw)
	if err != nil {
		return nil // a page without frontmatter declares no aliases
	}
	return t.Aliases
}

// cycles returns one key from every cycle in the parent graph — the smallest,
// so the same cycle is always reported at the same place.
//
// Reporting a single representative rather than every key on the cycle keeps
// one mistake from producing a wall of findings, and a task that merely has a
// cycle somewhere above it is not itself broken.
func cycles(parents map[string]string) []string {
	keys := make([]string, 0, len(parents))
	for key := range parents {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	reported := map[string]bool{}
	var found []string

	for _, start := range keys {
		position := map[string]int{}
		var chain []string

		for key := start; ; {
			if at, revisited := position[key]; revisited {
				representative := smallest(chain[at:])
				if !reported[representative] {
					reported[representative] = true
					found = append(found, representative)
				}
				break
			}
			parent, hasParent := parents[key]
			if !hasParent {
				break
			}
			position[key] = len(chain)
			chain = append(chain, key)
			key = parent
		}
	}

	sort.Strings(found)
	return found
}

func smallest(keys []string) string {
	least := keys[0]
	for _, k := range keys[1:] {
		if k < least {
			least = k
		}
	}
	return least
}

func entryFor(entries []vault.Entry, key string) vault.Entry {
	for _, e := range entries {
		if e.Key == key {
			return e
		}
	}
	return vault.Entry{Path: key}
}
