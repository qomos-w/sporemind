---
id: builtin:bundle:interface-controls
type: bundle
title: Interface Controls
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: search
  visual:
    icon: search
    accent: blue
    color: "#2563eb"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - interfacemanager.control
    - interfacemanager.query_interactions
    - workspace.create
    - workspace.create_agent
---

### Interface Controls

Drive the interface surfaces programmatically, observe what the user does, guide them with on-screen bubbles, and operate UI elements remotely. This covers every real omnibox (Ctrl+K command bar) option plus the UI-guidance loop.

- **interfacemanager.control** — Single callable that drives UI via an `Action` parameter:
  - `set_view` + `Mode` — switch the main content view. Mode is one of: `conversation`, `topology`, `files`, `problems`, `notes`, `ssh`, `multiconsole`.
  - `open_settings` + `Category` — open the settings panel at a category. Category is one of: `general`, `account`, `voice`, `model`, `agent`, `skills`, `prompts`, `frp`, `mcp`, `plugins`, `commands`, `index`, `developer`.
  - `switch_project` + `ProjectId` — switch the active project.
  - `focus_agent` + `AgentId` — open and focus an agent chat.
  - `anchor_catalog` — no parameters; returns the full catalog of addressable UI anchors as GuideAnchor records (`GuideId`, `Area`, `Summary`, `VisibleWhen`, `SuggestedGates`). Call this before composing a `show_guide` tour: it is the whitelist source for step validation, and each anchor's `SuggestedGates` suggests viable `ExpectedInteraction` values for that step.
  - `show_guide` + `Steps` — display a guide overlay: a non-blocking highlight ring around each step's target plus the step card. The overlay never intercepts clicks — the user can interact with anything at any time, and completion is observed via `ExpectedInteraction` gating, not click trapping. Each step is a GuideStep `{TargetGuideId, Title, Body, optional Placement, optional ExpectedInteraction, optional CapabilityKind, optional I18nTitleKey, optional I18nBodyKey}`; TargetGuideId addresses a UI element annotated with `data-guide-id` (e.g. `composer.input`, `sidebar.new-chat`) and is whitelist-validated against the anchor catalog: an unknown TargetGuideId rejects the whole call, and the error message points you to the `anchor_catalog` action to list valid anchors. Targets inside closed containers (dropdown items, settings sub-categories) are only reachable once the container is open — sequence a preceding gated step that has the user open it (see each anchor's `VisibleWhen`). I18nTitleKey/I18nBodyKey allow the frontend to resolve the title/body through i18n (recommended for backend-driven steps). Multi-step tours get prev/next/skip controls automatically.
  - `create_tutorial` + `Title` + `Steps` (optional `TutorialId` / `Description` / `AutoPlay`) — persist an interactive tutorial into the tutorial library (replayed from 设置→关于→教程库, Settings → About → Tutorial Library). Same step contract as `show_guide` (GuideStep with whitelist-validated `TargetGuideId`, max 12 steps), plus `Title` is required. `TutorialId` is an optional slug (`^[a-z0-9]+(-[a-z0-9]+)*$`; reserved words `basics`/`workflow`/`tools`/`settings` rejected; omitted = generated from Title). Upsert by TutorialId; library caps at 32 tutorials. Emits `interface_manager_event` carrying the full TutorialSpec + `AutoPlay` and returns the `TutorialId`; validation failures return `Accepted=false` with a structured `Error` you can self-correct from.
  - `delete_tutorial` + `TutorialId` — remove a tutorial from the library and emit an event carrying the TutorialId. Unknown TutorialId returns `Accepted=false` + `Error` (call `tutorial_catalog` to list valid ids).
  - `tutorial_catalog` — no parameters; returns the persisted tutorials as `Tutorials: TutorialSpec[]` (`TutorialId`, `Title`, `Description`, `Steps`, `CreatedAt`), sorted by TutorialId. Call before `create_tutorial` to avoid duplicating an existing tutorial. No event (same synchronous pattern as `anchor_catalog`).
  - `hide_guide` — dismiss the current guide overlay.
  - `interact` + `GuideId` + `Interaction` — remotely operate an annotated element. Interaction is one of: `click`, `focus`, `input` (requires `Text`; only works on input/textarea/contenteditable), `scroll_into_view`.
  The action fires an `interface_manager_event` the frontend dispatches to the same handlers as a manual omnibox pick. Validation is enforced server-side: pass the parameter matching the chosen `Action`. Only elements carrying a `data-guide-id` annotation can be targeted — this is a hard boundary.
- **interfacemanager.query_interactions** — Read what the user (or your remote actions) did. The frontend reports annotated UI interactions (clicks, value changes, view navigations) into a 500-entry ring buffer. Optional filters: `Since` (RFC3339 timestamp), `GuideId`, `Kind` (one of `click`/`input`/`navigate`/`guide_error`/`remote_ack`/`guide_progress`; `guide_progress` records step/tour progress of an externally driven guide, INCLUDING `target_missing:` detail events that mark normal pacing while a step waits for the user to open a container — these are not failures), `Limit` (default 50, max 200). Returns records newest-first.
- **workspace.create** — create a new project.
- **workspace.create_agent** — create a new agent.

Fire-and-forget with verification: `interfacemanager.control` returns an ack (`Accepted`) and emits the event; the UI change happens asynchronously on the frontend. Every remote `interact` execution and every real guide failure is reported back as an interaction record — after driving the UI, call `query_interactions` with `Kind=remote_ack` or `Kind=guide_error` to confirm the effect instead of assuming success. (A hidden guide target is NOT a failure — it surfaces as `guide_progress` with a `target_missing:` detail; wait, do not nag.)

Typical guidance loop: `query_interactions` to see where the user is stuck → `control` `show_guide` to walk them through the UI → `control` `interact` to perform a step for them when they ask → `query_interactions` to verify.
