# {{.Name}}

An [docket](https://github.com/vadymdidenkolab/docket) vault: a task board and a knowledge base
kept as Markdown files in git.

Clone it, open the folder in Obsidian, and you get a board, a backlog and a wiki. There is
nothing to install and nothing to run — tasks and pages are plain Markdown with YAML
frontmatter, and git is the history.

| Path | What |
|---|---|
| `tasks/` | One Markdown file per task, named after its key: `{{.Key}}-12.md` |
| `docs/` | Knowledge base — a free tree of wiki pages |
| `boards/` | Obsidian Bases views: board, backlog, my tasks |
| `templates/` | Templates for a new task and a new page |
| `project.yaml` | Project key, statuses, task types |
| `AGENTS.md` | How an agent works in this vault |

The format is specified in
[docket-board](https://github.com/vadymdidenkolab/docket-board/blob/main/docs/spec/vault-format.md).
