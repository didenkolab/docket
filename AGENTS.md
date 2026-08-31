# Working in this repository

This is the Go source of **docket**: a tracker and a knowledge base that live in an Obsidian
vault, where one git repository is one project and every change is a commit. The vault the
project is run on is a different repository — `docket-board` — and so is the template a new vault
is cloned from, and the fixture vault used to catch defects. This one holds only the tool.

Read this before changing anything. It is short because most of it is one idea applied in
several places.

## What the tool is allowed to be

Four sentences from `docs/purpose.md` in `docket-board`, which is normative and wins every
argument:

- **A repository is a project.** Its tasks, its documentation and its history are in it and
  nowhere else, so a project can be handed over as a clone.
- **One file is one thing.** The file *is* the task; there is no other copy anywhere.
- **Relationships are links, not fields.** A parent, a label, a sprint are wikilinks, because a
  wikilink is what Obsidian resolves, draws in its graph and counts as a backlink.
- **Every change is a commit**, and `git log` is the history. There is no activity table, because
  a second record of what happened is a record that can disagree with the first.
- **It works with nothing installed.** Clone the folder, open it in Obsidian, and it is a
  tracker. The tool makes the routine parts routine; it is never the thing that makes the data
  readable.

Before adding anything, answer: which of those does it serve, does it show up in Obsidian's
graph or only in our own interface, is it a commit, and does the vault still work if the tool is
deleted. An answer of "none", "only ours", "no" or "no" means the thing is wrong however useful
it looks.

## Layout

```
cmd/docket/           the binary, and nothing but wiring
internal/project/    docket.yaml — the projects and the vocabulary they share
internal/task/       one task file: frontmatter, body, links, relations
internal/vault/      a vault on disk: list, create, retitle, boards, sprints, the graph
internal/space/      one or more vaults presented as one
internal/gitvcs/     git, as this tool needs it: commit, history, blame, push
internal/check/      the numbered rules from the specification
internal/access/     the git hosts: who somebody is and what they may do
internal/server/     docket serve — the board, the wiki, the API
internal/mcp/        docket mcp — the same vault, over the Model Context Protocol
internal/cli/        argument parsing and the commands
```

The dependency direction is downwards: `server` and `mcp` know about `space`, `space` knows
about `vault`, `vault` knows about `task` and `project`. Nothing lower reaches up.

## How code here is written

**A comment says why, not what.** The code already says what it does. A comment earns its place
by recording the reason somebody would otherwise have to rediscover — the alternative that was
tried, the host that behaves differently from its documentation, the failure that made the rule.
If a comment would only restate the line below it, delete it.

**Write the reason before the rule.** In a doc comment, in a commit message, in a finding's text.
"Refused because two records of one fact will disagree" is worth reading; "refused" is not.

**Findings and errors are sentences.** Everything a person reads — a `check` finding, an HTTP
error page, a refusal from the MCP server — says what is wrong, why it matters and what to do,
in plain words. `fmt.Errorf("invalid status")` is not acceptable; naming the status, the ones
that are valid and where they are declared is.

**Names are the vault's, not ours.** A status, a type, a priority, an estimate's unit belong to
the vault and are read from `docket.yaml`. Nothing central has a list of them, and any code that
assumes English is a defect — the fixture vault is in Russian for exactly this reason.

**No dependency without a reason that is written down.** The tool is a single static binary with
a YAML parser and a Markdown renderer. Adding a third needs an argument, and probably a decision
document in `docket-board`.

## Tests

`go test ./...` must pass and `gofmt -l internal/ cmd/` must print nothing before anything is
committed.

**A test never reaches the network.** One did — it left `Options.Template` empty and so cloned
the published template — and it went red for a correct change to a repository it does not own.
Use `internal/vault/vaulttest`, which builds a template as a real git repository in a temporary
directory. The one exception is `TestLiveDeviceFlow`, which is skipped unless a client id is
passed in on purpose.

**Test the contract, not the fixture.** That same test asserted a list of file names that lived
in another repository. It now states what `Init` does: everything the template had, less the
template-only files, plus the project folder and the generated boards.

**A test's name and comment say what would break.** `TestBranchesIncludeTheOnesOnlyOnTheRemote`
is followed by two sentences explaining that a proposal you are asked to review is somebody
else's, so a list of local branches showed the wrong half. Somebody who breaks it should learn
from the test why it exists.

Tests use table-driven cases with a `what` field naming the case in words, so a failure reads as
a sentence.

## Commits

One commit per logical change. The message says **what changed and why**, for a reader who was
not here: the subject is a claim, not a file list, and the body is prose rather than bullets.

The convention this repository actually follows — read `git log` — is a subject that could be a
sentence in a design document (`A token belongs to a host; a role belongs to a repository`),
followed by paragraphs explaining what was wrong before, what was decided, and what it cost.
Where a bug was found by looking at something real rather than by reasoning, the message says so
and gives the number.

Never put a secret, a token or a client secret in a message, a log line, an argument vector or a
test fixture. A token lives in memory for the length of a session and nowhere else.

## The vault this project is run on

The tasks, the roadmap, the specification and the design documents are in `docket-board`, which is
its own repository and is itself a docket vault. A change to behaviour usually needs a change
there as well: a task moved, a rule added to `docs/spec/vault-format.md`, sometimes a decision in
`docs/decisions/`. That repository's `AGENTS.md` and `docs/spec/documents.md` say how those
documents are written.

`docket-testbed` is a fictional team's vault in a language that is not ours, kept to catch the
assumptions an English vault hides. Run a change against it before believing it works.
