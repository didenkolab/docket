package cli

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/vadymdidenkolab/docket/internal/gitvcs"
	"github.com/vadymdidenkolab/docket/internal/project"
	"github.com/vadymdidenkolab/docket/internal/server"
)

const serveUsage = `docket serve — a board and an API over a vault.

Usage:
  docket serve [flags] [directory]

A second client to the same files. Obsidian, an agent and this server can be
pointed at one repository at once: nothing is cached, and a write that would
land on top of a change made elsewhere is refused instead. Flags:
`

func runServe(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, serveUsage)
		flags.PrintDefaults()
	}

	addr := flags.String("addr", "127.0.0.1:8080", "address to listen on")
	author := flags.String("author", "", `who writes are attributed to, as "Name <email>"`)

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	start, code := oneDirectory(flags, "serve", stderr)
	if code != exitOK {
		return code
	}
	if *author == "" {
		fmt.Fprint(stderr, "docket serve: --author is required\n\n")
		fmt.Fprint(stderr, "Every write becomes a git commit, and a commit needs someone to\n")
		fmt.Fprint(stderr, "answer for it. There is no default worth guessing.\n\n")
		flags.Usage()
		return exitUsage
	}

	who, err := gitvcs.ParseAuthor(*author)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitUsage
	}
	root, err := project.FindRoot(start)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}

	s, err := server.New(root, who)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}

	fmt.Fprintf(stdout, "Serving %s on http://%s as %s\n", root, listener.Addr(), who)
	if err := http.Serve(listener, s.Handler()); err != nil {
		fmt.Fprintf(stderr, "docket serve: %v\n", err)
		return exitError
	}
	return exitOK
}
