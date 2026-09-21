# Workspace

A [docket](https://github.com/didenkolab/docket) workspace: several project vaults
assembled into one Obsidian vault.

Open this folder in Obsidian and you get every project at once — `[[wikilinks]]`, search,
backlinks and the graph work across project boundaries, because to Obsidian it is one file
tree.

Each project is its own git repository with its own remote, its own history and its own
access. This repository tracks only the Obsidian config and the manifest; the project folders
are ignored.

```bash
docket workspace add --key ACME --remote git@github.com:example/acme.git
docket workspace sync
```

`sync` clones what is missing and fast-forwards what is there. It never touches a project with
uncommitted changes — it reports it and moves on.
