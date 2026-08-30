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

Early. The format is settled and documented; the tool is a skeleton — `version` and `help` are
the only commands that exist. A vault is fully usable without any of it: clone, open in
Obsidian, work.

| Command | What | State |
|---|---|---|
| `docket version` | Print the version | works |
| `docket help` | Print the usage | works |
| `docket init` | Scaffold a new project vault | planned |
| `docket new` | Create a task with a valid key from the project's template | planned |
| `docket check` | Validate a vault against the specification | planned |
| `docket workspace sync` | Assemble several project repositories into one Obsidian vault | planned |
| `docket serve` | Web UI and HTTP API over the same repository | planned |
| `docket import` | Import from Jira and Confluence | planned |

Progress is tracked on the board in `docket-board`.

## Build

Go 1.26 or newer. No dependencies.

```bash
go build -o docket ./cmd/docket
./docket --version
```

Or, with Go installed:

```bash
go install github.com/vadymdidenkolab/docket/cmd/docket@latest
```

Release builds stamp the version in:

```bash
go build -ldflags "-X github.com/vadymdidenkolab/docket/internal/cli.version=$(git describe --tags)" \
         -o docket ./cmd/docket
```

Go and the single-binary distribution were chosen for the reasons in
[ADR-0002](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/decisions/0002-go-and-a-single-binary.md).

## License

MIT — see [LICENSE](LICENSE).
