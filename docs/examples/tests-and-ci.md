# Tests, and results from CI

The largest app in the library. It brings test plans, sets, cases, executions and runs — the
thing Zephyr and Xray sell — and it feeds on the output your pipeline already produces.

```bash
docket app add https://github.com/didenkolab/docket-apps.git#tests
docket check && git add -A && git commit -m "Installed the app tests"
```

## The vocabulary

Five types: `test_plan`, `test_set`, `test_execution`, `test`, `test_run`. Four relation pairs
that matter:

| Relation | Meaning |
|---|---|
| `tests` / `tested_by` | This case covers that story |
| `runs` / `run_by` | This run belongs to that execution |
| `found` / `found_in` | This failing run found that bug |
| `includes` / `included_in` | This set holds that case |

`found` is the one that pays for the app. A failing run links to the bug it produced, so the bug
page shows which test caught it and the test page shows what it has ever caught.

## Identity: the tag in your feature file

A case needs an identity your test suite also knows. Written where a person would write one:

```gherkin
@ACME-BKG-001
Scenario: a booking cannot overlap another on the same berth
```

Where nobody wrote one, the app derives an id from the scenario, so an untagged suite still
imports — it just cannot survive the scenario being renamed. Tag what you care about; let the
rest derive.

## Bringing the suite in

```bash
hooks/import-features.sh /path/to/repo
```

Reads the feature files and writes a `test` for each scenario, linked into sets by feature file.
Run it again after the suite changes and it updates rather than duplicating, because the
identity is the tag.

## Bringing the results in

Use **behave's JSON**, not JUnit:

```bash
behave --format json --outfile results.json
hooks/import-cucumber.sh results.json
```

JUnit carries the feature name and the scenario title, and nothing else. It cannot name the case
id, so results imported that way can only be matched by title — and a renamed scenario silently
becomes a new test with no history. The JSON carries the tags, which is the identity.

Each import writes a `test_execution` with a `test_run` per scenario, carrying `result`,
`ran_at`, `environment` and `revision`. The execution is a page you can open six months later
and see what the suite did on that commit.

## From your pipeline

```yaml
- run: behave --format json --outfile results.json
- run: hooks/import-cucumber.sh results.json
- run: |
    git add -A
    git commit -m "Test results at ${GITHUB_SHA}"
    git push
```

The results land as a commit, like everything else. There is no results database and no API to
post to — which also means a pipeline with no network access to your tracker is not a problem to
solve.

For a pipeline that cannot commit, the tests app brings `hooks/receive-junit.sh` and an inbox
the server accepts a file into, guarded by `DOCKET_JUNIT_SECRET`. Documented, and less good:
prefer the commit.

## Coverage, by blame

```bash
hooks/link-coverage.sh /path/to/repo
```

If your commit subjects name the task they were for — `ACME-42: refuse overlapping bookings` —
this works out by blame which story each scenario covers, and writes the `tests` relation. You
get a coverage page nobody had to fill in, from a convention you probably already follow.

## Known limitation

The **Run the scenarios** button that the app puts on a test page does not currently land a
result: its contract with `./run-tests` and the JUnit import do not meet. It is filed as DKT-57
on the project's board with the three specific breaks. The two routes above — a commit from CI,
and the JSON import — are the ones that work, and they are what the showcase vault uses.

This is written down rather than hidden because you will find the button.

## Next

- [Write your own app](write-an-app.md) — how these hooks are put together
- [A release is a tag](a-release-is-a-tag.md) — tying a green suite to what shipped
- [`docket-apps`](https://github.com/didenkolab/docket-apps) — the app's own documentation
