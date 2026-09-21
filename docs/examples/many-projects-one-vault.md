# Many projects, one vault

There are two different things people mean by "several projects", and they have different
answers.

## One repository, several projects

When the projects belong to one team and one access boundary, they are folders in one vault:

```bash
docket project add --key BETA --name "Beta Service"
```

Each project is a folder at the root, each with its own numbering, and `docket.yaml` lists them.
Adding one also regenerates the boards, because a board selects tasks by naming the project
folders — a project no board mentions is a project whose work is invisible.

Links across projects just work, because the projects are one file tree:

```bash
docket set ACME-12 blocked_by=BETA-7
```

This is the right answer more often than people expect. A company small enough to share one
access boundary gets cross-project links, one graph, one search and one clone.

## Several repositories, one Obsidian vault

When the projects have different access — different teams, different clients, one of them a
customer's — each stays its own git repository, and a workspace assembles them:

```bash
docket workspace init space && cd space
docket workspace add --key ACME --remote git@github.com:example/acme.git
docket workspace add --key BETA --remote git@github.com:example/beta.git
docket workspace sync
```

```
workspace.yaml     the manifest — the only thing the workspace itself tracks
.obsidian/         the shared Obsidian configuration
acme/              a clone, ignored by the workspace's git
beta/              a clone, ignored by the workspace's git
```

`sync` clones what is missing and fast-forwards what is there. A project with uncommitted changes
is reported and left alone — it will not rebase your working copy out from under you.

Open the workspace directory in Obsidian and it is one vault over many repositories. Run
`docket serve` in it and the board spans all of them, with each write committed to the repository
that owns the task.

## Which to choose

| | One repository | A workspace |
|---|---|---|
| Access | One boundary: everyone who can clone sees everything | Per repository, granted on the git host |
| Links across projects | Yes, resolved | Yes in Obsidian; each side commits to its own repo |
| Clone | One | One per project, plus the manifest |
| A client's board among your own | No | This is what it is for |

The deciding question is never "how many projects". It is **who may read this** — because anyone
who can clone a repository has everything in it, including its history. That is not a policy the
tracker can soften, so the repository boundary has to be the access boundary.

## A board across several repositories, without a workspace

`docket serve` in a workspace does this already. What it does not do is merge the projects'
vocabularies: columns are the union of their statuses, and a card is checked against the rules
of its own project. Two projects with different workflows stay different — which is the point,
since they are different teams.

## Next

- [Who may do what](who-may-do-what.md) — the access question this turns on
- [A project, from nothing to a release](a-project-from-nothing-to-a-release.md) — a single project first
- [Moving off Jira](moving-off-jira.md) — one Jira project becomes one repository
