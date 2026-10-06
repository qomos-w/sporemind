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

Agent-local code invocation: `eval_script` (run a spore script — fixed budget 10s / 1M instructions / 64 host calls / 64KB output), `eval_syntax` (language reference, `Lang`: en/zh), `eval_callables` (search the catalog; query full IDs like "project.read" — name matches rank first), `script_save` / `script_read` / `script_delete` (accumulate scripts — each saved script is projected as its own tool `script-<name>`, schema from its `run()` signature; no compile gate on save, fix drafts via read → edit → save).

```spore
import { invoke } from "host"

fun run(path: string): map<string, any> {
    var resp: any = invoke("project.read", { "Path": path })
    return resp as map<string, any>
}
```

Pitfalls (each has cost a round trip): `import { invoke } from "host"` — bare `import host` fails; typed vars — `var m: map<string, any>`, not `var m = ...`; `as` casts accept named types, not arrays; reading a missing map key throws — try/catch; `invoke` returns the target's FULL response map — pull fields by name. Inside a script, `invoke("eval_callables", ...)` and `invoke("eval_syntax", ...)` route back to you: discover and call in one script.

`invoke` propagates your caller role through the target's own gates — reach, not permission; a denial means policy said no. Probe read-only (`*_list` / `*_get`) before mutating; split jobs that exceed the budget into inspectable steps.
