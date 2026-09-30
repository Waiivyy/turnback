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
  lock              exists while a turnback command is changing state
```

Deleting the folder removes every trace of turnback from the repository.

## Snapshots

`.turnback/git` is a private git repository whose work tree is your working
tree. It has its own index, object database and refs. Taking a snapshot means
running `git add --all` and `git write-tree` against that private repository.

- Your staging area is never touched, because the private repository has its
  own index.
- Nothing shows up in `git log --all`, and `git gc` in your repository cannot
  delete snapshot data, because the private repository has its own refs and
  objects.
- The same ignore rules apply as in your repository. The `.gitignore` files
  in the working tree apply automatically, and `.git/info/exclude` and
  `core.excludesFile` are copied into the private repository on every run.
- Snapshots are exact bytes. The private repository disables line-ending
  conversion, clean and smudge filters (including Git LFS), keyword expansion
  and re-encoding, so restoring a snapshot reproduces files byte for byte.
- Hooks are disabled for the private repository.
- Each turn is stored as two commits in the private repository, before and
  after (with before as its parent), under `refs/turns/<id>`. That keeps the
  snapshot data alive and lets you inspect a turn with plain git:
  `git --git-dir=.turnback/git show refs/turns/3`.

## Which files turnback may touch

turnback works on exactly the files `git add --all` would pick up: tracked
files and untracked files that are not ignored. Ignored files such as
`node_modules/`, `.env` or build output are never read into a snapshot and
never written. Nothing outside the repository root is touched. An undo writes
only paths that the undone turn changed.

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
| Submodule | skipped and reported |

The three-way merge is the same operation `git revert` performs, done with
`git merge-file`. Edits that overlap or touch the undone lines are conflicts,
exactly as in git.

An undo is planned before anything is written. turnback builds the complete
result as a tree in the private repository and shows the diff from the
current state to that result. If any file conflicts, nothing is written; the
report shows the conflicting lines and which later turns touched each file.

Applying the plan runs `git read-tree -m -u` in the private repository. It
writes only the changed paths and refuses if any of them changed on disk
after the plan was made. The state just before the undo and the result are
recorded as a new turn.

## Safety rules

1. **Dry run by default.** Nothing is written without confirmation at the
   prompt or `--yes`.
2. **All or nothing.** If any file conflicts, no file is written.
3. **Unsaved work is protected.** A file is *dirty* when its current content
   is neither committed (matches `HEAD`) nor recorded (matches the latest
   snapshot). Undo refuses to write a dirty file unless `--force` is given.
   Uncommitted agent changes that turnback has recorded are not dirty,
   because turnback holds a copy of them.
4. **Every undo is undoable.** An undo is recorded as a turn.
5. **Fresh check before writing.** turnback takes a new snapshot right before
   it writes. If a file the undo would write changed after the dry run,
   turnback stops without writing anything.

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
