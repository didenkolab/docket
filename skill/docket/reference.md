# Reference

Detail the loop in `SKILL.md` does not need every time. Read the section you are in.

## Documents

Not everything is work. Pages live under `docs/` and each carries `title`, `type` and `updated`
(a plain `YYYY-MM-DD`, set on every change). `type` decides where it lives:

| `type` | What it is | Where |
|---|---|---|
| `decision` | A choice that was hard to make and is expensive to revisit | `docs/decisions/NNNN-kebab-title.md` |
| `design` | How something works and why it is that way | `docs/design/` |
| `spec` | What must be true; what `docket check` enforces | `docs/spec/`, with `status: normative` |
| `sprint` | A fortnight: the goal, what was cut, the retrospective | `docs/sprints/`, with `starts` and `ends` |
| `page` | Everything else | anywhere under `docs/` |

**A decision is the only kind with a required shape:** `## Context`, `## Decision`,
`## What this costs`, `## Alternatives considered` — spelled that way, in that order. It also
carries `status: proposed | accepted | superseded` and `date`. Numbers run from `0001` and are
never reused or renumbered, because the number is what gets cited.

Write a decision only when there was a real fork: alternatives somebody would have argued for,
and a cost that was accepted. Explaining how a mechanism works is a design page, even when it
argues hard for itself. The expensive mistake is the other direction — a fork recorded as a
design page never writes its alternatives down, and the argument gets had again.

## Never write an index page

No index of labels, no "see all", no front page linking every epic.

A link is a relationship, not a route. A list connects everything on it, so it lands in the
middle of the graph and collapses the distance between clusters that have nothing to do with each
other. On a vault of forty notes, two such pages were the two most connected notes in it and held
eighteen per cent of every edge.

Finding a page is what search, the quick switcher, the tag pane and backlinks are for — none of
them draws an edge. A front page may link the three or four pages somebody must read first; that
is a relationship. It may not link everything.

`docket graph` reports what a page costs, and `docket anomalies --kind concentrated` names the
offenders.

## Labels and tags

- **A label is a theme work gathers around.** It is a link, so it costs the graph an edge. One or
  two per task; a task with five has labels that each mean too little.
- **A label page never links another label page.** Its value is its backlinks; linking siblings
  turns the labels themselves into a blob.
- **A tag is a slice to search by.** It is not a node and costs the graph nothing, so use tags
  freely: `area/...` for where the work is, plus whatever a search would want. Written without
  the `#` and unquoted: `tags: [area/auth, needs-review]`. No spaces, and not all digits.
- **Never say the same thing twice.** `labels: ["[[payments]]"]` together with `tags: [payments]`
  records one fact in two places, and the copy is the one that goes stale.
- **A sub-task usually needs no labels** — it is inside a task that has them.

## Estimates

One unquoted number, after `priority`: `estimate: 3`. The unit and the allowed values are in
`docket.yaml`; a fresh vault uses points on a Fibonacci scale.

- A number off the declared scale is a finding, not something to round.
- **Never estimate a task that has children.** A container's estimate is the sum of its
  children's and is added up when shown; writing it down is a second record that will disagree.
- **Absent is not `0`.** `0` says there is no work in the task; absent says nobody has estimated
  it. Leave the field out rather than guessing — the reports treat the two differently.

## Sprints

A page under `docs/sprints/` named after its title, carrying `starts`, `ends`, and a body that is
the goal, what was cut and why, and the retrospective. There is no state field: whether a sprint
is running is a question about its dates and today.

A task joins one by linking to it, after `assignee`:

```bash
docket set PROJ-12 sprint="Sprint 14"
```

One sprint at a time. Carrying a task over means pointing the field at the new sprint; the old
sprint's retrospective is where it is recorded that the task did not finish.

**Do not list tasks on a sprint page.** Its contents are its backlinks.

## An imported vault

Fields carried in from another system are prefixed `x_` — kept, readable, and interpreted by
nothing. Do not try to keep `x_fixversion` in step with git tags; it is history, not state.

The sprint a task was in usually arrives as an unmapped property. Promoting it is a person's
call, with `docket adopt --sprint <property> --dry-run` first. Sprint dates are inferred from the
work and every page says so in a line somebody should correct — a guess written as a fact is
worse than a blank.

## Reading the vault as data

```bash
docket export --format json --body        # tasks, with their Markdown
docket export --format csv --fields key,title,status,assignee,estimate
docket report time-in-status --json       # how long work sat in each column
docket anomalies --json                   # what is odd about how the work is connected
docket graph                              # what shape the links are in
docket people                             # handles with no page yet
```

Ask `docket export` rather than parsing the vault yourself. The awkward reading — following a
task through renames across its history — is already done there and tested.

`docket anomalies --kind one-sided` is the one to run after writing a lot of relations: it finds
the pairs where only one side was written.

## Anomaly kinds

`adrift`, `only-owner`, `overgrown`, `one-sided`, `contained`, `unattended`, `concentrated`.

Every finding is a question, not a verdict. A task nobody links to may be the most important
thing in the project.
