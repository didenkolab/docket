**What this changes, and why**

Say what was wrong and what it cost, not only what the patch does. If there was a fork with
alternatives somebody would have argued for, that is a decision page in docket-board rather than
a paragraph here — link it.

**How it was checked**

```
go build ./...
go test ./...
docket check .
```

If it touches a vault or the board, say which vaults you ran `docket check` against.

**Anything knowingly left undone**

A task on the board, or a sentence here. Better said than found later.
