# A release is a tag

There is no version object, no `fixVersion` to set on every issue, and no release notes to
generate. A release is a git tag, and what went into it is computed from the repository every
time anyone looks.

```bash
git tag -a v1.2.0 -m "Sessions survive a restart"
git push --follow-tags
```

That is the whole procedure.

## When do you cut it — before the work, or after?

**After. Always after.** A tag points at a commit, so it cannot exist before the work does. That
is not a limitation being worked around; it is the property that makes a release page unable to
lie.

This is the one habit somebody arriving from Jira has to drop. There you create a version up
front, set `fixVersion` on every issue as you plan, and press Release at the end. Three records
of one intention — the version object, the field on each issue, and the notes generated from
them — kept in step by hand, and re-edited every time the plan changes.

Here the plan and the record are different objects, on purpose:

| You want | Use |
|---|---|
| "What are we doing next?" | A [sprint](sprints.md) — a page with dates, and tasks that link to it |
| "What if we shipped it this way instead?" | A [branch](../how-you-work-in-this.md) — the Branches page draws the board it would produce |
| "What is left before we can ship?" | The board. A status, not a version field |
| "What did we ship?" | The tag, cut when you ship it |

If you genuinely want a named thing to plan into — a page under `docs/` called `v1.1` with links
to the work intended for it — write one. It is a plan document and it says so, and when the
release happens the tag is the record and the page becomes history. What you should not do is
try to make the tag the plan: a tag moved to keep up with a changing plan is a tag that no
longer says what shipped, which was the only thing it was for.

## What the Releases page does with that

It reads the tags out of the repository, works out which task files changed between each tag and
the one before it, and parses those files as they were **at the tag**. So a release page shows
what the work looked like when it shipped, not what it looks like today.

Three consequences worth having:

**It cannot be stale.** Jira keeps a version object, a field on every issue pointing at it, and a
generator that turns the two into notes: three records of one fact, kept in step by hand. Here
there is one record and git maintains it.

**It is retroactive.** Tag a commit from three weeks ago and the release exists, complete, with
the right contents. Nothing had to be set on anything while the work was happening.

**There is nothing to forget.** Nobody has to remember to set a field on a task before closing
it. A task that changed between two tags is in that release because that is what the words mean.

## Ordering

Releases are ordered by the commit each tag points at, not by when somebody typed the tag
command. Tagging is often retroactive, and three releases labelled in one afternoon have tag
dates minutes apart — ordering by those would shuffle them into the order they were typed rather
than the order they shipped.

## What "went into a release" means exactly

Two questions, and the page answers them separately because they are not the same.

**Shipped** is the work that had reached a done status when the tag was cut. That is what a
release notes list is, and it comes first.

**Everything else the window touched** is behind a summary. A task created, edited or moved
between the two tags changed its file, so it is inside the range the release is computed from —
but it did not ship, and listing it beside the work that did is how a release page comes to say
a backlog item was released. It is still worth having: it is what was in flight when you cut.

A task whose acceptance criteria were rewritten and nothing else shows up in the second group,
which is right — something happened to it, and it was not shipping.

A release page shows how much of it was actually finished at the tag, because it parses the
files at that commit: a task still In progress when you tagged says so, on the release page,
forever. No amount of closing it afterwards changes what shipped.

## Annotated, not lightweight

```bash
git tag -a v1.2.0 -m "Sessions survive a restart"      # yes
git tag v1.2.0                                          # works, but says nothing
```

Use `-a`. The message is the release's description and there is nowhere else to write one.

## Pre-releases and the ones you undo

A tag is as disposable as any other git ref while it is only local:

```bash
git tag -d v1.2.0                          # never pushed: gone
git push --delete origin v1.2.0            # pushed: gone from the remote too
```

Deleting a pushed tag is the same conversation as force-pushing a branch, and has the same
answer: fine before anybody pulled it, rude afterwards. There is no state anywhere else to
clean up, which is the point — the release *was* the tag.

## Releasing from CI

Because the release is a tag, the pipeline that builds artefacts and the record of what shipped
are the same trigger:

```yaml
on:
  push:
    tags: ["v*"]
```

The binary this repository ships is built exactly that way: a tag pushed, a workflow that builds
for six platform pairs and attaches them to a GitHub release. Nothing in the tracker had to be
told that a release happened.

## What to do about a version field you already have

If you imported from Jira, every task probably carries `x_fixversion`. Leave it. It is history —
what the old system said — and prefixing it `x_` is how the format says "this came from
somewhere else and nothing here interprets it". Do not try to keep it in step with your tags;
you would be maintaining the second record this design exists to remove.

## Next

- [A project, from nothing to a release](a-project-from-nothing-to-a-release.md) — the loop this ends
- [Sprints](sprints.md) — the other kind of time box, and why it is a page and not a tag
- [Moving off Jira](moving-off-jira.md) — what happens to versions on the way in
