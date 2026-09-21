package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/didenkolab/docket/internal/access"
	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/project"
	"github.com/didenkolab/docket/internal/server"
	"github.com/didenkolab/docket/internal/space"
	"github.com/didenkolab/docket/internal/vault"
)

const serveUsage = `docket serve — a board and an API over a vault.

Usage:
  docket serve [flags] [directory]

A second client to the same files. Obsidian, an agent and this server can be
pointed at one repository at once: nothing is cached, and a write that would
land on top of a change made elsewhere is refused instead.

People sign in with a token for the git host that already holds the repository,
and what they may do is what that host says they may do. docket keeps no users
of its own — the repository is the trust boundary, and access is granted where
it is enforced. Flags:
`

func runServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, serveUsage)
		flags.PrintDefaults()
	}

	addr := flags.String("addr", "127.0.0.1:8080", "address to listen on")
	author := flags.String("author", "", `who unauthenticated writes are attributed to, as "Name <email>"`)
	auth := flags.String("auth", "auto", "auto, git, or none")
	host := flags.String("host", "", "which host the remote is: "+strings.Join(access.Kinds, ", ")+
		" (needed only for a self-hosted one)")
	api := flags.String("api", "", "the host's API base URL (needed only for a self-hosted one)")
	recheck := flags.Duration("recheck", 5*time.Minute,
		"how often to re-ask the host about a signed-in person, and so how long a revocation takes")
	life := flags.Duration("session", 12*time.Hour, "how long a session lasts")
	proxied := flags.Bool("behind-proxy", false,
		"a reverse proxy sits in front, so rate limits follow the client it names "+
			"rather than the proxy")
	template := flags.String("template", "",
		"the repository a new project is scaffolded from\n"+
			"    \t(default "+vault.DefaultTemplate+")")
	programs := flags.Bool("programs", false,
		"run the programs this vault declares — its reactions, pages and panels\n"+
			"    \t(off unless said here: a repository can declare a program, and only\n"+
			"    \twhoever starts the server can agree to execute it on this machine)")
	clientID := flags.String("device-client-id", "",
		"override the OAuth application people sign in with, for this server only\n"+
			"    \t(public, not a secret; normally set on the Access page and kept in docket.yaml)")

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	start, code := oneDirectory(flags, "serve", stderr)
	if code != exitOK {
		return code
	}

	// A vault or a workspace of them — the server reads both, so this only has
	// to find which one is here.
	sp, err := space.Open(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}
	root := sp.Root

	// Signing in asks the repository's git host who somebody is. A workspace is
	// not a repository, so the question goes to the first project in it: they
	// are the team's repositories, and one of them is as good as another for
	// asking who the team is.
	askHost := root
	if sp.Workspace {
		askHost = sp.Vaults()[0].Root
	}
	gitHost, code := resolveHost(*auth, *host, *api, askHost, stdout, stderr)
	if code != exitOK {
		return code
	}

	who := gitvcs.Author{Name: "docket", Email: "docket@localhost"}
	if *author != "" {
		parsed, err := gitvcs.ParseAuthor(*author)
		if err != nil {
			fmt.Fprintf(stderr, "docket serve: %v\n", err)
			return exitUsage
		}
		who = parsed
	} else if gitHost == nil {
		fmt.Fprint(stderr, "docket serve: --author is required when nobody signs in\n\n")
		fmt.Fprint(stderr, "Every write becomes a git commit, and a commit needs someone to\n")
		fmt.Fprint(stderr, "answer for it. There is no default worth guessing.\n")
		return exitUsage
	}

	s, err := server.New(root, server.Options{
		Author:      who,
		Host:        gitHost,
		Recheck:     *recheck,
		SessionLife: *life,
		BehindProxy: *proxied,
		// --auth none means nobody signs in, even though the repositories
		// could say who vouches for them.
		Unauthenticated: *auth == "none",
		// Reachable only from this machine, so the credentials already on it
		// may be used. --behind-proxy takes it away again, inside New.
		OnLoopback: onLoopback(*addr),

		DeviceClientID: deviceClientID(*clientID),
		Template:       *template,
		Programs:       *programs,
	})
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}

	what := "vault"
	if sp.Workspace {
		what = fmt.Sprintf("workspace of %d", len(sp.Vaults()))
	}
	fmt.Fprintf(stdout, "Serving %s (%s) on http://%s\n", root, what, listener.Addr())
	if len(sp.Missing) > 0 {
		fmt.Fprintf(stdout, "Not cloned, so not served: %s. Run docket workspace sync.\n",
			strings.Join(sp.Missing, ", "))
	}
	if gitHost != nil {
		fmt.Fprintf(stdout, "Sign in with a %s token for %s. Access is whatever that host says "+
			"it is, re-checked every %s.\n", gitHost.Name(), gitHost.Repository(), recheck.String())
	} else {
		fmt.Fprintf(stdout, "No sign-in: anyone who can reach this can write, and every change "+
			"is attributed to %s.\n", who)
	}

	// Timeouts, because the default is none: a connection that sends a header
	// slowly and never finishes holds a goroutine until the process dies, and
	// enough of them are the whole attack.
	//
	// Writes get longer than reads because one of them is a git commit, and a
	// repository with a lot of history takes its time.
	httpd := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// A write is a file and then a commit. Killed between the two, the vault
	// has a change git never saw — so an interrupt stops taking new requests
	// and lets the ones in flight finish.
	stopping := make(chan os.Signal, 1)
	signal.Notify(stopping, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-stopping
		fmt.Fprintln(stdout, "\nFinishing what is in flight, then stopping.")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = httpd.Shutdown(ctx)
		close(done)
	}()

	if err := httpd.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}
	<-done
	return exitOK
}

// resolveHost decides who answers "may this person write".
//
// `auto` is the default and is deliberately quiet about failing: a vault with
// no remote, or a remote on a host docket does not recognise, still has to be
// servable. It says which it chose either way, because a tracker that looks
// authenticated and is not is worse than one that says it is open.
func resolveHost(auth, kind, api, root string, stdout, stderr io.Writer) (access.Host, int) {
	switch auth {
	case "none":
		return nil, exitOK

	case "git", "auto":
		// The flag wins, then the vault's own docket.yaml. A self-hosted host has
		// to be named because a hostname does not say what API is behind it —
		// and the place to name it is the repository, so that cloning the vault
		// is enough. That is what sign_in.kind is for, and the server was the
		// one path that never read it: it refused to start on a vault that had
		// said, in its own configuration, exactly what it needed to say.
		if kind == "" || api == "" {
			if c, err := project.Load(root); err == nil {
				if kind == "" {
					kind = c.HostKind()
				}
				if api == "" {
					api = c.HostAPI()
				}
			}
		}

		host, err := access.FromRepository(root, kind, api)
		if err == nil {
			return host, exitOK
		}
		if auth == "git" {
			fmt.Fprintf(stderr, "docket serve: --auth git, but %v\n", err)
			return nil, exitError
		}
		fmt.Fprintf(stdout, "No sign-in available: %v\n", err)
		return nil, exitOK

	default:
		fmt.Fprintf(stderr, "docket serve: --auth %q: want auto, git or none\n", auth)
		return nil, exitUsage
	}
}

// deviceClientID is an OAuth application named for this server rather than for
// the vault.
//
// The flag wins, then the environment. Neither being set is the normal case:
// the id then comes out of docket.yaml, where the Access page writes it, so it
// travels with the repository and nobody has to remember a flag. This is the
// override for somebody who wants a different application on one server.
func deviceClientID(flag string) string {
	if flag = strings.TrimSpace(flag); flag != "" {
		return flag
	}
	return strings.TrimSpace(os.Getenv("DOCKET_DEVICE_CLIENT_ID"))
}

// onLoopback reports whether an address is reachable only from this machine.
//
// A hostname is not resolved: this decides whether to trust "you can reach this
// port" as authentication, and a name that resolves to loopback today may not
// tomorrow. Only the two literal loopback addresses count.
func onLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}
