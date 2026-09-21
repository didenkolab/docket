# What the board cannot see

A board answers "what is in which column". It cannot answer "what is adrift", "which label has
stopped meaning anything" or "where does everything depend on one person" — because in a
tracker with a database those links are rows in a table and nobody looks at their shape.

Here the vault is a graph, so those questions are answerable.

```bash
docket anomalies
```

```
concentrated
  0001-a-session-is-a-signed-cookie holds a large share of every link in the vault
                             2 edges — 40% of all of them
```

## The seven kinds

```bash
docket anomalies --kind adrift
```

| Kind | The question it answers |
|---|---|
| `adrift` | What has nothing pointing at it — no parent, no relation, nothing linking in |
| `only-owner` | What only one person has ever touched |
| `overgrown` | Which container has grown past the size anyone can hold |
| `one-sided` | Where two tasks disagree: A says it blocks B, B does not say it is blocked |
| `contained` | What sits inside something it should not, by level |
| `unattended` | What has not moved in a long time while claiming to be in progress |
| `concentrated` | Which note holds a large share of every link in the vault |

**Every finding is a question, not a verdict.** A task nobody links to may be the most important
thing in the project. What this can say is "unusual, and here is why it noticed".

`one-sided` is the one to act on without thinking: nothing writes the other side of a relation
for you, so a one-sided link is almost always a mistake rather than a statement.

## The shape of the whole thing

```bash
docket graph
```

```
Most connected:
  0001-a-session-is-a-signed-cookie             2  50.0% of every link
  AGENTS                                        2  50.0% of every link

2 notes nothing links to, which is the question a list cannot answer:
  ACME-1 Fix login redirect loop
  ACME-2 The session model, rewritten
```

Opening the graph in Obsidian tells you it is busy. It does not tell you whether the busyness is
structure or a hairball — and on this project the picture looked the same before and after a
change that was measurably wrong twice: navigation pages holding eighteen per cent of every
edge, and sprint pages reaching twenty-eight. Both were found by counting, not by looking.

That is the argument against index pages, and it is why `AGENTS.md` forbids them: a page whose
purpose is to list other pages connects everything on it, lands in the middle of the graph, and
collapses the distance between clusters that have nothing to do with each other.

## Colouring it in your own words

```bash
docket graph --colours
```

Rewrites `.obsidian/graph.json` so the graph is drawn in this vault's vocabulary — its
containers, its statuses, its sprints — with forces that let clusters sit apart. Your zoom and
which panels you had folded are left as you set them.

## Who the work is on

```bash
docket people                 # what the vault has, and what it is missing
docket people --write         # a page for every handle the tasks name
docket people --from-git      # also everybody who has committed here
```

An assignee is a handle, and a handle should be somebody: a page in `people/` that the task
links to. Then Obsidian answers "what is Marina on" with its backlinks pane, and a typo is a
finding rather than a colleague.

Nothing is invented — a page holds the handle, the name if one is known, and nothing else.

## Running these after an agent

This is the sweep to run after an agent has been busy, before you merge its branch. It is
cheaper than reading the diff, and it catches the two things a diff hides: a relation written on
one side only, and a page that quietly became the most connected note in the vault.

## Next

- [An agent runs the board](an-agent-runs-the-board.md) — where this sweep belongs
- [Where the time went](where-the-time-went.md) — the time question
- [Write your own app](write-an-app.md) — `--json` on both commands, for your own page
