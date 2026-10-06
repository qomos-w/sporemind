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

Agent-local code invocation.

- `eval_script` — run a spore script. Budget: 10s, 1M instructions, 64 host calls, 64KB output.
- `eval_syntax` — language reference. `Lang`: `en` or `zh`.
- `eval_callables` — search the callable catalog. Query full IDs, e.g. "project.read"; name matches rank first.
- `script_save` / `script_read` / `script_delete` — accumulate scripts. Each saved script becomes its own tool, `script-<name>`; its schema comes from the `run()` signature. No compile gate on save. Fix drafts via read → edit → save.

Skeleton:

```spore
import { invoke } from "host"

fun run(path: string): map<string, any> {
    var resp: any = invoke("project.read", { "Path": path })
    return resp as map<string, any>
}
```

Pitfalls — each one costs a round trip:

1. Import by name: `import { invoke } from "host"`. Bare `import host` fails.
2. Declare types: `var m: map<string, any>`. Untyped `var m = ...` fails.
3. `as` accepts named types, not arrays.
4. Reading a missing map key throws. Use try/catch.
5. `invoke` returns the FULL response map. Pull fields by name.

Inside a script, `invoke("eval_callables", ...)` and `invoke("eval_syntax", ...)` call back into yourself. Discover and invoke in one script.

`invoke` carries your caller role; the target's own policy applies. A denial means policy said no. Probe read-only (`*_list`, `*_get`) before mutating. Split jobs that exceed the budget.
