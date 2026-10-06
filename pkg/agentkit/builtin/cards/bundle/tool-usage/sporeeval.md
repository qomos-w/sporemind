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
- `eval_callables` — Search the callable catalog the agent can see: `Query` (case-insensitive substring on name or description), optional `Limit` (default 200, cap 500). Rows carry request params and service names. Discovery only — it never invokes.

### Workflow

1. **Search first** — `eval_callables` to find the callable IDs and their request fields.
2. **Check syntax** — `eval_syntax` is the language reference; write the script, `eval` it, read the diagnostics, iterate.
3. **Batch work into scripts** — a chain of host calls, loops, and data shaping belongs in ONE script, not in a dozen tool calls: fewer round trips, exact logic, inspectable result.

### Rules

1. **Targets enforce their own policy** — every `host.invoke` propagates your caller role and passes the target's own gates; a denial means the policy said no, not that you failed to reach it.
2. **Budget your reach** — one eval_script allows at most 64 host calls and 10s total; a bigger job should be split into steps you can inspect between.
3. **Read-only probes first** — when unsure of a callable's payload shape, call a `*_list` / `*_get` variant before mutating calls.
