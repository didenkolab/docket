# docket — the tool

This repository is the Go source of docket.

**Read [AGENTS.md](AGENTS.md) before changing anything in it.** That file is the whole of what
you need: what the tool is allowed to be, where the packages sit and which way they depend, how
a comment and a finding are written, why a test never reaches the network, and what a commit
here says.

It is one file rather than two because the rules are the same whichever agent is reading them,
and two copies of a rule is one copy that goes stale.
