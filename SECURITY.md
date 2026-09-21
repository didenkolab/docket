# Security

## Reporting something

Use GitHub's [private vulnerability reporting](https://github.com/didenkolab/docket/security/advisories/new)
on this repository, or write to **vadym@didenkolab.com**. Please do not open a public issue for
anything that lets somebody read or change what they should not.

Say what you did, what happened, and what you expected. A proof of concept helps and is not
required. You will get an answer within a few days, and credit in the release notes unless you
would rather not.

## What is in scope

The binary and the server in this repository, and the hooks the apps in
[docket-apps](https://github.com/didenkolab/docket-apps) bring.

Out of scope: the git host's own access control — docket keeps no users and decides nothing about
who may read a repository, which is the whole of
[ADR-0004](https://github.com/didenkolab/docket-board/blob/main/docs/decisions/0004-access-comes-from-git.md).
If somebody can clone the repository they have everything in it, including its history; that is
the design, not a finding.

## What the design already assumes

Three things are deliberate, and are not vulnerabilities on their own. Say so if you think one of
them is wrong — that is a design argument worth having.

**A vault can declare programs, and the server will not run them unless it is started with
`--programs`.** A repository declaring a program is reviewed like any other diff; agreeing to
execute it on a particular machine is a separate decision, made by whoever starts the server
there. Push access to a repository is not permission to run code on somebody's laptop.

**A program is a path inside the repository and an executable file, with no shell.** An event
reaches it as JSON on stdin, so nothing written in a task's title can become part of a command.
There is a test for that with the title `"; touch /tmp/docket-pwned; echo "`.

**The inbox secret lives in the server's environment, never in the vault.** A secret in a file
everybody clones is not a secret. An inbox with no secret named is refused on any server that has
sign-in.

## What the server does on its own account

A write must come from a page this server drew: every form carries a token in a cookie the page
cannot read, and a request admitting another origin — by `Sec-Fetch-Site` or by `Origin` — is
refused before anything else is looked at. A client with no cookies is not asked for a token: an
agent or a `curl` has no ambient session to hijack.

Requests are metered per client, much tighter for signing in, because every sign-in is a call to
the git host and a loop against it burns that host's rate limit for everybody. Behind a reverse
proxy, pass `--behind-proxy` so the limit follows the client the proxy names.

Pages are served under a content security policy with no inline anything, and attachments are
served sandboxed — an uploaded SVG is embeddable as an image and cannot run as a page.

## Versions

The latest release is the only one that gets fixes. There is one line of development and no
back-porting.
