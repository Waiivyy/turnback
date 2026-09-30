# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `turnback undo <turn>` takes one turn back out of the working tree while
  keeping everything that happened after it, using a per-file three-way merge.
  It is a dry run by default, asks for confirmation in a terminal, and supports
  `--yes`, `--dry-run`, `--file` and `--force`. If any file conflicts, nothing
  is written and the overlapping lines and later turns are shown. Files with
  unsaved changes, compared by content, are only rewritten with `--force`,
  ignored files are never read or overwritten, and every undo is recorded as
  a turn so it can be undone. Every write is verified on disk; an undo that
  cannot finish is rolled back, or recorded as a partial undo turn if even
  that fails. Large turns work too: no file list is passed on a command line.
  Case-only renames undo correctly on case-insensitive disks.
- A file that git starts ignoring during a turn is shown as "now ignored by
  git, still on disk" rather than as deleted.
- `turnback end` points to `show` and `undo` for the turn it just recorded.
- `turnback log` lists turns newest first, filtered by `--since` (dates,
  times, durations, `today`, `yesterday`), `--file` (repeatable, relative to
  the current directory, matching renamed files by either name) and `-n`.
- `turnback show` prints a turn's details, files and diff, the latest turn by
  default, with `--file` and `--stat`.
- `--json` output for `log` and `show`, colored diffs and paging through
  `less` in a terminal.
- `turnback start`, `turnback end` and `turnback status` to record an agent
  turn as the difference between two snapshots of the working tree.
  `end --discard` stops recording without saving a turn.
- Turns without a description get a short summary of the files they changed.
- Private snapshot store in `.turnback/git`. It never writes to your
  repository's history, index, refs or stash, and stores exact file bytes.
  Snapshots hold exactly what your repository tracks plus the untracked files
  it does not ignore, so a file that becomes ignored is never read again and
  force-added files that match an ignore pattern are recorded.
- README with a demo, an example session, a command reference, a safety
  section and a FAQ.
- CI on Linux and macOS with the oldest supported and the latest Go.
- Project scaffold: Go module, MIT license, README, changelog and design notes.
- Command-line skeleton with `help` and `version`.
