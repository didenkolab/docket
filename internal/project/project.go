// Package project reads docket.yaml — the projects a vault holds and the
// vocabulary they share.
package project

import (
	"bytes"
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

// SignIn is how people get into a board over this vault.
//
// It lives in docket.yaml, and therefore in the repository, because it belongs
// to the repository: the host is whoever holds the remote, and the application
// is registered on that host. Clone the vault and signing in already works —
// which is the same reason the vocabulary lives here rather than in a config
// file beside the server.
//
// There is nothing secret in it. The device flow has no client secret; the id
// identifies the application and is meant to be published. Anything that were
// secret would not go in a file that gets committed.
type SignIn struct {
	// Kind is which API is behind the remote: github, gitlab or bitbucket.
	//
	// Needed only for a self-hosted host, because a hostname does not say what
	// is behind it. github.com, gitlab.com and bitbucket.org say so themselves.
	// A repository declares this about its own host — nothing central has a
	// list — which is the same reason the vocabulary lives here.
	Kind string `yaml:"kind,omitempty"`
	// API is the host's API base URL, for a self-hosted one that does not put
	// it where the convention says.
	API string `yaml:"api,omitempty"`
	// DeviceClientID is the OAuth application to sign people in as. Empty
	// means the sign-in page offers the token field alone.
	DeviceClientID string `yaml:"device_client_id,omitempty"`
}

// Reaction is one declaration: an event, a program in the repository, and what
// narrows it. Read by internal/reaction, which is where the rules are.
type Reaction struct {
	On      string `yaml:"on"`
	Run     string `yaml:"run"`
	Name    string `yaml:"name,omitempty"`
	Status  string `yaml:"status,omitempty"`
	Project string `yaml:"project,omitempty"`
}

// Surface is a page or a panel drawn from what a program prints.
//
// The output is Markdown, rendered the way every other page in the vault is:
// an app that could return HTML would be an app that could put anything on a
// page somebody trusts, and Markdown is the format the vault is written in
// anyway.
type Surface struct {
	// Name is what it is called in an address: /app/<name>.
	Name string `yaml:"name"`
	// Title is what the navigation says. Defaults to the name.
	Title string `yaml:"title,omitempty"`
	// Run is the program, as a path inside the repository.
	Run string `yaml:"run"`
}

// Called is what to show for a surface.
func (s Surface) Called() string {
	if strings.TrimSpace(s.Title) != "" {
		return s.Title
	}
	return s.Name
}

// Inbound is one address the outside can post to.
type Inbound struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
	// SecretEnv names an environment variable holding the shared secret a
	// caller must send as X-Docket-Secret.
	//
	// The name is in the repository and the secret is not: a secret in a file
	// everybody clones is not a secret, and one in the server's environment is
	// held by whoever runs the server, which is who decides what may be posted
	// to it. An inbox with no secret named is refused unless the server has no
	// sign-in at all.
	SecretEnv string `yaml:"secret_env,omitempty"`
}

// Called is what to show for an inbox.
func (i Inbound) Called() string { return i.Name }

// Action is a program a page can run, on request.
type Action struct {
	Name  string `yaml:"name"`
	Title string `yaml:"title,omitempty"`
	Run   string `yaml:"run"`
	// On is the page it belongs to, so a page shows its own buttons and not
	// everybody else's.
	On string `yaml:"on,omitempty"`
	// Confirm is what to ask before running it. Empty asks nothing, which is
	// right for reading something and wrong for anything that writes.
	Confirm string `yaml:"confirm,omitempty"`
}

// Called is what the button says.
func (a Action) Called() string {
	if strings.TrimSpace(a.Title) != "" {
		return a.Title
	}
	return a.Name
}

// App is a pack this vault has installed.
type App struct {
	Name    string `yaml:"name"`
	Source  string `yaml:"source"`
	Version string `yaml:"version,omitempty"`
	// Brought is what this app added, by name.
	//
	// Kept so an app can be upgraded. A new version that changes its own type
	// or its own field is not in conflict with the vault — it is in conflict
	// with its own last version, which is what an upgrade is. Without this the
	// installer could only refuse, and the only way forward would be editing
	// docket.yaml by hand.
	Brought Brought `yaml:"brought,omitempty"`
}

// Brought is the names one app contributed.
type Brought struct {
	Types     []string `yaml:"types,omitempty"`
	Fields    []string `yaml:"fields,omitempty"`
	Relations []string `yaml:"relations,omitempty"`
	Pages     []string `yaml:"pages,omitempty"`
	Panels    []string `yaml:"panels,omitempty"`
	Actions   []string `yaml:"actions,omitempty"`
}

// Empty says nothing was recorded — an app installed before the vault kept
// track of what each one brought.
func (b Brought) Empty() bool {
	return len(b.Types) == 0 && len(b.Fields) == 0 && len(b.Relations) == 0 &&
		len(b.Pages) == 0 && len(b.Panels) == 0 && len(b.Actions) == 0
}

// Owns reports whether this app contributed that name.
func (b Brought) Owns(kind, name string) bool {
	var in []string
	switch kind {
	case "type":
		in = b.Types
	case "field":
		in = b.Fields
	case "relation":
		in = b.Relations
	case "page":
		in = append(append([]string{}, b.Pages...), b.Panels...)
	case "action":
		in = b.Actions
	}
	for _, got := range in {
		if strings.EqualFold(got, name) {
			return true
		}
	}
	return false
}

// Status is a name people use paired with a category machines act on.
type Status struct {
	Name     string `yaml:"name"`
	Category string `yaml:"category"`
}

// Estimates is how big a piece of work is said to be.
//
// The vault chooses the unit, because the unit is an argument nobody else can
// settle: points, hours, days, shirt sizes. And it chooses whether there is a
// scale — a declared list means the interface offers those values and nothing
// else, which is the same shape as statuses and priorities.
//
// No scale means a free number, which is what a team counting hours wants: a
// list of every hour anybody might say is not a vocabulary, it is a nuisance.
type Estimates struct {
	// Unit is the word for one, shown wherever a number is: "points", "hours".
	Unit string `yaml:"unit"`
	// Scale is the values that may be used, in the order they are offered.
	// Empty means any number.
	//
	// Fibonacci is the usual one and it is why this is a list rather than a
	// minimum and a maximum: 1, 2, 3, 5, 8, 13 is a set of choices, and the
	// gaps in it are the point.
	Scale []float64 `yaml:"scale,omitempty"`
}

// A field a vault added for itself.
//
// Jira lets a team add fields and say what type each one is, and a tracker that
// cannot is a tracker somebody keeps a spreadsheet beside. The importer already
// carried eighteen of them out of a real project — as x_ properties nobody
// declared, nothing validated and no page showed.
//
// So they are declared. A field is a frontmatter property, which means it is a
// property in Obsidian too: visible in the editor, filterable in a Base,
// readable with the tool deleted. Nothing here invents a store.
//
// What a field is not is a relationship. A choice with a page behind it is a
// label, and how-things-connect §3 says which mechanism answers which question:
// a field is a property of one task, and anything that joins two of them is a
// link. The kinds below are all scalars for that reason.
type Field struct {
	// Name is the property as written in frontmatter: lower case, underscores.
	Name string `yaml:"name"`
	// Label is what a person reads. Empty means the name will do.
	Label string `yaml:"label,omitempty"`
	// Kind is what may go in it.
	Kind string `yaml:"kind"`
	// Choices are the values a choice field offers, in the order offered.
	Choices []string `yaml:"choices,omitempty"`
	// Types are the task types that have this field. Empty means all of them —
	// a field every kind of work carries is the ordinary case, and listing
	// every type to say so is noise.
	Types []string `yaml:"types,omitempty"`
	// Required means a task of those types must carry it.
	Required bool `yaml:"required,omitempty"`
	// Help is one line saying what to put in it, shown beside the control.
	Help string `yaml:"help,omitempty"`
}

// The kinds a field can be. Each is something a single task can be true of on
// its own, because anything that joins two tasks is a link and not a field.
const (
	FieldText   = "text"   // a line of words
	FieldNumber = "number" // an amount
	FieldDate   = "date"   // a day, YYYY-MM-DD
	// FieldMoment is a point in time rather than a day. Jira has both, and a
	// real import brought a "date of first response" that is a timestamp — so
	// a vault with only days had to call it text and lose the sorting.
	FieldMoment = "datetime"
	FieldChoice = "choice" // one of a declared list
	FieldFlag   = "flag"   // yes or no
	FieldLink   = "link"   // a URL somewhere else
)

// FieldKinds is every kind, for a form and for an error that has to list them.
var FieldKinds = []string{FieldText, FieldNumber, FieldDate, FieldMoment,
	FieldChoice, FieldFlag, FieldLink}

// OwnedProperties is every property the format itself owns.
//
// A declared field that took one of these would shadow it: `status` as a free
// text field is a board that cannot draw a column. The relation names are
// written out rather than read from package task, because task is the layer
// above this one and reading upwards would be a cycle — TestReservedNamesAgree
// keeps the two lists honest.
var OwnedProperties = map[string]bool{
	"key": true, "title": true, "type": true, "status": true, "status_category": true,
	"priority": true, "assignee": true, "created": true, "updated": true,
	"aliases": true, "tags": true, "labels": true, "parent": true, "sprint": true,
	"estimate": true, "order": true,

	"blocks": true, "blocked_by": true, "duplicates": true, "duplicated_by": true,
	"causes": true, "caused_by": true, "relates": true,
}

// FieldName is the shape of a property name: what YAML, Obsidian's property
// editor and a Bases formula can all hold without quoting.
//
// Letters in any script. It was `[a-z][a-z0-9_]*`, which refused every property
// a Russian project has — and the importer writes exactly those, so a vault
// imported from one could not declare a single one of its own fields. The same
// ASCII assumption had just been taken out of the importer's own slugging; it
// went straight back in here, in a regular expression written from memory.
var FieldName = regexp.MustCompile(`^\p{Ll}[\p{L}\p{N}_]*$`)

// Shown is what to call the field to a person.
func (f Field) Shown() string {
	if strings.TrimSpace(f.Label) != "" {
		return f.Label
	}
	return f.Name
}

// AppliesTo reports whether a task of this type carries the field.
func (f Field) AppliesTo(taskType string) bool {
	if len(f.Types) == 0 {
		return true
	}
	for _, t := range f.Types {
		if strings.EqualFold(t, taskType) {
			return true
		}
	}
	return false
}

// Offers reports whether a choice field offers this value.
func (f Field) Offers(value string) bool {
	for _, c := range f.Choices {
		if c == value {
			return true
		}
	}
	return false
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
	Types      []Type    `yaml:"types"`
	Priorities []string  `yaml:"priorities"`

	// Fields are the properties this vault added for itself, beyond the ones
	// the format defines.
	Fields []Field `yaml:"fields,omitempty"`

	// Reactions are what this vault asks to be run when something happens.
	//
	// Declared here so the team agrees on them, reviews them and gets the same
	// ones — and run only where somebody said so: a server runs nothing unless
	// it was started with --reactions. Push access to a repository is not
	// permission to execute code on somebody's machine, which is the same
	// reason git does not share .git/hooks. See internal/reaction.
	Reactions []Reaction `yaml:"reactions,omitempty"`

	// Pages and Panels are surfaces drawn from a program's output: a page of
	// its own in the navigation, and a block on every task.
	//
	// The mechanism the reporting apps need — twenty two of the marketplace's
	// top hundred are a query and a drawing, and the query is a command. Run
	// under the same consent as a reaction: only a server started with
	// --programs runs any of them.
	Pages  []Surface `yaml:"pages,omitempty"`
	Panels []Surface `yaml:"panels,omitempty"`

	// Inbox is what this vault accepts from outside: a program per address, run
	// with whatever was posted to it on stdin.
	//
	// The way results arrive without anybody opening a browser — a CI job posts
	// its JUnit and the board has an execution before the pipeline finishes.
	// The fourth thing an app can be: reactions listen to the vault, pages draw
	// it, actions change it on request, and this lets the outside speak.
	Inbox []Inbound `yaml:"inbox,omitempty"`

	// Actions are programs a page can be asked to run — the button an app puts
	// on its own page. Everything else a vault declares is read; this is the
	// one that does something, so it is the one that asks first and says what
	// happened afterwards.
	Actions []Action `yaml:"actions,omitempty"`

	// Apps are the packs this vault has taken on: what they added is in the
	// vocabulary above like anything else, and this is the record of where it
	// came from, so `docket app list` can say and a later version can tell what
	// it is replacing.
	Apps []App `yaml:"apps,omitempty"`

	// Declared are the relations this vault added — a verb between two tasks
	// that the format does not ship. Read through Relations(), which puts the
	// built-in ones first and expands each declared pair into both directions.
	Declared []Relation `yaml:"relations,omitempty"`

	// Estimates is the unit work is sized in, and the scale if there is one.
	// Omitted when the vault does not size work — a vault that never asked for
	// estimates should not grow a field it has to leave empty.
	Estimates *Estimates `yaml:"estimates,omitempty"`

	// SignIn is how people sign in, when the vault has said. Omitted otherwise,
	// so a vault that never cared keeps a file it recognises.
	SignIn *SignIn `yaml:"sign_in,omitempty"`

	// Transitions is the workflow: which statuses each status can move to.
	//
	// Empty means anything to anything, and that is what a new vault gets. A
	// workflow nobody asked for is a workflow that gets in the way, and it is
	// easier to add one later than to discover why a task will not move.
	Transitions map[string][]string `yaml:"transitions,omitempty"`
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

	return Parse(raw)
}

// Parse reads a configuration from bytes, for the callers that have the content
// without having a directory — reading a vault at a point in history, where the
// file comes out of git rather than off the disk.
func Parse(raw []byte) (*Config, error) {
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
	var body bytes.Buffer
	body.WriteString("# The projects this vault holds, and the vocabulary they share.\n")
	body.WriteString("# A key is PROJECT-NUMBER and the project is a folder at the root.\n")
	body.WriteString("# transitions is the workflow; leaving it out means anything can move\n")
	body.WriteString("# to anything.\n")

	enc := yaml.NewEncoder(&body)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, FileName), body.Bytes(), 0o644)
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
	if err := c.validateRelations(); err != nil {
		return err
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

	for from, targets := range c.Transitions {
		if !statuses[from] {
			return fmt.Errorf("transitions name %q, which is not a status", from)
		}
		for _, to := range targets {
			if !statuses[to] {
				return fmt.Errorf("%q is allowed to move to %q, which is not a status", from, to)
			}
		}
	}

	if len(c.Types) == 0 {
		return errors.New("no task types")
	}
	if len(c.Priorities) == 0 {
		return errors.New("no priorities")
	}
	named := map[string]bool{}
	for i := range c.Fields {
		f := &c.Fields[i]
		f.Name = strings.TrimSpace(f.Name)
		f.Kind = strings.ToLower(strings.TrimSpace(f.Kind))

		if !FieldName.MatchString(f.Name) {
			return fmt.Errorf("field %q is not a usable property name: lower case, digits and "+
				"underscores, starting with a letter", f.Name)
		}
		if OwnedProperties[f.Name] {
			return fmt.Errorf("field %q is a property the format already owns, and a second "+
				"meaning for it is one the board cannot read", f.Name)
		}
		if named[f.Name] {
			return fmt.Errorf("field %q is declared twice", f.Name)
		}
		named[f.Name] = true

		if !knownKind(f.Kind) {
			return fmt.Errorf("field %q is of kind %q, want one of %s",
				f.Name, f.Kind, strings.Join(FieldKinds, ", "))
		}
		if f.Kind == FieldChoice && len(f.Choices) == 0 {
			return fmt.Errorf("field %q is a choice and offers nothing to choose", f.Name)
		}
		if f.Kind != FieldChoice && len(f.Choices) > 0 {
			return fmt.Errorf("field %q is of kind %q and has choices, which only a choice "+
				"field has", f.Name, f.Kind)
		}
		for _, t := range f.Types {
			if !c.HasType(t) {
				return fmt.Errorf("field %q is for type %q, which this vault does not have",
					f.Name, t)
			}
		}
	}

	if c.Estimates != nil {
		if strings.TrimSpace(c.Estimates.Unit) == "" {
			return errors.New("estimates have no unit: a number without one says nothing")
		}
		c.Estimates.Unit = strings.TrimSpace(c.Estimates.Unit)
		seen := map[float64]bool{}
		for _, v := range c.Estimates.Scale {
			if v < 0 {
				return fmt.Errorf("the estimate scale holds %s, and work cannot be "+
					"smaller than nothing", Amount(v))
			}
			if seen[v] {
				return fmt.Errorf("the estimate scale holds %s twice", Amount(v))
			}
			seen[v] = true
		}
	}
	return nil
}

func knownKind(kind string) bool {
	for _, k := range FieldKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// FieldsFor is the fields a task of this type carries, in the order declared.
func (c *Config) FieldsFor(taskType string) []Field {
	var out []Field
	for _, f := range c.Fields {
		if f.AppliesTo(taskType) {
			out = append(out, f)
		}
	}
	return out
}

// FieldNamed is the declaration for a property, if the vault has one.
func (c *Config) FieldNamed(name string) (Field, bool) {
	for _, f := range c.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Sizes reports whether this vault sizes work at all.
func (c *Config) Sizes() bool { return c.Estimates != nil }

// Unit is the word for one estimate, or empty when the vault does not size
// work.
func (c *Config) Unit() string {
	if c.Estimates == nil {
		return ""
	}
	return c.Estimates.Unit
}

// EstimateScale is the values that may be used, or nil for any number.
func (c *Config) EstimateScale() []float64 {
	if c.Estimates == nil {
		return nil
	}
	return c.Estimates.Scale
}

// OnScale reports whether a value is one the vault offers. Always true when
// there is no scale.
func (c *Config) OnScale(value float64) bool {
	scale := c.EstimateScale()
	if len(scale) == 0 {
		return true
	}
	for _, v := range scale {
		if v == value {
			return true
		}
	}
	return false
}

// Amount writes an estimate the way somebody typed it: 3 rather than 3.0, and
// 0.5 kept.
//
// Fractions matter for a vault counting days, and a trailing zero on every
// whole number makes a board of points look like a spreadsheet.
func Amount(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
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

// DeviceClientID is the application to sign people in as, or empty.
func (c *Config) DeviceClientID() string {
	if c.SignIn == nil {
		return ""
	}
	return strings.TrimSpace(c.SignIn.DeviceClientID)
}

// HostKind is which API is behind this repository's remote, or empty when the
// hostname says so by itself.
func (c *Config) HostKind() string {
	if c.SignIn == nil {
		return ""
	}
	return strings.TrimSpace(c.SignIn.Kind)
}

// HostAPI is where this repository's host serves its API, or empty for the
// convention.
func (c *Config) HostAPI() string {
	if c.SignIn == nil {
		return ""
	}
	return strings.TrimSpace(c.SignIn.API)
}

// SetHost records what kind of host vouches for this repository.
func (c *Config) SetHost(kind, api string) {
	kind, api = strings.TrimSpace(kind), strings.TrimSpace(api)
	if kind == "" && api == "" {
		if c.SignIn != nil {
			c.SignIn.Kind, c.SignIn.API = "", ""
			if c.SignIn.DeviceClientID == "" {
				c.SignIn = nil
			}
		}
		return
	}
	if c.SignIn == nil {
		c.SignIn = &SignIn{}
	}
	c.SignIn.Kind, c.SignIn.API = kind, api
}

// SetDeviceClientID records it, or forgets it when given nothing. Forgetting
// drops the whole section when nothing else is in it, so the file says either
// something or nothing.
func (c *Config) SetDeviceClientID(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		if c.SignIn != nil {
			c.SignIn.DeviceClientID = ""
			if c.SignIn.Kind == "" && c.SignIn.API == "" {
				c.SignIn = nil
			}
		}
		return
	}
	if c.SignIn == nil {
		c.SignIn = &SignIn{}
	}
	c.SignIn.DeviceClientID = id
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

// CanMove reports whether the workflow allows a move.
//
// A vault with no workflow allows everything. Moving a task to the status it
// already has is always allowed: it is not a move.
func (c *Config) CanMove(from, to string) bool {
	if from == to || len(c.Transitions) == 0 {
		return true
	}
	for _, allowed := range c.Transitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Reachable lists the statuses a task in this one can move to, in board order,
// including the one it is already in.
func (c *Config) Reachable(from string) []Status {
	var out []Status
	for _, s := range c.Statuses {
		if c.CanMove(from, s.Name) {
			out = append(out, s)
		}
	}
	return out
}

// RenameInTransitions keeps the workflow pointing at a status that was renamed.
func (c *Config) RenameInTransitions(was, now string) {
	if len(c.Transitions) == 0 {
		return
	}
	renamed := make(map[string][]string, len(c.Transitions))
	for from, targets := range c.Transitions {
		if from == was {
			from = now
		}
		moved := make([]string, 0, len(targets))
		for _, to := range targets {
			if to == was {
				to = now
			}
			moved = append(moved, to)
		}
		renamed[from] = moved
	}
	c.Transitions = renamed
}

// DropFromTransitions removes a status that no longer exists.
func (c *Config) DropFromTransitions(name string) {
	if len(c.Transitions) == 0 {
		return
	}
	delete(c.Transitions, name)
	for from, targets := range c.Transitions {
		kept := targets[:0]
		for _, to := range targets {
			if to != name {
				kept = append(kept, to)
			}
		}
		c.Transitions[from] = kept
	}
}

// FirstStatus is the status a new task starts in: the first one listed, which
// is also the leftmost column of the board.
func (c *Config) FirstStatus() Status { return c.Statuses[0] }

// HasType reports whether the vault defines a task type.
// HasPriority reports whether the vault defines a priority.
func (c *Config) HasPriority(v string) bool { return contains(c.Priorities, v) }

// DefaultPriority is the middle of the road: "normal" when the vault has it,
// otherwise the middle of the list.
//
// The first one listed would be wrong, and quietly. Priorities are written in
// order, so the first is an extreme — a vault that lists низкий, обычный,
// высокий, критичный would have made every task it created низкий, and a vault
// that lists them the other way up would have made every task критичный.
// Neither is what "no priority given" means. The middle is, and it is what
// "normal" means in the vault that has the word.
func (c *Config) DefaultPriority() string {
	if c.HasPriority("normal") {
		return "normal"
	}
	return c.Priorities[(len(c.Priorities)-1)/2]
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

// TaskKeyPattern is the shape of a task key: the project key, a hyphen, and a
// number. One spelling, used in the frontmatter, in the file name, in links and
// in URLs — see ADR-0005.
var TaskKeyPattern = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-([1-9][0-9]*)$`)

// SplitKey takes a task key apart.
func SplitKey(key string) (projectKey string, number int, err error) {
	m := TaskKeyPattern.FindStringSubmatch(key)
	if m == nil {
		return "", 0, fmt.Errorf("key %q is not PROJECT-NUMBER", key)
	}
	number, err = strconv.Atoi(m[2])
	if err != nil {
		return "", 0, fmt.Errorf("key %q does not end in a task number", key)
	}
	return m[1], number, nil
}

// Key builds a task key from its parts.
func Key(projectKey string, number int) string {
	return fmt.Sprintf("%s-%d", projectKey, number)
}
