# Contributing

## Where the work is tracked

Not in GitHub issues. The plan lives in
[docket-board](https://github.com/didenkolab/docket-board), which is a docket vault — so a task
is a Markdown file, and proposing one is a pull request that adds a file to `DKT/`. That is not
a gimmick: this project tracks itself with the tool, which is why its board is a working example
rather than a description of one.

Issues here are for a bug in the tool that you would rather report than file. Either is fine.

## Building and checking

```bash
go build ./...
go test ./...
```

The suite needs no network and no git identity — that is checked, and a test that reaches for
either is a bug. It scaffolds vaults from a local fixture rather than from the published
template, for the reason in `internal/vault/vaulttest`.

Before committing anything that touches a vault:

```bash
docket check .
```

It exits non-zero on a finding, so it works as a pre-commit hook:

```sh
#!/bin/sh
exec docket check .
```

## Commits

One commit per logical change, and the message says what changed for the reader rather than which
files moved. The commit history *is* the change history of this project — there is no separate
audit log — so "wip" throws away the only record there was.

Write the subject as a statement of the new truth, in the present tense: *A release lists what
shipped*, not *fix release page*. The body is where the reasoning goes — what was wrong, what it
cost, what was decided and what that costs. Look at `git log` for the tone; the bodies are the
most useful documentation this repository has.

**English only**, everywhere: subjects, bodies, comments, identifiers. Non-English strings inside
test fixtures are welcome and deliberate — they are how the tool proves it is not ASCII-only.

## When something is a decision

A fork with alternatives somebody would have argued for, and a cost that was accepted, is a
decision page under `docs/decisions/` in docket-board — not a comment in a patch. It has a
required shape: `## Context`, `## Decision`, `## What this costs`, `## Alternatives considered`.
Explaining how a mechanism works is a design page instead.

## What gets a change turned down

- **A second record of a fact the repository already holds.** The whole design is that git is the
  only store; a cache, an index or a field that must be kept in step with something else is the
  bug this project exists to avoid.
- **A feature of the board that could be a pack.** Apps are the extension mechanism, and the
  built-in report page was deleted for exactly this reason.
- **A rule the tool enforces but does not explain.** Every finding names a file, a line and what
  to do instead.
- **Vocabulary the tool decides on the team's behalf.** Statuses, types, priorities and the
  workflow belong to the vault.

## Licence

MIT, as in [LICENSE](LICENSE). By contributing you agree your work is published under it.
