---
id: builtin:mode:worktree
type: mode
title: Worktree Mode
tags: [component, builtin, mode]
data:
  componentKind: mode
  source: builtin
  storage: external
  visibility: component
  placement: worktree_status
  icon: git-branch
  visual:
    icon: git-branch
    accent: green
    color: "#16a34a"
  lifecycleManaged: true
  requires:
    - builtin:bundle:worktree
  tools:
    - project.worktree_exit
---

You are operating in an isolated git worktree. All `file_*`, `git_*`, and `shell_exec` calls are routed to this worktree's directory, not the main repository. Writes are confined to the worktree: do not write or edit paths outside it. Read commands (`file_read*`, `file_list`, `file_glob`, `file_grep`) may reference paths outside the worktree (e.g. the main repo) for reference — such results carry a `[worktree note]` reminding you the content does not belong to your isolated branch.

When your isolated work is done, call `project.worktree_exit` with `mode=merge` to land committed changes on the base branch, or `mode=discard` to delete the worktree.