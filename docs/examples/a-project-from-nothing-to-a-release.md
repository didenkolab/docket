# A project, from nothing to a release

The whole loop in one sitting: an empty directory becomes a tracked project, work moves across
a board, and a release is cut. Every command here was run against a fresh vault, and the output
is what it printed.

## Make the vault

A vault is a git repository. `docket init` fills one; it does not create it, because deciding
where your repository lives is not the tracker's business.

```bash
mkdir acme && cd acme && git init -q
docket init --key ACME --name "Acme Platform"
```

You now have a project folder `ACME/`, a `docs/` wiki, boards Obsidian can draw, templates, and
`docket.yaml` — the vocabulary this vault will use. Read that file before anything else: it is
the only place statuses, types, priorities and the workflow are defined, and none of them are
universal.

## Put work in it

```bash
docket new "Fix login redirect loop" --type bug --priority high
docket new "Session model" --type task
```

```
ACME-1  ACME/ACME-1 Fix login redirect loop.md
ACME-2  ACME/ACME-2 Session model.md
```

The file is named after the task — the key, a space, the title. The key makes it sortable and
unambiguous; the title is what Obsidian shows in the graph, in search and in the file explorer.
A file called `12.md` tells nobody anything.

Open the task and fill in the body: what has to be true for this to be done goes under
`## Acceptance`, as checkboxes. That is not decoration — the checklists app counts them and puts
the progress on the card.

## Say how the work relates

```bash
docket set ACME-1 blocked_by=ACME-2
```

```
ACME-1: blocked_by ACME-2
```

Given a key, `docket set` writes the whole link:

```yaml
blocked_by: ["[[ACME-2 Session model]]"]
```

That matters. A wikilink is the only pointer Obsidian resolves, draws in the graph and counts as
a backlink; the same key written plainly connects nothing. Because it is a link, ACME-2 now has
a backlink from ACME-1 without anything being written on ACME-2.

Nothing writes the other side of a relation for you. If you say A blocks B, say B is blocked by
A as well.

## Move it

```bash
docket set ACME-1 assignee=agent/claude status="In progress"
```

```
ACME-1: assignee agent/claude, Backlog → In progress
```

Use `docket set` rather than an editor for anything in frontmatter. It checks what it writes
against what the vault declared, and refuses rather than writing a file the board cannot render:

```bash
docket set ACME-1 status="Shipping"
```

```
docket set: "Shipping" is not a status: it is one of Backlog, Ready, In progress,
In review, Done, Dropped
```

It also keeps `status` and `status_category` together, which is the pair a hand edit forgets —
and a task whose category does not match its status lands in a column nothing draws.

## Look at it

```bash
docket serve --auth none --author "Your Name <you@example.com>"
```

A board at <http://127.0.0.1:8080>, with columns from `docket.yaml`, cards you drag, task pages,
the wiki and a search. Dragging a card is the same status move as the form on the task page: it
goes through the API, is checked against the fingerprint the card was drawn from, and lands in
git as a commit.

The same folder opened in Obsidian is the same board. Neither view is an export of the other.

## Check before you commit

```bash
docket check
```

```
No findings.
```

It prints a file and a line for anything wrong and exits non-zero when there is at least one
finding, so it is a pre-commit hook in three lines:

```sh
#!/bin/sh
exec docket check .
```

## Commit

```bash
git add -A && git commit -m "The redirect loop is fixed: the session cookie is set on /"
```

One commit per logical change, describing what changed for the reader rather than which files
moved. The commit history *is* the change history of the tracker — there is no separate audit
log, so `git log` is the only record there will ever be of who moved what.

## Cut the release

```bash
docket set ACME-1 status=Done
git commit -qam "ACME-1 is done"
git tag -a v0.1.0 -m "First cut"
```

That is the whole release procedure. There is no version object to create, no `fixVersion` to
set on every issue and no notes to generate: the Releases page reads the tags out of the
repository each time it is opened and works out what went into each one from the task files that
changed since the tag before it. It cannot be stale, because there is nothing being kept in
step.

## What you have now

```bash
docket export --format csv --fields key,title,status,priority
```

```
key,title,status,priority
ACME-1,Fix login redirect loop,Done,high
ACME-2,"The session model, rewritten",Backlog,normal
```

A git repository. Clone it and you have the whole tracker, offline, with its history. Delete the
tool and the project is still readable, because it was only ever Markdown.

## Next

- [A release is a tag](a-release-is-a-tag.md) — what the Releases page computes, and why
- [Sprints](sprints.md) — putting the work in fortnights
- [An agent runs the board](an-agent-runs-the-board.md) — handing this loop to an agent
