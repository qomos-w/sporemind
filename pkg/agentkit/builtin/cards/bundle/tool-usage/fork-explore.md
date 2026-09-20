---
id: builtin:bundle:fork-explore
type: bundle
title: Fork Explore
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: compass
  visual:
    icon: compass
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
    toolName: fork_explore
    childKind: explorer
    description: "Launch a read-only child agent to handle complex exploration tasks. The child receives your description and prompt and recent conversation context, then runs autonomously with filesystem read/search tools, returning a concise summary."
    maxIterations: 5
---

### Explore

**fork_explore** — Launch a read-only child agent to handle complex exploration tasks. The child receives your description and prompt and recent conversation context, then runs autonomously with filesystem read/search tools, returning a concise summary.

You may launch multiple explore children in parallel when tasks involve independent sub-problems or disjoint file areas; each gets its own child agent and they execute concurrently.
