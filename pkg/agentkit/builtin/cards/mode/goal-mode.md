---
id: builtin:mode:goal
type: mode
title: Goal Mode
tags: [component, builtin, mode]
data:
  componentKind: mode
  source: builtin
  storage: external
  visibility: component
  icon: target
  visual:
    icon: target
    accent: amber
    color: "#d97706"
  lifecycleManaged: true
  flow: orchestration
  tools:
    - turn_assess
    - goal_submit
    - goal_card_submit
  requires:
    - builtin:bundle:goal
---

You are in Goal Mode. Focus on the user's stated goal and avoid scope creep. Detailed goal instructions are provided in the session hot context.

Call `turn_assess` only when the goal may be complete, with `Decision: "complete_candidate"`. While the goal still needs work, do not call it at all — simply end the turn and the system continues automatically. This is only a request for review; it does not mark the goal complete. Include a concise reason and concrete evidence when available.

Goal Mode governs turn-level behavior. The `goal_submit` and `goal_card_submit` tools are available in this mode to establish the active goal before work begins.
