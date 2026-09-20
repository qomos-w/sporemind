---
id: builtin:bundle:git-tools
type: bundle
title: Git Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: git-branch
  visual:
    icon: git-branch
    accent: red
    color: "#dc2626"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - project.git_status
    - project.git_log
    - project.git_diff
    - project.git_add
    - project.git_commit
    - project.git_push
    - project.git_pull
    - project.git_branch
    - project.git_checkout
---

## Git Tools Usage

Git tools provide repository operations. Use them for version control, change inspection, and branch management.

### Inspection

- **git_status** — Show working tree status (modified, staged, untracked files).
- **git_log** — Show commit history. Use `limit` to control how many commits to return.
- **git_diff** — Show changes for a specific file at a specific commit. Requires `file_path` and `commit_hash`.

### Staging and Committing

- **git_add** — Stage files. Pass `paths` as an array of file paths.
- **git_commit** — Commit staged changes with a `message`.
- **git_push** — Push commits to a `remote`.

### Branch Operations

- **git_branch** — List branches (no args) or create/switch branches.
- **git_checkout** — Switch to a `branch`. Use `create=true` to create and switch.
- **git_pull** — Pull from a `remote`.

### Rules

1. **Review before commit** — Always check `git_status` and `git_diff` before committing to ensure only intended changes are staged.
2. **Meaningful messages** — Write clear, descriptive commit messages that explain what changed and why.
3. **Stage explicitly** — Use `git_add` with specific paths rather than staging everything.
