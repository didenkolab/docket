# A release is a tag

There is no version object, no `fixVersion` to set on every issue, and no release notes to
generate. A release is a git tag, and what went into it is computed from the repository every
time anyone looks.

```bash
git tag -a v1.2.0 -m "Sessions survive a restart"
git push --follow-tags
```

That is the whole procedure.

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

The task files whose contents changed between the two tags. Not "tasks closed in that window" —
a task that was created, worked on and closed shows up; so does one whose acceptance criteria
were rewritten. That is usually what you want from release notes, and when it is not, the diff
is right there to look at.

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
