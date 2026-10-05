# Why not the built-in checkpoints?

Most coding agents can already take back their own edits, and git can take
back anything you committed. This page compares those features with
turnback, so you can tell whether you need it at all.

What other tools can do changes often. Everything below about them was
checked against their official documentation on 2026-10-05, and the
[sources](#sources) are listed at the end.

- [What turnback does and does not do](#what-turnback-does-and-does-not-do)
- [Cursor](#cursor)
- [GitHub Copilot](#github-copilot)
- [Aider](#aider)
- [Plain git](#plain-git)
- [At a glance](#at-a-glance)
- [Sources](#sources)

## What turnback does and does not do

turnback records each agent turn as the difference between two snapshots of
your working tree, one taken when the turn starts and one when it ends. You
mark those moments with `turnback start` and `turnback end`, from an agent's
hooks where it has them, or at every commit with `turnback hook install`.
turnback only looks at files, never at the agent, so it works the same with
any agent.

`turnback undo` takes one turn back out, or only some of its files, and
keeps the changes made after it, whether a later turn or you made them. It
does this with a three-way merge per file, the same operation `git revert`
uses. It shows the result as a dry run first and writes nothing until you
confirm. If any file conflicts, it writes nothing at all. Every undo is
recorded as a turn of its own, so an undo can be undone too. turnback never
creates commits and never changes your index, branches, refs or stash. Its
records live in `.turnback/`, which git ignores, until you delete that
folder.

Things to know before relying on it:

- **Conflict detection is textual.** turnback compares lines, not code. If
  a later turn calls a function that the undone turn added, the undo applies
  cleanly and the build breaks. turnback tells you when later turns exist;
  run your tests after an undo.
- **A turn holds everything that changed between its start and its end**,
  whoever changed it: the agent's edits, files written by commands it ran,
  and edits you made yourself in the meantime.
- **Nothing outside a turn is recorded.** Changes made before `start` or
  after `end` belong to no turn.
- **Your unsaved edits are protected.** If a file the undo must rewrite
  holds edits that are neither committed nor part of a recorded turn,
  turnback refuses unless you pass `--force`, and then saves the file first.
- **Files git ignores are never recorded or restored**, such as `.env` or
  build output, and neither is anything outside the repository.
- **It needs a git repository** and git 2.30 or newer, even if you never
  commit.
- **It only handles files.** It cannot rewind an agent's conversation; the
  agent's own feature does that.

## Cursor

**What it does.** Before significant edits, Cursor's agent saves a
checkpoint of the files it is about to change. Checkpoints are stored
locally, apart from git. From the chat you can preview the files at any
checkpoint and restore them, which puts all of them back to that point; the
conversation stays as it is. Before you keep the agent's edits, you can also
reject any of them in the diff view. The Cursor CLI has `/rewind`, also
available as `/undo` and `/restore` and on by default since June 2026. It
restores files and the conversation to an earlier turn, shows each turn's
diff, and can restore the conversation alone.

**What it does not cover.** A restore goes back to a point in the chat, so
the agent's changes after that point are rolled back too. Cursor describes
checkpoints as a way to undo the agent's changes and points to git for
lasting version control. Its documentation does not say whether checkpoints
capture files changed by terminal commands or by you, or how long they are
kept.

**Where turnback differs.** turnback can take back an earlier turn and keep
the turns after it, or take back one file of it. It records whatever changed
during the turn, including files written by commands. Its records do not
depend on a chat and stay in `.turnback/` until you delete them, and it works
the same with other agents.

**When Cursor's own feature is enough.** When the step you regret is the
latest one, or you are happy to lose everything after it, and you are still
in the chat where it happened. Checkpoints also restore something turnback
cannot: the conversation.

## GitHub Copilot

Copilot runs in several places, and each has its own way back.

**VS Code.** Before each request, VS Code takes a snapshot of the workspace
files the request affects. Restoring a checkpoint removes the later requests
from the conversation and puts the files back as they were, and Redo brings
the changes back. Checkpoints do not reverse terminal commands or changes
made outside the workspace, and VS Code describes them as temporary, not a
replacement for git. In older sessions, each pending edit can also be kept
or undone on its own before you accept it.

**Visual Studio.** In agent mode you can keep or undo each chunk of an edit,
and restore the checkpoint before a prompt. The documentation says the agent
does not support stepwise undo or redo.

**JetBrains IDEs and Xcode.** The plugins' release notes mention restoring
checkpoints, but the documentation does not describe them, so this page
cannot say what they cover.

**Copilot CLI.** Pressing Esc twice, or typing `/undo` or `/rewind`, opens a
list of earlier turns. Copilot tracks the files changed in each turn,
including by shell commands and subagents, so it works without git.
Restoring shows a preview per file and then puts back the files Copilot
changed in that turn and in every later turn, skipping files you have edited
since. The conversation after that point is removed, and a rewind cannot be
undone. Rewind is not available in remote sessions, skips files over 10 MB
and does not capture a turn that changed more than 500 files. One GitHub
page still carries an older warning that rewind also reverts your own edits;
the current guide and the changelog for version 1.0.78 say it skips them.

**Copilot cloud agent.** The agent on GitHub.com works on a branch and
records its steps as commits in a pull request, so its work is undone with
git: push a fix, ask it for a change, or revert the pull request.

**Where turnback differs.** Copilot CLI's rewind is the closest to turnback:
it works per turn and covers shell commands. The differences are that a
rewind always goes back to a point and discards every turn after it, cannot
be undone, and only works inside that local session. turnback takes back one
earlier turn while keeping the later ones, can take back a single file,
records each undo so it can be undone, and keeps its records across sessions
and agents. Where Copilot CLI skips a file you edited later, turnback merges
the turn's change out of it and keeps your edit, or reports a conflict when
both touched the same lines. If your edit is neither committed nor recorded,
it asks for `--force` first.

**When Copilot's own features are enough.** In an editor, when you want to
step back to before a request, and in VS Code perhaps redo it. In the CLI,
when the turn you regret is the latest one and you are still in that
session. With the cloud agent, git and pull request review already are the
undo.

## Aider

**What it does.** Aider commits every change it makes, with a descriptive
message, and before editing a file that has changes you have not committed,
it commits those first. `/undo` undoes Aider's last commit and discards its
change, and you can run it again to go further back. It only acts on commits
Aider made.

**What it does not cover.** `/undo` always starts with the newest commit, so
taking back an older change means first taking back everything Aider did
after it.

**Where turnback differs.** turnback takes back one earlier turn and keeps
the later ones, and it leaves your history alone: the undo changes files, and
committing the result is up to you. The two also work together: with
`turnback hook install`, every commit Aider makes is recorded as a turn,
which turnback can then take back on its own.

**When Aider's own feature is enough.** When you want to drop Aider's latest
change, or the last few, right after it made them.

## Plain git

**`git stash`** saves the changes in your working tree and index and returns
to your last commit, including untracked files only with `-u`. It sets
everything aside at once rather than one turn, and applying it again can
conflict.

**A commit per turn** gives you per-turn history: commit after every agent
turn, with `git add -A` so that new files are included, and each turn is a
commit. Your own edits from the same time go into the same commit, and your
branch fills up with turn commits unless you squash them later.

**`git revert`** makes a new commit that takes an earlier commit back out and
keeps the later ones. It needs a clean working tree, works on whole commits,
and leaves conflicts in the files for you to resolve. For a single file,
`git restore` brings back an old version, but it discards your later changes
to that file.

**Where turnback differs.** turnback works on changes you have not
committed, takes back a single file of a turn by merging rather than
overwriting, shows a dry run, writes nothing at all when there is a
conflict, and does not add commits.

**When git is enough.** If you already commit after every turn and do not
mind revert commits in your history, `git revert` gives you the same
selective undo. `turnback hook install` builds on that habit.

## At a glance

| Feature | What a restore takes back | Can the restore be undone? |
|---|---|---|
| Cursor checkpoints and CLI `/rewind` | everything after the chosen point | not documented |
| VS Code checkpoints | the chosen request and every later one | yes, with Redo |
| Copilot CLI rewind | the chosen turn and every later one, skipping files you edited | no |
| Aider `/undo` | Aider's newest commit; repeat to go further back | not documented |
| `git revert` | one commit, keeping the later ones | yes, revert the revert |
| `turnback undo` | one turn, or some of its files, keeping the later ones | yes, the undo is a turn |

## Sources

Last checked on 2026-10-05.

- Cursor: [Agent overview, Checkpoints](https://cursor.com/docs/agent/overview#checkpoints),
  [Agent help](https://cursor.com/help/ai-features/agent),
  [CLI slash commands](https://cursor.com/docs/cli/reference/slash-commands),
  [CLI changelog](https://cursor.com/docs/cli/changelog)
- VS Code: [Review code edits](https://code.visualstudio.com/docs/agents/run/review-code-edits)
- Visual Studio: [Copilot agent mode](https://learn.microsoft.com/en-us/visualstudio/ide/copilot-agent-mode?view=visualstudio)
- JetBrains: GitHub Copilot plugin release notes for
  [1.5.48](https://plugins.jetbrains.com/plugin/17718-github-copilot--your-ai-pair-programmer/versions/stable/786198)
  and [1.18.0](https://plugins.jetbrains.com/plugin/17718-github-copilot--your-ai-pair-programmer/versions/stable/1173984)
- Xcode: [Copilot for Xcode changelog](https://github.com/github/CopilotForXcode/blob/main/CHANGELOG.md)
- Copilot CLI: [Rolling back changes](https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/roll-back-changes),
  [Canceling and rolling back](https://docs.github.com/en/copilot/concepts/agents/copilot-cli/cancel-and-roll-back),
  [Command reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-command-reference),
  [Changelog](https://github.com/github/copilot-cli/blob/main/changelog.md)
- Copilot cloud agent: [About the cloud agent](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/about-cloud-agent),
  [Responsible use of agents](https://docs.github.com/en/copilot/responsible-use/agents),
  [Reverting a pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/incorporating-changes-from-a-pull-request/reverting-a-pull-request)
- Aider: [Git integration](https://aider.chat/docs/git.html),
  [In-chat commands](https://aider.chat/docs/usage/commands.html),
  [Release history](https://aider.chat/HISTORY.html)
- git: [git-stash](https://git-scm.com/docs/git-stash),
  [git-commit](https://git-scm.com/docs/git-commit),
  [git-add](https://git-scm.com/docs/git-add),
  [git-revert](https://git-scm.com/docs/git-revert)
