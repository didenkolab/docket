# Who may do what

docket keeps no users of its own. There is no user table, no invite, no seat and no admin screen
for permissions — because the repository already has all of that, and a second copy of it would
only be the one that is wrong.

```bash
docket serve --auth git
```

People sign in with a token for the git host that holds the repository — GitHub, GitLab or
Bitbucket, hosted or your own. What they may do is what that host says:

| On the host | Here |
|---|---|
| Read | **viewer** — can read, cannot write |
| Write | **member** — can create, move, comment |
| Administer | **admin** — can also change the vault's vocabulary |

Commits are authored by the person who made them, so `git log` says who moved what. Nobody is
"the docket user".

## Why it is not a feature to add

Anyone who can clone the repository has everything in it, including its whole history. A button
in this interface offering to hide a project from somebody would be offering something it cannot
deliver — they can clone it. So access is granted where it is enforced, which is the host, and
the repository boundary is the access boundary.

That is also the answer to "can I have a private project inside a shared vault". No. Make it a
separate repository and assemble the two with a workspace.

## Signing in without pasting anything

```bash
docket serve                      # --auth auto: works it out
```

Two conveniences, both about not making people find a token:

**The device flow.** The server shows a code, you approve it on the host, and that is the login.
Configured with a public OAuth client id in `docket.yaml` — public, not a secret, and overridable
per server with `--device-client-id`.

**The credentials already on the machine.** A board somebody runs over their own clone is run by
somebody whose git already talks to that host. `--auth auto` notices and uses it.

For a single-person board, skip all of it:

```bash
docket serve --auth none --author "Your Name <you@example.com>"
```

Every write is attributed to that author. Do not put this on a network.

## A token belongs to a host; a role belongs to a repository

In a workspace spanning two hosts, a permission is per repository, resolved against the host
that repository is on. This is worth stating because the obvious shortcut — one authority, one
host, permissions by URL path — let somebody write to a repository they had no access to at all.
That was a real defect here, fixed, and the reasoning is in ADR-0004.

## Running it where other people can reach it

```bash
docket serve --auth git --behind-proxy --addr 0.0.0.0:8080
```

`--behind-proxy` makes rate limits follow the client the proxy names rather than the proxy
itself. Terminate TLS at the proxy.

What the server does on its own account: refuses a change that did not come from a page it drew
(every form carries a token in a cookie the page cannot read, and a request admitting another
origin is refused before anything else is looked at); meters requests per client, much tighter
for sign-in, because every sign-in is a call to the git host and a loop against it burns that
host's rate limit for everyone; serves pages under a content security policy with no inline
anything, and attachments sandboxed, so an uploaded SVG is embeddable as an image and cannot run
as a page.

None of that replaces the git host. It keeps a browser from being used against its owner.

## Programs are a separate decision

```bash
docket serve --programs
```

Without `--programs`, nothing the vault declared is executed. A repository can declare a program
and have that declaration reviewed like any other diff; agreeing to run it on **this machine**
is a decision made by whoever starts the server. Push access to a repository is not permission
to run code on somebody's laptop.

## Next

- [Many projects, one vault](many-projects-one-vault.md) — when the boundary means separate repositories
- [An agent runs the board](an-agent-runs-the-board.md) — what to let an agent decide
- [Write your own app](write-an-app.md) — what a program can do once you have consented
