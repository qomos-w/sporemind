---
id: builtin:bundle:sporecall
type: bundle
title: Sporecall
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: phone-forwarded
  visual:
    icon: phone-forwarded
    accent: violet
    color: "#7c3aed"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workspace.host_call
    - mcp.call_tool
    - appmanager.invoke
---

## Sporecall

**sporecall** is the agent's bridge to invoke host callables at runtime: any host service callable, MCP tools, and app callables. Mounting this bundle is the gate — unmounted agents get none of these surfaces.

### When to Use

- Invoke a host service callable by ID that is not exposed as a dedicated tool (`workspace.host_call`).
- Call an MCP server tool (`mcp.call_tool`).
- Invoke another app's callable (`appmanager.invoke`).

### Tools

- `workspace.host_call` — Invoke any host actor callable by dotted ID (`<service>.<callable>`), e.g. `"workspace.slashcommands_list"`. Payload must match the target callable's request schema.
- `mcp.call_tool` — Invoke a tool on a configured MCP server: `Id` (server), `Tool` (tool name), `Arguments` (JSON args).
- `appmanager.invoke` — Invoke a registered app's callable: `ID` (app), `Callable`, `Payload`, optional `AgentID`.

### Rules

1. **Dedicated tools first** — if a callable is already exposed as a dedicated tool, call the tool; sporecall is for surfaces not pre-declared on your tool list.
2. **Targets enforce their own policy** — every relayed call still passes the target-side gates; sporecall only grants the reach, not the permission.
