# Aider

Aider has no hooks for prompts. It does commit every change it makes, and
turnback can record a turn at every commit through git's own post-commit
hook.

## Set it up

In the repository you use Aider in:

```bash
turnback hook install
```

From then on, every commit records a turn holding everything that changed
since the previous turn, named after the commit message. That includes your
own commits. If the repository already has a post-commit hook, or manages
its hooks with a tool, `hook install` changes nothing and says which line to
add to that hook instead.

## Check that it works

1. Run Aider in the repository and ask for a small change. Aider commits it.
2. Run this in a terminal in that repository:

```bash
turnback log
```

The newest turn carries the message of Aider's commit.

## How it maps

- Aider commits each change it makes; its auto-commits are on by default.
  Each commit closes a turn.
- Aider adds `--no-verify` to its commits unless you pass
  `--git-commit-verify`. That skips only git's pre-commit and commit-msg
  hooks; the post-commit hook still runs.
- Before Aider edits a file that has changes you have not committed, it
  commits those first, so that commit becomes a turn of your own edits.
- According to Aider's source code, `/undo` resets the branch rather than
  committing, so what it takes back shows up in the next commit's turn.
- With `--no-auto-commits`, Aider makes no commits, so its changes are
  recorded only when you commit them.

## Remove it

```bash
turnback hook uninstall
```

## Keep your own edits apart

Commit turns hold everything since the previous commit, your edits
included. To keep the agent's work apart from yours, wrap each prompt in
`turnback start --agent aider` and `turnback end` instead. While a turn is
recorded that way, commits do not close turns.

## Sources

Last checked on 2026-10-05.

- Aider: [Git integration](https://aider.chat/docs/git.html) and
  [options](https://aider.chat/docs/config/options.html) for
  `--auto-commits`, `--dirty-commits`, `--git-commit-verify` and
  `--no-auto-commits`; [notifications](https://aider.chat/docs/usage/notifications.html)
  for the one command Aider runs when it waits for input, which its
  [source code](https://github.com/Aider-AI/aider/blob/main/aider/io.py)
  also runs in the middle of a turn when Aider asks a question, so it cannot
  mark the end of one; the source code of
  [`/undo`](https://github.com/Aider-AI/aider/blob/main/aider/commands.py)
- git: [githooks](https://git-scm.com/docs/githooks) for which hooks
  `--no-verify` skips and when post-commit runs
