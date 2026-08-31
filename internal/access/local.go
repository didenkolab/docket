package access

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Signing in with the credentials already on this machine.
//
// A board somebody runs on their own laptop, over a repository they cloned,
// asks them to paste a token for a host they are already authenticated to. The
// token is in the keychain; git uses it every time they push. Asking for it
// again is asking somebody to look up something they already have.
//
// So: ask git. `git credential fill` is the same question git asks itself
// before a push, and it answers from whatever credential helper is configured —
// the macOS keychain, libsecret, the gh helper, a plain file. Nothing here
// knows or cares which.
//
// This is only ever offered on a loopback listener that is not behind a proxy,
// because it is authentication by "you can reach this port". See
// server.Options.OnLoopback for why the proxy case has to be excluded
// explicitly.

// ErrNoLocalCredentials means the machine has nothing for that host.
var ErrNoLocalCredentials = errors.New("no credentials for that host on this machine")

// LocalToken asks the machine for a token for a host.
//
// git first, because it is the tool that owns the credential and works with
// every helper. `gh` second, because somebody may have authenticated with it
// and never configured it as a git helper, which is a normal state to be in.
func LocalToken(ctx context.Context, host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", ErrNoLocalCredentials
	}

	if token, err := fromGitCredential(ctx, host); err == nil && token != "" {
		return token, nil
	}
	if token, err := fromGitHubCLI(ctx, host); err == nil && token != "" {
		return token, nil
	}
	return "", ErrNoLocalCredentials
}

// fromGitCredential asks git the question it asks itself before a push.
func fromGitCredential(ctx context.Context, host string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	// Nothing may prompt. A helper that decides to ask a human would hang a
	// request forever, and there is no human at the other end of an HTTP
	// handler.
	cmd.Env = append(cmd.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
	)

	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// The answer is key=value lines. The token is the password; the username is
	// whatever the helper stored and is not used — the host is asked who the
	// token belongs to, rather than being told.
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		if password, ok := strings.CutPrefix(scanner.Text(), "password="); ok {
			return strings.TrimSpace(password), nil
		}
	}
	return "", ErrNoLocalCredentials
}

// fromGitHubCLI asks gh, for somebody who authenticated with it and never made
// it a git helper.
func fromGitHubCLI(ctx context.Context, host string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", host).Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
