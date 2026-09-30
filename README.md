# turnback

Track, inspect, and selectively undo the changes AI coding agents make to your
code, one turn at a time.

`turnback` records every agent turn (the batch of file edits an agent makes
between two points in time) as its own unit on top of git. You can list turns,
read their diffs, and undo a single turn, or a single file from a turn, while
keeping everything that happened after it, including your own edits.

> **Status:** early development. The first release is being built in the open.
> See [CHANGELOG.md](CHANGELOG.md) for progress.

## License

[MIT](LICENSE)
