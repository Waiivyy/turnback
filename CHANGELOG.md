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
  unsaved changes are only rewritten with `--force`, ignored files are never
  overwritten, and every undo is recorded as a turn so it can be undone.
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
  repository's history, index, refs or stash, honors your ignore rules and
  stores exact file bytes.
- README with a demo, an example session, a command reference, a safety
  section and a FAQ.
- CI on Linux and macOS with the oldest supported and the latest Go.
- Project scaffold: Go module, MIT license, README, changelog and design notes.
- Command-line skeleton with `help` and `version`.
