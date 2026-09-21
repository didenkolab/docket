# Write your own app

An app is a git repository holding a `docket-app.yaml` and some files. Installing one adds its
types, fields and relations to the vault's `docket.yaml` and copies its templates, boards, pages
and hooks in. Nothing is executed at install time and nothing is committed for you: what an
installation did is a `git diff`, and you throw it away with `git checkout`.

There is nothing to register with and nobody to ask. A URL is the whole distribution mechanism.

## Install one first, to see the shape

```bash
docket app add https://github.com/vadymdidenkolab/docket-apps.git#checklists
```

```
wrote boards/sprint.base
wrote docket.yaml
wrote hooks/across.sh
wrote hooks/progress.sh

7 files changed. Look at them, then commit them.
```

```bash
git status                 # everything it wrote, unstaged
docket check
git add -A && git commit -m "Installed the app checklists"
```

A conflict is refused rather than merged: a field the vault already has as another kind, a
relation with a different inverse, a file somebody has edited — all of them are named, and
nothing is written. Installing the same app twice writes nothing.

`docket app list` says what a vault has taken on. Re-running `add` from the same place is how you
upgrade.

## The smallest useful app: vocabulary only

Many apps are nothing but words. Here is the whole of the risk register:

```yaml
# docket-app.yaml
name: risks
version: "1"
description: A risk register — likelihood, impact, and what each risk threatens

vocabulary:
  types:
    - name: risk
  fields:
    - name: likelihood
      label: Likelihood
      kind: choice
      choices: [rare, unlikely, possible, likely, almost_certain]
      types: [risk]
      required: true
    - name: impact
      label: Impact
      kind: choice
      choices: [negligible, minor, moderate, major, severe]
      types: [risk]
      required: true
    - name: owner
      label: Owner
      kind: text
      types: [risk]
      help: Who is watching this one. A risk nobody owns is a risk nobody sees.
  relations:
    - name: threatens
      inverse: threatened_by
    - name: mitigated_by
      inverse: mitigates
```

That is a product Atlassian sells. It is twenty-five lines of YAML here because the board was
already a graph of Markdown files — the register is a type, the severity is a choice field, and
"what this threatens" is a link.

`types` on a field scopes it: `likelihood` appears on a risk and nowhere else, so the bug form
does not grow two dropdowns nobody fills in.

## Put it in a vault

```bash
mkdir -p ~/apps/risks && cd ~/apps/risks
git init -q
$EDITOR docket-app.yaml
git add -A && git commit -m "The risks app"

cd ~/work/acme
docket app add ~/apps/risks
```

A path works everywhere a URL does, which is how you develop one. When it is right, push it
anywhere and the URL is the distribution.

## Drawing something: a panel

A surface is a program that prints Markdown. The vault declares it, and `docket serve
--programs` runs it — `--programs` is the consent, and without it nothing a vault declared is
executed. A repository can declare a program; only whoever starts the server agrees to run it on
that machine.

```yaml
surfaces:
  panels:
    - name: progress
      title: Acceptance
      run: hooks/progress.sh
  pages:
    - name: acceptance
      title: Acceptance across the board
      run: hooks/across.sh
```

A **panel** is drawn on a task page and receives that task as JSON on standard input. A **page**
is a whole page in the wiki and receives nothing. Both print Markdown to standard output.

Here is a real one — the panel that counts the acceptance list on a task:

```sh
#!/bin/sh
# What is ticked on this one task. Silent when there is no list.
set -eu
docket="${DOCKET_BIN:-docket}"
key=$(cat | sed -n 's/.*"key":"\([^"]*\)".*/\1/p')
[ -n "$key" ] || exit 0

"$docket" export --format csv --fields key,checked,boxes "$DOCKET_ROOT" |
  awk -F',' -v key="$key" 'NR > 1 && $1 == key {
    if ($3 == 0) exit
    printf "**%d of %d** ticked.\n\n", $2, $3
    if ($2 < $3) printf "%d left before this can be called done.\n", $3 - $2
    else printf "Everything on the list is ticked.\n"
  }'
```

Nineteen lines, and it is a paid add-on elsewhere.

Note what it does **not** do: it does not parse the vault. It asks `docket export` for the data
and formats the answer. That is the rule for every hook — the awkward reading stays in one place
that is tested, and your script stays a formatter.

## The environment a program gets

| Variable | What |
|---|---|
| `DOCKET_ROOT` | The vault on disk |
| `DOCKET_BIN` | The binary to call back into — always use it, never a bare `docket` |
| `DOCKET_EVENT` | What happened, for a reaction |
| `DOCKET_PREFIX` | Where the vault sits in the server's URL space, so a link you write resolves |

Print Markdown, exit zero. Exit non-zero and the surface says the program failed, which is what
you want — a panel that silently prints nothing is a panel nobody notices is broken.

Being silent when there is nothing to say is different, and correct: the panel above exits zero
without output when the task has no list.

## Rules worth knowing before you publish

- **Version your app.** `version` is a string, and bumping it is how a vault knows a re-install
  is an upgrade.
- **Declare an inverse for every relation.** A relation with only one side is half a graph.
- **Never write a page whose purpose is to list other pages.** A list connects everything on it
  and lands in the middle of the graph; `docket graph` will show you what that costs.
- **Bring templates for your types.** A type with no template is a type people fill in wrongly.
- **Do not execute anything at install time.** You cannot: installation copies files. This is
  the property that makes installing an app from a stranger safe to look at first.

## Next

- [`docket-apps`](https://github.com/vadymdidenkolab/docket-apps) — twelve of them to read, and `WRITING-AN-APP.md`
- [Tests, and results from CI](tests-and-ci.md) — the largest app, and what it takes to feed one
- [What the board cannot see](what-the-board-cannot-see.md) — what `export` will give your hook
