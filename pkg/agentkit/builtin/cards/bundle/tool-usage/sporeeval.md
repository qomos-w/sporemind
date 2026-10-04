---
id: builtin:bundle:sporeeval
type: bundle
title: Sporeeval
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: braces
  visual:
    icon: braces
    accent: violet
    color: "#7c3aed"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workspace.eval
    - workspace.spore_syntax
    - workspace.search_callables
    - workspace.host_call
    - mcp.call_tool
    - appmanager.invoke
---

## Sporeeval

**sporeeval** is the agent's code-invocation bundle: write and evaluate spore script, read the language syntax reference, search the live callable catalog to learn how to invoke host surfaces, and relay calls to host callables, MCP tools, and app callables. Mounting this bundle is the gate — unmounted agents get none of these surfaces.

### When to Use

- Evaluate an ad-hoc spore script snippet (pure computation) and get the result (`workspace.eval`).
- Look up spore language syntax while writing a script (`workspace.spore_syntax`).
- Discover a callable's ID and request shape before invoking it (`workspace.search_callables`).
- Invoke a host service callable by ID (`workspace.host_call`), an MCP server tool (`mcp.call_tool`), or another app's callable (`appmanager.invoke`).

### Tools

- `workspace.eval` — Evaluate a spore script: `Script` (source; must define the `run()` entry function), optional `Args` (positional arguments). Pure computation — no host bindings; fixed 10s / 1M-instruction budget and 64KB output cap. Compile/runtime failures return an `Error` diagnostic you can fix and retry.
- `workspace.spore_syntax` — Return the spore language syntax reference markdown; `Lang`: `"en"` (default) or `"zh"`.
- `workspace.search_callables` — Search the live callable catalog: `Query` (case-insensitive substring on name or description), optional `Limit` (default 200, cap 500). Rows carry request params and service names.
- `workspace.host_call` — Invoke any host actor callable by dotted ID (`<service>.<callable>`), e.g. `"workspace.slashcommands_list"`. Payload must match the target callable's request schema.
- `mcp.call_tool` — Invoke a tool on a configured MCP server: `Id` (server), `Tool` (tool name), `Arguments` (JSON args).
- `appmanager.invoke` — Invoke a registered app's callable: `ID` (app), `Callable`, `Payload`, optional `AgentID`.

### Workflow

1. **Search first** — before invoking an unknown surface, `workspace.search_callables` to find the callable ID and its request fields.
2. **Check syntax** — when writing spore script, `workspace.spore_syntax` is the language reference; `workspace.eval` runs the snippet with no host side effects.
3. **Dedicated tools first** — if a callable is already exposed as a dedicated tool, call the tool; the relay tools are for surfaces not pre-declared on your tool list.

### Rules

1. **Targets enforce their own policy** — every relayed call still passes the target-side gates; sporeeval only grants the reach, not the permission.
2. **eval is sandboxed** — `workspace.eval` binds no host functions; host side effects belong on the relay tools.
