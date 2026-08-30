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

To see one without reading anything, clone
[`docket-demo`](https://github.com/vadymdidenkolab/docket-demo) and open it in Obsidian: two
projects, a board, a backlog and a wiki, with nothing installed.

## Status

The vault format is settled, and the whole local workflow works. A vault is usable without any
of this — clone, open in Obsidian, work — but the tool makes the routine parts routine.

```bash
docket init --key ACME --name "Acme Platform" acme   # a complete vault, ready to open
cd acme
docket new "Fix login redirect loop" --type bug      # ACME-1, with a valid key
docket project add --key BETA --name "Beta"          # a second project in the same vault
docket new --project BETA "Ship the widget"          # BETA-1
docket check                                         # nine rules, file:line findings
docket check --fix                                   # rename files whose title moved on
```

A key is `PROJECT-NUMBER`, and the file is named after the task:
`ACME/ACME-1 Fix login redirect loop.md`. The key makes it sortable and unambiguous, the title
makes the graph readable, and `[[ACME-1 Fix login redirect loop]]` resolves in Obsidian with no
help — the reasoning is in
[ADR-0005](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/decisions/0005-a-file-is-named-after-its-task.md).
One vault holds as many projects as you like, and links between projects work because the
projects are one file tree.

| Command | What | State |
|---|---|---|
| `docket init` | Scaffold a new vault | works |
| `docket project` | List the projects a vault holds, or add one | works |
| `docket new` | Create a task with a valid key from the project's template | works |
| `docket check` | Validate a vault against the specification | works |
| `docket workspace` | Assemble several project repositories into one Obsidian vault | works |
| `docket serve` | A board and an API over the same repository | works |
| `docket mcp` | Serve the vault to an agent over the Model Context Protocol | works |
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
drag between columns and up and down inside them, plus task pages, the wiki, a search you can
narrow by project, status, type, priority, assignee and label, settings, and a JSON API.

Dragging a card is the same status move as the form on the task page: it goes through the API,
is checked against the fingerprint the card was drawn from, and lands in git as a commit. Where
it lands in the column is written down too, as an `order` on the task, so a column somebody
arranged is still arranged after a reload — and a vault where nobody has dragged anything is
simply sorted by key. Dragging is an enhancement, not the mechanism: without JavaScript the
board is still a board and every task page still moves its own status.

### What happened to this task

Every change is a commit, so the history of a task is the history of its file — and the
interface shows it as a tracker does rather than as a diff: who, when, and which fields moved.
`status  Backlog → In review`, `added 1 comment`, `edited the description`.

It is read from git on the way past. There is no activity table to fall out of step with the
files, and a change made in Obsidian or by an agent appears here beside one made on this page,
because there was only ever one record.

A task's history is found by its key rather than by following one file. A retitle renames the
file, and git can only follow a rename by guessing from how similar the two versions look — a
retitle that also rewrites the body falls under the threshold, and the history silently stops.
The key is in the file name, so `ACME/ACME-12 *.md` is the whole life of ACME-12 and nothing
else, decided by the format instead of by a heuristic.

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

### Standing up to the open internet

A change has to come from a page this server drew. Every form carries a token that lives in a
cookie the page cannot read, and a request that admits to coming from another origin — by
`Sec-Fetch-Site` or by `Origin` — is refused before anything else is looked at. A client with no
cookies at all is not asked for a token: an agent or a `curl` has no ambient session to hijack,
and demanding a `GET` before every write would buy nothing.

Requests are metered per client: generous for reading, tighter for writing, and much tighter for
signing in, because every sign-in attempt is a call to the git host and a loop against it burns
that host's rate limit for everyone. Behind a reverse proxy, pass `--behind-proxy` so the limit
follows the client the proxy names rather than the proxy itself.

Pages are served under a content security policy with no inline anything, and attachments are
served sandboxed — an uploaded SVG is still embeddable as an image but cannot run as a page.
An interrupt stops taking new requests and lets the ones in flight finish, because a write is a
file and then a commit, and stopping between the two leaves a change git never saw.

None of this replaces the git host. Anyone who can clone the repository has everything in it;
what is here keeps a browser from being used against its owner.

### The workflow

Which status may move to which is part of the vault's configuration, so it lives in
`docket.yaml` and changes to it show up in `git log` like everything else. Leave it out and any
task can go anywhere, which is what a new vault does. Set it and only the allowed moves are
offered — on the board, on the task page and through the API alike. It is a second client
to the same files, not an owner of them: nothing is cached, every write becomes a git commit
attributed to whoever made it, and a write that would land on top of a change made in Obsidian
or by an agent is refused rather than applied. Point all three at one repository at once.

### Running it somewhere

In a container, with `compose.yaml` from this repository:

```bash
VAULT=~/work/acme docker compose up
```

That builds from the checkout, so it needs nothing but Docker — no registry, no login, no
token. `VAULT` is the repository to serve and defaults to the directory you run it from;
`PORT` and `AUTH` are the other two knobs.

The vault is not in the image. It is your git repository, mounted — baking it in would make the
image the source of truth, which is the opposite of the whole design. The image carries the
binary, git and certificates and keeps no state, so restarting it loses nothing and two of them
against one clone is only a question of file locking.

There is a published image as well — `ghcr.io/vadymdidenkolab/docket` — which needs a
`docker login ghcr.io` for as long as this repository is private. Building does not, which is
why compose builds by default.

Or without any of it, since this is one static binary: put it on the machine, clone the vault
beside it, and run `docket serve --auth git`.

Behind a reverse proxy that terminates TLS, add `--behind-proxy` so rate limits follow the
client the proxy names rather than the proxy. `--auth git` makes who may do what a question for
the git host rather than for whoever finds the port.

To let a server take changes people push to the remote — and to push its own back — pull on a
schedule beside it; docket does not fetch on its own, because a tracker that rebases your working
copy out from under you is a tracker you stop trusting.

## An agent, over a protocol

An agent can edit the files directly — that is the point of the format, and nothing here
replaces it. But an agent driving a tracker wants "move this to In review" to be one call that
validates, writes and commits, not four file operations that might each be half-right. That is
what `docket mcp` is:

```bash
docket mcp --author "Claude <claude@example.com>"
```

It speaks JSON-RPC on stdin and stdout — eight tools: `list_tasks`, `get_task`, `create_task`,
`update_task`, `search`, `read_page`, `write_page`, `check`. In Claude Code:

```bash
claude mcp add docket -- docket mcp --author "Claude <claude@example.com>" /path/to/vault
```

`--author` is required, because every write becomes a git commit and an agent's commits should
say which agent made them. `get_task` returns a `version` — the same fingerprint the board
draws its cards from — and `update_task` refuses a write whose version no longer matches, so an
agent cannot land on top of an edit somebody made in Obsidian while it was thinking. The
workflow applies here exactly as it does on the board: a move the vault forbids comes back as
an error that names the moves it allows instead.

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
