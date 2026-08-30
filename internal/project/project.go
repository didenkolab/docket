// Package project reads project.yaml — a vault's vocabulary of statuses,
// types and priorities.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the file that marks the root of a vault.
const FileName = "project.yaml"

// The three status categories. Names of statuses belong to a project; these do
// not, because boards, filters and metrics act on them.
const (
	CategoryTodo  = "todo"
	CategoryDoing = "doing"
	CategoryDone  = "done"
)

var categories = map[string]bool{
	CategoryTodo:  true,
	CategoryDoing: true,
	CategoryDone:  true,
}

// KeyPattern is the shape of a project key. It becomes the head of every task
// file name and never changes, so it is checked once, strictly.
var KeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// Status is a name people use paired with a category machines act on.
type Status struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
}

// Project is the content of project.yaml.
type Project struct {
	Key        string   `yaml:"key"`
	Name       string   `yaml:"name"`
	Statuses   []Status `yaml:"statuses"`
	Types      []string `yaml:"types"`
	Priorities []string `yaml:"priorities"`
}

// ErrNotAVault is returned when a directory holds no project.yaml.
var ErrNotAVault = errors.New("not a docket vault: no " + FileName)

// Load reads and validates the project.yaml in dir.
func Load(dir string) (*Project, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", dir, ErrNotAVault)
	}
	if err != nil {
		return nil, err
	}

	var p Project
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return &p, nil
}

// FindRoot walks up from start until it finds a directory holding project.yaml,
// so that commands work from anywhere inside a vault the way git does.
func FindRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, FileName)); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("%s and its parents: %w", start, ErrNotAVault)
		}
		dir = parent
	}
}

func (p *Project) validate() error {
	if !KeyPattern.MatchString(p.Key) {
		return fmt.Errorf("key %q is not usable: 2 to 10 characters, upper-case letters and "+
			"digits, starting with a letter", p.Key)
	}
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("name is empty")
	}
	if len(p.Statuses) == 0 {
		return errors.New("no statuses: a project with no statuses has no board")
	}

	seen := map[string]bool{}
	for _, s := range p.Statuses {
		if s.Name == "" {
			return errors.New("a status has no name")
		}
		if seen[s.Name] {
			return fmt.Errorf("status %q is listed twice", s.Name)
		}
		seen[s.Name] = true
		if !categories[s.Category] {
			return fmt.Errorf("status %q has category %q, want one of %s, %s, %s",
				s.Name, s.Category, CategoryTodo, CategoryDoing, CategoryDone)
		}
	}

	if len(p.Types) == 0 {
		return errors.New("no task types")
	}
	if len(p.Priorities) == 0 {
		return errors.New("no priorities")
	}
	return nil
}

// CategoryOf reports the category of a status name, and whether the project
// knows that status at all.
func (p *Project) CategoryOf(status string) (string, bool) {
	for _, s := range p.Statuses {
		if s.Name == status {
			return s.Category, true
		}
	}
	return "", false
}

// FirstStatus is the status a new task starts in: the first one listed, which
// is also the leftmost column of the board.
func (p *Project) FirstStatus() Status { return p.Statuses[0] }

// HasType reports whether the project defines a task type.
func (p *Project) HasType(t string) bool { return contains(p.Types, t) }

// HasPriority reports whether the project defines a priority.
func (p *Project) HasPriority(v string) bool { return contains(p.Priorities, v) }

// DefaultPriority is the middle of the road: "normal" when the project has it,
// otherwise the first one listed.
func (p *Project) DefaultPriority() string {
	if p.HasPriority("normal") {
		return "normal"
	}
	return p.Priorities[0]
}

// StatusNames lists every status name, in board order.
func (p *Project) StatusNames() []string {
	names := make([]string, len(p.Statuses))
	for i, s := range p.Statuses {
		names[i] = s.Name
	}
	return names
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
