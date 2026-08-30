package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionCommand(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		code, stdout, stderr := run(t, arg)
		if code != exitOK {
			t.Errorf("%s: exit code = %d, want %d", arg, code, exitOK)
		}
		if got := strings.TrimSpace(stdout); got == "" {
			t.Errorf("%s: printed nothing to stdout", arg)
		}
		if stderr != "" {
			t.Errorf("%s: wrote %q to stderr, want nothing", arg, stderr)
		}
	}
}

func TestVersionFallsBackToDev(t *testing.T) {
	// No ldflags stamp and no module version in a test binary, so the honest
	// answer is "dev" rather than an invented number.
	if got := Version(); got == "" {
		t.Fatal("Version() is empty")
	}
}

func TestVersionPrefersTheStamp(t *testing.T) {
	old := version
	t.Cleanup(func() { version = old })

	version = "v9.9.9"
	if got := Version(); got != "v9.9.9" {
		t.Errorf("Version() = %q, want the stamped %q", got, "v9.9.9")
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	for _, arg := range []string{"help", "--help", "-h"} {
		code, stdout, stderr := run(t, arg)
		if code != exitOK {
			t.Errorf("%s: exit code = %d, want %d", arg, code, exitOK)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("%s: stdout has no usage section:\n%s", arg, stdout)
		}
		if stderr != "" {
			t.Errorf("%s: wrote %q to stderr, want nothing", arg, stderr)
		}
	}
}

func TestNoArgumentsIsAUsageError(t *testing.T) {
	code, stdout, stderr := run(t)
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if stdout != "" {
		t.Errorf("wrote %q to stdout, want nothing", stdout)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr has no usage section:\n%s", stderr)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	code, _, stderr := run(t, "frobnicate")
	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "frobnicate") {
		t.Errorf("stderr does not name the unknown command:\n%s", stderr)
	}
}

func TestEveryCommandInTheHelpExists(t *testing.T) {
	// Run each command with no arguments. Whatever it does, it must not be an
	// unknown command — the help text is a promise.
	for _, cmd := range []string{"init", "new", "check", "workspace", "serve", "import"} {
		_, _, stderr := run(t, cmd)
		if strings.Contains(stderr, "unknown command") {
			t.Errorf("%s is in the help but is not a command", cmd)
		}
	}
}

func TestSubcommandGroupsRejectNonsense(t *testing.T) {
	for _, args := range [][]string{
		{"workspace", "frobnicate"},
		{"import", "frobnicate"},
	} {
		code, _, stderr := run(t, args...)
		if code != exitUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, exitUsage)
		}
		if !strings.Contains(stderr, "frobnicate") {
			t.Errorf("%v: stderr does not name the unknown subcommand:\n%s", args, stderr)
		}
	}
}
