---
name: docket
description: Use when working in a docket vault — a task tracker kept as Markdown in git, where a task is a file, every move is a commit, and docket.yaml is at the repository root. Covers finding what to pick up, taking and moving tasks, linking work, recording what happened, and cutting a release. Triggers on a repository holding docket.yaml, on "the board", "the backlog", "this ticket", "what should I pick up", and on any task key of the form PROJ-12. Not for editing application code, and not for Jira or Linear.
allowed-tools: Bash(docket:*), Bash(git status:*), Bash(git log:*), Bash(git diff:*), Bash(git add:*), Bash(git commit:*), Bash(git switch:*), Bash(git branch:*), Read, Grep, Glob
---

# Working in a docket vault

## Before anything else

```bash
docket check          # is the tool here, and is the vault sound
cat docket.yaml       # the words this vault uses
```

If `docket` is not on the `PATH`, **stop and say so.** Editing frontmatter by hand is how a vault
gets corrupted, and it is what this skill exists to prevent. Reading files is fine either way.

`docket.yaml` is the vocabulary — the statuses and their categories, the types and their levels,
the priorities, the extra fields, the relations, and which status may move to which. **None of it
is universal.** Never carry names over from another project.

If the vault has an `AGENTS.md`, read it. It is the vault's own rules and it wins over this file.

Never state a task's status, owner or contents without opening the file or asking `docket
export`. Conversation history is not the vault.

## Work on a branch

```bash
git switch -c agent/proj-12
```

A branch is how work here is reviewed: the diff shows every task you created, moved or linked,
and it is read on the lines like code. Commit to `main` only when the person asked for that.

## The loop

### 1. Find the work

```bash
docket export --open --format json                            # everything unfinished
docket export --format csv --fields key,title,status,assignee
```

Open the task file before starting: the body holds the acceptance criteria. A task whose
`blocked_by` points at unfinished work is blocked — leave it and say what it waits on.

### 2. Take it

```bash
docket set PROJ-12 assignee=agent/claude status="In progress"
```

Use `docket set` for everything in frontmatter. It checks each value against what the vault
declared and refuses rather than writing a file the board cannot render; it moves `status` and
`status_category` together; it stamps `updated`. A script reaching for `sed` gets none of that
and breaks on the first title with a colon in it.

### 3. Record what you found

Append under `## Comments`, newest last:

```markdown
**agent/claude · 2026-09-21 14:05** — Reproduced on a fresh vault. The redirect loop is the
session cookie being set on the wrong path.
```

Write what you found. The status already says you are working on it.

### 4. Connect it

```bash
docket set PROJ-12 blocked_by=PROJ-4     # given a key, docket writes the whole link
docket set TEST-5 found=PROJ-12
```

Write both sides yourself — nothing writes the inverse for you. Use `parent` for hierarchy and a
relation for everything else; they are not interchangeable, because `parent` decides what the
board does.

### 5. Close it

```bash
docket set PROJ-12 status=Done
docket check
git add -A && git commit -m "The redirect loop is fixed: the session cookie is set on /"
```

One commit per logical change, saying what changed for the reader. `git log` is the only change
history this tracker has.

### 6. Cut a release, when asked

```bash
git tag -a v1.2.0 -m "What this release is"
```

A release is a tag; what went into it is computed from the repository. Nothing to set on a task.

## Creating a task

```bash
docket new "Fix login redirect loop" --type bug --priority high --parent PROJ-4
```

`docket new` allocates the next free key and names the file after the task. Writing the file
yourself means picking a key by hand, and two agents on two branches pick the same one.

## The rules that break a vault when broken

1. **A relationship is a link.** `parent`, `labels` and every relation are wikilinks **by note
   name** — `"[[PROJ-4 Session model]]"`. Not `[[PROJ-4]]`, not a bare word: Obsidian resolves
   note names and consults nothing else, so any other form draws no edge and yields no backlink.
   Quote them, or YAML reads `[[auth]]` as a nested list.
2. **`status` and `status_category` move together.**
3. **Frontmatter is flat.** No nested objects. A nested field is uneditable by hand and invisible
   to the board. Fields carried in from another system are prefixed `x_`.
4. **A key is permanent.** Never edit `key` or `created`; never renumber to close a gap.
5. **Retitling renames the file and every link to it.** `docket set` cannot change a title: edit
   `title`, run `docket check --fix` to rename the file, then grep for the old title and rewrite
   every link naming it. `--fix` does not follow links and `check` will not warn you — its rule
   matches the key inside the link, and the key did not change (DKT-61).
6. **Run `docket check` before every commit.** It exits non-zero on a finding, and prints a file
   and a line.

## When MCP is connected

Prefer its tools for anything that changes a task — `create_task`, `update_task`, `get_task`,
`list_tasks`, `search`, `read_page`, `write_page`, `check`. They do the same checks and commit
for you, and `update_task` refuses a write whose `version` is stale, so you cannot land on top of
an edit a person made while you were thinking.

## Stay inside the vocabulary

If a status, type, priority, field or relation is not in `docket.yaml`, it does not exist. Adding
one changes the vault's configuration, which is a person's decision — ask instead.

Close only what is finished; when it is not, say so and leave it open.

## More detail

`reference.md`, beside this file: documents and their required shapes, labels versus tags,
estimates, sprints, and what to do about an imported vault.
