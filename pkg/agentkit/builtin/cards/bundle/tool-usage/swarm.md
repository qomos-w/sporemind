---
id: builtin:bundle:swarm
type: bundle
title: Swarm
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: network
  visual:
    icon: network
    accent: cyan
    color: "#0891b2"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workspace.agent_spawn_swarm
    - workspace.list_agents
    - workspace.agent_send_message
    - workspace.agent_terminate
---

### Swarm

**workspace.agent_spawn_swarm** — Spawn a persistent, card-free sub-agent that runs autonomously like a workflow worker but needs no task card: the child self-assigns your Description/Prompt as its goal, starts its first turn immediately, and stays alive until you terminate it. The child mounts the swarm bundle itself, so it can spawn its own swarm sub-agents (recursion is capped at 3 levels and 8 live children per parent; the spawn error tells you when a cap is hit). Required: Description (short task summary), Prompt (full instructions). Optional: AgentKind (default general; read-only kinds and worker are rejected), MaxTurns, ProjectId. Returns ChildActorId, DisplayName, Depth.

**Operating a swarm**

- Spawn is asynchronous: the call returns the child's AgentActorId immediately. Continue your own work; the child reports back via `workspace.agent_send_message` when its goal is complete (its final message lands in your inbox and wakes you if idle).
- Monitor with `workspace.list_agents` (`ChildrenOnly: true`) — children carry LifecycleScope "swarm" with live status and last activity. Message a child mid-flight with `workspace.agent_send_message` to redirect it.
- Clean up with `workspace.agent_terminate` once a child's result is consumed or it is stuck; children persist until terminated.
- When spawning multiple children, include each child's file ownership in its prompt to prevent parallel conflicts (same contract as fork_general). Prefer spawning code-modifying children only when you operate in a worktree; children without a worktree share the project root with you.
- Delegate recursively: a child at depth < 3 may spawn its own sub-agents for sub-problems, but keep the tree shallow — deeper levels lose parent context and duplicate effort.
