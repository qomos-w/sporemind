---
id: builtin:bundle:coordinator-wearable
type: bundle
title: Coordinator Wearable
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: watch
  visual:
    icon: watch
    accent: emerald
    color: "#10b981"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  devOnly: true
  tools:
    - coordinator_wearable_notify
    - coordinator_wearable_call
---

### Coordinator Wearable

Coordinator-only high-level wearable interaction tools. This bundle is mounted
only on the Coordinator agent kind; no other agent sees these tools, and the
underlying `glass_interact` render/speak/debug internal callables are never
exposed to any LLM.

## Tools

- **coordinator_wearable_notify** — Fire high-level speech and/or display to
  the active Glass session. `Text` is spoken via `glass_interact.speak`
  (at-most-once by `MessageId`); a non-empty `Frame` is rendered via
  `glass_interact.render` (latest-only lastFrame). Requires an active online
  Glass session; returns a clear error when none exists.
- **coordinator_wearable_call** — Request an interaction with the wearable
  user. The `Prompt` is delivered through the high-level speak surface (plus an
  optional rendered `Frame`) and recorded as a bounded pending interaction so
  the eventual reply can be correlated. Returns the pending interaction
  response with an `InteractionId`.

## Do Not

- Do not call `glass_interact.render`, `glass_interact.speak`,
  `glass_interact.debug.*`, or other low-level Glass Interact callables
  directly — they are internal and not part of the LLM tool surface.
- Do not use `coordinator_wearable_call` as a fire-and-forget notification;
  use `coordinator_wearable_notify` when no reply is expected.
