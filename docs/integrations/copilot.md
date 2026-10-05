# GitHub Copilot

GitHub Copilot CLI runs hooks from JSON files, and Copilot in VS Code reads
the same files. One file records a turn for every prompt in both.

## Set it up

1. Check that your turnback knows `hook start`; see
   [Before you start](README.md#before-you-start).
2. Save the settings as `~/.copilot/hooks/turnback.json`, or
   `%USERPROFILE%\.copilot\hooks\turnback.json` on Windows. If
   `COPILOT_HOME` is set, the folder is `$COPILOT_HOME/hooks/` instead.

```json
{
  "version": 1,
  "hooks": {
    "userPromptSubmitted": [
      {
        "type": "command",
        "bash": "turnback hook start --agent copilot || exit 0",
        "powershell": "turnback hook start --agent copilot; exit 0",
        "timeoutSec": 30
      }
    ],
    "agentStop": [
      {
        "type": "command",
        "bash": "turnback hook end || exit 0",
        "powershell": "turnback hook end; exit 0",
        "timeoutSec": 30
      }
    ]
  }
}
```

The file also works in a repository's `.github/hooks/` folder, but then it
runs for everyone who works on the repository, and in the Copilot cloud
agent. For a tool of your own, your home folder is the better place.

## Check that it works

1. Start Copilot CLI in a git repository. Hook files are read when the CLI
   starts; its startup message counts the hooks it loaded, and `/env` lists
   them with the files they came from.
2. Ask for a small change, then run this in a terminal in that repository:

```bash
turnback log
```

The newest turn shows `copilot` in the AGENT column. If nothing is there,
`turnback status` says whether a turn is still open.

## How it maps

- `userPromptSubmitted` runs when you submit a prompt:
  `turnback hook start --agent copilot`.
- `agentStop` runs when the main agent has finished its turn:
  `turnback hook end`.
- `bash` runs on macOS and Linux and `powershell` on Windows; `timeoutSec`
  is in seconds.
- Hook commands run in the project root, and the input names the folder
  Copilot works in as `cwd`.
- Copilot CLI treats status 2 from these hooks as a warning and other
  failures as logged errors, and carries on either way. turnback's status is
  always 0.

## VS Code

VS Code's agent hooks are in preview. The local agent reads hook files from
`~/.copilot/hooks/`, maps Copilot's event names to its own, and runs them
while the setting `chat.useHooks` is on, which it is by default. VS Code's
Copilot sessions on the Agent Host use the same implementation as Copilot
CLI.

To check the setup, run **Chat: Configure Hooks** from the Command Palette
to see which hook files VS Code found, and open the **GitHub Copilot Chat
Hooks** channel in the Output panel to see each run and its errors.

VS Code passes the folder as `cwd` too, but marks it as optional; without
it, turnback uses the folder the hook runs in, which is the workspace root.
VS Code shows a warning for any exit status other than 0 and blocks on 2,
which turnback never returns.

According to VS Code's source code, it does not run the stop hook when you
cancel a request. That turn stays open until your next prompt; see
[Things to know](README.md#things-to-know).

## JetBrains IDEs

Copilot in JetBrains IDEs supports agent hooks in `.github/hooks/` in the
repository. Its announcements list `userPromptSubmitted` among the supported
events, but not `agentStop`. Without an end hook, each turn stays open until
the next prompt, so it also takes in the edits you make in between. This has
not been verified in a JetBrains IDE.

## Copilot cloud agent

Not supported. The cloud agent on GitHub.com runs hooks from `.github/hooks/`
in a sandbox that is discarded when its job ends, so there would be nothing
left to undo. If the file is in a repository the cloud agent works on,
`|| exit 0` keeps it harmless there.

## Remove it

Delete `~/.copilot/hooks/turnback.json`. To keep the file but switch its
hooks off, add `"disableAllHooks": true` at its top level.

## Limits

- Copilot CLI's documentation does not say whether `agentStop` runs when
  you interrupt a turn.
- The `powershell` commands have not been tried on Windows.

## Sources

Last checked on 2026-10-05.

- [Hooks reference](https://docs.github.com/en/copilot/reference/hooks-reference):
  locations, schema, events and their input, exit codes, timeouts,
  `disableAllHooks`, the cloud agent's sandbox
- [Using hooks with Copilot CLI](https://docs.github.com/en/copilot/how-tos/copilot-cli/customize-copilot/use-hooks)
  and the [Copilot CLI changelog](https://github.com/github/copilot-cli/blob/main/changelog.md):
  when hook files are read, the startup message, `/env`, the project root as
  the default folder
- [CLI command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference):
  `/env`
- VS Code: [Configure agent hooks](https://code.visualstudio.com/docs/agent-customization/hooks)
  and the [hooks reference](https://code.visualstudio.com/docs/agents/reference/hooks-reference);
  that the stop hook is skipped on cancel comes from VS Code's
  [source code](https://github.com/microsoft/vscode/blob/main/extensions/copilot/src/extension/intents/node/toolCallingLoop.ts)
- JetBrains: GitHub changelog of
  [2026-03-11](https://github.blog/changelog/2026-03-11-major-agentic-capabilities-improvements-in-github-copilot-for-jetbrains-ides/)
  and [2026-06-02](https://github.blog/changelog/2026-06-02-introducing-copilot-cli-and-agentic-capabilities-enhancements-in-jetbrains-ides/)
