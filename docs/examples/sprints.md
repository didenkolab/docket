# Sprints

A sprint is a page, and being in it is a link. There is no sprint object, no board configuration
and no ceremony in the tool — which means a team that does not work in sprints pays nothing for
the feature existing.

## Make one

A page under `docs/sprints/`, named after its title:

```markdown
---
title: Sprint 14
type: sprint
starts: 2026-09-21
ends: 2026-10-04
updated: 2026-09-21
---

# Sprint 14

The goal is that a session survives a restart. Everything else is what we will drop first.

## Cut from this sprint

The exporter rewrite. It is bigger than the goal and would eat the fortnight.

## Retrospective

<!-- written at the end -->
```

There is no state field. Whether a sprint is running is a question about its dates and today,
and a field saying `active` is a second record of that fact — the one that will be wrong on a
Monday morning.

## Put work in it

```bash
docket set ACME-12 sprint="Sprint 14"
```

Written as a link, after `assignee`:

```yaml
sprint: "[[Sprint 14]]"
```

**Do not list the tasks on the sprint page.** Its contents are its backlinks — Obsidian's
backlinks pane answers "what is in Sprint 14" without anything being maintained, and a list
written by hand is a copy that goes stale the first time somebody carries a task over.

## Carrying work over

One sprint at a time: point the field at the new sprint.

```bash
docket set ACME-12 sprint="Sprint 15"
```

The old sprint's retrospective is where it is recorded that ACME-12 did not finish. That is the
honest place for it — a sprint's value afterwards is the account of what happened, and a task
silently moving out of it leaves no account at all.

## The board for a sprint

The template ships `boards/sprint.base`, an Obsidian Base that selects the tasks linking to the
current sprint. Open it in Obsidian and it is a sprint board. Edit it and it is your sprint
board — it is a file.

## What did we commit to, and what happened

```bash
docket report time-in-status
```

```
2 tasks, 8 moves

STATUS                  ENTERED    NOW         MEDIAN        LONGEST
Backlog                       1      1              —              —
In progress                   1      0              —              —
Done                          1      1              —              —
```

Every move was a commit, so this is a read of `git log` and some arithmetic — not a changelog
API behind a paywall. Four of the Jira marketplace's top hundred sell this one report.

## Estimates, if you use them

One unquoted number, after `priority`:

```yaml
estimate: 3
```

The unit and the allowed values are in `docket.yaml` — points on a Fibonacci scale in a fresh
vault. Two ways to get it wrong, and `docket check` reports both:

- a number that is not on the declared scale, which is a finding rather than something to round;
- an estimate on a task that has children, because a container's estimate is the sum of its
  children's and is added up when shown. Writing that sum down is a second record of one fact,
  and it is the one that will disagree.

Absent is not zero. `0` says there is no work in the task; absent says nobody has estimated it.
Leave the field out rather than filling it with a guess — `docket report` and the estimation app
both treat the two differently, and so should you.

```bash
docket serve --programs        # the "Not sized" page, from the estimation app
```

## If you do not work in sprints

Do not make the pages. Nothing requires them, no board depends on them, and no report is empty
because of it. The field is optional and the directory can stay missing.

## Next

- [A release is a tag](a-release-is-a-tag.md) — the other time box, and why that one is a tag
- [Where the time went](where-the-time-went.md) — time in status, in detail
- [OKRs and the portfolio](okrs-and-the-portfolio.md) — the quarter above the fortnight
