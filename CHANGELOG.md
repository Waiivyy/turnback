# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- `turnback start`, `turnback end` and `turnback status` to record an agent
  turn as the difference between two snapshots of the working tree.
  `end --discard` stops recording without saving a turn.
- Turns without a description get a short summary of the files they changed.
- Private snapshot store in `.turnback/git`. It never writes to your
  repository's history, index, refs or stash, honors your ignore rules and
  stores exact file bytes.
- CI on Linux and macOS with the oldest supported and the latest Go.
- Project scaffold: Go module, MIT license, README, changelog and design notes.
- Command-line skeleton with `help` and `version`.
