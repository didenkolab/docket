// Command docket is the command-line tool for docket vaults.
//
// A vault works without it: tasks and pages are Markdown files, and anything
// this tool does can be done by hand. See the specification in docket-board.
package main

import (
	"os"

	"github.com/didenkolab/docket/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
