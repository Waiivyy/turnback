# Record turns from your agent's hooks

Many coding agents can run a command when you submit a prompt and again when
they have finished. Point those two hooks at turnback, and every turn is
recorded without typing `turnback start` and `turnback end`.

| Agent | Mechanism | Status | Guide |
|---|---|---|---|
| Cursor, editor and CLI | `hooks.json`: `beforeSubmitPrompt`, `stop` | Supported | [cursor.md](cursor.md) |
| GitHub Copilot CLI | hook files: `userPromptSubmitted`, `agentStop` | Supported | [copilot.md](copilot.md) |
| GitHub Copilot in VS Code | the same hook files; VS Code hooks are in preview | Supported | [copilot.md](copilot.md#vs-code) |
| GitHub Copilot in JetBrains IDEs | the same hook files | Partial: no end hook documented | [copilot.md](copilot.md#jetbrains-ides) |
| Codex CLI | `hooks.json`: `UserPromptSubmit`, `Stop`, `Interrupt` | Supported | [codex.md](codex.md) |
| Gemini CLI | `settings.json`: `BeforeAgent`, `AfterAgent` | Supported | [gemini-cli.md](gemini-cli.md) |
| Aider | no hooks; it commits every change | Use the git hook | [aider.md](aider.md) |
| Copilot cloud agent | hooks run in a sandbox that is discarded after each job | Not supported | [copilot.md](copilot.md#copilot-cloud-agent) |
| Other agents | | Not covered yet | [below](#other-agents) |

Supported means that the settings in [examples/hooks](../../examples/hooks)
are tested with the hook input each agent documents, run through a shell the
way the agent runs them. They are not tested inside the agents themselves.

## What the hooks run

- When you submit a prompt: `turnback hook start --agent <name>`
- When the agent has finished: `turnback hook end`

These work like `turnback start` and `turnback end`, with differences that
keep a hook from getting in the agent's way:

- **They always exit with status 0.** Agents read a hook's exit status as an
  instruction: most of them block your prompt on status 2, and some make the
  agent keep working.
- **They print nothing when all goes well**, because several agents add the
  output of a prompt hook to what the model reads. When something goes
  wrong, they print one line on standard error, which some agents show you.
- **They find the project in the JSON the agent passes to the hook**, so
  they also work when an agent runs hooks from another folder.
- **`hook start` first records a turn that was never ended**, for example
  because you stopped the agent before it finished, so that the next prompt
  starts a turn of its own.
- **`hook end` does nothing when no turn is being recorded.**

Every command in the settings ends with `|| exit 0`, or `; exit 0` in
PowerShell. If turnback is not installed, or is too old to know
`hook start`, the shell would otherwise fail with a status that the agent may
read as "block this prompt".

## Before you start

The hooks need a turnback that knows `hook start`. turnback 0.1.0 does not;
check with:

```bash
turnback help hook
```

If the help does not mention `turnback hook start`, install the latest
version. Until a release includes it, install from the main branch with Go:

```bash
go install github.com/Waiivyy/turnback@main
```

## Things to know

- **One agent at a time per working tree.** Two agents working in the same
  folder would end each other's turns. Separate git worktrees each have their
  own `.turnback/`.
- **A turn holds everything that changed between the two hooks**, including
  edits you make yourself while the agent works.
- **A stopped agent may leave its turn open.** Not every agent runs its end
  hook when you stop it; each guide says what its agent does. The turn then
  stays open until your next prompt and takes in the edits you make
  meanwhile. Run `turnback end` yourself after you stop the agent if you
  want to edit first.
- **Undo from your own terminal while the agent is idle.** turnback does not
  undo while a turn is being recorded, so asking the agent to run
  `turnback undo` does not work: its prompt hook has just started a turn.
- **Settings in your home folder apply to every project.** On the first
  prompt in a git repository, turnback creates `.turnback/` there, which git
  ignores. Outside a git repository, the prompt hook prints one line and the
  agent carries on.
- **Other stop hooks.** If another hook makes the agent keep working after
  turnback recorded the turn, that extra work belongs to no turn, unless the
  agent sends it as a new prompt.
- **Duplicate settings are harmless.** Some agents also run hooks from other
  agents' settings files. If turnback is set up in two of them, the second
  call finds nothing to do.

## Other agents

These agents document hooks with similar events, but no settings for them
are tested here yet: [Factory Droid](https://docs.factory.com/harness/hooks),
[Kiro](https://kiro.dev/docs/hooks/),
[Devin Desktop](https://docs.devin.ai/cli/extensibility/hooks/overview),
which replaced Windsurf's Cascade agent,
[Qwen Code](https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md),
[Trae](https://docs.trae.ai/ide/hook-configuration-reference?_lang=en),
[Junie CLI](https://junie.jetbrains.com/docs/junie-cli-hooks.html) and
[Goose](https://goose-docs.ai/docs/guides/context-engineering/hooks/).
[Auggie](https://docs.augmentcode.com/cli/hooks) has a hook for the end of a
turn but none for a submitted prompt.
[Amp](https://ampcode.com/docs/customize/plugins) and
[OpenCode](https://opencode.ai/docs/plugins/) use plugins written in code
instead of settings files. Cline's file hooks are described in
[its repository](https://github.com/cline/cline/blob/main/.clinerules/hooks/README.md)
but no longer in its documentation, so they are not covered either.

With any agent, you can run `turnback start --agent <name>` before you send
a prompt and `turnback end` when the agent is done. If the agent commits its
own work, `turnback hook install` records a turn at every commit instead, as
described for [Aider](aider.md).

## Sources

Last checked on 2026-10-05. Each guide lists the documentation it is based
on. The list of other agents comes from the pages linked above and from
[Devin Desktop's FAQ](https://docs.devin.ai/desktop/devin-desktop-faq) and
[changelog](https://docs.devin.ai/desktop/changelog) for Windsurf's change of
name and agent.
