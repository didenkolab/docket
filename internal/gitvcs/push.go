package gitvcs

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Sending what was written to where everybody else will read it.
//
// The board commits every write, and until today that was where it stopped: the
// commits sat in the clone the server happens to be running over, and nobody
// else saw them. That undoes the premise — a project is a repository you hand
// over as a clone — because the clone somebody takes is missing everything the
// board did.
//
// The interesting part is the credential. Pushing over HTTPS needs one, and
// there are several ways to give git a token, most of them wrong:
//
//   - in the URL (https://token@host/...) — the URL is in argv, and argv is
//     readable by every process on the machine;
//   - in a credential.helper that echoes it — the same problem, the token is
//     in the command line;
//   - in a file a helper reads — the token touches disk, which the sign-in page
//     promises it never does.
//
// So: the helper is a shell snippet that reads an environment variable, and
// only the *name* of the variable is in argv. A process's environment is
// readable by its own user, which is the user running the server; it is never
// written anywhere and goes away with the process.
//
// The configured helpers are cleared first. Without that, git would consult the
// keychain — and worse, a helper with `store` behaviour could write the token
// somewhere on our behalf.

// ErrNotFastForward means somebody else pushed first, so what is here is not a
// continuation of what is there.
var ErrNotFastForward = errors.New("the remote has moved on since this clone")

// ErrNoUpstream means the branch does not track anything, so there is nowhere
// obvious to push it.
var ErrNoUpstream = errors.New("this branch tracks nothing")

// Credential is how to prove to a host that a push is allowed.
//
// User is whatever the host wants in the username field beside a token —
// "x-access-token" on GitHub, "oauth2" on GitLab. Empty means let git find a
// credential the way it does for a person at a terminal, which is what happens
// when nobody has signed in and the machine's own helper is the right answer.
type Credential struct {
	User  string
	Token string
}

// Push sends the current branch to its upstream.
func (r *Repo) Push(cred Credential) error {
	branch := r.Current()
	if branch == "" {
		return errors.New("not on a branch, so there is nothing to push")
	}

	args := append(PushArgs(cred), "push", "origin", branch)
	if _, err := r.outputWithEnv(PushEnv(cred), args...); err != nil {
		return classify(err)
	}
	return nil
}

// PushArgs is the configuration git is given, and it is a separate function
// because what is in it is the point: the name of an environment variable, and
// never the token itself. Exported because cloning needs the same thing.
func PushArgs(cred Credential) []string {
	if cred.Token == "" {
		return nil
	}
	return []string{
		// The empty value clears whatever helpers are configured, so this token
		// is the only one considered and no helper can store it for us.
		"-c", "credential.helper=",
		"-c", `credential.helper=!f() { ` +
			`printf 'username=%s\npassword=%s\n' "$DOCKET_PUSH_USER" "$DOCKET_PUSH_TOKEN"; ` +
			`}; f`,
	}
}

// PushEnv is where the token actually travels: a process's environment, which
// only the user running it can read, and which is never written anywhere.
func PushEnv(cred Credential) []string {
	env := []string{
		// Nothing may ask a human: there is nobody at the other end of an HTTP
		// handler, and a prompt would hang the request until it timed out.
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	}
	if cred.Token != "" {
		env = append(env,
			"DOCKET_PUSH_USER="+orDefault(cred.User, "x-access-token"),
			"DOCKET_PUSH_TOKEN="+cred.Token,
		)
	}
	return env
}

// Unpushed is how many commits are here and not on the upstream.
//
// Zero and no error means in step. ErrNoUpstream means the question does not
// apply — a branch nobody has pushed yet — which is a different thing from
// being in step and is worth saying differently.
func (r *Repo) Unpushed() (int, error) {
	if _, err := r.output("rev-parse", "--abbrev-ref", "@{upstream}"); err != nil {
		return 0, ErrNoUpstream
	}
	out, err := r.output("rev-list", "--count", "@{upstream}..HEAD")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// HasRemote reports whether there is anywhere to push at all.
func (r *Repo) HasRemote() bool {
	out, err := r.output("remote")
	return err == nil && strings.TrimSpace(out) != ""
}

// classify turns git's refusal into the one distinction that changes what to do
// next: somebody else pushed first, or something else went wrong.
func classify(err error) error {
	text := strings.ToLower(err.Error())
	for _, sign := range []string{"non-fast-forward", "fetch first", "rejected"} {
		if strings.Contains(text, sign) {
			return fmt.Errorf("%w: %v", ErrNotFastForward, err)
		}
	}
	return err
}

// outputWithEnv runs git with extra environment. It exists so that Push can
// hand over a token without it appearing in the command line.
func (r *Repo) outputWithEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	cmd.Env = append(cmd.Environ(), env...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// The output may quote the remote URL, and a URL is not secret — but
		// the token never appears in either, because it was never an argument.
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// Start makes dir a repository on a default branch, pointed at remote.
//
// For a project whose repository was just created on a host and is empty: there
// is nothing to clone, so the local side is made first and pushed.
func Start(dir, remote string) error {
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", remote},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %s", strings.Join(args, " "),
				strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// PushNew sends a branch that has no upstream yet and sets one.
//
// Push refuses to guess where a branch belongs; this is the one case where the
// answer is not a guess, because the remote was made for it a moment ago.
func (r *Repo) PushNew(cred Credential) error {
	branch := r.Current()
	if branch == "" {
		return errors.New("not on a branch, so there is nothing to push")
	}
	args := append(PushArgs(cred), "push", "-u", "origin", branch)
	if _, err := r.outputWithEnv(PushEnv(cred), args...); err != nil {
		return classify(err)
	}
	return nil
}

// Looking the other way.
//
// Every write here is a commit and every commit is sent, and that was the whole
// of it: the board pushed and never once looked at what had arrived. A vault is
// a repository, so the other ways in are ordinary — somebody editing in Obsidian
// on another machine, a merged proposal, an agent working in a clone — and the
// board went on drawing a board that was out of date with no sign that it was.

// Waiting is how many commits the remote has that this clone does not.
//
// It fetches, because the question cannot be answered from what is already
// here: a stale remote-tracking ref answers about the last time somebody looked.
// Fetching moves nothing in the working tree, so it is safe to do on a timer.
func (r *Repo) Waiting(cred Credential) (int, error) {
	upstream, err := r.output("rev-parse", "--abbrev-ref", "@{upstream}")
	if err != nil {
		return 0, ErrNoUpstream
	}
	upstream = strings.TrimSpace(upstream)

	args := append(PushArgs(cred), "fetch", "--quiet")
	if _, err := r.outputWithEnv(PushEnv(cred), args...); err != nil {
		return 0, classify(err)
	}

	out, err := r.output("rev-list", "--count", "HEAD.."+upstream)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// ErrWouldDiverge means the remote has moved and so has this clone, so taking
// what arrived is a merge rather than a fast-forward.
var ErrWouldDiverge = errors.New("both sides have commits the other does not")

// ErrNotClean means the working tree has changes that a fast-forward would have
// to overwrite.
var ErrNotClean = errors.New("the working tree has uncommitted changes")

// Take brings the remote's commits into this clone, and only when doing so
// changes nothing that is here.
//
// Fast-forward only, and deliberately. A merge of two plans is a decision — the
// same reasoning that stops the board rebasing when a push is refused — and
// this one would be made against a working tree somebody may have open in
// Obsidian. A divergence is reported with the two numbers and left to a person.
func (r *Repo) Take(cred Credential) (int, error) {
	waiting, err := r.Waiting(cred)
	if err != nil || waiting == 0 {
		return 0, err
	}
	ahead, err := r.Unpushed()
	if err != nil && !errors.Is(err, ErrNoUpstream) {
		return 0, err
	}
	if ahead > 0 {
		return waiting, ErrWouldDiverge
	}

	// A fast-forward rewrites files, and anything uncommitted in the way of one
	// is somebody's unsaved work. The board commits everything it writes, so
	// this is a person editing in the folder — which is exactly who must not
	// lose anything.
	if dirty, err := r.output("status", "--porcelain"); err == nil && strings.TrimSpace(dirty) != "" {
		return waiting, ErrNotClean
	}

	if _, err := r.output("merge", "--ff-only", "@{upstream}"); err != nil {
		return waiting, err
	}
	return waiting, nil
}
