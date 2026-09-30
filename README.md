<p align="center">
  <img src="docs/assets/icon.svg" width="96" height="96" alt="">
</p>

<h1 align="center">turnback</h1>

<p align="center">
  <strong>Per-turn history and selective undo for AI coding agents, built on git.</strong>
</p>

<p align="center">
  <a href="https://github.com/Waiivyy/turnback/actions/workflows/ci.yml"><img alt="CI status" src="https://github.com/Waiivyy/turnback/actions/workflows/ci.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/Waiivyy/turnback?logo=go&amp;logoColor=white"></a>
  <img alt="No dependencies" src="https://img.shields.io/badge/dependencies-none-brightgreen">
  <a href="LICENSE"><img alt="MIT license" src="https://img.shields.io/github/license/Waiivyy/turnback"></a>
</p>

<p align="center">
  <a href="#install">Install</a> &nbsp;&middot;&nbsp;
  <a href="#quickstart">Quickstart</a> &nbsp;&middot;&nbsp;
  <a href="#commands">Commands</a> &nbsp;&middot;&nbsp;
  <a href="#how-it-works">How it works</a> &nbsp;&middot;&nbsp;
  <a href="#faq">FAQ</a>
</p>

<p align="center">
  <img src="docs/assets/demo.svg" width="760" alt="A terminal running turnback log, which lists three agent turns, and turnback show 2, which prints the files and diff of the second turn">
</p>

AI coding agents change many files in a single turn. When one of those turns
goes wrong, your options are to pick through the diff by hand, or to reset to
an older commit and lose the good turns that came after it, along with the
edits you made yourself in between.

**turnback** records every agent turn as its own unit, right on top of git.
You can list your turns, see exactly what each one changed, and undo only the
one you don't want while everything else stays put.

- **Per-turn history.** Every turn gets an id, a time, a description, the files
  it touched and its diff.
- **Your edits stay yours.** Changes you make between agent turns are never
  counted as part of a turn.
- **Out of git's way.** No commits, branches, stash entries or index changes in
  your repository. Files your `.gitignore` excludes are never read or written.
- **One small binary.** Written in Go, no dependencies beyond git, nothing to
  configure.
- **Works with any agent.** Cursor, Copilot, Aider, Codex, or anything else that
  edits files in your working tree.

> [!NOTE]
> turnback is in early development. Recording and inspecting turns work
> today. Selective undo is being built next; see the [roadmap](#roadmap).

## Install

turnback needs **git 2.30 or newer**.

**With Go** (1.22 or newer):

```bash
go install github.com/Waiivyy/turnback@latest
```

**From source:**

```bash
git clone https://github.com/Waiivyy/turnback.git
cd turnback
go build -o turnback .
```

Prebuilt binaries for macOS, Linux and Windows will come with the first
release.

## Quickstart

Inside any git repository:

```bash
turnback start -m "Add rate limiting"   # right before you prompt the agent
# ... the agent edits files ...
turnback end                            # when it is done
turnback log                            # every turn, newest first
turnback show 1                         # what turn 1 changed
```

That is the whole workflow. `turnback status` shows what the turn in progress
has changed so far, and `turnback end --discard` drops a turn you don't want to
keep.

## Example session

A small TypeScript API, three agent turns, and one manual README edit made
between the first two turns.

Recording a turn:

```console
$ turnback start -m "Add rate limiting to the API" --agent cursor
Initialized turnback in .turnback/ (git ignores this folder).
Recording a new turn: Add rate limiting to the API (snapshot of 3 files).
Run 'turnback end' when the agent is done.

$ turnback end
Recorded turn 1: Add rate limiting to the API
  A  src/rateLimit.ts  +23
  M  src/server.ts     +2 -1
2 files changed, +25 -1
```

Listing turns, all of them or only those that touched a file:

```console
$ turnback log
ID  WHEN         FILES  CHANGES  AGENT   DESCRIPTION
 3  today 15:42      1  +21 -0   aider   Add tests for the rate limiter
 2  today 15:41      1  +5 -9    cursor  Switch the limiter to a sliding window
 1  today 15:39      2  +25 -1   cursor  Add rate limiting to the API

$ turnback log --file src/rateLimit.ts
ID  WHEN         FILES  CHANGES  AGENT   DESCRIPTION
 2  today 15:41      1  +5 -9    cursor  Switch the limiter to a sliding window
 1  today 15:39      2  +25 -1   cursor  Add rate limiting to the API
```

Inspecting a turn:

```console
$ turnback show 2
turn 2  Switch the limiter to a sliding window
Recorded  2026-09-30 15:41 (1 minute ago), took 1 minute
Agent     cursor

  M  src/rateLimit.ts  +5 -9
1 file changed, +5 -9

diff --git a/src/rateLimit.ts b/src/rateLimit.ts
index e16ca76..84a05cf 100644
--- a/src/rateLimit.ts
+++ b/src/rateLimit.ts
@@ -2,22 +2,18 @@ import type { Request, Response, NextFunction } from "express";
 
 const WINDOW_MS = 60_000;
 const LIMIT = 100;
-const hits = new Map<string, { count: number; windowStart: number }>();
+const hits = new Map<string, number[]>();
 
 export function rateLimit(req: Request, res: Response, next: NextFunction) {
   const now = Date.now();
   const key = req.ip ?? "unknown";
-  const windowStart = Math.floor(now / WINDOW_MS) * WINDOW_MS;
-  const entry = hits.get(key);
+  const recent = (hits.get(key) ?? []).filter((t) => now - t < WINDOW_MS);
 
-  if (!entry || entry.windowStart !== windowStart) {
-    hits.set(key, { count: 1, windowStart });
-    return next();
-  }
-  if (entry.count >= LIMIT) {
+  if (recent.length >= LIMIT) {
     res.status(429).json({ error: "Too many requests" });
     return;
   }
-  entry.count++;
+  recent.push(now);
+  hits.set(key, recent);
   next();
 }
```

The README edit made between turns 1 and 2 appears in neither turn.

## Commands

| Command | What it does |
|---|---|
| `turnback start` | Snapshot the working tree and start recording a turn |
| `turnback end` | Record everything that changed since `start` as a new turn |
| `turnback status` | Show whether a turn is being recorded and what it has changed so far |
| `turnback log` | List recorded turns, newest first |
| `turnback show [<turn>]` | Show a turn's details, files and diff; the latest turn by default |
| `turnback undo <turn>` | Revert one turn, or one file from it *(coming next)* |

`turnback help <command>` lists every option. The ones you will use most:

<details>
<summary><strong><code>turnback start</code> and <code>turnback end</code></strong></summary>

| Option | Description |
|---|---|
| `-m, --message <text>` | Describe the turn. Given to `end`, it replaces the one given to `start`. Without a description, turnback writes a short summary of the changed files. |
| `--agent <name>` | `start` only. Record which agent made the changes, for example `cursor`. |
| `--discard` | `end` only. Stop recording without saving a turn. |

</details>

<details>
<summary><strong><code>turnback log</code></strong></summary>

| Option | Description |
|---|---|
| `--since <when>` | Only turns recorded since then: `2026-09-30`, `"2026-09-30 14:00"`, `30m`, `2h`, `3d`, `1w`, `today` or `yesterday`. |
| `--file <path>` | Only turns that changed this file, or anything inside this folder. Repeat it to match several paths. Paths are relative to the current directory, and a renamed file matches by its old and its new name. |
| `-n, --limit <count>` | Show at most this many turns. |
| `--json` | Print the turns as JSON, for scripts. |

</details>

<details>
<summary><strong><code>turnback show</code></strong></summary>

| Option | Description |
|---|---|
| `<turn>` | A turn id from `turnback log`, or `last`. Defaults to `last`. |
| `--file <path>` | Only show this file, or the files inside this folder. Repeat it to show several paths. |
| `--stat` | Show the list of files without the diff. |
| `--json` | Print the turn and its diff as JSON. |

</details>

In a terminal, long output is paged through `less` and diffs are colored. Use
`--no-pager` or `PAGER=cat` to turn paging off, and `NO_COLOR=1` to turn colors
off.

## Using it with your agent

turnback does not care which agent edits your files: run `turnback start`
before you send a prompt and `turnback end` when the agent is done. Tag turns
with `--agent` if you switch between agents.

If your agent or editor can run a shell command when a turn begins and when it
ends, point those hooks at `turnback start` and `turnback end` and every turn
is recorded without you thinking about it. A git-hook mode for tools without
hooks is on the [roadmap](#roadmap).

## How it works

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/assets/recording-dark.svg">
  <img src="docs/assets/recording-light.svg" width="880" alt="A timeline: your edits, turn 1, your edits, turn 2, turn 3. Each turn spans from start to end. Your edits in between are not part of any turn.">
</picture>

- `turnback start` takes a snapshot of every file git would track: tracked
  files plus new files that are not ignored. `turnback end` takes another, and
  the difference between the two is the turn.
- Snapshots live in a private git repository inside `.turnback/`, with its own
  index, objects and refs. Your history, staging area, branches and stash are
  never touched, and the folder ignores itself, so it never shows up in
  `git status`.
- Snapshots store exact bytes, whatever your line-ending settings or filters
  such as Git LFS would do, so a restored file matches the original byte for
  byte.
- Each turn is also saved as readable JSON plus a standard `.patch` file in
  `.turnback/turns/`, and as commits you can inspect with plain git:

  ```bash
  git --git-dir=.turnback/git log --oneline refs/turns/2
  ```

The full design, including how undo merges around later changes, is in
[docs/DESIGN.md](docs/DESIGN.md).

## FAQ

<details>
<summary><strong>Does turnback replace git?</strong></summary>

No. turnback records working tree states while you work; committing, branching
and pushing are still yours to do with git. turnback never creates commits in
your repository.

</details>

<details>
<summary><strong>Does it send my code anywhere?</strong></summary>

No. turnback never opens a network connection. Everything it records stays in
`.turnback/` inside your repository.

</details>

<details>
<summary><strong>Should I commit the <code>.turnback/</code> folder?</strong></summary>

No. It is a local, personal history, and it tells git to ignore it on its own,
so you don't need to edit your `.gitignore`.

</details>

<details>
<summary><strong>How do I remove it?</strong></summary>

Delete the `.turnback/` folder. turnback changes nothing else in your
repository.

</details>

<details>
<summary><strong>How much disk space does it use?</strong></summary>

The first snapshot stores a compressed copy of your working tree in
`.turnback/git`. Later snapshots only store the files that changed, and git
packs them over time.

</details>

<details>
<summary><strong>Can I run it from a subdirectory?</strong></summary>

Yes. turnback always works on the whole repository, and paths you pass to
`--file` are relative to the directory you run it in.

</details>

<details>
<summary><strong>Does it work on Windows?</strong></summary>

turnback builds for Windows, but so far it is only tested on macOS and Linux.

</details>

## Roadmap

- [x] Record agent turns with `start` and `end`
- [x] Inspect turns with `log` and `show`
- [ ] Selective undo of a turn or a single file, with a dry run, conflict
      detection and undo of an undo
- [ ] Automatic recording through a git hook
- [ ] Local web UI for browsing turns
- [ ] Smarter automatic descriptions
- [ ] Prebuilt binaries and a Homebrew formula

## Contributing

Bug reports and pull requests are welcome. The test suite runs against real git
repositories in temporary directories, so Go and git are all you need:

```bash
go test ./...
```

Please run `gofmt` and keep new behavior covered by tests. The changelog lives
in [CHANGELOG.md](CHANGELOG.md).

## License

turnback is released under the [MIT License](LICENSE).
