# OKRs and the portfolio

Two apps, one idea: work rolls up. An epic is a container of tasks, a key result is a container
of epics, and the roll-up is computed from the links rather than typed into a status report.

```bash
docket app add https://github.com/didenkolab/docket-apps.git#okr
docket app add https://github.com/didenkolab/docket-apps.git#portfolio
docket check && git add -A && git commit -m "Installed the apps okr and portfolio"
```

## The vocabulary they bring

The OKR app adds two types above the epic — `objective` at level 2 and `key_result` at level 1 —
and the fields that make a key result measurable:

| Field | On | Why it is required |
|---|---|---|
| `target` | `key_result` | The number that means done. "Ninety per cent" is `90` |
| `current` | `key_result` | Where it is now |
| `measure` | `key_result` | Where the number comes from. A key result nobody can measure is a wish |
| `quarter` | both | Which quarter it belongs to |

`measure` being required is the opinion in this app. A key result whose source of truth is "ask
someone" is the one that gets reported green for two months.

It also brings one relation — `contributes_to`, with the inverse `advanced_by` — which is how an
epic says which key result it moves.

## Writing a quarter

```bash
docket new "Sessions people do not notice" --type objective
docket new "Median session restore under 200ms" --type key_result --parent ACME-30
docket set ACME-31 target=200 current=1400 measure="p50 of restore_ms in the weekly export" quarter=2026Q4
```

Then point the work at it:

```bash
docket set ACME-12 contributes_to=ACME-31
```

Because that is a link, the key result gets a backlink from every epic advancing it, and the
graph shows which objectives have work under them and which are aspirations with nothing
attached. That second group is the whole reason to draw it.

## The roll-up

```bash
docket serve --programs
```

The portfolio app adds a **Portfolio** page: every container with what is under it, how much is
finished, and how much is not sized. It is computed on each render from `docket export`, so it
cannot disagree with the board — there is no snapshot, no nightly job and nothing to refresh.

Two rules the roll-up depends on, both enforced by `docket check`:

- **A container's estimate is the sum of its children's.** Do not write an estimate on a task
  that has children; it will be the record that disagrees.
- **A parent sits above its child by level.** An epic holds a task, a task holds a sub-task, and
  a bug cannot hold an epic. `docket.yaml` says which level each type is at, which is why levels
  are declared rather than guessed — a real team names its types in its own language.

## What is not sized

```bash
docket serve --programs      # the "Not sized" page, from the estimation app
```

Unestimated work is the hole in every roll-up, and the honest thing is to show it as a hole
rather than counting it as zero. Absent and `0` mean different things here: `0` says there is no
work in the task, absent says nobody has estimated it.

## Why this is not a reporting product

Nothing here aggregates on a schedule, stores a snapshot, or has a dashboard that can be stale.
Each page is a program that runs when the page is opened and reads the same files the board
reads. When you want it drawn differently, the program is nineteen lines of shell in your own
repository — see [Write your own app](write-an-app.md).

## Next

- [Sprints](sprints.md) — the fortnight below the quarter
- [Write your own app](write-an-app.md) — changing what the roll-up draws
- [What the board cannot see](what-the-board-cannot-see.md) — objectives with nothing under them
