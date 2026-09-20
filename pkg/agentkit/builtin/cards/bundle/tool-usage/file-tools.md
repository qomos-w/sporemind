---
id: builtin:bundle:file-tools
type: bundle
title: File Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: file-code
  visual:
    icon: file-code
    accent: cyan
    color: "#0891b2"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - project.grep
    - project.glob
    - project.read
    - project.write
    - project.edit
    - project.list
    - project.rm
---

## File Tools Usage

Dedicated file tools are your primary interface to the codebase. Prefer them over shell commands for any file operation.

### Search and Discovery

- **project.grep** — Search file contents with a Go-compatible regular expression. Defaults to `output_mode="files"` (returns matching file paths); use `output_mode="content"` for matching lines/context or `output_mode="count"` for per-file counts. Use `glob` to narrow the files and `path` to narrow the root; searches recurse by default, and `depth` limits directory levels. An empty result is a successful search with no matches, not an error. If the tool returns `project.grep: invalid pattern`, fix the regular expression (including escaping); other `project.grep:` errors indicate the underlying search failed. Searches skip node_modules, .git, vendor, dist, build, .gitignored entries, and `exclude` entries by default; set `no_ignore=true` only when those files are intentionally needed.
- **project.glob** — Find files by name pattern (e.g. `**/*.go`). Supports `path`, `maxdepth`, `no_ignore`.
- **project.list** — List directory contents, one entry per line (dirs end with `/`). `depth`: 0/omit = current dir, 1 = one level, -1 = full recursion. `all`: include hidden entries (starting with `.`). `detail=true`: each line appends mtime (`YYYY-MM-DD HH:MM:SS ±HH:MM`) and `Size:N` for files — need size/mtime to pick this. Skips node_modules, .git, vendor, dist, build by default.

**Search before read**: Locate with project.grep/project.glob first, then read targeted files. Don't read blindly.

### Reading

- **project.read** — Read a file with optional `offset`, `limit`, `tail`, and `filter`. Use `tail` to read the last N lines. Use `filter` to grep within a single file.

### Writing

- **project.write** — Create or overwrite a file. Use `confirm=true` to confirm overwriting an existing file.
- **project.edit** — Edit an existing file by providing an exact, non-empty `old_string` and a different `new_string`. By default the old text must occur exactly once: if it is absent, re-read the file and use its current exact text; if it occurs multiple times, narrow the old text or set `replace_all=true` only when every occurrence must change. Use `overwrite=true` to replace the entire existing file, and `project.write` to create a missing file. Errors are prefixed with `project.edit`; successful responses include replacement counts and hunks. Paths outside configured roots or a bound worktree are rejected.
- **project.rm** — Remove a file or directory. Use `recursive=true` for directories.

### Rules

1. **Dedicated over generic** — Never use shell for cat, head, tail, find, ls, grep. Use the file tools instead.
2. **Read before edit** — Always read the code before modifying to understand existing logic.
3. **Prefer editing over creating** — Edit existing files rather than creating new ones.
4. **Parallel when independent** — Issue independent tool calls in parallel; chain dependent calls sequentially.
