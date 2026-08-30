// Package gitvcs commits changes made through the server.
//
// Every write the server performs becomes a commit attributed to whoever made
// it. ADR-0001 says the change history of a task is its git history; a server
// that writes files without committing them would leave that history with
// holes exactly where the interesting changes are.
package gitvcs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git working tree.
type Repo struct {
	Root string
}

// Open returns the repository rooted at dir, or an error when dir is not one.
func Open(dir string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, fmt.Errorf("%s is not a git repository: the history of every change "+
			"lives in git, so serving a vault that is not under git would quietly lose it", dir)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git is not on PATH: %w", err)
	}
	return &Repo{Root: dir}, nil
}

// Author is who a commit is attributed to.
type Author struct {
	Name  string
	Email string
}

// ParseAuthor reads the usual "Name <email>" form.
func ParseAuthor(s string) (Author, error) {
	s = strings.TrimSpace(s)
	open := strings.LastIndex(s, "<")
	closed := strings.LastIndex(s, ">")

	if open < 1 || closed != len(s)-1 || closed < open {
		return Author{}, fmt.Errorf("author %q is not in the form \"Name <email>\"", s)
	}
	name := strings.TrimSpace(s[:open])
	email := strings.TrimSpace(s[open+1 : closed])
	if name == "" || email == "" {
		return Author{}, fmt.Errorf("author %q is missing a name or an email", s)
	}
	return Author{name, email}, nil
}

func (a Author) String() string { return fmt.Sprintf("%s <%s>", a.Name, a.Email) }

// Commit stages the given paths, relative to the repository root, and commits
// them. Paths that did not change produce no commit and no error.
func (r *Repo) Commit(paths []string, message string, author Author) error {
	if len(paths) == 0 {
		return nil
	}

	if err := r.run(append([]string{"add", "--"}, paths...)...); err != nil {
		return err
	}

	staged, err := r.output(append([]string{"diff", "--cached", "--name-only", "--"}, paths...)...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(staged) == "" {
		return nil // nothing actually changed
	}

	args := []string{
		"-c", "user.name=" + author.Name,
		"-c", "user.email=" + author.Email,
		"commit", "--only", "-m", message, "--",
	}
	return r.run(append(args, paths...)...)
}

func (r *Repo) run(args ...string) error {
	_, err := r.output(args...)
	return err
}

func (r *Repo) output(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
