# Changelog

## v0.6.0 — 2026-09-21

The first release anybody outside can install. `go install` against v0.5.0 fails — that tag
predates the move to `github.com/didenkolab`, so its `go.mod` declares a module path that no
longer resolves. This is that release, cut after the move.

### The account is didenkolab

`github.com/vadymdidenkolab` became `github.com/didenkolab`, and the module path with it:

```bash
go install github.com/didenkolab/docket/cmd/docket@latest
```

Fixed forward rather than rewritten. An old module path breaks nothing — `go.mod` and the
imports agree inside every commit — so the build works at any point in the history, unlike
`igile.yaml`, which was a file the new binary could not read.

### Added

- **SECURITY.md**, **CONTRIBUTING.md** and issue and pull request templates. The security page
  says which three behaviours are deliberate — programs need `--programs`, a program is a path
  and never a shell, and the inbox secret lives in the server's environment — so a reader can
  tell a finding from a design decision.

### Fixed

- **A page is named after its title** (DKT-46). The rule was in the specification and enforced
  for tasks only, so this project's own vault broke it twenty-one times. `check` reports it now
  and `check --fix` settles it, with two carve-outs: a decision keeps its number, and `index.md`
  is a role rather than a title.
- **A case-only rename works** on a case-insensitive file system. `vault.Rename` refused one
  outright, because Stat reports the target as existing — so a task could never be retitled from
  "fix login" to "Fix login" on macOS or Windows.
- **A release lists what shipped first.** The page listed every task whose file changed between
  two tags, which put a task somebody had only written into the backlog beside the work that went
  out. Shipped comes first now; what the window merely touched is behind a summary that says so.
- **A tag that is not a version is not a release** (DKT-65, ADR-0006). The showcase's release
  page opened with the generator's build marker, captioned "No task changed in this release".
- **A task page says each thing once.** A declared relation arrived as a backlink as well, so the
  same tasks appeared under two headings — three times over once an app's panel added its copy.
- **The board**: priority reads as priority rather than as a label, a column total carries a
  sigma instead of being a second bare number, the assignee row reads as a value until it is
  used, and a phone spends 76 pixels on navigation instead of 180.
- Every commit message is in English, and the old board key `IGL-` is gone from the code.

### Known

- `check --fix` renames a file by case and git quietly keeps the old name on a case-insensitive
  file system, so the rename can be committed as nothing (DKT-67).
- The **Run the scenarios** button in the tests app does not land a result; a commit from CI and
  `hooks/import-cucumber.sh` are the routes that work (DKT-57).
- The Confluence half of `docket import` has never been pointed at a real space.

## v0.5.0 — 2026-09-21

The first release under the name, and the first one anybody outside could read.

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
| `github.com/didenkolab/igile` | `github.com/didenkolab/docket` |
| `agent@igile.local` | `agent@docket.local` |
| The project's own board, keys `IGL-*` | `DKT-*` |

**Every repository is new.** The old ones were not renamed; the history was rewritten and pushed
to repositories created for it, and the old ones deleted. A clone of an `igile` repository cannot
be pulled forward and has to be replaced:

```bash
git clone https://github.com/didenkolab/docket.git
```

An existing vault is migrated by renaming one file and one string in it:

```bash
git mv igile.yaml docket.yaml
git commit -m "The tool is called docket"
```

Apps installed from the old library carry the old source URL in `docket.yaml`; re-running
`docket app add` with the new URL updates the record.

### Added

- **The README leads with what this actually is**: git and Obsidian made into one tracker that a
  person and an agent both find obvious. Neither half is new; what was missing between them was a
  format each reads as if it were its own.
- **`docs/how-you-work-in-this.md`** — the shape of a week. Who decides what, where each kind of
  thing goes, three ways into one set of files, and what you stop doing.
- **A skill for agents** — `skill/docket/SKILL.md`. One file an agent reads once and then knows
  the loop: what to pick up, how to take it, how to move it, what to write down, and the rules
  that break a vault when broken. Copy it into `~/.claude/skills/`.
- **A cookbook** — `docs/examples/`, twelve recipes from a first vault to a Jira migration.

### Fixed

- **A retitle no longer leaves dangling links** (DKT-61). `docket check --fix` renamed a retitled
  task's file and left every link naming the old title pointing at nothing, then reported the
  vault clean — the rule that reads a relation matches the key inside the link, and the key had
  not changed. Valid to the tool, and drawing no edge in Obsidian. `check` now reports such a
  link and says what to write instead; `--fix` renames first and then puts every link back on the
  note, for parents and for every declared relation.
- **A write during a push is no longer dropped** (DKT-62). The background push loop stopped when
  the unpushed count was no longer falling, reading that as a push that sends nothing. A push
  that sends one commit while the next write lands leaves the count exactly where it was, so the
  loop stopped with that write still in the folder and the board reporting nothing wrong.
  Progress is now the upstream ref advancing, which says work was done whatever else arrived
  meanwhile.
- **The first release a project cuts lists its work** (DKT-64). With no tag before it there was
  no range, and the code passed the tag alone to `git diff` — which compares the working tree
  against that tag rather than the tag against the beginning of the repository. Every new project
  met "No task changed in this release" once, under a heading promising the work up to here.
- **No test reaches the network, and neither does an import.** Nine tests scaffolded a vault by
  cloning the published template, so the suite depended on a repository somewhere being public.
  `import apply` did the same at its last step, after the only part that is supposed to need a
  network had finished; it takes `--template` now, and a path works.
- The test that writes a git tag now says who is writing it. It tagged with no identity, which
  passes on any machine with a global git config and fails on every CI runner — eleven red runs
  on main, all of them this one test.

### Known

- The **Run the scenarios** button in the tests app does not land a result; the two routes that
  work are a commit from CI and `hooks/import-cucumber.sh`. Filed as DKT-57.
- The Confluence half of `docket import` has never been pointed at a real space.

## v0.4.0 — 2026-09-01

Released as igile. See the repository's tags for v0.1.0 through v0.4.0; their contents are
unchanged apart from the rename.
