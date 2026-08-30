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
docket new "Fix login redirect loop" --type bug      # ACME/1, with a valid key
docket project add --key BETA --name "Beta"          # a second project in the same vault
docket new --project BETA "Ship the widget"          # BETA/1
docket check                                         # nine rules, file:line findings
```

A key is `PROJECT/NUMBER` and it is also the path: `ACME/12` lives in `ACME/12.md`. One vault
holds as many projects as you like, `[[ACME/12]]` resolves in Obsidian with no help, and links
between projects work because the projects are one file tree.

| Command | What | State |
|---|---|---|
| `docket init` | Scaffold a new vault | works |
| `docket project` | List the projects a vault holds, or add one | works |
| `docket new` | Create a task with a valid key from the project's template | works |
| `docket check` | Validate a vault against the specification | works |
| `docket workspace` | Assemble several project repositories into one Obsidian vault | works |
| `docket serve` | A board and an API over the same repository | works |
| `docket version` | Print the version | works |
| `docket import` | Bring in an existing Jira and Confluence instance | works |

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

## A board in the browser

For people who do not run Obsidian:

```bash
docket serve                       # signs people in against the repository's git host
docket serve --auth none --author "Your Name <you@example.com>"   # one person, no sign-in
```

A board grouped by status across every project at once — or narrowed to one — with cards you
drag between columns, plus task pages, the wiki, search, settings, and a JSON API.

Dragging a card is the same status move as the form on the task page: it goes through the API,
is checked against the fingerprint the card was drawn from, and lands in git as a commit. It is
an enhancement, not the mechanism — without JavaScript the board is still a board and every
task page still moves its own status.

### Who may do what

docket keeps no users of its own. People sign in with a token for the git host that already
holds the repository — GitHub, GitLab or Bitbucket, hosted or your own — and what they may do
is what that host says they may do: read becomes **viewer**, write becomes **member**, and
administer becomes **admin**, who may also change the vault's vocabulary. Commits are authored
by the person who made them, so `git log` says who moved what.

Access is granted on the host, not here. Anyone who can clone the repository has everything in
it, so a button in this interface would appear to hand out something it cannot. The reasoning
is in
[ADR-0004](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/decisions/0004-access-comes-from-git.md).

### The workflow

Which status may move to which is part of the vault's configuration, so it lives in
`docket.yaml` and changes to it show up in `git log` like everything else. Leave it out and any
task can go anywhere, which is what a new vault does. Set it and only the allowed moves are
offered — on the board, on the task page and through the API alike. It is a second client
to the same files, not an owner of them: nothing is cached, every write becomes a git commit
attributed to whoever made it, and a write that would land on top of a change made in Obsidian
or by an agent is refused rather than applied. Point all three at one repository at once.

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
