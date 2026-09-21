# An agent runs the board

The plan and the code are in git, so an agent that is already reading one can read the other
from the same checkout — and what it did to both arrives in one pull request. This is the part
no hosted tracker can offer, because its plan lives somewhere your agent can only reach through
an API and a token you had to issue.

There are three ways in. They drive the same files and differ only in how much the agent has to
be told.

## 1. The skill

`skill/docket/SKILL.md` in this repository is one file an agent reads once and then knows the
loop: what to pick up, how to take it, how to move it, what to write down, how to close a
release, and the handful of rules that break a vault when broken.

```bash
cp -r skill/docket ~/.claude/skills/
```

After that, "pick up the next unblocked bug in this vault and fix it" is a complete instruction.
The skill is a file in this repository, so you can read what you are installing, and change it
for your team.

## 2. AGENTS.md, with nothing installed

Every vault `docket init` scaffolds carries an `AGENTS.md` at its root: the format rules in full.
An agent that reads `AGENTS.md` or `CLAUDE.md` — most do — is oriented without you installing
anything at all.

This is the floor, and it is deliberately high: a vault a stranger's agent wanders into should
be hard to corrupt.

## 3. MCP, when a tool call beats a file write

An agent can edit the files directly, and that is the point of the format. But "move this to In
review" is better as one call that validates, writes and commits than as four file operations
that might each be half-right.

```bash
claude mcp add docket -- docket mcp --author "Claude <claude@acme.dev>" /path/to/vault
```

Eight tools: `list_tasks`, `get_task`, `create_task`, `update_task`, `search`, `read_page`,
`write_page`, `check`.

`--author` is required. Every write is a commit, and an agent's commits should say which agent
made them — six months later, `git log --author=claude` is how anyone answers "what did the
agent actually do here".

Two things MCP gives you that file writes do not:

**A write cannot clobber a person.** `get_task` returns a `version` — the same fingerprint the
board draws its cards from — and `update_task` refuses a write whose version no longer matches.
An agent cannot land on top of an edit somebody made in Obsidian while it was thinking.

**The workflow is enforced.** A move the vault forbids comes back as an error naming the moves
it allows, instead of a file in a state the board cannot render.

## What to let it do

The useful division is not "read-only versus write". It is **who decides**.

| Let the agent | Keep for a person |
|---|---|
| Create tasks, move them, comment, link them | Changing `docket.yaml` — the vocabulary and the workflow |
| Write design and specification pages | Writing a decision that had a real fork |
| Run `docket check`, `anomalies`, `report` | Closing work it did not finish |
| Import results, update estimates | Cutting a release tag |

None of that needs enforcing in the tool, and the tool does not try. It is enforced where
everything else is: the agent works on a branch, and the branch is reviewed.

## The branch is the review

```bash
git switch -c agent/acme-12
# agent works
git push -u origin agent/acme-12
```

The diff is the whole change: which tasks it created, which it moved, what it wrote in each
body, which relations it drew. Reviewed on the lines, like code, because it *is* lines. Merge it
or close it, and if you close it nothing happened — there is no live instance that already
applied the change and no activity feed recording an edit you rejected.

The **Branches** page draws the board each branch would produce, read out of the object
database, so you can look at the agent's proposed plan as a board before merging it.

## Keeping the agent honest

```bash
docket check          # before every commit; non-zero when something is wrong
docket anomalies      # what is odd about how the work is now connected
docket graph          # whether the links it drew are structure or a hairball
```

`anomalies` is the one to run after an agent has been busy. It answers questions a board cannot
ask: what is adrift with nothing pointing at it, which label has stopped meaning anything,
where two tasks disagree about their own relationship. Every finding is a question, not a
verdict — a task nobody links to may be the most important thing in the project.

## What this is not

It is not an agent that runs by itself on a schedule. Nothing here polls, fetches or acts
without being asked: a tracker that rebases your working copy out from under you is a tracker
you stop trusting. The agent runs when you run it, in a checkout you can see, and everything it
did is a commit you can read or drop.

## Next

- [A project, from nothing to a release](a-project-from-nothing-to-a-release.md) — the loop the skill teaches
- [What the board cannot see](what-the-board-cannot-see.md) — anomalies, in detail
- [Who may do what](who-may-do-what.md) — access, when more than one person is involved
