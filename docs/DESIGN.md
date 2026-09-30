# Design

This document explains how turnback records and undoes agent turns, and why
it is safe to run on a working tree you care about.

## Goals

- Treat each agent turn as its own addressable, inspectable, revertible unit.
- When undoing a turn, keep everything that happened after it, including your
  own edits.
- Never lose data and never silently corrupt a file.
- Stay out of git's way: no commits, branches, refs, stash entries or index
  changes in your repository.

## Non-goals

- Replacing git history. turnback records working tree states; committing is
  still up to you.
- Understanding code. Conflict detection is textual (see
  [Limitations](#limitations)).

## Concepts

- **Snapshot:** the exact content, at one moment, of every file git would
  consider: tracked files plus untracked files that are not ignored.
- **Turn:** a pair of snapshots (before and after) plus metadata. Its diff is
  what changed during the turn.
- **Session:** a turn in progress. `turnback start` took the "before"
  snapshot and `turnback end` has not run yet.
- **Undo turn:** an undo is recorded as a turn of its own, so it can be undone
  like any other turn.

## Storage layout

Everything lives in `.turnback/` at the root of the working tree:

```
.turnback/
  .gitignore        contains "*", so git ignores the whole folder
  state.json        storage format version and the next turn id
  session.json      the session in progress, if any
  turns/0001.json   turn metadata: times, description, files, snapshot ids
  turns/0001.patch  the turn's diff as a standard patch
  git/              private git repository that holds the snapshots
  lock              locked by the command that is changing state, if any
```

Deleting the folder removes every trace of turnback from the repository.

Commands that change state take an operating system lock on `lock` (`flock`
on Unix, `LockFileEx` on Windows), so it is released the moment its holder
exits, even if it crashes. The file stays in place and holds the pid of the
last holder, which a waiting command names.

The folder is only ever created by turnback, so turnback refuses anything
there it did not make. If git tracks a path inside `.turnback/`, in any
case, the files came with the repository from whoever can push to it, and
every command stops with an explanation. If the folder, `git/`, `turns/` or
`lock` is a symbolic link or not the kind of entry turnback creates, commands
stop too, and the lock is opened without following links. Otherwise a link
planted by a repository or an archive could aim turnback's writes at any file
the user owns.

## Commit turns

With `turnback hook install`, a post-commit hook records a turn at every
commit, so nobody has to remember `start` and `end`.

- A checkpoint, `refs/turnback/checkpoint` in the private repository, marks
  where the next commit turn starts. At each commit the hook snapshots the
  working tree, records everything that changed since the checkpoint as a
  turn named after the commit's subject line, and moves the checkpoint.
  Every other recorded turn, from `end` or `undo`, moves it too, so nothing
  is recorded twice.
- While `start` and `end` are recording a turn, commits do not close turns;
  the explicit turn wins. During a rebase nothing is recorded, because the
  working tree jumps between commits.
- turnback never replaces a hook it did not write, and leaves hooks alone
  that a tool manages through `core.hooksPath`. The hook it writes never
  makes a commit fail and does nothing when it is already running inside
  itself.
- The checkpoint only exists while the hook is installed.

## Snapshots

`.turnback/git` is a private git repository whose work tree is your working
tree. It has its own index, object database and refs. Taking a snapshot means
bringing that private index in line with your files and running
`git write-tree`.

- Your staging area is never touched, because the private repository has its
  own index.
- Nothing shows up in `git log --all`, and `git gc` in your repository cannot
  delete snapshot data, because the private repository has its own refs and
  objects.
- Which files a snapshot holds is decided by your repository, not by the
  private one: exactly the files it tracks plus the untracked files it does
  not ignore, as `git ls-files` lists them with your own ignore rules. A file
  that becomes ignored leaves the next snapshot and is never read again; a
  tracked file that matches an ignore pattern, such as a force-added lock
  file, stays in. Nested repositories are recorded as a pointer to their
  current commit when they have one.
- Snapshots are exact bytes. The private repository disables line-ending
  conversion, clean and smudge filters (including Git LFS), keyword expansion
  and re-encoding, so restoring a snapshot reproduces files byte for byte.
- Hooks are disabled for the private repository.
- Each turn is stored as two commits in the private repository, before and
  after (with before as its parent), under `refs/turns/<id>`. That keeps the
  snapshot data alive and lets you inspect a turn with plain git:
  `git --git-dir=.turnback/git show refs/turns/3`.

## Which files turnback may touch

turnback works on exactly the files your repository tracks, plus untracked
files it does not ignore. Ignored files such as `node_modules/`, `.env` or
build output are never read into a snapshot and never written, and nothing
outside the repository root is touched.

An undo writes only paths that the undone turn changed:

- A path git ignores by now is left alone and listed as skipped, even if the
  turn changed it. The rest of the undo still applies.
- An undo never overwrites or writes through something git does not track.
  If an untracked file, a folder of build output or a symlinked folder stands
  where a file must come back, that file is a conflict and nothing is
  written.

## Undo

For each file the turn changed, turnback has three versions: before the turn
(**B**), after the turn (**A**) and now (**C**, from a fresh snapshot). The
goal is **C** with the turn's changes taken out.

| Situation | Result |
|---|---|
| C equals A: nothing changed the file since | restore B (delete the file if the turn created it) |
| C equals B: already reverted | nothing to do |
| Text file, changed since, present in all three versions | three-way merge of the change from A to B into C |
| Created by the turn, edited since | conflict |
| Modified by the turn, deleted since | conflict |
| Deleted by the turn, re-created since with different content | conflict |
| Binary file or symlink, changed since | conflict |
| Something git does not track where the file must come back | conflict |
| Path ignored by git now | skipped and reported |
| Submodule | skipped and reported |

The three-way merge is the same operation `git revert` performs, done with
`git merge-file`. Edits that overlap or touch the undone lines are conflicts,
exactly as in git.

An undo is planned before anything is written. turnback builds the complete
result as a tree in the private repository and shows the diff from the
current state to that result. If any file conflicts, nothing is written; the
report shows the conflicting lines and which later turns touched each file.

Applying the plan happens in two phases, both in the private repository:

1. Files that exist now are changed or deleted with `git read-tree -m -u`.
   It writes only those paths, and refuses before writing anything if one
   of them changed on disk after the plan was made.
2. Files the undo brings back are created with `git checkout-index`, which
   only creates a file where nothing exists yet, inside real folders.
   `read-tree -u` is not used for this step because it silently replaces an
   ignored file standing in the way, such as a `.env`.

git's exit status alone is not trusted: `read-tree` can stop halfway
(a folder that is not writable) or skip a deletion with only a warning. After
each phase, every written path is checked on disk against the plan, by
content, type and executable bit. On any error or mismatch, turnback puts
every path back and checks that too. Then either nothing changed, and it
says so, or, if even putting things back failed, whatever did change is
recorded as a partial undo turn that can itself be undone. No change is ever
left unrecorded. While files are being written, Ctrl-C is ignored and git
runs outside the terminal's process group.

The state just before the undo and the result are recorded as a new turn.

## Safety rules

1. **Dry run by default.** Nothing is written without confirmation at the
   prompt or `--yes`.
2. **All or nothing.** If any file conflicts, no file is written.
3. **Unsaved work is protected.** A file is *dirty* when its current content
   is neither committed (matches `HEAD`) nor recorded (matches the latest
   snapshot). Both are compared by content, the way git compares it, so a
   file marked skip-worktree cannot hide local edits. Undo refuses to write a
   dirty file unless `--force` is given.
   Uncommitted agent changes that turnback has recorded are not dirty,
   because turnback holds a copy of them.
4. **Every undo is undoable.** An undo is recorded as a turn.
5. **Fresh check before writing.** turnback takes a new snapshot right before
   it writes. If a file the undo would write changed after the dry run,
   turnback stops without writing anything.
6. **Verified writes.** Every written file is checked on disk. If anything
   did not land, the undo is rolled back, or recorded as a partial undo when
   a rollback is impossible.
7. **Only its own state.** turnback refuses to run when git tracks files in
   `.turnback/`, and never follows a link there.

## The web page

`turnback ui` serves one page, embedded in the binary, and a small JSON API
that the page reads. The page shows the user's code, so the server trusts no
request until it has checked it:

- It listens on `127.0.0.1` only, on a free port unless `--port` names one.
- Every request must carry a token of 24 random bytes, either in the `token`
  query parameter of the printed address or in the `X-Turnback-Token` header
  the page sends. The comparison takes constant time, and the token is new
  each time the server starts.
- A request whose `Host` header is not `127.0.0.1`, `localhost` or `[::1]`
  with the server's port is refused. A site that rebinds its own domain to
  the loopback address still sends its own name, so DNS rebinding gets
  nothing.
- Only `GET` and `HEAD` are served. The server takes no lock and changes
  nothing in the repository or in its turns, so it can run next to any other
  command.
- The page may run only its own inline script and style, allowed by a nonce
  that is new for every response, under a policy that blocks everything else.
  Responses are marked `no-store`, `nosniff` and `no-referrer`.
- The page inserts every value as text, never as markup, so a description or
  a file that contains HTML is shown as it is.

While it is visible, the page checks `/api/state` every few seconds. The state
includes the number of turns and the latest turn id, read from the directory
listing alone, and the page loads the turns again only when those change. A
turn's diff is cut at a line end beyond 16 MB and marked as truncated, and the
page draws long diffs a part at a time, so a huge turn cannot freeze it.

## Limitations

- Conflict detection is textual. Undoing a turn that added a function which a
  later turn calls from another file merges cleanly and breaks the build.
  turnback warns when later turns touched the same files; run your tests
  after an undo.
- A rename is undone as a delete plus an add. If the renamed file was edited
  afterwards, that is a conflict; the edits are not moved back to the old
  name.
- Submodules are recorded as commit pointers only and are never undone.
- The first snapshot copies the content of every file into `.turnback/git`,
  so it costs disk space in proportion to the working tree. Later snapshots
  store only files that changed.
