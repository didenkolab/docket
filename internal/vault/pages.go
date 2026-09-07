package vault

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The knowledge base, read as documents rather than as text.
//
// A vault's pages had no shape at all — the instructions said "there is no
// schema, it is a wiki" — and the five decisions in this project's own vault
// drifted into three different shapes without anybody choosing that. An agent
// cloning a repository and asked to write a decision had nothing to follow.
//
// So a page declares what kind of document it is, and the kinds that have a
// shape are checked against it. Not every kind does: a design page is an
// argument, and a required shape would flatten it into a form.

// Page is one document in the knowledge base.
type Page struct {
	// Note is the file name without .md — what a wikilink to it says.
	Note string
	// Path is relative to the vault root, in slashes.
	Path  string
	Title string
	// Type is what kind of document this is: decision, design, spec, sprint,
	// page. Empty when the page never said.
	Type string
	// Status is `status:`, which means something different per kind — accepted
	// for a decision, normative for a spec.
	Status string
	Date   string
	// Supersedes is the decision this one replaces, as written.
	Supersedes string
	// Sections are its `##` headings, in order, which is what a required shape
	// is checked against.
	Sections []string
	Body     string
	// Example says the page is a worked example the template ships to teach by
	// being good rather than by being a form.
	//
	// It matters at exactly one moment: an import brings real content, and a
	// document beside it that reads like a real sprint or a real decision but
	// was invented is worse than no example at all. Somebody will believe it.
	Example bool
}

// PageTypes are the kinds a document can declare. A vault may use others — the
// format does not own the knowledge base — but these are the ones with rules.
const (
	PageDecision = "decision"
	PageDesign   = "design"
	PageSpec     = "spec"
	PagePlain    = "page"
)

// requiredSections is the shape each kind must have.
//
// Only the decision has one. It is the kind where the missing part is the
// expensive part: a decision without its alternatives is a decision nobody can
// revisit, because the work of finding out what else was possible was done once
// and then not written down.
var requiredSections = map[string][]string{
	PageDecision: {"Context", "Decision", "What this costs", "Alternatives considered"},
}

// RequiredSections is the shape a kind must have, or nil.
func RequiredSections(kind string) []string { return requiredSections[strings.ToLower(kind)] }

// decisionName is what a decision's file is called: numbered in the order taken
// and never renumbered, so the number is a stable name to cite.
var decisionName = regexp.MustCompile(`^\d{4}-[a-z0-9]+(-[a-z0-9]+)*\.md$`)

// IsDecisionName reports whether a file name is a decision's.
func IsDecisionName(base string) bool { return decisionName.MatchString(base) }

// DecisionStatuses are what a decision's status may be.
var DecisionStatuses = []string{"proposed", "accepted", "superseded"}

// Pages reads every document under docs/.
func Pages(root string) ([]Page, error) {
	var out []Page

	docs := filepath.Join(root, DocsDir)
	err := filepath.WalkDir(docs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A vault with no docs directory has no pages, which is not a
			// fault: a project can be all tasks.
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if Hidden(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		p, ok := ParsePage(raw)
		if !ok {
			return nil // no frontmatter: not a document this can say anything about
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		p.Path = filepath.ToSlash(rel)
		p.Note = strings.TrimSuffix(d.Name(), ".md")
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out, nil
}

// ParsePage reads a file as a document, and reports whether it has frontmatter
// at all.
func ParsePage(raw []byte) (Page, bool) {
	front, body, ok := frontmatter(raw)
	if !ok {
		return Page{}, false
	}

	var read struct {
		Title      string `yaml:"title"`
		Type       string `yaml:"type"`
		Status     string `yaml:"status"`
		Date       string `yaml:"date"`
		Supersedes string `yaml:"supersedes"`
		Example    bool   `yaml:"example"`
	}
	if err := yaml.Unmarshal(front, &read); err != nil {
		return Page{}, false
	}

	p := Page{
		Title:      strings.TrimSpace(read.Title),
		Type:       strings.TrimSpace(read.Type),
		Status:     strings.TrimSpace(read.Status),
		Date:       strings.TrimSpace(read.Date),
		Supersedes: strings.TrimSpace(read.Supersedes),
		Example:    read.Example,
		Body:       body,
	}
	p.Sections = sectionsOf(body)
	return p, true
}

// sectionsOf is a body's `##` headings, in order.
//
// Only the second level, and not inside a fenced block: a specification full of
// examples would otherwise be read as having whatever headings its examples
// show.
func sectionsOf(body string) []string {
	var out []string
	fenced := false

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
		}
	}
	return out
}

// OutOfOrder reports the first required section that appears after one that
// should follow it, or empty when the order holds.
//
// Order is checked because the drift started with it: a decision written by
// glancing at whichever other one was open picks up its headings in whatever
// sequence that one had, and by the third document nobody can tell which
// sequence was meant.
func (p Page) OutOfOrder(required []string) (section, after string) {
	at := -1
	seen := ""
	for _, want := range required {
		for i, got := range p.Sections {
			if !strings.EqualFold(got, want) {
				continue
			}
			if i < at {
				return want, seen
			}
			at, seen = i, got
			break
		}
	}
	return "", ""
}

// Has reports whether the page has a section by that name, ignoring case.
func (p Page) Has(section string) bool {
	for _, got := range p.Sections {
		if strings.EqualFold(got, section) {
			return true
		}
	}
	return false
}

// DropExamples removes the template's worked examples from a vault.
//
// `docket init` keeps them: they teach by being good documents rather than empty
// headings, and somebody starting a vault has nothing else to look at. An
// import is the opposite case — it arrives with a thousand real tasks, and an
// invented sprint sitting among them reads as one the team ran.
func DropExamples(root string) ([]string, error) {
	pages, err := Pages(root)
	if err != nil {
		return nil, err
	}
	var dropped []string
	for _, p := range pages {
		if !p.Example {
			continue
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(p.Path))); err != nil {
			return dropped, err
		}
		dropped = append(dropped, p.Path)
	}
	sort.Strings(dropped)
	return dropped, nil
}
