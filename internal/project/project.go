// Package project reads docket.yaml — the projects a vault holds and the
// vocabulary they share.
package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the file that marks the root of a vault.
const FileName = "docket.yaml"

// The three status categories. Names of statuses belong to a vault; these do
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

// KeyPattern is the shape of a project key. It is a folder name and the head of
// every key in that project, and it never changes, so it is checked once and
// strictly.
var KeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)

// Reserved names cannot be project keys, because a vault already uses them.
var Reserved = map[string]bool{
	"DOCS": true, "BOARDS": true, "TEMPLATES": true, "ATTACHMENTS": true, "SCRIPTS": true,
}

// Status is a name people use paired with a category machines act on.
type Status struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
}

// Project is one project in a vault: a key, which is also its folder, and a
// name for people.
type Project struct {
	Key  string `yaml:"key"`
	Name string `yaml:"name"`
}

// Config is the content of docket.yaml.
//
// The vocabulary is shared by every project in the vault. That is what lets one
// board show them all: projects that disagreed about their columns would force
// a combined board to invent a merged set, and whatever it invented would be
// wrong for one of them. See ADR-0003.
type Config struct {
	Name       string    `yaml:"name"`
	Projects   []Project `yaml:"projects"`
	Statuses   []Status  `yaml:"statuses"`
	Types      []string  `yaml:"types"`
	Priorities []string  `yaml:"priorities"`
}

// ErrNotAVault is returned when a directory holds no docket.yaml.
var ErrNotAVault = errors.New("not a docket vault: no " + FileName)

// Load reads and validates the docket.yaml in dir.
func Load(dir string) (*Config, error) {
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s: %w", dir, ErrNotAVault)
	}
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return &c, nil
}

// Save writes docket.yaml.
func (c *Config) Save(dir string) error {
	if err := c.validate(); err != nil {
		return err
	}
	body, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	header := "# The projects this vault holds, and the vocabulary they share.\n" +
		"# A key is PROJECT/NUMBER and the project is a folder at the root.\n"
	return os.WriteFile(filepath.Join(dir, FileName), append([]byte(header), body...), 0o644)
}

// FindRoot walks up from start until it finds a vault, so that commands work
// from anywhere inside one the way git does.
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

func (c *Config) validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("the vault has no name")
	}
	if len(c.Projects) == 0 {
		return errors.New("no projects: a vault with no projects can hold no tasks")
	}

	seen := map[string]bool{}
	for i := range c.Projects {
		p := &c.Projects[i]
		p.Key = strings.TrimSpace(p.Key)
		if err := ValidKey(p.Key); err != nil {
			return err
		}
		if seen[p.Key] {
			return fmt.Errorf("project %s is listed twice", p.Key)
		}
		seen[p.Key] = true
		if strings.TrimSpace(p.Name) == "" {
			p.Name = p.Key
		}
	}

	if len(c.Statuses) == 0 {
		return errors.New("no statuses: a vault with no statuses has no board")
	}
	statuses := map[string]bool{}
	for _, s := range c.Statuses {
		if s.Name == "" {
			return errors.New("a status has no name")
		}
		if statuses[s.Name] {
			return fmt.Errorf("status %q is listed twice", s.Name)
		}
		statuses[s.Name] = true
		if !categories[s.Category] {
			return fmt.Errorf("status %q has category %q, want one of %s, %s, %s",
				s.Name, s.Category, CategoryTodo, CategoryDoing, CategoryDone)
		}
	}

	if len(c.Types) == 0 {
		return errors.New("no task types")
	}
	if len(c.Priorities) == 0 {
		return errors.New("no priorities")
	}
	return nil
}

// ValidKey checks a project key.
func ValidKey(key string) error {
	if !KeyPattern.MatchString(key) {
		return fmt.Errorf("project key %q is not usable: 2 to 10 characters, upper-case "+
			"letters and digits, starting with a letter", key)
	}
	if Reserved[key] {
		return fmt.Errorf("project key %q is a name the vault already uses", key)
	}
	return nil
}

// HasProject reports whether the vault holds a project.
func (c *Config) HasProject(key string) bool {
	for _, p := range c.Projects {
		if p.Key == key {
			return true
		}
	}
	return false
}

// ProjectKeys lists the project keys, in the order docket.yaml gives them.
func (c *Config) ProjectKeys() []string {
	keys := make([]string, len(c.Projects))
	for i, p := range c.Projects {
		keys[i] = p.Key
	}
	return keys
}

// ProjectName is a project's human name, or its key when it has none.
func (c *Config) ProjectName(key string) string {
	for _, p := range c.Projects {
		if p.Key == key {
			return p.Name
		}
	}
	return key
}

// AddProject appends a project.
func (c *Config) AddProject(key, name string) error {
	if err := ValidKey(key); err != nil {
		return err
	}
	if c.HasProject(key) {
		return fmt.Errorf("project %s is already in this vault", key)
	}
	if name == "" {
		name = key
	}
	c.Projects = append(c.Projects, Project{Key: key, Name: name})
	return nil
}

// CategoryOf reports the category of a status name, and whether the vault knows
// that status at all.
func (c *Config) CategoryOf(status string) (string, bool) {
	for _, s := range c.Statuses {
		if s.Name == status {
			return s.Category, true
		}
	}
	return "", false
}

// FirstStatus is the status a new task starts in: the first one listed, which
// is also the leftmost column of the board.
func (c *Config) FirstStatus() Status { return c.Statuses[0] }

// HasType reports whether the vault defines a task type.
func (c *Config) HasType(t string) bool { return contains(c.Types, t) }

// HasPriority reports whether the vault defines a priority.
func (c *Config) HasPriority(v string) bool { return contains(c.Priorities, v) }

// DefaultPriority is the middle of the road: "normal" when the vault has it,
// otherwise the first one listed.
func (c *Config) DefaultPriority() string {
	if c.HasPriority("normal") {
		return "normal"
	}
	return c.Priorities[0]
}

// StatusNames lists every status name, in board order.
func (c *Config) StatusNames() []string {
	names := make([]string, len(c.Statuses))
	for i, s := range c.Statuses {
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

// SplitKey takes a task key apart. A key is PROJECT/NUMBER, and it is also the
// path to the file, which is the whole point of the shape.
func SplitKey(key string) (projectKey string, number int, err error) {
	slash := strings.Index(key, "/")
	if slash < 1 || slash == len(key)-1 {
		return "", 0, fmt.Errorf("key %q is not PROJECT/NUMBER", key)
	}
	projectKey = key[:slash]
	if !KeyPattern.MatchString(projectKey) {
		return "", 0, fmt.Errorf("key %q does not start with a usable project key", key)
	}
	number, err = strconv.Atoi(key[slash+1:])
	if err != nil || number < 1 {
		return "", 0, fmt.Errorf("key %q does not end in a task number", key)
	}
	return projectKey, number, nil
}

// Key builds a task key from its parts.
func Key(projectKey string, number int) string {
	return fmt.Sprintf("%s/%d", projectKey, number)
}
