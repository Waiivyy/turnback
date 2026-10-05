# Gemini CLI

Gemini CLI runs hooks configured under `hooks` in its `settings.json`, and
hooks are on by default. These settings record a turn for every prompt.

## Set it up

1. Check that your turnback knows `hook start`; see
   [Before you start](README.md#before-you-start).
2. Add the `hooks` key below to `~/.gemini/settings.json` to record turns in
   every project, or to `.gemini/settings.json` in one project. The file
   holds your other settings too, so merge rather than replace it: if it
   already has a `hooks` key, add the `BeforeAgent` and `AfterAgent` entries
   to it.

```json
{
  "hooks": {
    "BeforeAgent": [
      {
        "hooks": [
          {
            "name": "turnback-start",
            "type": "command",
            "command": "turnback hook start --agent gemini-cli || exit 0",
            "timeout": 30000
          }
        ]
      }
    ],
    "AfterAgent": [
      {
        "hooks": [
          {
            "name": "turnback-end",
            "type": "command",
            "command": "turnback hook end || exit 0",
            "timeout": 30000
          }
        ]
      }
    ]
  }
}
```

## Check that it works

1. Start Gemini CLI in a git repository and open `/hooks panel`: it lists
   `turnback-start` and `turnback-end`.
2. Ask for a small change, then run this in a terminal in that repository:

```bash
turnback log
```

The newest turn shows `gemini-cli` in the AGENT column. If nothing is
there, `turnback status` says whether a turn is still open.

## How it maps

- `BeforeAgent` runs after you submit a prompt, before the agent starts
  planning: `turnback hook start --agent gemini-cli`.
- `AfterAgent` runs once per turn, after the final response:
  `turnback hook end`.
- The input names the folder Gemini CLI works in as `cwd`. `timeout` is in
  milliseconds; Gemini CLI's default is 60 seconds.
- Gemini CLI parses what a hook prints on stdout as JSON and shows plain
  text to you. turnback prints nothing on stdout. According to its source
  code, Gemini CLI reads stderr instead when stdout is empty, so when
  turnback cannot record a turn, its one line appears as a message.
- Gemini CLI blocks the prompt when a hook exits with status 2, or retries
  the turn when the end hook does. According to its source code, it does
  the same for any status other than 0 and 1 that comes with output, as
  when the shell cannot find a command. `|| exit 0` prevents both.

## Stopped turns

According to Gemini CLI's source code, it does not run `AfterAgent` when
you cancel a turn. That turn stays open until your next prompt; see
[Things to know](README.md#things-to-know).

## Remove it

Delete the two entries. To keep them but switch them off, run
`/hooks disable turnback-start` and `/hooks disable turnback-end`. Setting
`hooksConfig.enabled` to `false` turns off every hook after a restart.

## Limits

- Gemini CLI warns you about a project's hooks the first time they run and
  whenever their name or command changes.
- According to its source code, Gemini CLI runs hooks with PowerShell on
  Windows. `|| exit 0` works in PowerShell 7 but not in Windows
  PowerShell 5.1. These settings have not been tried on Windows.

## Sources

Last checked on 2026-10-05, at commit fb972b2 of Gemini CLI's repository.

- [Hooks](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/index.md):
  events, exit codes, the settings files, `/hooks` commands, project hook
  warnings
- [Hooks reference](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md):
  the schema, timeouts, the input and output of `BeforeAgent` and
  `AfterAgent`
- [Configuration](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md):
  `hooksConfig.enabled`
- Gemini CLI's source code:
  [hookRunner.ts](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/hooks/hookRunner.ts)
  for how output and exit codes are read,
  [client.ts](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/core/client.ts)
  for `AfterAgent` on cancel, and
  [shell-utils.ts](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/utils/shell-utils.ts)
  for the shell
