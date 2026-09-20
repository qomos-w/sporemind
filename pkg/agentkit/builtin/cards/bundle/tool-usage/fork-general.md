---
id: builtin:bundle:fork-general
type: bundle
title: Fork General
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: git-merge
  visual:
    icon: git-merge
    accent: purple
    color: "#9333ea"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - fork_agent
  fork:
    toolName: fork_general
    childKind: general
    description: "Fork a general-purpose child agent that inherits the full coder tool surface (file edit, shell exec, git, etc.) and current context. Use only for self-contained tasks that can run independently without conflicting with other active work. When spawning multiple children, include each child's file ownership in its prompt to prevent parallel conflicts; a child must not modify files assigned to another child or the main model, and must stop and report if ownership overlaps. Multiple independent fork_general calls may run in parallel; continue the main task while they run, then inspect and verify every child summary and resulting change before considering the work complete. The child runs autonomously in a single turn, reports a summary back, and cannot spawn further children."
    maxIterations: 5
---

### Fork General

**fork_general** — Fork a general-purpose child agent that inherits the full coder tool surface (file edit, shell exec, git, etc.) and current context (environment, skills, recent conversation). Use only for self-contained tasks that can run independently without conflicting with other active work. When spawning multiple children, include each child's file ownership in its prompt to prevent parallel conflicts; a child must not modify files assigned to another child or the main model, and must stop and report if ownership overlaps. Multiple independent fork_general calls may run in parallel while the main task continues. After they finish, inspect each child summary and verify the resulting changes, tests, and integration. The child runs autonomously in a single turn, reports a summary back, cannot spawn further children, and its context is not persisted.

Use fork_general only for self-contained tasks whose files, resources, and outcomes do not conflict with other active work. You may call multiple fork_general tools in parallel for independent tasks and continue the main task while they run. When child agents finish, inspect their summaries and verify their resulting changes, tests, and integration before treating the overall work as complete.
