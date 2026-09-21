# Moving off Jira

Three commands, and only the first touches the network. Everything after it runs against a cold
snapshot on your disk, so a mapping can be redone as many times as it takes without pulling the
source again — and an interrupted extract resumes where it stopped.

```bash
docket import extract --site https://example.atlassian.net \
  --email you@example.com --token "$DOCKET_TOKEN" --snapshot ./snap
docket import plan --snapshot ./snap
docket import apply --snapshot ./snap --project ACME --vault ~/work/acme
```

`apply` writes the vault in one reviewable commit. If the mapping was wrong, `git reset --hard`
and plan it again.

## What `plan` is for

It proposes mappings and writes them where you can edit them: which Jira status becomes which of
yours and in which category, which issue type becomes which type at which level, which custom
field becomes which field, and who each account is. Read them. The defaults are guesses, and the
ones about people and about status categories are the ones that matter.

A cancelled-in-done workflow survives the trip because status is a pair here — the name and the
category — so `Cancelled` can be `done` without pretending it was finished.

## What arrives

| In Jira | Here |
|---|---|
| Issue | A Markdown file named after its key and title |
| Issue key that outlived a rename | `aliases`, so old links still resolve |
| Custom field nobody mapped | `x_`-prefixed frontmatter, untouched and uninterpreted |
| Issue link | A relation, written as a wikilink |
| Comment | Under `## Comments`, in order |
| Attachment | A file under `attachments/`, linked from the task |
| History Jira had and git never saw | `_history/` |

The `x_` prefix is the honest answer to a field nobody claimed: it is kept, it is readable, and
nothing pretends to understand it. `x_fixversion` is history — what the old system said. Do not
try to keep it in step with your git tags.

## The part everybody needs afterwards

An import cannot know which of a dozen custom fields is the sprint. It arrives as
`x_customfield_10020` or `x_спринт`, and the board's own sprint stays empty beside it. On a real
imported project that is ninety-nine tasks in twelve sprints that no sprint page and no
"what did we commit to" can see.

```bash
docket adopt --sprint x_customfield_10020 --estimate x_storypoints --people --dry-run
docket adopt --sprint x_customfield_10020 --estimate x_storypoints --people
```

`--sprint` writes a page per sprint and points every task at it. `--estimate` promotes a number
to the vault's own estimate. `--people` writes a page for each handle the tasks name and turns
the assignees into links.

Sprint dates are inferred from the work — the earliest task created and the last one changed.
That is a guess, and every page says so in a line you should correct. A guess written as a fact
is worse than a blank.

Always `--dry-run` first.

## After the import

```bash
docket check
docket anomalies --kind one-sided
docket people
docket graph
```

`one-sided` is the one that finds import damage: Jira's link table has both directions and a
one-sided relation here means a pair did not survive. `docket people` finds the accounts that
never got a page.

## What this has and has not been run against

The pipeline has been run against a live instance of eleven hundred issues, which found four
defects nothing else had. Extraction is otherwise exercised against a stub server, and
everything downstream against snapshots built in tests.

The **Confluence half is not exercised**. `extract` takes `--spaces` and the code path exists;
nobody has pointed it at a real space. Treat it as untested, and read the diff before you commit
it.

## What you lose, honestly

Automation rules, dashboards, boards configured in the UI, and anything a paid add-on stored in
its own tables. The first three have answers here that are files rather than settings. The
fourth does not come across at all — export it separately before you turn the instance off.

## Next

- [Many projects, one vault](many-projects-one-vault.md) — one Jira project becomes one repository
- [Sprints](sprints.md) — what `adopt --sprint` produced
- [A release is a tag](a-release-is-a-tag.md) — what to do about `fixVersion`
