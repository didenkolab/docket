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

Early. The format is settled and documented; the tool is not written yet. A vault is fully
usable without it — clone, open in Obsidian, work. What the tool will add:

| Command | What |
|---|---|
| `docket init` | Scaffold a new project vault |
| `docket new` | Create a task with a valid key from the project's template |
| `docket check` | Validate a vault against the specification |
| `docket workspace sync` | Assemble several project repositories into one Obsidian vault |
| `docket serve` | Web UI and HTTP API over the same repository |
| `docket import` | Import from Jira and Confluence |

Progress is tracked on the board in `docket-board`.

## License

MIT — see [LICENSE](LICENSE).
