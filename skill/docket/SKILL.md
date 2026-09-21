---
name: docket
description: Use when running a project in a docket vault — a task tracker kept as Markdown in git, where a task is a file and every move is a commit. Covers finding what to work on, creating and moving tasks, linking work together, writing pages and sprints, recording what happened, and cutting a release. Triggers on a repository holding docket.yaml, on "the board", "the backlog", "this ticket", "what should I pick up", or on any task key of the form PROJ-12.
---

# Running a project in a docket vault

A docket vault is a git repository whose tasks are Markdown files. There is no API you must be
taught and no server that has to be running. You read it with the tools you already use, and
you change it the way you change any other file — except that each change is a commit, so the
history of the project is `git log`, and it says which of the changes were yours.

You are not a visitor here. In most vaults you will be the one who does most of the writing,
and a person will read the board afterwards.

## Know where you are

A vault has `docket.yaml` at its root. Read it first, every time, before writing anything:

```bash
docket check                 # is the vault sound right now
cat docket.yaml              # the projects, and the words this vault uses
cat AGENTS.md                # the format rules, in full — this file is the short version
```

`docket.yaml` is the vocabulary: which statuses exist and what category each is in, which types
exist and at what level, the priorities, the extra fields, the relations, and the workflow —
which status may move to which. **None of it is universal.** A vault may call its columns
anything; another may forbid the move you are about to make. Never assume the names from
another project.

If `AGENTS.md` is present it is authoritative over this skill: it is the vault's own
instructions, and it may add rules a team decided on.

## The loop

### 1. Find the work

```bash
docket export --open --format json      # every unfinished task, as data
docket export --format csv --fields key,title,status,assignee,priority
docket anomalies                        # what is odd about how the work is connected
```

`--open` leaves out what is finished, which is almost always what you want. Read the task file
itself before you start on it: the body holds the acceptance criteria, and the frontmatter says
whether something is blocking it.

A task with `blocked_by` pointing at unfinished work is blocked. Do not pick it up; say what it
is waiting on.

### 2. Take it

```bash
docket set PROJ-12 assignee=agent/claude status="In progress"
```

Use `docket set`, not an editor, for anything in frontmatter. It checks what it writes against
what the vault declared — a status that is not on the list, a move the workflow forbids, a
relation pointing at a key that does not exist, a number that will not parse — and refuses
rather than writing a file the board cannot render. A script reaching for `sed` gets none of
that and breaks on the first title with a colon in it.

`status` and `status_category` must move together. `docket set` does both; by hand it is two
edits and forgetting the second puts the task in a column nothing draws.

### 3. Do the work, and write down what happened

The task body is where the work is described. Append comments under `## Comments`, newest last:

```markdown
**agent/claude · 2026-09-21 14:05** — Reproduced on a fresh vault. The redirect loop is the
session cookie being set on the wrong path; fix is one line in `internal/server/session.go`.
```

Say what you found, not that you are working on it. A comment that says "working on this" is
noise the next reader has to skim past — the status already says that.

### 4. Connect it

Relations are how two pieces of work relate, and the property name is the verb. They are
**links**, written by note name, quoted:

```bash
docket set PROJ-12 blocked_by=PROJ-4      # by key: docket writes the link for you
docket set TEST-5 found=PROJ-12           # a failing test found this bug
```

`parent` is hierarchy and decides what the board does. A relation is an annotation a person
reads. Do not use one for the other. Nothing writes the other side for you — if you say A
blocks B, say B is blocked by A as well.

### 5. Close it

```bash
docket set PROJ-12 status=Done
```

Then commit. One commit per logical change, describing what changed for the reader:

```bash
git add -A && git commit -m "The redirect loop is fixed: the session cookie is set on /"
```

The commit history *is* the change history of the tracker. There is no separate audit log, so a
commit message that says "update files" throws away the only record there was.

### 6. Cut a release

A release is a tag. There is no version object to create and no field to set on a task:

```bash
git tag -a v1.2.0 -m "What this release is"
git push --follow-tags
```

What went into it is computed from the repository — the work whose files changed since the
previous tag. It cannot be out of date, because there is nothing to keep in step.

## Creating a task

```bash
docket new "Fix login redirect loop" --type bug --priority high --parent PROJ-4
```

`docket new` allocates the next free key for the project, fills the vault's own template and
names the file after the task — `PROJ/PROJ-12 Fix login redirect loop.md`. Writing the file
yourself means picking a key by hand, and two agents on two branches will pick the same one.

## Pages, not tasks

Not everything is work. A decision, a design, a specification and a sprint are pages under
`docs/`, each carrying `title`, `type` and `updated`.

Write a **decision** only when there was a real fork — alternatives somebody would have argued
for, and a cost that was accepted. It has a required shape: `## Context`, `## Decision`,
`## What this costs`, `## Alternatives considered`, in that order. Explaining how a mechanism
works is a **design** page instead.

**Do not write a page whose only purpose is to list other pages.** No index of labels, no "see
all", no front page linking every epic. A link is a relationship, not a route: a list connects
everything on it, lands in the middle of the graph, and collapses the distance between clusters
that have nothing to do with each other. Finding a page is what search, backlinks and the file
explorer are for.

## The rules that break a vault when broken

1. **A relationship is a link.** `parent` and `labels` are wikilinks by note name —
   `"[[PROJ-4 Session model]]"`, never `[[PROJ-4]]` and never a bare word. Obsidian resolves
   note names and does not consult aliases; a bare word connects nothing. Quote them, or YAML
   reads `[[auth]]` as a nested list.
2. **`status` and `status_category` move together.**
3. **Frontmatter is flat.** No nested objects, ever — a nested field is uneditable by hand and
   invisible to the board. Fields carried in from another system are prefixed `x_`.
4. **A key is permanent.** Never edit `key` or `created`, and never renumber to close a gap.
5. **Retitling renames the file, and every link to it.** `docket set` cannot change a title —
   edit `title` in the frontmatter, then run `docket check --fix` to rename the file, and then
   **rewrite by hand every link that named the old title**. `--fix` renames but does not follow
   the links, and `check` will not tell you: its relation rule matches the key inside the link,
   and the key did not change. The result passes validation and is broken in Obsidian, where a
   link resolves by note name and nothing else. Grep for the old title before you commit.
6. **Set `updated`** on every change. `docket set` does it.

`docket check` prints a file and a line for what it finds, and exits non-zero when there is at
least one finding, so it works as a pre-commit hook. Run it before every commit — but see rule
5 for the case it currently misses.

## When MCP is connected

If `docket mcp` is available, prefer its tools for anything that changes a task —
`create_task`, `update_task`, `get_task`, `list_tasks`, `search`, `read_page`, `write_page`,
`check`. They do the same checks as `docket set` and commit for you, and `update_task` refuses
a write whose `version` no longer matches the one `get_task` returned, so you cannot land on
top of an edit a person made in Obsidian while you were thinking.

Read with file tools freely either way. Reading is never the risky part.

## What not to do

- **Do not invent vocabulary.** If a status, type, priority, field or relation is not in
  `docket.yaml`, it does not exist. Adding one is a change to the vault's configuration, which
  is a decision a person makes.
- **Do not edit frontmatter with `sed` or a regex.** Use `docket set`.
- **Do not close a task you did not finish** because it is the last one on a list. Say it is
  not done and why.
- **Do not write a status report as a page.** The board is the report, and `docket report
  time-in-status` and `docket anomalies` are the parts a board cannot show.
- **Do not commit a vault that `docket check` fails on.**
