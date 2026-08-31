package vault

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// A sprint is a page.
//
// Jira makes it an object with a state — started, completed — and the state is
// the part that goes wrong: a sprint nobody remembered to close sits "started"
// for a year, and the dates disagree with it. Here there is no state. A sprint
// runs between two dates, and whether it is on is a question about today, which
// cannot go stale.
//
// The page's body is what Jira has no room for: the goal in more than one line,
// what was cut and why, and the retrospective — written where the work is. That
// is also what makes it a legitimate hub by the test in
// docs/design/how-things-connect.md §5: delete the page and knowledge is lost.

// SprintDir is where sprint pages live in a vault built from the template.
// A vault may file them anywhere — they are found by their type, not by their
// folder, the same way a task is found by its frontmatter.
const SprintDir = "docs/sprints"

// SprintType is the value of `type:` on a sprint page.
const SprintType = "sprint"

// DateFormat is how a sprint's dates are written: a plain day, unquoted, which
// is what Obsidian shows as a date rather than as a string.
const DateFormat = "2006-01-02"

// Sprint is one sprint page.
type Sprint struct {
	// Note is the page's file name without .md, which is what a wikilink to it
	// says and therefore what a task's `sprint:` holds.
	Note  string
	Path  string
	Title string
	// Starts and Ends are the fortnight. Both are days rather than instants: a
	// sprint is not over at a particular minute.
	Starts time.Time
	Ends   time.Time
	// Body is the prose — the goal, what was cut, the retrospective.
	Body string
	// Trouble is why this page could not be read as a sprint, for a page that
	// has to say so rather than pretend the sprint is not there.
	Trouble string
}

// On reports whether the sprint is running on a given day.
//
// Inclusive at both ends: a sprint that ends on the 28th is on for the whole of
// the 28th, which is what somebody looking at a board on that day means.
func (s Sprint) On(on time.Time) bool {
	if s.Starts.IsZero() || s.Ends.IsZero() {
		return false
	}
	on = day(on)
	return !on.Before(s.Starts) && !on.After(s.Ends)
}

// Over reports whether the sprint has finished.
func (s Sprint) Over(on time.Time) bool {
	return !s.Ends.IsZero() && day(on).After(s.Ends)
}

// Ahead reports whether the sprint has not started.
func (s Sprint) Ahead(on time.Time) bool {
	return !s.Starts.IsZero() && day(on).Before(s.Starts)
}

// Days is how long the sprint is, counting both ends.
func (s Sprint) Days() int {
	if s.Starts.IsZero() || s.Ends.IsZero() {
		return 0
	}
	return int(s.Ends.Sub(s.Starts).Hours()/24) + 1
}

func day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Sprints reads every sprint page in a vault, newest first.
//
// Found by their `type:`, not by their folder. A vault that files sprints
// somewhere else is still a vault, and a rule about a folder would be a rule
// the format does not need — a task is found the same way.
func Sprints(root string) ([]Sprint, error) {
	var out []Sprint

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] {
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
		s, ok := ParseSprint(raw)
		if !ok {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		s.Path = filepath.ToSlash(rel)
		s.Note = strings.TrimSuffix(d.Name(), ".md")
		if s.Title == "" {
			s.Title = s.Note
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Newest first, and by name where a vault has two sprints on one day —
	// which happens when somebody is planning ahead.
	sort.SliceStable(out, func(a, b int) bool {
		if !out[a].Starts.Equal(out[b].Starts) {
			return out[a].Starts.After(out[b].Starts)
		}
		return out[a].Note > out[b].Note
	})
	return out, nil
}

// ParseSprint reads a file as a sprint page, and reports whether it is one.
//
// A page that says `type: sprint` is one even if its dates are unreadable: the
// team wrote a sprint and got a date wrong, and hiding the page is a worse
// answer than showing it with what is wrong with it said out loud.
func ParseSprint(raw []byte) (Sprint, bool) {
	front, body, ok := frontmatter(raw)
	if !ok {
		return Sprint{}, false
	}

	var page struct {
		Title  string `yaml:"title"`
		Type   string `yaml:"type"`
		Starts string `yaml:"starts"`
		Ends   string `yaml:"ends"`
	}
	if err := yaml.Unmarshal(front, &page); err != nil {
		return Sprint{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(page.Type), SprintType) {
		return Sprint{}, false
	}

	s := Sprint{Title: strings.TrimSpace(page.Title), Body: body}
	s.Starts, s.Ends, s.Trouble = readSpan(page.Starts, page.Ends)
	return s, true
}

// readSpan turns two written days into a span, and says what is wrong with it
// in one sentence rather than several.
func readSpan(from, to string) (starts, ends time.Time, trouble string) {
	starts, errFrom := readDay(from)
	ends, errTo := readDay(to)

	switch {
	case errFrom != nil && errTo != nil:
		return starts, ends, "neither starts nor ends is a date (YYYY-MM-DD)"
	case errFrom != nil:
		return starts, ends, "starts is not a date (YYYY-MM-DD)"
	case errTo != nil:
		return starts, ends, "ends is not a date (YYYY-MM-DD)"
	case ends.Before(starts):
		return starts, ends, "it ends before it starts"
	}
	return starts, ends, ""
}

func readDay(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	// Quoted or not, and a full timestamp is accepted because somebody will
	// write one and meaning it as a day is unambiguous.
	if len(value) > len(DateFormat) {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return day(t), nil
		}
	}
	t, err := time.Parse(DateFormat, value)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// Running is the sprint on today, and whether there is one.
//
// Two sprints covering the same day is a mistake nobody should have to handle
// twice, so the one that started later wins — it is the one somebody meant.
func Running(sprints []Sprint, on time.Time) (Sprint, bool) {
	for _, s := range sprints {
		if s.On(on) {
			return s, true
		}
	}
	return Sprint{}, false
}

// frontmatter splits a page into its YAML block and the prose after it.
//
// The same shape as a task file — see task.Parse — but a page is not a task and
// borrowing that parser would mean every page had to satisfy a task's rules.
func frontmatter(raw []byte) (front []byte, body string, ok bool) {
	const fence = "---\n"
	if !bytes.HasPrefix(raw, []byte(fence)) {
		return nil, "", false
	}
	rest := raw[len(fence):]
	end := bytes.Index(rest, []byte("\n"+fence))
	if end < 0 {
		return nil, "", false
	}
	return rest[:end+1], string(rest[end+len("\n"+fence):]), true
}
