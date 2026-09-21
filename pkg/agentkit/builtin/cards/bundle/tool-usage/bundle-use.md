---
id: builtin:bundle:bundle-use
type: bundle
title: Bundle Use
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: package
  visual:
    icon: package
    accent: teal
    color: "#0d9488"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - component_list
    - component_snapshot
    - component_mount
    - component_unmount
    - component_set_enabled
    - project.component_list
    - project.component_get
    - mcp.list_servers
    - mcp.add_server
    - mcp.reconnect
---

### Bundle Use

Bundle discovery and active mounting. This bundle gives an agent the ability to discover what bundles are available in the system, inspect what is currently mounted on itself, mount or unmount bundles at runtime to dynamically expand or narrow its own capabilities, and manage MCP servers — registering new ones system-wide and mounting/unmounting them for itself.

## Retrieval

When a task needs a capability you do not currently have, do not conclude it is unavailable — retrieve the catalog first:

1. **Ask yourself**: does the current tool surface cover the task? Check `component_snapshot` if unsure what you have.
2. **Retrieve the catalog**: call `project.component_list`. Scan the returned descriptors by title, kind (`bundle`/`mode`/`skill`/`prompt`), and tool lists for anything matching the missing capability (e.g. browser control, image generation, ssh, git). The catalog is small — a full scan is cheap and correct; there is no keyword filter, so match by reading titles and tool IDs.
3. **Inspect candidates**: call `project.component_get` with the candidate cardId to read its full tool list and `requires` dependencies before mounting.
4. **Mount and verify**: `component_mount`, then `component_snapshot` to confirm the new tools are live.

Retrieval rules:

- Retrieve **before** reporting "I don't have that tool" — the user may have installed a bundle that provides it.
- Retrieve when the user mentions a capability by name ("search the web", "take a screenshot", "run a skill") but no matching tool is visible.
- Do not mount eagerly. Mount only when the task at hand needs the capability; unmount or disable when the work is done (`component_set_enabled` keeps the mount but silences it).
- Never mount the same bundle twice; check `component_list` for an existing mount first.

## Discovery

A live status section ("Currently mounted components" + "Available bundles") is injected into your system prompt at the start of every turn while this bundle is mounted: every mount is listed with its scope and a LOCKED marker when it cannot be unmounted at runtime (`kind-config` scope = pinned by the agent kind config file; protected builtin = system bundle). Prefer reading that section over calling `component_list` for a quick picture of what is active and what is available; it is compiled at turn start, so mounts you perform mid-turn appear there on the next turn (your tool surface, however, refreshes immediately).

Two layers of discovery are available:

**Self-mounted discovery** — inspect what is currently mounted on this agent:
- **component_list** — List all component cards currently mounted on this agent, including their enabled state, scope (builtin / user / dependency), and kind. Use this to understand which bundles and tools are active right now.
- **component_snapshot** — Get the fully resolved component snapshot: the flat list of mounted cards plus the prompts and tools they contribute after dependency resolution and deduplication. This shows the effective tool surface and prompt context the agent operates with.

**System-wide discovery** — discover all bundles that exist and can be mounted:
- **project.component_list** — List every component descriptor available in the project catalog (builtin cards, persisted project cards, and external provider cards). Each descriptor includes its ref (cardId, kind), title, icon, tools, declared dependencies, and ShadowedById (non-empty when a persisted card overrides an external/builtin card of the same ID). Use this to find bundles by kind or title before mounting.
- **project.component_get** — Get a single component descriptor by cardId. Use it to inspect a specific bundle's tools and dependencies before deciding to mount it.

## MCP Servers

MCP (Model Context Protocol) servers extend your tool surface. Each configured server exists system-wide and is mountable per-agent as a bundle card with ID `mcp:<server-id>` (e.g. `mcp:srv-0`).

**Discover** — call `mcp.list_servers` to see every configured server with its transport, enabled flag, and live status (connected, toolCount, error). MCP cards also appear in `project.component_list` alongside other bundles.

**Add to the system** — when the user asks to use an MCP server that is not configured yet, register it with `mcp.add_server`. Transport is either stdio (`Stdio: {Command, Args, Env}`) or http (`Http: {Url, Headers}`); set `Enabled: true` so the server auto-connects. Only add a server the user explicitly asked for. Secrets in `Stdio.Env` / `Http.Headers` are write-only: they are accepted once and never returned by any callable.

**Add to yourself (mount)** — `component_mount` the server's `mcp:<server-id>` card. The server must be connected for its tools to appear (check `mcp.list_servers` status); a freshly added server connects asynchronously, so its tools may only materialize on the next turn. Mounted MCP tools appear as `mcp-<server>-<tool>`. While any `mcp:<server-id>` card is mounted, a "Mounted MCP Servers" live-status section is injected into your system prompt each turn listing every mounted server's connected/toolCount/error state.

**Reconnect (self-heal)** — when a mounted server shows `DISCONNECTED` in that section, or its tools error out mid-task, call `mcp.reconnect` with the server's `Id`. It force-tears-down any wedged session, resets the reconnect budget, and establishes a fresh connection; the server's tools re-enter your surface once it is connected again. A failed reconnect keeps retrying in the background with backoff.

**Remove from yourself (unmount)** — `component_unmount` the `mcp:<server-id>` card; the server's tools drop out of your surface immediately. Unmounting does NOT remove the server from the system — the config stays registered for other agents (system-level removal stays a human UI action).

## Active Mounting

- **component_mount** — Mount a component card on this agent by cardId. Dependencies declared in the card's `requires` list are auto-mounted transitively. Optional `Enabled` (default true), `Order` (mount order), and `Scope` (default "user"; "builtin" for kind defaults, "dependency" for auto-mounted deps).
- **component_unmount** — Unmount a component card by cardId. Protected cards with `builtin` scope cannot be unmounted. Use this to shed capabilities you no longer need.
- **component_set_enabled** — Toggle a mounted component's enabled state without removing the mount. A disabled card stays in the mount list but contributes no tools or prompts. Use this to temporarily suppress a bundle.

## Usage

Mount this bundle on any agent kind (via `DefaultBundleIDs` or runtime `component_mount`) to enable bundle self-management; it is opt-in and not included in any kind by default.