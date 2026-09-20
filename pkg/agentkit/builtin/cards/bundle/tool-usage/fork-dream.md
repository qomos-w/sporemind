---
id: builtin:bundle:fork-dream
type: bundle
title: Fork Dream
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: moon
  visual:
    icon: moon
    accent: purple
    color: "#9333ea"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: false
  fork:
    toolName: fork_dream
    childKind: dreamer
    description: "Internal: spawn an isolated memory dreamer child to consolidate the memory graph and return sleep instructions."
    maxIterations: 1
---

### Fork Dream

**fork_dream** — Internal system route that spawns an isolated memory dreamer child. The dreamer receives the current memory graph snapshot and returns JSON sleep instructions (merge, promote, connect). Not exposed to user-facing agents.
