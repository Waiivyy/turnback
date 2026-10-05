# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `turnback hook install` and `turnback hook uninstall`: a post-commit hook
  that records a turn at every commit, named after the commit, so nobody has
  to remember `start` and `end`. Explicit turns take precedence, rebases are
  skipped, and existing or tool-managed hooks are never replaced.
- `turnback show` names the commit that closed a commit turn, and
  `turnback status` says when commits record turns.
- `turnback hook start` and `turnback hook end`, for an agent's own prompt
  and stop hooks. They record turns like `start` and `end`, but always exit
  0, print nothing unless something goes wrong, find the project in the JSON
  the agent passes, and `hook start` first records a turn that was never
  ended, so a stopped agent neither blocks the next prompt nor merges it into
  the old turn.
- Hook settings for Cursor, GitHub Copilot (CLI and VS Code), Codex CLI and
  Gemini CLI in `examples/hooks`, with a guide per agent in
  `docs/integrations`, tested with the input each agent documents. Aider is
  covered by the git hook.
- Turns without a description are described from their diff, naming the
  functions and types they add, change or remove in Go, JavaScript and
  TypeScript, Python, Rust and Ruby, as in "Add withRetry; update Get".
- `turnback ui` opens a local, read-only web page for browsing turns: the
  list of turns next to each turn's details and its diff by file, with line
  numbers. It updates as turns are recorded, filters by text, file or agent,
  and works in light and dark mode and at phone width. It is served on
  127.0.0.1 only, behind a secret address, refuses requests that name another
  host, and runs only its own script.
- Release archives carry a signed build provenance attestation, which
  `gh attestation verify` checks.
- CI runs the tests on Windows too, and against git 2.30.0, the oldest
  version turnback supports.
- `docs/FAQ.md` compares turnback with the undo features of Cursor, GitHub
  Copilot, Aider and git, and says when each of them is enough.

### Changed

- The first snapshot in a repository copies the committed files from git's
  own store as one pack instead of writing them one by one. In a repository
  of 30,000 files, `turnback start` went from 24 seconds to 4, and the
  `turnback end` after it from nearly 3 minutes, spent packing those files,
  to a third of a second.
- Building from source needs Go 1.24 or newer. Older Go versions leave out a
  load command that current macOS requires of programs using the network
  package, which `turnback ui` needs, so their builds would not start. With
  Go's default settings, `go install` fetches a new enough Go by itself. The
  prebuilt binaries are not affected.

### Fixed

- Undoing a turn that renamed a file whose name git ignores, such as a
  force-added `.env.example` moved with `git mv`, deleted the new name while
  leaving the old one missing, so the file was lost. A rename is now undone
  whole or not at all, and a file git ignores by name is left as it is.
- `turnback end` and `turnback undo` failed with a git error when a turn had
  replaced a folder with a symbolic link or a submodule, or when a file name
  started with `:`. Such paths are now handled like any other.
- An undo that failed partway, for example on a full disk, could leave a
  partly written file behind while saying nothing was changed, and a git
  process killed midway left a lock file that made every later command fail.
  Partly written files are now removed, and turnback clears the locks its
  own git commands leave behind.
- Finding the files git ignores no longer searches the whole index once per
  file. A dry run for a turn that deleted 20,000 of 30,000 files went from
  about 4 seconds to under half a second.
- `turnback status` shows a file that git began ignoring during the turn the
  way `turnback end` records it, instead of as deleted.
- Error messages keep their line breaks, so git's errors that span several
  lines stay readable. Other control characters are still escaped.
- Ctrl-C stops the housekeeping at the end of an undo, as intended, and
  turnback still reports the undo.
- On file systems that cannot lock files, such as NFS without a lock
  service, every command failed. turnback now runs without the lock there.
- git's pathspec environment variables, such as `GIT_LITERAL_PATHSPECS`, no
  longer change how turnback's own git commands read paths.
- The install script resolves the latest release once, so the archive and
  its checksum always come from the same release; stops on Ctrl-C instead of
  carrying on; says so when no SHA-256 tool is installed rather than
  reporting a checksum mismatch; replaces the binary in one step even when
  the temporary folder is on another file system; points Git Bash users to
  the Windows download; and cannot run half of itself if its own download is
  cut short.

### Security

- A repository could ship a symbolic link at `.turnback/lock` and make the
  first `turnback start` overwrite the file it points to. The lock is now
  opened without following links, links anywhere in turnback's folder are
  refused, and turnback does not run in a repository that tracks files in
  `.turnback/`, since those can come from anyone who can push to it.
- The install script and the one-liner in the README download from GitHub
  over HTTPS only.

## [0.1.0] - 2026-09-30

First release.

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
- Prebuilt binaries for macOS, Linux, Windows and FreeBSD on every release,
  with checksums, and a one-line install script.
- Commands in the same repository are serialized with an operating system
  file lock, which is released even if turnback crashes.
- Project scaffold: Go module, MIT license, README, changelog and design notes.
- Command-line skeleton with `help` and `version`.

[Unreleased]: https://github.com/Waiivyy/turnback/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Waiivyy/turnback/releases/tag/v0.1.0
