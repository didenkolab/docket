# Where the time went

Every status move is a commit, so how long work sat in each column is arithmetic over `git log`.
Nothing had to be recording it while it happened.

```bash
docket report time-in-status
```

```
2 tasks, 8 moves

STATUS                  ENTERED    NOW         MEDIAN        LONGEST
Backlog                       1      1              —              —
In progress                   1      0              —              —
Done                          1      1              —              —

Waiting longest
  ACME-1     Done               —  Fix login redirect loop
  ACME-2     Backlog            —  The session model, rewritten
```

On a real board the median and longest columns fill in, and the bottom section is the one people
act on: what has been sitting in one column longest, right now.

## Why this is a command and not a page

A program that wants to draw it its own way runs this and formats the JSON:

```bash
docket report time-in-status --json
```

The awkward part — following a task through renames across its whole history, because retitling
renames the file — stays in one place that is tested. Your hook stays a formatter. That is the
same rule every app here follows.

## What it is reading

Nothing special. The history of each task's file, which is the history of the task, found by key
rather than by following one path:

```bash
git log --follow -p "ACME/ACME-1 Fix login redirect loop.md"
```

`--follow` matters, because git can only follow a rename by guessing from how similar two
versions look — and a retitle that also rewrites the body falls under the threshold, after which
the history silently stops. The key is in the file name, so `ACME/ACME-1 *.md` is the whole life
of ACME-1 and nothing else. That is decided by the format rather than by a heuristic, and it is
why the file is named the way it is.

## In Jira this is a product

Four of the marketplace's top hundred sell this one report, because Jira's changelog is behind an
API and aggregating it is somebody's business. Here the changelog is the commit history, which
you already have, offline, in every clone.

## The app, if you want it on the board

```bash
docket app add https://github.com/didenkolab/docket-apps.git#time-in-status
docket serve --programs
```

That adds a **Time in status** page drawn from the same command. `--programs` is the consent:
without it the server runs nothing the vault declared, because a repository declaring a program
and a person agreeing to run it on their laptop are two different decisions.

## Reading it honestly

A long median in **In review** is a queue, not a person. A long median in **In progress** with a
short one everywhere else usually means the column is doing two jobs and wants splitting. And a
column with one task in it produces a median that means nothing at all — the report prints the
count so you can see when that is the case.

## Next

- [What the board cannot see](what-the-board-cannot-see.md) — the other question a board cannot answer
- [Sprints](sprints.md) — the fortnight this is usually read against
- [Write your own app](write-an-app.md) — turning `--json` into your own page
