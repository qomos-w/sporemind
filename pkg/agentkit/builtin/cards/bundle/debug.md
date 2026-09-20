---
id: builtin:bundle:debug
type: bundle
title: Debug & Introspection
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: bug
  visual:
    icon: bug
    accent: amber
    color: "#d97706"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workspace.list_agents
---

### Debug & Introspection

Recursive self-improvement and runtime diagnostics for an agent operating on the source code of the system that runs it.

You can diagnose problems or improve the system:
1. Read the relevant code and runtime evidence (logs, diagnostics) before proposing changes.
2. Make minimal, targeted edits and carry each change through implementation, registration, and tests.
3. Verify before reporting done: run tests, check output, or inspect the result.

Introspection tools (mounted with this bundle):
- **get_problems** — fetch system diagnostics (errors, warnings, info events produced during agent turns, tool-call failures, compaction, filesystem, other actor ops). Filter by severity, source, agent, turn, recency.
- **get_system_logs** — fetch recent system logs. Default reads backend Go logs; set `Source="console"` to read persisted frontend (browser/desktop UI) console logs when diagnosing UI/client-side issues.
- **search_services** — discover exposed App-level services (actors registered with `ctx.Expose`).
- **list_callables** — list callable interfaces (exact IDs, params, descriptions) before invoking them.
- **invoke_callable** — invoke any callable by service name + call ID with a JSON payload. Use only for dynamic exploration; prefer well-known tools when available.
- **frontend_debug** — execute JavaScript in the current frontend webview (desktop runtime only) or read page info. Gated by the runtime `debug.EvalJS` hook.
- **inspect_actor** — inspect an actor's runtime details: lifecycle state (created/initialized/started/idle/stopped), business state (for agents), pipeline water level (owner/system/reply queue depth vs capacity), and the count of stuck outbound invocations awaiting response. All reads are non-blocking point-in-time snapshots. Pass an `ActorPath` (ULID, service name, or actor type).
- **capture_profile** — capture a Go runtime pprof profile of the running process and return it as readable text. Profiles: `goroutine` (all goroutine stack traces — diagnose deadlocks, leaks, stuck actors), `heap` (memory allocations — diagnose leaks, large objects), `cpu` (CPU hotspots over a time window; set `Seconds`, default 30), `mutex` (lock contention; requires `runtime.SetMutexProfileFraction > 0`), `block` (blocking on channels/mutexes; requires `runtime.SetBlockProfileRate > 0`), `threadcreate` (OS thread creation). Modes: **Summary** (default) parses and sorts entries by count, showing top `TopN` (default 20) with function frames — compact and actionable. **Diff** (`Diff=true`, named profiles only) captures two snapshots `Seconds` apart (default 10) and returns only entries that changed (NEW/GROWING/SHRINKING) — use this to confirm goroutine leaks or memory growth that a single snapshot cannot reveal. **Raw stacks** (`Debug=2`) returns full WriteTo output. Use goroutine first when diagnosing hangs; use heap for memory growth; use cpu for performance analysis; use Diff=true to detect growth over time.

Mount this bundle on any agent kind (via `DefaultBundleIDs` or runtime `component_mount`) to enable introspection; it is opt-in and not included in any kind by default.
