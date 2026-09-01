package vault

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// The people a vault works with, as pages.
//
// An assignee was a string nobody checked, so a typo made a new colleague:
// `vadym_didekno` appeared in the suggestions and in the filter menu as a
// person with work on them. Worse, the question people actually ask — "what is
// Marina on, and what did she close" — had nowhere to be asked, because a
// string is not a place you can stand.
//
// So a person is a note, like a label and like a sprint, and `assignee:` is a
// link to it. Obsidian then answers the question with the machinery it already
// has: the backlinks pane on the person's page is their work, and the graph
// draws them among it. And a handle that names nobody is a finding rather than
// a colleague.
//
// What a person page is not: an account. Nothing here signs anybody in or
// grants them anything — that is the git host's business, and the sign-in
// handles below are how the two are matched, not a credential.

// PeopleDir is the folder a person's page lives in.
const PeopleDir = "people"

// Person is one of them.
type Person struct {
	// Handle is what `assignee:` says — the file name without .md.
	Handle string
	// Name is what to call them, or empty when nobody has said, in which case
	// the handle is the name.
	Name string
	Path string
	// GitHub and GitLab are their logins on the hosts this vault signs people
	// in with, so that whoever is signed in can be recognised as this person.
	// Neither is required and neither is a credential.
	GitHub string
	GitLab string
	// Emails are the addresses this person commits under, so a line of history
	// can be attributed to them.
	//
	// Not a privacy decision taken lightly, and it is a small one: the address
	// is in every commit this repository already holds, and `git log` prints
	// it. What the page adds is the join — "this address is that person" —
	// which is the only way blame and a board can be about the same people.
	Emails []string
	Body   string
}

// Called is what to show: their name if they have one, their handle otherwise.
func (p Person) Called() string {
	if strings.TrimSpace(p.Name) != "" {
		return p.Name
	}
	return p.Handle
}

// People reads every person page in the vault, by handle.
func People(root string) ([]Person, error) {
	dir := filepath.Join(root, PeopleDir)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		// A vault that has not written any is a vault where assignees are
		// still bare handles, which reads perfectly well. This is not a fault.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Person
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		p, ok := ParsePerson(raw)
		if !ok {
			continue
		}
		p.Handle = strings.TrimSuffix(e.Name(), ".md")
		p.Path = PeopleDir + "/" + e.Name()
		out = append(out, p)
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].Handle < out[b].Handle })
	return out, nil
}

// ParsePerson reads a page as a person, and reports whether it is one.
func ParsePerson(raw []byte) (Person, bool) {
	front, body, ok := frontmatter(raw)
	if !ok {
		return Person{}, false
	}

	var read struct {
		Type   string   `yaml:"type"`
		Name   string   `yaml:"name"`
		GitHub string   `yaml:"github"`
		GitLab string   `yaml:"gitlab"`
		Emails []string `yaml:"emails"`
	}
	if err := yaml.Unmarshal(front, &read); err != nil {
		return Person{}, false
	}
	if strings.TrimSpace(read.Type) != "person" {
		// A README in the folder is not a colleague.
		return Person{}, false
	}
	p := Person{
		Name:   strings.TrimSpace(read.Name),
		GitHub: strings.TrimSpace(read.GitHub),
		GitLab: strings.TrimSpace(read.GitLab),
		Body:   body,
	}
	for _, address := range read.Emails {
		if address = strings.TrimSpace(address); address != "" {
			p.Emails = append(p.Emails, address)
		}
	}
	return p, true
}

// PersonPage is the file a person's page starts as.
//
// Deliberately thin. What a person is for is the links pointing at them; a form
// asking for a job title and a time zone would be a form nobody fills in and a
// file nobody reads.
func PersonPage(handle, name string) string {
	if strings.TrimSpace(name) == "" {
		name = handle
	}
	// Marshalled rather than written by hand: a name with a colon in it —
	// "Marina: on leave until March" — is the kind of thing somebody types, and
	// a hand-quoted frontmatter block is where that becomes a file nobody can
	// parse.
	front, err := yaml.Marshal(struct {
		Type string `yaml:"type"`
		Name string `yaml:"name"`
	}{Type: "person", Name: name})
	if err != nil {
		front = []byte("type: person\n")
	}
	return "---\n" + string(front) + "---\n\n" +
		"Work on " + name + " is everything that links here — the backlinks pane, " +
		"and the board narrowed to them.\n"
}

// HandlesIn is every handle the tasks name, whether or not anybody has written
// a page for them, with how many tasks each carries.
//
// The list a vault has before it has any pages at all, and what `docket check`
// compares against the pages it does have.
func HandlesIn(entries []Entry) map[string]int {
	out := map[string]int{}
	for _, e := range entries {
		if e.Task == nil {
			continue
		}
		if who := strings.TrimSpace(e.Task.Assignee); who != "" {
			out[who]++
		}
	}
	return out
}

// PersonOf finds the page for a handle.
func PersonOf(people []Person, handle string) (Person, bool) {
	for _, p := range people {
		if strings.EqualFold(p.Handle, handle) {
			return p, true
		}
	}
	return Person{}, false
}

// PersonSignedInAs finds whose page names this login on that host, so somebody
// signed in can be recognised without being asked who they are.
func PersonSignedInAs(people []Person, host, login string) (Person, bool) {
	if strings.TrimSpace(login) == "" {
		return Person{}, false
	}
	for _, p := range people {
		var claimed string
		switch strings.ToLower(host) {
		case "github", "github.com":
			claimed = p.GitHub
		case "gitlab", "gitlab.com":
			claimed = p.GitLab
		default:
			// A self-hosted GitLab is still GitLab. The host name is what the
			// server knows; the field is what the vault writes.
			claimed = p.GitLab
		}
		if claimed != "" && strings.EqualFold(claimed, login) {
			return p, true
		}
	}
	return Person{}, false
}

// PersonWhoWrote finds whose page claims the address a commit was authored
// under, so history and the board can be about the same people.
func PersonWhoWrote(people []Person, email string) (Person, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return Person{}, false
	}
	for _, p := range people {
		for _, claimed := range p.Emails {
			if strings.EqualFold(claimed, email) {
				return p, true
			}
		}
	}
	return Person{}, false
}

// Handle is a name turned into what a file can be called and a link can say.
//
// Letters and digits of any script, because the vaults this is for are written
// in Cyrillic and a rule of [a-z0-9] silently turned every Russian name into an
// empty string once already — twice, in fact, in this codebase.
func Handle(name string) string {
	var out []rune
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out = append(out, r)
		case r == '_' || r == '-' || r == '.':
			out = append(out, r)
		case unicode.IsSpace(r):
			if len(out) > 0 && out[len(out)-1] != '_' {
				out = append(out, '_')
			}
		}
	}
	return strings.Trim(string(out), "_-.")
}

// AssignedTo is every task on a person.
func AssignedTo(entries []Entry, handle string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Task != nil && strings.EqualFold(e.Task.Assignee, handle) {
			out = append(out, e)
		}
	}
	return out
}
