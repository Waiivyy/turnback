# turnback

Track, inspect, and selectively undo the changes AI coding agents make to your
code, one turn at a time.

`turnback` records every agent turn (the batch of file edits an agent makes
between two points in time) as its own unit on top of git. You can list turns,
read their diffs, and undo a single turn, or a single file from a turn, while
keeping everything that happened after it, including your own edits.

> **Status:** early development. Recording turns works today. Inspecting turns
> (`log`, `show`) and undoing them (`undo`) are being built next. See
> [CHANGELOG.md](CHANGELOG.md).

## Install

You need git 2.30 or newer.

```bash
go install github.com/Waiivyy/turnback@latest
```

## Record a turn

Run `turnback start` before you hand a task to your agent, and `turnback end`
when it is done:

```console
$ turnback start -m "Add retry logic to the HTTP client"
Initialized turnback in .turnback/ (git ignores this folder).
Recording a new turn: Add retry logic to the HTTP client (snapshot of 3 files).
Run 'turnback end' when the agent is done.

$ turnback end
Recorded turn 1: Add retry logic to the HTTP client
  M  src/http/client.go  +5 -1
  A  src/http/retry.go   +15
2 files changed, +20 -1
```

Edits you make yourself before `start` or after `end` are never part of the
turn. `turnback status` shows what the turn in progress has changed so far,
and `turnback end --discard` stops recording without saving anything.

## How it stays out of your way

Everything turnback stores lives in `.turnback/` at the root of your
repository. The folder ignores itself, so it never shows up in `git status`,
and turnback never touches your commits, branches, index or stash. Files your
`.gitignore` excludes are never read or written. See
[docs/DESIGN.md](docs/DESIGN.md) for how snapshots and undo work.

## License

[MIT](LICENSE)
