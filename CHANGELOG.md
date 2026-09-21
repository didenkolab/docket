# Changelog

## Unreleased

### The project is called docket

It was called **igile** until 2026-09-21. The name is recorded here rather than quietly dropped,
because somebody will find an old clone and deserve to know it is the same thing.

The rename went through the history rather than on top of it. `igile.yaml` in an old commit
would be a file the new binary cannot read, so every historical commit was rewritten to be
self-consistent: a vault checked out at any point in this repository's history opens with the
binary built from that point. What changed, everywhere:

| Was | Is |
|---|---|
| `igile` | `docket` |
| `igile.yaml` | `docket.yaml` |
| `igile-app.yaml` | `docket-app.yaml` |
| `IGILE_*` environment variables | `DOCKET_*` |
| `X-Igile-*` request headers | `X-Docket-*` |
| `github.com/vadymdidenkolab/igile` | `github.com/vadymdidenkolab/docket` |
| `agent@igile.local` | `agent@docket.local` |
| The project's own board, keys `IGL-*` | `DKT-*` |

**Every repository is new.** The old ones were not renamed; the history was rewritten and pushed
to repositories created for it, and the old ones deleted. A clone of an `igile` repository cannot
be pulled forward and has to be replaced:

```bash
git clone https://github.com/vadymdidenkolab/docket.git
```

An existing vault is migrated by renaming one file and one string in it:

```bash
git mv igile.yaml docket.yaml
git commit -m "The tool is called docket"
```

Apps installed from the old library carry the old source URL in `docket.yaml`; re-running
`docket app add` with the new URL updates the record.

### Added

- **A skill for agents** — `skill/docket/SKILL.md`. One file an agent reads once and then knows
  the loop: what to pick up, how to take it, how to move it, what to write down, and the rules
  that break a vault when broken. Copy it into `~/.claude/skills/`.
- **A cookbook** — `docs/examples/`, twelve recipes from a first vault to a Jira migration.

### Fixed

- The test that writes a git tag now says who is writing it. It tagged with no identity, which
  passes on any machine with a global git config and fails on every CI runner — eleven red runs
  on main, all of them this one test.

### Known

- `docket check --fix` renames a retitled task's file and leaves every link naming the old title
  pointing at nothing, then reports the vault clean — the relation rule matches the key inside
  the link, and the key did not change. Filed as DKT-61. Until it is fixed, grep for the old
  title after a retitle.
- The **Run the scenarios** button in the tests app does not land a result; the two routes that
  work are a commit from CI and `hooks/import-cucumber.sh`. Filed as DKT-57.
- The Confluence half of `docket import` has never been pointed at a real space.

## v0.4.0 — 2026-09-01

Released as igile. See the repository's tags for v0.1.0 through v0.4.0; their contents are
unchanged apart from the rename.
