// Package reaction runs what a vault asked to be run when something happens.
//
// Nineteen of the Jira marketplace's top hundred are automation, and fourteen
// more are integrations; both are the same shape — when this happens, do that.
// Here "do that" is a program, because the state is a git repository: a
// reaction reads the event, writes files, and the change is a commit like any
// other. Nothing needs an API to the data, and nothing needs a plugin runtime.
//
// The hard part is not running things. It is who gets to decide what runs.
//
// Git faces exactly this and answers it by not sharing hooks: .git/hooks is not
// in the repository, because a repository that could ship an executable would
// mean that cloning one runs it. But a team does want to agree on its
// automation, review it and keep it in history — so the two halves are split.
//
// The declaration is in the vault: which event, which program, under what
// condition. It is in docket.yaml, so it is a diff, it is reviewed, and everyone
// gets the same one.
//
// The consent is not. A server runs nothing unless it was started with
// --reactions, which is a decision made by whoever runs it, on the machine it
// would run on. Push access to a repository is not permission to execute code
// on somebody's laptop, and without that line the two would be the same thing.
//
// And what may be run is narrow: a path inside the repository, executed
// directly with no shell. No `sh -c`, so nothing a task's title contains can
// become part of a command.
package reaction

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Events a reaction can be asked for.
const (
	OnMoved   = "task.moved"
	OnCreated = "task.created"
	OnEdited  = "task.edited"
)

// Events is every event, for validation and for a message that lists them.
var Events = []string{OnMoved, OnCreated, OnEdited}

// Timeout is how long a reaction may take.
//
// A person is waiting: a card was dragged, and the page is not answered until
// the write is committed. A reaction that talks to a slow service should write
// a file and let something else do the talking.
const Timeout = 20 * time.Second

// Event is what happened, as a reaction is told it.
type Event struct {
	Event   string `json:"event"`
	Key     string `json:"key"`
	Project string `json:"project"`
	// Path is the task's file, relative to the vault root.
	Path  string `json:"path"`
	Title string `json:"title"`
	// From and To are the statuses, for a move. Empty otherwise.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Who is the person the change was attributed to, and When is when.
	Who  string `json:"who"`
	When string `json:"when"`
	// Root is the vault on disk, so a reaction can read anything else it needs
	// without being handed the whole vault.
	Root string `json:"root"`
}

// Declared is one reaction as the vault writes it.
type Declared struct {
	// On is the event: task.moved, task.created, task.edited.
	On string `yaml:"on"`
	// Run is the program, as a path inside the repository.
	Run string `yaml:"run"`
	// Name is what to call it in a commit message and in a log. Defaults to the
	// program's file name.
	Name string `yaml:"name,omitempty"`
	// Status narrows a move to one destination — the common case by a mile:
	// "when something reaches Done".
	Status string `yaml:"status,omitempty"`
	// Project narrows it to one project in a workspace.
	Project string `yaml:"project,omitempty"`
}

// Called is what to call this reaction.
func (d Declared) Called() string {
	if strings.TrimSpace(d.Name) != "" {
		return d.Name
	}
	return filepath.Base(d.Run)
}

// Wants reports whether this reaction is about that event.
func (d Declared) Wants(e Event) bool {
	if d.On != e.Event {
		return false
	}
	if d.Status != "" && !strings.EqualFold(d.Status, e.To) {
		return false
	}
	if d.Project != "" && !strings.EqualFold(d.Project, e.Project) {
		return false
	}
	return true
}

// Validate reports what is wrong with a declaration, before anything runs.
func (d Declared) Validate() error {
	known := false
	for _, e := range Events {
		if d.On == e {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%q is not an event: it is one of %s", d.On, strings.Join(Events, ", "))
	}

	run := strings.TrimSpace(d.Run)
	switch {
	case run == "":
		return fmt.Errorf("a reaction with nothing to run")
	case filepath.IsAbs(run):
		return fmt.Errorf("%q is an absolute path: a reaction runs a program in the "+
			"repository, so that what runs is what was reviewed", run)
	case strings.HasPrefix(filepath.ToSlash(filepath.Clean(run)), "../"):
		return fmt.Errorf("%q leads outside the repository", run)
	case strings.ContainsAny(run, " \t|&;<>$`"):
		return fmt.Errorf("%q looks like a shell command. A reaction is a program, run "+
			"directly with no shell — put the pipeline in a script and name the script", run)
	}
	return nil
}

// Result is what a reaction did.
type Result struct {
	Name string
	// Output is what it printed, trimmed. Kept because a reaction that refuses
	// has to be able to say why.
	Output string
	Err    error
}

// Run runs every reaction that wants this event, in the order they are
// declared, and reports what each did.
//
// One at a time on purpose: two reactions writing the same file at once is a
// race nobody can debug, and the order in the file is a decision somebody made.
func Run(ctx context.Context, root string, declared []Declared, e Event) []Result {
	var out []Result
	for _, d := range declared {
		if !d.Wants(e) {
			continue
		}
		out = append(out, run(ctx, root, d, e))
	}
	return out
}

func run(ctx context.Context, root string, d Declared, e Event) Result {
	result := Result{Name: d.Called()}
	if err := d.Validate(); err != nil {
		result.Err = err
		return result
	}
	said, err := Output(ctx, root, d.Run, e, Timeout)
	result.Output = said
	if err != nil {
		result.Err = fmt.Errorf("%s: %w", d.Called(), err)
	}
	return result
}

// Posted runs a program with bytes on stdin rather than an event.
//
// The same rules and the same one place: what arrives from outside is a body
// somebody else wrote, and handing it to a shell would be handing a stranger a
// command line.
func Posted(ctx context.Context, root, run string, body []byte, limit time.Duration) (string, error) {
	return output(ctx, root, run, body, nil, limit)
}

// Output runs a program in the repository and returns what it printed.
//
// The one place a vault's own program is executed, so the rules live here and
// nothing else has to remember them: a path inside the repository, an
// executable file, no shell, and everything the program is told arrives on
// stdin as JSON. A page that renders a program's output and a reaction that
// writes a file are the same act with different consequences, and they must not
// be able to disagree about what is safe.
func Output(ctx context.Context, root, run string, told any, limit time.Duration) (string, error) {
	if err := (Declared{On: OnMoved, Run: run}).Validate(); err != nil {
		return "", err
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(run)))
	if err != nil {
		return "", fmt.Errorf("%s: %w", run, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s is not an executable file — chmod +x it", run)
	}

	body, err := json.Marshal(told)
	if err != nil {
		return "", err
	}
	return output(ctx, root, run, body, told, limit)
}

// output is the one place a vault's own program is executed.
func output(ctx context.Context, root, run string, body []byte, told any, limit time.Duration) (string, error) {
	program := filepath.Join(root, filepath.FromSlash(run))

	ctx, stop := context.WithTimeout(ctx, limit)
	defer stop()

	// No shell, and one argument: the program itself. Everything it is told
	// arrives on stdin as JSON, so nothing a person typed into a title can
	// become part of a command line.
	cmd := exec.CommandContext(ctx, program)
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(body)
	cmd.Env = append(os.Environ(), "DOCKET_ROOT="+root)
	// The tool itself, so a program can ask it for data rather than parsing the
	// vault again. This is what keeps a report an app: the hard reading —
	// history, renames, the vault's vocabulary — stays in one tested place, and
	// the app formats what it prints.
	if self, err := os.Executable(); err == nil {
		cmd.Env = append(cmd.Env, "DOCKET_BIN="+self)
	}
	if e, ok := told.(Event); ok {
		cmd.Env = append(cmd.Env, "DOCKET_EVENT="+e.Event)
	}

	var said bytes.Buffer
	cmd.Stdout, cmd.Stderr = &said, &said
	err := cmd.Run()
	return strings.TrimSpace(said.String()), err
}
