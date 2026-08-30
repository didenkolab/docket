# Working in this vault

This repository is a docket vault: a task tracker and a knowledge base made of Markdown files.
You change it with ordinary file tools. There is no API to call and no server to ask.

If `docket mcp` is connected, prefer its tools for anything that changes a task: they allocate
the key, keep `status` and `status_category` together, check the move against the workflow,
rename the file when the title changes and commit — each of which is a way to corrupt the vault
by hand. Read with file tools freely; this file tells you how.

The [vault format](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/spec/vault-format.md)
is normative. This file is the short version — the rules you need in order to not corrupt the
vault.

## Keys and where files live

A key is `{{.Key}}-12`. The file is named after the task — the key, a space, and the title:

```
{{.Key}}/{{.Key}}-12 Fix login redirect loop.md
```

The name carries the title because that is what Obsidian shows in the graph, in the file
explorer and in search. A file called `12.md` tells nobody anything.

The projects this vault holds are listed in `docket.yaml`, each a folder at the root. Numbers
start at 1 per project and are never reused.

**Link to a task by its whole note name**, not by its key:
`[[{{.Key}}-12 Fix login redirect loop]]`. Obsidian resolves the name of a note and does not
consult aliases, so `[[{{.Key}}-12]]` on its own points at nothing. `docket check` says so and
prints the form to use.

To pick a key, list the project's folder, take the highest number, add one — or run
`docket new`, which does exactly that. If two agents pick the same number in parallel branches,
git reports an add/add conflict on merge: rename one task and fix inbound links. Do not
renumber existing tasks to close gaps; a key is permanent.

## Creating a task

Copy `templates/task.md` to `{{.Key}}/{{.Key}}-<n> <title>.md` and fill the frontmatter. Every
field in the template is required except `parent`, `labels` and `aliases`.

```markdown
---
key: {{.Key}}-12
title: Fix login redirect loop
type: bug
status: In progress
status_category: doing
priority: high
assignee: agent/claude
parent: "[[{{.Key}}-4 Session model]]"
labels: ["[[auth]]"]
created: 2026-01-01T09:00:00Z
updated: 2026-01-01T09:00:00Z
aliases: []
---
```

## The three rules that matter most

**A relationship is a link.** `parent` and every entry in `labels` are wikilinks — not bare
words:

```yaml
parent: "[[{{.Key}}-4 Session model]]"
labels: ["[[auth]]", "[[regression]]"]
```

A wikilink is the only pointer Obsidian resolves, draws in the graph and counts as a backlink.
The same word written plainly connects nothing, so an epic written that way has no edge to its
tasks and a label groups nothing. Link by note name — `[[{{.Key}}-4 Its title]]`, never
`[[{{.Key}}-4]]`, because Obsidian does not consult aliases. Quote them: unquoted,
`[[auth]]` is a nested list in YAML. A label link need not resolve; an unresolved link is still
an edge, and writing the page later is what gives the label somewhere to explain itself.

**Frontmatter is flat.** No nested objects, ever. Obsidian's property editor only handles flat
values and Bases only filters on top-level properties — a nested field makes the task
uneditable by hand and invisible to the board. Vault-specific fields go at the same level as
core ones. Fields carried in from another system are prefixed `x_`.

**`status` and `status_category` move together.** `status` is the human name from `docket.yaml`;
`status_category` is its category in that same file. Changing one without the other puts the
task in a column the board cannot render. Both change in the same edit.

## Editing a task

- Set `updated` to the current UTC time on every change.
- Leave `order` alone. It is where somebody dragged the card in its column; a task without
  one sorts after the ones that have one, which is where a new task belongs.
- Never edit `key` or `created`. Changing `title` renames the file too; `docket check`
  reports a name that no longer matches.
- Append comments under `## Comments`, newest last, in the form
  `**<author> · <YYYY-MM-DD HH:mm>** — text`.
- Link to other tasks and pages by note name: `[[{{.Key}}-4 Its title]]`, `[[docs/some-page]]`.

## Writing documentation

Pages go anywhere under `docs/`, in whatever tree makes sense. There is no schema — it is a
wiki. The one convention: connect pages with `[[wikilinks]]`, so the graph and backlinks stay
useful. A page that nothing links to is a page nobody will find.

## Committing

One commit per logical change, describing what changed for the reader, not which files moved.
Moving a task to `In review` is a commit. Writing a page is a commit. The commit history *is*
the change history of the tracker: there is no separate audit log, and
`git log --follow -p "{{.Key}}/{{.Key}}-12 Fix login redirect loop.md"` is how anyone sees
who moved what and when — with `--follow`, because retitling renames the file.
