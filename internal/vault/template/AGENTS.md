# Working in this vault

This repository is a docket vault: a task tracker and a knowledge base made of Markdown files.
You change it with ordinary file tools. There is no API to call and no server to ask.

The [vault format](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/spec/vault-format.md)
is normative. This file is the short version — the rules you need in order to not corrupt the
vault.

## Keys and where files live

A key is `PROJECT/NUMBER`, and it is also the path: `{{.Key}}/12` is the task in
`{{.Key}}/12.md`. Knowing the key is enough to open the file, and `[[{{.Key}}/12]]` resolves
in Obsidian without any help.

The projects this vault holds are listed in `docket.yaml`, each a folder at the root. Numbers
start at 1 per project and are never reused.

To pick a key, list the project's folder, take the highest number, add one — or run
`docket new`, which does exactly that. If two agents pick the same number in parallel branches,
git reports an add/add conflict on merge: rename one task and fix inbound links. Do not
renumber existing tasks to close gaps; a key is permanent.

## Creating a task

Copy `templates/task.md` to `{{.Key}}/<n>.md` and fill the frontmatter. Every field in the
template is required except `parent`, `labels` and `aliases`.

```markdown
---
key: {{.Key}}/12
title: Fix login redirect loop
type: bug
status: In progress
status_category: doing
priority: high
assignee: agent/claude
parent: {{.Key}}/4
labels: [auth]
created: 2026-01-01T09:00:00Z
updated: 2026-01-01T09:00:00Z
aliases: []
---
```

## The two rules that matter most

**Frontmatter is flat.** No nested objects, ever. Obsidian's property editor only handles flat
values and Bases only filters on top-level properties — a nested field makes the task
uneditable by hand and invisible to the board. Vault-specific fields go at the same level as
core ones. Fields carried in from another system are prefixed `x_`.

**`status` and `status_category` move together.** `status` is the human name from `docket.yaml`;
`status_category` is its category in that same file. Changing one without the other puts the
task in a column the board cannot render. Both change in the same edit.

## Editing a task

- Set `updated` to the current UTC time on every change.
- Never edit `key`, `created`, or the file name.
- Append comments under `## Comments`, newest last, in the form
  `**<author> · <YYYY-MM-DD HH:mm>** — text`.
- Link to other tasks and pages with `[[{{.Key}}/4]]` and `[[docs/some-page]]`.

## Writing documentation

Pages go anywhere under `docs/`, in whatever tree makes sense. There is no schema — it is a
wiki. The one convention: connect pages with `[[wikilinks]]`, so the graph and backlinks stay
useful. A page that nothing links to is a page nobody will find.

## Committing

One commit per logical change, describing what changed for the reader, not which files moved.
Moving a task to `In review` is a commit. Writing a page is a commit. The commit history *is*
the change history of the tracker: there is no separate audit log, and
`git log -p {{.Key}}/12.md` is how anyone sees who moved what and when.
