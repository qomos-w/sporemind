---
id: builtin:bundle:worktree
type: bundle
title: Worktree Sandbox
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: git-branch
  visual:
    icon: git-branch
    accent: green
    color: "#16a34a"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  modeManaged: true
  tools:
    - project.worktree_enter
    - project.worktree_list
    - project.worktree_get
    - project.worktree_agent_bindings
---

## Worktree Sandbox Usage

A worktree is an isolated git checkout that an agent may opt into. You are NOT
in a worktree by default; if you were, your prompt would explicitly say "You
are operating in an isolated git worktree". When you do enter one (via
`project.worktree_enter`), every `file_*`, `git_*`, and `shell_exec` call you
make is transparently routed to that worktree's directory, not the main
repository. This lets you work in parallel with other agents without
clobbering each other.

### Lifecycle

- **enter** — Enter (and if needed create + bind) a per-agent exclusive
  worktree for this session. Idempotent: if you are already bound, it returns
  the current one. The worktree name/branch is derived from your caller id
  (`<callerID>-<random>`); the `Name` parameter is ignored and never collides.
  While bound, writes are hard-isolated: you cannot write or edit the main
  repository or any other agent's worktree (absolute paths and `..` escapes are
  denied). Read commands stay browse-anywhere — reading outside the worktree is
  allowed, and such results carry a `[worktree note]` so you know the content
  is not from your isolated branch.
- **exit** — Leave the worktree and return to the main repository. `mode` is
  either `discard` (delete the worktree and throw away its branch) or `merge`
  (merge its branch into the main branch, then delete the worktree).

### Inspection

- **list** — List all worktrees tracked by this project.
- **get** — Fetch a single worktree by ID (name, path, branch, status).
- **agent_bindings** — Return your own worktree binding only; you cannot see other agents' bindings.

### Rules

1. **Enter before you isolate.** Call `enter` only when you actually need an
   isolated branch; do not wrap every trivial edit in a worktree.
2. **Always exit explicitly.** When your isolated work is done, call `exit`.
   Choose `merge` when the branch should land on the main branch, or `discard`
   when the experiment should be thrown away. Never leave a worktree dangling.
3. **Resolve conflicts before merge.** `exit(merge)` fails on conflict and
   keeps the worktree intact so you can resolve the conflict in place and retry.
