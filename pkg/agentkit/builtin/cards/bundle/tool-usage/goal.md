---
id: builtin:bundle:goal
type: bundle
title: Goal Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: flag
  visual:
    icon: flag
    accent: amber
    color: "#d97706"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  modeManaged: true
---

## Goal Tools Usage

**goal_submit** — Submit your interpretation of the active goal for user confirmation. This tool is available only while Goal Mode is active.

**goal_card_submit** — Submit an existing task-card binding (`CardId`) for user confirmation. On approval the current agent claims that existing task card, binds itself to it, and starts executing its card body as a bound task — the same initialization as a workspace assign-goal. Use it only when the user's intent matches an existing actionable task card; use `goal_submit` for ordinary session-level goals or when no matching card exists.

- Call `goal_submit` once you understand the user's intent, with `InterpretedGoal` describing the target end-state and acceptance criteria — not implementation steps.
- Before `goal_card_submit`, search existing task cards with project wiki tools. Give the matching card's exact `CardId`; `InterpretedGoal` is optional. Never create a card through this tool.
- When working on a bound task card, finish its necessary child tasks and call `project.wiki_set_status` with status `done` for the current task card before completion assessment.
- Call exactly one of `goal_submit` / `goal_card_submit` — never both.
- The user confirms or revises the proposal before work proceeds.
- These tools are available only while Goal Mode is active.
