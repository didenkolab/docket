# How you work in this

The README says what the tool is. This says what a week looks like when you use it — who does
what, where each kind of thing goes, and what you stop doing.

Read this before the cookbook. The recipes assume the shape described here.

## The one idea

**The plan lives where the code lives.** Not beside it, not linked to it — in the same
repository, in the same branch, arriving in the same pull request.

Everything below follows from that. The reason a task is a file is so that a change to the plan
is a diff. The reason every move is a commit is so that the history of the project is the history
of the repository. The reason an agent is the first-class user is that an agent already has the
repository open.

If you take one habit from this page, take this one: **when you change what you are building,
change the plan in the same commit.** That is the whole discipline, and nothing else here is
hard.

## Who does what

The useful split is not "human writes, agent executes". It is **who decides**.

| The agent does | You decide |
|---|---|
| Creates tasks, moves them, comments, links them | What the vocabulary is — statuses, types, the workflow in `docket.yaml` |
| Writes design and specification pages | Whether a fork was real enough to be a decision |
| Runs `check`, `anomalies`, `report`, `graph` | Whether work is actually finished |
| Imports test results, updates estimates | When to cut a release |
| Drafts the plan for a change, on a branch | Whether to merge that branch |

None of that is enforced by the tool, and the tool does not try. It is enforced where everything
else is: the agent works on a branch, and you read the diff.

Give the agent `skill/docket/` once and it knows the loop. After that, "pick up the next
unblocked bug and fix it" is a complete instruction, and what comes back is a branch with the
code change and the plan change in it.

## A day

**Morning — what is there.** Not a standup, a query:

```bash
docket export --open --format csv --fields key,title,status,assignee,priority
```

Or open the board — in Obsidian if you live there, at `docket serve` if you do not. They are the
same files; use whichever you already have open.

**Pick something up.** `docket set PROJ-12 assignee=you status="In progress"`. If it is blocked,
the frontmatter says so and says by what.

**Work.** Code in the repository, plan in the same repository. When the work changes what the
task said, edit the task in the same commit as the code. This is the habit that makes everything
else true.

**Write down what you found, not that you are busy.** The status already says you are busy. A
comment is worth writing when it holds something the next person would otherwise have to
rediscover.

**Commit as you go.** One commit per logical change, with a message that says what changed for
the reader. There is no audit log other than `git log`, so a message saying "wip" throws away the
only record there was.

## A week

**Proposals go on branches.** Re-scoping a release, splitting an epic, dropping a quarter: make a
branch, change the tasks, open a pull request. The Branches page draws the board that branch
would produce, so a plan change can be looked at as a board before anyone argues about it. Merge
it, or close it and nothing happened.

This is the part no hosted tracker can do. In Jira a plan change is applied immediately and
irreversibly to the one live instance, and the record of it is an activity feed nobody reads.

**Once a week, run the two questions a board cannot answer:**

```bash
docket anomalies        # what is adrift, one-sided, overgrown, concentrated
docket report time-in-status
```

`anomalies --kind one-sided` finds relations somebody wrote on one side only. The time report
tells you which column is a queue. Neither is a dashboard that can go stale — both are computed
when you ask.

**After an agent has been busy, run them before you merge.** They are cheaper than reading the
whole diff and they catch the two things a diff hides.

## A release

```bash
git tag -a v1.2.0 -m "What this release is"
```

That is it. No version object, no field to set on every task, no notes to generate. What went
into the release is computed from the repository, so it cannot be wrong, and it can be done
retroactively — tag a commit from three weeks ago and the release exists, complete.

## Where each thing goes

The most common way to get lost is putting something in the wrong kind of file.

| You have | It goes in |
|---|---|
| Work somebody must do | A task — `docket new` |
| Something you learned while doing it | A comment on that task |
| How a mechanism works, and why it is that way | A design page under `docs/design/` |
| A fork in the road that was hard and is expensive to revisit | A decision under `docs/decisions/`, with its alternatives |
| What must be true, that `docket check` enforces | A spec under `docs/spec/` |
| What this fortnight is for | A sprint page under `docs/sprints/` |
| A theme work gathers around | A label — a link, one or two per task |
| A slice you want to search by | A tag — costs the graph nothing, use freely |

Two rules that save the most grief:

**Never write a page whose purpose is to list other pages.** No index, no "see all", no front
page linking every epic. A list connects everything on it, lands in the middle of the graph, and
collapses the distance between things that have nothing to do with each other. Search, backlinks
and the file explorer are how a page is found, and none of them draws an edge.

**A relation is a link, by note name.** `"[[PROJ-4 Session model]]"` — not `[[PROJ-4]]` and not a
bare word. That is what makes an epic have an edge to each of its tasks and a label be a hub. The
same word written plainly connects nothing.

## Three ways in, one set of files

| | Use it for |
|---|---|
| **Obsidian** | Reading, writing bodies, following links, the graph. Where thinking happens |
| **`docket serve`** | A board for people who do not run Obsidian; dragging cards; giving somebody a URL |
| **An agent** | The repetitive parts, and anything that touches the code and the plan together |

Point all three at one repository at once. Nothing is cached, and a write that would land on top
of a change made elsewhere is refused rather than applied.

## What you stop doing

- **Keeping the tracker in step with reality.** They are the same commit now.
- **Writing status reports.** The board is the report; `time-in-status` and `anomalies` are the
  parts a board cannot show.
- **Grooming a backlog in a web UI.** It is a folder of files — rename, move, edit in bulk, with
  the tools you already have.
- **Asking who changed something.** `git log --follow` on the file.
- **Paying for add-ons.** Twelve packs install with a URL and a `git diff`.
- **Migrating.** `git clone` is the export, and it is also the backup and the archive.

## How it goes wrong

**Somebody edits frontmatter by hand and breaks it.** Use `docket set`; it validates. Run
`docket check` as a pre-commit hook and it cannot reach the remote:

```sh
#!/bin/sh
exec docket check .
```

**Two branches allocate the same key.** Git reports an add/add conflict on merge. Rename one task
and fix the inbound links. Do not renumber to close gaps — a key is permanent.

**The graph turns into a hairball.** Usually one page that links everything. `docket graph` names
it and says what share of every edge it holds.

**A retitle is two changes.** Editing `title` leaves the file name and every link to it naming
the old one. `docket check --fix` settles both — it renames the file, then puts every link back
on the note. Commit them together.

**Somebody expects the server to sync.** It does not fetch on its own, by design: a tracker that
rebases your working copy out from under you is a tracker you stop trusting. Pull on a schedule
beside it if you want that.

## Next

- [The cookbook](examples/) — twelve recipes, starting with a project from nothing to a release
- [`skill/docket/`](../skill/docket) — what to hand your agent
- The README's [How it works](../README.md#how-it-works) — the mechanisms, in detail
