package gitvcs

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A bare repository to push at, and a clone that tracks it. Local paths, so
// nothing here touches a network.
func clonePair(t *testing.T) (remote string, work *Repo) {
	t.Helper()

	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", remote).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}

	dir := filepath.Join(root, "work")
	run(t, root, "clone", "-q", remote, dir)
	run(t, dir, "config", "user.email", "test@example.com")
	run(t, dir, "config", "user.name", "Test")

	write(t, dir, "docket.yaml", "name: A vault\n")
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-q", "-m", "start")
	run(t, dir, "push", "-q", "-u", "origin", "main")

	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return remote, repo
}

func TestPushSendsWhatWasCommitted(t *testing.T) {
	remote, repo := clonePair(t)

	if n, err := repo.Unpushed(); err != nil || n != 0 {
		t.Fatalf("a fresh clone has %d unpushed, %v", n, err)
	}

	write(t, repo.Root, "one.md", "a task\n")
	if err := repo.Commit([]string{"one.md"}, "ACME-1: a task",
		Author{Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}

	if n, err := repo.Unpushed(); err != nil || n != 1 {
		t.Fatalf("after committing: %d unpushed, %v — the board would look in step", n, err)
	}
	if err := repo.Push(Credential{}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if n, err := repo.Unpushed(); err != nil || n != 0 {
		t.Errorf("after pushing: %d unpushed, %v", n, err)
	}

	// And it is really there, not merely reported as sent.
	out, err := exec.Command("git", "-C", remote, "log", "--format=%s", "-1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "ACME-1: a task" {
		t.Errorf("the remote's last commit is %q", got)
	}
}

// The whole point of the credential handling: a token must never be an
// argument, because argv is readable by every process on the machine.
func TestATokenIsNeverInTheCommandLine(t *testing.T) {
	_, repo := clonePair(t)
	const secret = "ghp_pretend_this_is_a_real_secret_token"

	// Push with a credential at a remote that will refuse it, and look at what
	// git was asked to do rather than at whether it worked.
	run(t, repo.Root, "remote", "set-url", "origin", "https://127.0.0.1:1/nowhere.git")
	err := repo.Push(Credential{User: "x-access-token", Token: secret})
	if err == nil {
		t.Fatal("a push to nowhere succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the token is in the error, so it is in the logs too:\n%v", err)
	}

	// The helper git is given names an environment variable; the value lives in
	// the environment, which only this user can read.
	args := pushArgs(Credential{User: "x-access-token", Token: secret})
	joined := strings.Join(args, " ")
	if strings.Contains(joined, secret) {
		t.Errorf("the token is in argv:\n%s", joined)
	}
	if !strings.Contains(joined, "DOCKET_PUSH_TOKEN") {
		t.Errorf("the helper does not read the token from the environment:\n%s", joined)
	}
	// And the configured helpers are cleared, so nothing can store it for us.
	if !strings.Contains(joined, "credential.helper= ") && !strings.Contains(joined, "credential.helper=\n") {
		if args[1] != "credential.helper=" {
			t.Errorf("the configured helpers are not cleared first:\n%s", joined)
		}
	}
}

// Somebody else pushing first is reported, not fixed. A rebase rewrites a
// working tree somebody may have open, so it is a decision rather than
// something a server does behind them.
func TestSomebodyElsePushingFirstIsReportedNotFixed(t *testing.T) {
	remote, repo := clonePair(t)

	// A second clone pushes something.
	other := filepath.Join(t.TempDir(), "other")
	run(t, filepath.Dir(other), "clone", "-q", remote, other)
	run(t, other, "config", "user.email", "sam@example.com")
	run(t, other, "config", "user.name", "Sam")
	write(t, other, "theirs.md", "their task\n")
	run(t, other, "add", "-A")
	run(t, other, "commit", "-q", "-m", "theirs")
	run(t, other, "push", "-q", "origin", "main")

	// And now this one tries.
	write(t, repo.Root, "ours.md", "our task\n")
	if err := repo.Commit([]string{"ours.md"}, "ours",
		Author{Name: "Dana", Email: "dana@example.com"}); err != nil {
		t.Fatal(err)
	}

	err := repo.Push(Credential{})
	if err == nil {
		t.Fatal("the push succeeded, which means it overwrote somebody's work")
	}
	if !errors.Is(err, ErrNotFastForward) {
		t.Errorf("the refusal is not recognised as the remote having moved: %v", err)
	}

	// Nothing was rebased, merged or stashed behind anybody's back.
	out, err := exec.Command("git", "-C", repo.Root, "log", "--format=%s", "-1").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "ours" {
		t.Errorf("the local history was changed: last commit is %q", got)
	}
}

// A branch tracking nothing is a different thing from being in step, and saying
// "0 to send" about it would be a lie.
func TestABranchThatTracksNothingSaysSo(t *testing.T) {
	_, repo := clonePair(t)
	run(t, repo.Root, "checkout", "-q", "-b", "proposal/something")

	if _, err := repo.Unpushed(); !errors.Is(err, ErrNoUpstream) {
		t.Errorf("Unpushed on an untracked branch returned %v", err)
	}
}

func TestARepositoryWithNoRemoteIsNotPushed(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	repo, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if repo.HasRemote() {
		t.Error("a repository with no remote claims to have one")
	}
}
