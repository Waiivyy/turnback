# Cursor

Cursor's agent runs hooks configured in a `hooks.json` file, in the editor
and, according to its changelog, in the Cursor CLI too. These settings
record a turn for every prompt you send.

## Set it up

1. Check that your turnback knows `hook start`; see
   [Before you start](README.md#before-you-start).
2. Save the settings as `~/.cursor/hooks.json` to record turns in every
   project, or as `.cursor/hooks.json` in one project. If the file already
   exists, add the two entries to its `hooks` object instead.

```json
{
  "version": 1,
  "hooks": {
    "beforeSubmitPrompt": [
      {
        "command": "turnback hook start --agent cursor || exit 0",
        "timeout": 120
      }
    ],
    "stop": [
      {
        "command": "turnback hook end || exit 0",
        "timeout": 120
      }
    ]
  }
}
```

Cursor reloads `hooks.json` when you save it. If the hooks do not show up,
restart Cursor.

## Check that it works

1. Open a git repository in Cursor and ask the agent for a small change.
2. When it is done, run this in a terminal in that repository:

```bash
turnback log
```

The newest turn shows `cursor` in the AGENT column. If nothing is there,
`turnback status` says whether a turn is still open, and the Hooks tab in
Cursor's Customize view and its Hooks output channel show which hooks
Cursor loaded and any errors.

## How it maps

- `beforeSubmitPrompt` runs right after you send a prompt, before Cursor
  sends the request: `turnback hook start --agent cursor`.
- `stop` runs when the agent loop ends: `turnback hook end`. Its input
  reports whether the loop completed, was aborted or failed, so a turn you
  stop is recorded too.
- Hooks in your home folder run from `~/.cursor/`, not from the project.
  turnback finds the project in `workspace_roots`, the list of folders in
  Cursor's hook input. In a workspace with several folders, it records in
  the first one that is a git repository.
- `timeout` is in seconds.
- With turnback's exit status always 0 and no output, Cursor lets the
  prompt through. Cursor's own example of a logging hook on these two events
  prints nothing either.

## Remove it

Delete the two entries, or the whole file if it holds nothing else. Cursor
picks up the change when you save.

## Limits

- Cursor's documentation does not say which shell runs hooks on Windows.
  `|| exit 0` works in sh, cmd and PowerShell 7, but not in Windows
  PowerShell 5.1. These settings have not been tried on Windows.
- The CLI has no page of its own about hooks, so how it differs from the
  editor is not documented.
- Cloud agents do not run hooks from `~/.cursor/hooks.json`.
- Cursor also loads hooks from another agent's settings files while its
  setting "Include Third-Party Plugins, Skills, and Other Configs" is on,
  which it is by default. If turnback is set up there too, the duplicate
  calls are harmless; see [Things to know](README.md#things-to-know).

## Sources

Last checked on 2026-10-05.

- [Hooks](https://cursor.com/docs/hooks): locations, schema, events and
  their input, exit codes, the folders hooks run from, the Hooks tab and
  output channel
- [Third-party hooks](https://cursor.com/docs/reference/third-party-hooks):
  hooks loaded from other agents' settings files
- [Cursor 1.7 changelog](https://cursor.com/changelog/1-7): hooks introduced,
  in beta
- [Cursor CLI changelog](https://cursor.com/docs/cli/changelog): hooks in
  the CLI
