# Codex CLI

The Codex CLI runs hooks from a `hooks.json` file, and hooks are on by
default. These settings record a turn for every prompt, including the ones
you interrupt.

## Set it up

1. Check that your turnback knows `hook start`; see
   [Before you start](README.md#before-you-start).
2. Save the settings as `~/.codex/hooks.json` to record turns in every
   project, or as `.codex/hooks.json` in a project you trust. If the file
   already exists, add the three entries to its `hooks` object instead.

```json
{
  "description": "Record every Codex turn with turnback.",
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "turnback hook start --agent codex || exit 0",
            "commandWindows": "turnback hook start --agent codex",
            "timeout": 120
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "turnback hook end || exit 0",
            "commandWindows": "turnback hook end",
            "timeout": 120
          }
        ]
      }
    ],
    "Interrupt": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "turnback hook end || exit 0",
            "commandWindows": "turnback hook end",
            "timeout": 3
          }
        ]
      }
    ]
  }
}
```

3. Trust the hooks. Codex skips a new or changed hook until you have
   reviewed it, and warns at startup when hooks are waiting. Run `/hooks` in
   Codex and trust the three turnback hooks.

## Check that it works

1. Start Codex in a git repository. `/hooks` lists the turnback hooks as
   trusted.
2. Ask for a small change, then run this in a terminal in that repository:

```bash
turnback log
```

The newest turn shows `codex` in the AGENT column. If nothing is there,
check in `/hooks` that the hooks are trusted and enabled, and
`turnback status` says whether a turn is still open.

## How it maps

- `UserPromptSubmit` runs before Codex sends your prompt:
  `turnback hook start --agent codex`.
- `Stop` runs when Codex has finished the turn: `turnback hook end`.
- `Interrupt` runs when you interrupt a turn: `turnback hook end`. If `Stop`
  runs as well, the second call finds nothing to do. Codex gives this hook
  at most three seconds. If turnback needs longer, in a very large
  repository, the turn stays open and your next prompt records it.
- Commands run in the session's folder, which the input also names as
  `cwd`. `timeout` is in seconds.
- Codex reads plain text printed by a prompt hook as context for the model,
  and expects JSON or nothing from a stop hook. turnback prints nothing.

## Remove it

Delete the three entries, or the file if it holds nothing else. To turn
off every hook, set `hooks = false` in the `[features]` table of
`~/.codex/config.toml`.

## Limits

- According to its source code, Codex runs hooks with your shell, which on
  Windows can be PowerShell or cmd, and no guard works in both. So
  `commandWindows` runs turnback without one. turnback still exits 0
  whatever happens, but a turnback too old to know `hook start` fails with
  status 2 there, which blocks your prompt: check the version first, as
  [Before you start](README.md#before-you-start) says. These settings have
  not been tried on Windows.
- The Codex IDE extension shares its configuration with the CLI, but its
  documentation does not say whether it runs hooks.

## Sources

Last checked on 2026-10-05.

- [Hooks](https://learn.chatgpt.com/docs/hooks): locations, review and
  trust, the shape of `hooks.json`, timeouts, events and their input and
  output, turning hooks off
- [Developer settings for the IDE extension](https://learn.chatgpt.com/docs/developer-settings?surface=ide):
  the configuration it shares with the CLI
- Codex's [source code](https://github.com/openai/codex/tree/main/codex-rs/hooks):
  the shell hooks run in, and that a stop hook may print nothing
