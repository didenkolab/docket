// Package check validates a vault against the rules in the format
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
	"time"

	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/task"
	"github.com/vadymdidenkolab/docket/internal/vault"
)

// Rules, numbered as in the specification.
const (
	RuleFrontmatter = 1  // frontmatter exists and key equals the path
	RuleUniqueKeys  = 2  // no key twice
	RuleStatus      = 3  // status is known and its category agrees
	RuleVocabulary  = 4  // type and priority are known
	RuleParent      = 5  // parent exists and the graph has no cycles
	RuleFlat        = 6  // no nested frontmatter values
	RuleTimestamps  = 7  // timestamps parse and are in order
	RuleLinks       = 8  // every wikilink resolves
	RuleProjects    = 9  // docket.yaml, the folders and the boards agree
	RuleRelations   = 10 // a relationship is a link, not a string
	RuleTags        = 11 // a tag is a set somebody asks for, said once
	RuleEstimates   = 12 // an estimate is on the scale, and not on a container
	RuleSprints     = 13 // a sprint is a page, said as a link, and owns its days
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
func Run(root string) ([]Finding, error) { return RunIn(root, nil) }

// RunIn checks one vault, treating alsoKnown as resolvable in addition to what
// the vault itself holds.
//
// It exists for a workspace. Each repository is checked on its own, because
// each is a project and is valid or not by itself — but a link from one project
// to a note in another resolves when the workspace is opened in Obsidian, which
// is the only place both are present. Checking the repository alone would
// report it as dead; checking it inside the workspace should not.
func RunIn(root string, alsoKnown map[string]bool) ([]Finding, error) {
	c, err := project.Load(root)
	if err != nil {
		return nil, err
	}

	entries, err := vault.List(root, c)
	if err != nil {
		return nil, err
	}

	names, err := resolvable(root)
	if err != nil {
		return nil, err
	}
	for name := range alsoKnown {
		names[name] = true
	}

	// key → the note name a link has to use, for the suggestion below.
	byKey := map[string]string{}
	for _, e := range entries {
		byKey[e.Key] = e.Note()
	}

	var findings []Finding
	add := func(f Finding) { findings = append(findings, f) }

	seen := map[string]string{} // key -> path that claimed it
	parents := map[string]string{}
	exists := map[string]bool{}

	typeOf := map[string]string{}
	// hasChildren is which tasks are containers, which is a fact about the
	// other files rather than about the task, so it is worked out before any
	// of them is judged.
	hasChildren := map[string]bool{}
	for _, e := range entries {
		exists[e.Key] = true
		if e.Task != nil {
			typeOf[e.Key] = e.Task.Type
			if e.Task.Parent != "" {
				hasChildren[e.Task.Parent] = true
			}
		}
	}

	for _, e := range entries {
		if e.Err != nil {
			add(Finding{e.Path, 0, RuleFrontmatter, e.Err.Error()})
			continue
		}
		t := e.Task

		if t.Key != e.Key {
			add(Finding{e.Path, t.PropertyLine("key"), RuleFrontmatter,
				fmt.Sprintf("key is %q but the file is named %q — the name starts with the key",
					t.Key, filepath.Base(e.Path))})
		}
		if title := t.Title; title != "" {
			if want := vault.FileName(t.Key, title); want != filepath.Base(e.Path) {
				add(Finding{e.Path, t.PropertyLine("title"), RuleFrontmatter,
					fmt.Sprintf("the file should be named %q — the name carries the title, so "+
						"the graph and the file explorer can say what this is", want)})
			}
		}
		if where, taken := seen[t.Key]; taken {
			add(Finding{e.Path, t.PropertyLine("key"), RuleUniqueKeys,
				fmt.Sprintf("key %q is already used by %s", t.Key, where)})
		} else {
			seen[t.Key] = e.Path
		}

		checkStatus(add, e, c)
		checkVocabulary(add, e, c)
		checkEstimate(add, e, c, hasChildren[e.Key])

		if t.Parent != "" {
			parents[e.Key] = t.Parent
			if !exists[t.Parent] {
				add(Finding{e.Path, t.PropertyLine("parent"), RuleParent,
					fmt.Sprintf("parent %q does not exist", t.Parent)})
			} else if c.Layered() {
				// A parent has to sit above its child, or an epic is a word
				// rather than a container. Only checked in a vault that has
				// said what its levels are — see project.Layered.
				if parent, ok := typeOf[t.Parent]; ok && !c.CanParent(parent, t.Type) {
					add(Finding{e.Path, t.PropertyLine("parent"), RuleParent,
						fmt.Sprintf("a parent sits above its child, and %q is at level %d "+
							"while %q is at level %d",
							parent, c.LevelOf(parent), t.Type, c.LevelOf(t.Type))})
				}
			}
		}

		checkRelations(add, e, byKey)

		for _, name := range t.NestedProperties() {
			add(Finding{e.Path, t.PropertyLine(name), RuleFlat,
				fmt.Sprintf("property %q holds a nested value: Obsidian cannot edit it and "+
					"Bases cannot filter on it", name)})
		}

		checkTimestamps(add, e)
		checkLinks(add, e, names, byKey)
	}

	for _, key := range cycles(parents) {
		e := entryFor(entries, key)
		add(Finding{e.Path, 0, RuleParent, fmt.Sprintf("%s is part of a parent cycle", key)})
	}

	findings = append(findings, checkPageLinks(root, names, byKey)...)
	findings = append(findings, checkProjects(root, c)...)
	findings = append(findings, checkTags(entries)...)

	// A vault that has no sprints is the ordinary case, and reading none is not
	// a failure — a walk that cannot be done says so about the vault, not about
	// the sprints.
	if sprints, err := vault.Sprints(root); err == nil {
		findings = append(findings, checkSprints(entries, sprints, time.Now().UTC())...)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func checkStatus(add func(Finding), e vault.Entry, c *project.Config) {
	t := e.Task
	category, known := c.CategoryOf(t.Status)
	switch {
	case t.Status == "":
		add(Finding{e.Path, t.PropertyLine("status"), RuleStatus, "no status"})
	case !known:
		add(Finding{e.Path, t.PropertyLine("status"), RuleStatus,
			fmt.Sprintf("status %q is not one of %s", t.Status, strings.Join(c.StatusNames(), ", "))})
	case t.StatusCategory != category:
		add(Finding{e.Path, t.PropertyLine("status_category"), RuleStatus,
			fmt.Sprintf("status %q is in category %q, but the task says %q — they move together",
				t.Status, category, t.StatusCategory)})
	}
}

// checkEstimate holds the two things an estimate can be wrong about.
//
// Off the declared scale is the ordinary one: a vault that said 1, 2, 3, 5, 8,
// 13 meant it, the same way it meant its statuses.
//
// The other is the interesting one. A container's estimate is the sum of its
// children, computed on the way past — so a container carrying its own number
// is a second record of a fact that is already written down, and the two will
// disagree the first time a child is re-estimated. That is what purpose §4
// refuses, and it is refused here rather than reconciled.
func checkEstimate(add func(Finding), e vault.Entry, c *project.Config, parents bool) {
	t := e.Task
	if !t.Sized() {
		return
	}
	line := t.PropertyLine("estimate")

	if !c.Sizes() {
		add(Finding{e.Path, line, RuleEstimates,
			fmt.Sprintf("estimate %s, but %s says nothing about estimates — give it a unit "+
				"or take the property off", project.Amount(t.Size()), project.FileName)})
		return
	}
	if parents {
		add(Finding{e.Path, line, RuleEstimates,
			fmt.Sprintf("estimate %s on a task that has children: a container's size is what "+
				"its children add up to, and a number here is a second answer to the "+
				"same question", project.Amount(t.Size()))})
	}
	if !c.OnScale(t.Size()) {
		add(Finding{e.Path, line, RuleEstimates,
			fmt.Sprintf("estimate %s is not on the scale %s",
				project.Amount(t.Size()), amounts(c.EstimateScale()))})
	}
}

// amounts writes a scale the way the configuration says it.
func amounts(scale []float64) string {
	out := make([]string, 0, len(scale))
	for _, v := range scale {
		out = append(out, project.Amount(v))
	}
	return strings.Join(out, ", ")
}

func checkVocabulary(add func(Finding), e vault.Entry, c *project.Config) {
	t := e.Task
	if !c.HasType(t.Type) {
		add(Finding{e.Path, t.PropertyLine("type"), RuleVocabulary,
			fmt.Sprintf("type %q is not one of %s", t.Type, strings.Join(c.TypeNames(), ", "))})
	}
	if !c.HasPriority(t.Priority) {
		add(Finding{e.Path, t.PropertyLine("priority"), RuleVocabulary,
			fmt.Sprintf("priority %q is not one of %s", t.Priority, strings.Join(c.Priorities, ", "))})
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

func checkLinks(add func(Finding), e vault.Entry, names map[string]bool, byKey map[string]string) {
	for _, target := range e.Task.Links() {
		if names[strings.ToLower(target)] {
			continue
		}
		add(Finding{e.Path, e.Task.LineOf(target), RuleLinks, deadLink(target, byKey)})
	}
}

// deadLink says what is wrong, and — for the mistake everyone makes once —
// what to write instead. Obsidian resolves a note's name, not a task's key.
func deadLink(target string, byKey map[string]string) string {
	if note, ok := byKey[target]; ok {
		return fmt.Sprintf("[[%s]] resolves to nothing — a key is not a note name; write [[%s]]",
			target, note)
	}
	return fmt.Sprintf("[[%s]] resolves to nothing in this vault", target)
}

// checkProjects catches the two ways docket.yaml, the folders and the boards can
// drift apart: a folder full of tasks nobody declared, and a declared project
// no board shows. Both make work invisible, which is the one thing a tracker
// must not do.
func checkProjects(root string, c *project.Config) []Finding {
	var findings []Finding

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	known := map[string]bool{}
	for _, key := range c.ProjectKeys() {
		known[key] = true
	}

	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || known[name] {
			continue
		}
		if !project.KeyPattern.MatchString(name) || project.Reserved[name] {
			continue
		}
		if tasks, _ := filepath.Glob(filepath.Join(root, name, "*.md")); len(tasks) > 0 {
			findings = append(findings, Finding{name, 0, RuleProjects,
				fmt.Sprintf("%s holds %d task file(s) but is not a project in %s",
					name, len(tasks), project.FileName)})
		}
	}

	findings = append(findings, checkGeneratedBoards(root, c)...)

	boards, err := os.ReadDir(filepath.Join(root, vault.BoardsDir))
	if err != nil {
		return findings
	}
	var mentioned string
	for _, board := range boards {
		if strings.HasSuffix(board.Name(), ".base") {
			raw, err := os.ReadFile(filepath.Join(root, vault.BoardsDir, board.Name()))
			if err == nil {
				mentioned += string(raw)
			}
		}
	}
	if mentioned == "" {
		return findings
	}
	for _, key := range c.ProjectKeys() {
		if !strings.Contains(mentioned, `"`+key+`"`) {
			findings = append(findings, Finding{vault.BoardsDir, 0, RuleProjects,
				fmt.Sprintf("no board mentions project %s — run docket project add or "+
					"regenerate the boards, or its tasks are invisible", key)})
		}
	}
	return findings
}

// checkGeneratedBoards reports a generated board that no longer says what the
// configuration says.
//
// A board is derived from docket.yaml, so it goes stale whenever the vocabulary
// changes or the tool learns to write a better one — and a stale board is not a
// cosmetic problem: it groups by a status the vault renamed, or draws the
// columns of a pipeline in the wrong order. There is one right answer, so
// `--fix` writes it.
//
// Only files still carrying vault.Marker are checked. Removing that line is how
// a vault says a board is its own.
// generatedFor is what this vault's boards should hold, including the ones that
// exist only because of what is in it — the sprint board, which a vault without
// sprint pages does not get and must not be told it is missing.
func generatedFor(root string, c *project.Config) map[string]string {
	sprints, err := vault.Sprints(root)
	if err != nil {
		sprints = nil
	}
	return vault.GeneratedIn(c, len(sprints) > 0)
}

func checkGeneratedBoards(root string, c *project.Config) []Finding {
	var findings []Finding
	for rel, want := range generatedFor(root, c) {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))

		// A board that is not there at all. Only the sprint board can get into
		// this state: the other three are written by `docket init`, so their
		// absence is somebody having removed them. The sprint board appears
		// when a vault starts working in sprints, which is a thing that happens
		// long after init — and a vault with sprint pages and no sprint board
		// has a board it does not know it could have.
		//
		// The escape is the same as for the others, and it is the marker rather
		// than the file: write your own sprint.base without that first line and
		// nothing here has an opinion about it again.
		if os.IsNotExist(err) {
			if rel != vault.SprintFile {
				continue
			}
			findings = append(findings, Finding{rel, 0, RuleProjects,
				"this vault has sprint pages and no sprint board — run docket check --fix " +
					"to write one. It is the only way a sprint is visible in Obsidian, " +
					"because Bases can only filter on what a note itself says"})
			continue
		}
		if err != nil || !strings.HasPrefix(string(got), vault.Marker) || string(got) == want {
			continue
		}
		findings = append(findings, Finding{rel, 0, RuleProjects,
			"this board no longer matches " + project.FileName +
				" — run docket check --fix to regenerate it, or delete its first " +
				"line to keep it as your own"})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}

// checkPageLinks applies rule 8 to the knowledge base as well: a broken link in
// a page is as dead as one in a task.
func checkPageLinks(root string, names map[string]bool, byKey map[string]string) []Finding {
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
				deadLink(target, byKey)})
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

// resolvable collects what a wikilink can point at, the way Obsidian resolves
// one: a file name without its extension, or a vault-relative path.
//
// Aliases are deliberately not included. Obsidian's resolver does not consult
// them, so accepting them here would pass links that are dead in the app —
// a validator that is more generous than the thing it validates is worse than
// none.
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
		return nil
	})
	return names, err
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

// checkRelations reports a relationship written as a string.
//
// A parent and a label are what connect an epic to its tasks and a task to
// everything else carrying the same label. Written as plain words they connect
// nothing: Obsidian resolves wikilinks and nothing else, so the relationship
// exists for docket's own tools and is absent from the graph, the backlinks and
// the quick switcher — which is where it was supposed to show. See
// docs/purpose.md §3.
//
// `docket check --fix` rewrites them, because unlike every other finding this
// one has a right answer.
func checkRelations(add func(Finding), e vault.Entry, byKey map[string]string) {
	t := e.Task

	for _, r := range task.Relations {
		for _, raw := range t.RawRelated(r.Field) {
			if !task.IsLink(raw) {
				add(Finding{e.Path, t.PropertyLine(r.Field), RuleRelations,
					fmt.Sprintf("%s %q is a string, not a link: it connects nothing in Obsidian. "+
						"Write it as %q", r.Field, raw, task.Link(noteFor(raw, byKey)))})
			}
		}
		// A relation pointing at nothing is a relation about a task that was
		// renamed away or never existed, and is worth the same attention as a
		// parent that does not exist.
		for _, key := range t.Related(r.Field) {
			if _, ok := byKey[key]; !ok {
				add(Finding{e.Path, t.PropertyLine(r.Field), RuleParent,
					fmt.Sprintf("%s %q, which is not in this vault", r.Field, key)})
			}
		}
	}

	if raw := t.RawParent(); raw != "" && !task.IsLink(raw) {
		add(Finding{e.Path, t.PropertyLine("parent"), RuleRelations,
			fmt.Sprintf("parent %q is a string, not a link: an epic written this way draws no "+
				"edge to its tasks in Obsidian. Write it as %q — docket check --fix does it",
				raw, task.Link(noteFor(raw, byKey)))})
	}

	for _, raw := range t.RawLabels() {
		if task.IsLink(raw) {
			continue
		}
		add(Finding{e.Path, t.PropertyLine("labels"), RuleRelations,
			fmt.Sprintf("label %q is a string, not a link: it connects nothing in Obsidian. "+
				"Write it as %q — docket check --fix does it", raw, task.Link(raw))})
	}
}

// noteFor is the note name a key should be linked by, or the key itself when
// nothing in the vault has it — a dangling link is still a link, and rule 5
// already reports a parent that does not exist.
func noteFor(key string, byKey map[string]string) string {
	if note, ok := byKey[key]; ok {
		return note
	}
	return key
}
