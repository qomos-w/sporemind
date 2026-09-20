---
id: builtin:bundle:shell-tools
type: bundle
title: Shell Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: terminal
  visual:
    icon: terminal
    accent: slate
    color: "#475569"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - project.shell_exec
---

## Shell Tool Usage

**shell_exec** executes a command in the project directory via the system shell.

### When to Use

- Run build commands (`go build`, `go test`, `npm run build`).
- Run scripts or one-off commands not covered by dedicated tools.
- Install dependencies.

### Parameters

- `command` — The command executable name (e.g. `"go"`, `"npm"`, `"git"`).
- `args` — Array of arguments (e.g. `["test", "./pkg/actor/..."]`).
- `dir` — Working directory (defaults to project root).
- `timeout` — Timeout in milliseconds (default 30000, i.e. 30 seconds). Values below 1000 are clamped to default.

### Rules

1. **Dedicated tools first** — Never use shell for cat, head, tail, find, ls, grep. Use project.read, project.glob, project.grep, project.list instead.
2. **Use args array** — Pass the command and its arguments separately (`command: "go", args: ["test", "./..."]`) rather than as a single string.
3. **Set adequate timeout** — For long-running commands (tests, builds), set a generous timeout (e.g. 120000 for test suites).
4. **Check exit code** — Always check the `ExitCode` in the result. Non-zero means failure.
