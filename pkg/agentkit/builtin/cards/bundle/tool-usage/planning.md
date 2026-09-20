---
id: builtin:bundle:planning
type: bundle
title: Planning Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: notebook-pen
  visual:
    icon: notebook-pen
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - plan_submit
    - task_create
    - task_update
---

## Planning Tools Usage

Planning tools help structure multi-step work and get user approval before executing complex changes.

### Plan Submission

**plan_submit** — Submit a completed plan for user approval. Before calling it, you must first mount and use the `plan-module` skill via `agent_skill_use` (or use the already-mounted skill body in context). Do not call `plan_submit` before the planning skill is loaded. Always provide non-empty `Title` and complete markdown `Body`; call it once as the final action of the planning turn.

```json
{"Title":"Refactor module loader","Body":"## Goal\n..."}
```

### Task Tracking

- **create_task** — Create a task in the task list. Always provide a non-empty `Subject`.

  ```json
  {"Subject":"Add retry logic"}
  ```

- **update_task** — Update a task's `status` to `pending`, `in_progress`, or `completed`.

### Rules

1. **Track multi-step work** — Use create_task/update_task for tasks with 3+ steps.
2. **Plan before executing** — For complex changes, submit a plan first and wait for approval before making changes.
3. **Update status proactively** — Mark tasks in_progress when you start them, completed when done.
