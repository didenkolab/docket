package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Git runs git commands in a directory. It is an interface so that sync can be
// tested without a network or a real repository.
type Git interface {
	Run(dir string, args ...string) (string, error)
}

// ExecGit runs the git on PATH.
type ExecGit struct{}

// Run executes one git command and returns its combined output.
func (ExecGit) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %s",
			strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// What sync did to one project.
const (
	ActionCloned  = "cloned"
	ActionUpdated = "updated"
	ActionCurrent = "up to date"
	ActionSkipped = "skipped"
	ActionFailed  = "failed"
)

// Result is the outcome for one project.
type Result struct {
	Project Project
	Action  string
	Detail  string
}

func (r Result) String() string {
	line := fmt.Sprintf("%-10s %-12s %s", r.Project.Key, r.Action, r.Project.Path)
	if r.Detail != "" {
		line += "  — " + r.Detail
	}
	return line
}

// Sync brings every project in the manifest into the workspace: it clones what
// is missing and fast-forwards what is there.
//
// A project with uncommitted changes is reported and left alone. Stashing or
// resetting someone's work in progress to make a sync succeed is not a service,
// and a tool that does it once will be trusted with nothing afterwards.
//
// One project failing does not stop the others; every result comes back.
func Sync(root string, m *Manifest, git Git) []Result {
	results := make([]Result, 0, len(m.Projects))

	for _, p := range m.Projects {
		target := filepath.Join(root, filepath.FromSlash(p.Path))
		results = append(results, syncOne(root, target, p, git))
	}
	return results
}

func syncOne(root, target string, p Project, git Git) Result {
	info, err := os.Stat(target)
	switch {
	case os.IsNotExist(err):
		if _, err := git.Run(root, "clone", p.Remote, p.Path); err != nil {
			return Result{p, ActionFailed, err.Error()}
		}
		return Result{p, ActionCloned, p.Remote}

	case err != nil:
		return Result{p, ActionFailed, err.Error()}

	case !info.IsDir():
		return Result{p, ActionFailed, "the path exists and is not a directory"}
	}

	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		return Result{p, ActionFailed, "the directory exists but is not a git repository"}
	}

	status, err := git.Run(target, "status", "--porcelain")
	if err != nil {
		return Result{p, ActionFailed, err.Error()}
	}
	if strings.TrimSpace(status) != "" {
		return Result{p, ActionSkipped, "uncommitted changes — left untouched"}
	}

	out, err := git.Run(target, "pull", "--ff-only")
	if err != nil {
		return Result{p, ActionFailed, err.Error()}
	}
	if strings.Contains(out, "Already up to date") || strings.Contains(out, "up-to-date") {
		return Result{p, ActionCurrent, ""}
	}
	return Result{p, ActionUpdated, ""}
}

// Failed reports whether any project could not be synced.
func Failed(results []Result) bool {
	for _, r := range results {
		if r.Action == ActionFailed {
			return true
		}
	}
	return false
}
