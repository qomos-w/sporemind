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
    - eval_script
    - eval_syntax
    - eval_callables
    - script_save
    - script_delete
    - script_read
---

## Sporeeval

**sporeeval** is the agent's code-invocation bundle, fully agent-local: write spore script that computes and reaches host callables through `host.invoke`, consult the language syntax reference, and search the callable catalog for IDs and request shapes. Mounting this bundle is the gate — unmounted agents get none of these surfaces.

### When to Use

- Do a deterministic job — compute, transform, batch-operate host callables — by writing one spore script and running it (`eval_script`).
- Look up spore language syntax while writing a script (`eval_syntax`).
- Discover a callable's ID and request shape before writing the `host.invoke` call (`eval_callables`).

### Tools

- `eval_script` — Evaluate a spore script on this agent: `Script` (source; must define the `run()` entry function), optional `Args` (positional). Inside the script, `invoke("<service>.<callable>", payload)` (from the `host` module) reaches any host callable — the caller role propagates and the target's own policy applies (reach, not permission). Fixed budget: 10s, 1M instructions, 64 host calls, 64KB output. Compile/runtime failures return an `Error` diagnostic you can fix and retry.
- `eval_syntax` — Return the spore language syntax reference markdown; `Lang`: `"en"` (default) or `"zh"`.
- `eval_callables` — Search the callable catalog the agent can see: `Query` (case-insensitive substring on name or description; name matches rank first — query full IDs like "project.read" for precision), optional `Limit` (default 200, cap 500). Rows carry request params and service names. Discovery only — it never invokes. Agent-local callables (eval_* / script_*) appear without a service name and are invocable from scripts via the same `invoke`.
- `script_save` — Save (or overwrite) a reusable spore script under a stable `Name`: `Script` (source), optional `Description`. Each saved script is projected as its own tool (`script-<name>`, typed schema derived from the `run()` signature) and listed as an index line in hot context; saving an existing Name overwrites it. No compile gate — broken drafts can be saved and fixed via read → edit → save.
- `script_delete` — Delete a saved script by `Name`; deleting an unknown Name is an error. The projected tool disappears with it.
- `script_read` — Fetch a saved script's full source by `Name`. The edit path: read → modify → save.
- **`script-<name>` (projected)** — every saved script is directly callable as its own tool with typed parameters (from its `run()` signature); execution runs the stored body under the same eval budget and host-bridge semantics. The tool list follows the saved set live — save mid-turn and the tool is there.

### Workflow

1. **Search first** — `eval_callables` to find the callable IDs and their request fields; inside a script, `invoke("eval_callables", {...})` routes back to the agent itself, so discovery and invocation can share one eval_script.
2. **Check syntax** — `eval_syntax` is the language reference; write the script, `eval_script` it, read the diagnostics, iterate.
3. **Batch work into scripts** — a chain of host calls, loops, and data shaping belongs in ONE script, not in a dozen tool calls: fewer round trips, exact logic, inspectable result.
4. **Accumulate** — a script you have rewritten twice is worth `script_save`-ing: it becomes its own tool (`script-<name>`, typed params, grab-and-run) plus an index line in context. Edit via `script_read` → modify → save; delete with `script_delete` when it stops earning its tokens.

### Rules

1. **Targets enforce their own policy** — every `host.invoke` propagates your caller role and passes the target's own gates; a denial means the policy said no, not that you failed to reach it.
2. **Budget your reach** — one eval_script allows at most 64 host calls and 10s total; a bigger job should be split into steps you can inspect between.
3. **Read-only probes first** — when unsure of a callable's payload shape, call a `*_list` / `*_get` variant before mutating calls.

