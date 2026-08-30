# docket

A task tracker and a knowledge base that live in a git repository as Markdown files and open
in Obsidian as a board and a wiki.

Built for agents first. An agent creates a task by writing a file and moves it by editing two
lines — no API to call, no schema it cannot read. The same folder, opened in Obsidian, is a
board with columns, a backlog, a linked wiki and a graph. Neither view is an export of the
other; there is one set of files. Git is the history, and `git clone` is the export.

This repository holds the tool. **The format, the specification and the project's own board
live in [`docket-board`](https://github.com/vadymdidenkolab/docket-board)** — which is itself an
docket vault, and therefore the working example.

## Status

The vault format is settled, and the whole local workflow works. A vault is usable without any
of this — clone, open in Obsidian, work — but the tool makes the routine parts routine.

```bash
docket init --key ACME --name "Acme Platform" acme   # a complete vault, ready to open
cd acme
docket new "Fix login redirect loop" --type bug      # ACME-1, with a valid key
docket check                                         # eight rules, file:line findings
```

| Command | What | State |
|---|---|---|
| `docket init` | Scaffold a new project vault | works |
| `docket new` | Create a task with a valid key from the project's template | works |
| `docket check` | Validate a vault against the specification | works |
| `docket workspace` | Assemble several project repositories into one Obsidian vault | works |
| `docket version` | Print the version | works |
| `docket serve` | Web UI and HTTP API over the same repository | planned |
| `docket import` | Import from Jira and Confluence | planned |

Progress is tracked on the board in `docket-board`.

## Install

Download the binary for your platform from
[Releases](https://github.com/vadymdidenkolab/docket/releases) and put it on your `PATH`. That
is the whole procedure — there is no runtime to install.

With Go already on the machine:

```bash
go install github.com/vadymdidenkolab/docket/cmd/docket@latest
```

From a checkout — Go 1.26 or newer, one dependency:

```bash
go build -o docket ./cmd/docket
./docket --version
```

Go and the single-binary distribution were chosen for the reasons in
[ADR-0002](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/decisions/0002-go-and-a-single-binary.md).

## A workspace

Several projects, each its own repository, opened in Obsidian as one vault:

```bash
docket workspace init space && cd space
docket workspace add --key ACME --remote git@github.com:example/acme.git
docket workspace sync
```

`sync` clones what is missing and fast-forwards what is there. A project with uncommitted
changes is reported and left alone.

## License

MIT — see [LICENSE](LICENSE).
