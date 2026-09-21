package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/didenkolab/docket/internal/gitvcs"
	"github.com/didenkolab/docket/internal/mcp"
)

const mcpUsage = `docket mcp — serve a vault to an agent over the Model Context Protocol.

Usage:
  docket mcp --author "Agent <agent@example.com>" [directory]

Speaks JSON-RPC on stdin and stdout, one object per line. Every write it makes
is a git commit attributed to the author given here, so the history says which
agent did what. Flags:
`

func runMCP(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, mcpUsage)
		flags.PrintDefaults()
	}
	author := flags.String("author", "", `who writes are attributed to, as "Name <email>"`)

	if err := flags.Parse(permute(flags, args)); err != nil {
		return exitUsage
	}
	start, code := oneDirectory(flags, "mcp", stderr)
	if code != exitOK {
		return code
	}
	if *author == "" {
		fmt.Fprint(stderr, "docket mcp: --author is required\n\n")
		fmt.Fprint(stderr, "Every write becomes a git commit, and an agent's commits should say\n")
		fmt.Fprint(stderr, "which agent made them.\n")
		return exitUsage
	}

	who, err := gitvcs.ParseAuthor(*author)
	if err != nil {
		fmt.Fprintf(stderr, "docket mcp: %v\n", err)
		return exitUsage
	}
	server, err := mcp.New(start, who)
	if err != nil {
		fmt.Fprintf(stderr, "docket mcp: %v\n", err)
		return exitError
	}

	// Progress goes to stderr: stdout is the protocol, and a stray line on it
	// is a parse error at the other end.
	fmt.Fprintf(stderr, "docket mcp: serving %s as %s\n", server.Root(), who)

	if err := server.Serve(os.Stdin, stdout); err != nil {
		fmt.Fprintf(stderr, "docket mcp: %v\n", err)
		return exitError
	}
	return exitOK
}
