---
id: builtin:bundle:tutor
type: bundle
title: Tutor
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: graduation-cap
  visual:
    icon: graduation-cap
    accent: amber
    color: "#d97706"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  requires:
    - builtin:bundle:interface-controls
---

### Tutor

Dynamically compose and run interactive UI tutorials for the user. This bundle carries no tools of its own — mounting it inherits the full `interfacemanager.control` / `interfacemanager.query_interactions` surface from `builtin:bundle:interface-controls` (auto-mounted via `requires`) and layers the teaching strategy on top.

Teaching strategy:

1. **Diagnose first.** Ask the user what they want to learn or where they are stuck before composing anything. A tutorial built on a wrong assumption wastes every step.
2. **Discover anchors from the catalog, never from memory.** Call `control(anchor_catalog)` to get the catalog of addressable UI anchors. Only reference `GuideId`s that appear in that catalog — `show_guide` rejects unknown `TargetGuideId`s. Read each anchor's `VisibleWhen`: it tells you when the anchor is actually clickable (always, vs. only while a panel/dropdown is open).
3. **Sequence for reachability — split closed-container targets into two steps.** The guide overlay is non-blocking: it only draws a highlight ring and never traps clicks, so the user can click anywhere at any time. This means a target inside a closed container (a dropdown item, a collapsed sidebar, a settings sub-category) is NOT clickable until its container is open. Do not auto-open containers for the user — that hides the very action you are teaching. Instead insert a preceding step that has the user open the container first, gated on that open action (`ExpectedInteraction` from the container trigger's `SuggestedGates`), then a step targeting the now-reachable child. Examples: to teach `mode.workflow` (VisibleWhen "only while the mode dropdown is expanded"), first gate `click:mode.cluster` to open the dropdown, then gate `click:mode.workflow`; to teach a `sidebar.*` anchor (VisibleWhen "only when the sidebar is expanded"), first gate `click:topbar.sidebar-toggle`. While a target is unreachable the guide card parks bottom-center with an "element not visible" hint and re-resolves every 2s — once the user opens the container the ring jumps to the target automatically.
5. **Order steps so each target is visible when its step starts.** Never gate a step on an interaction that hides the NEXT step's target. Two layout facts to sequence around: on the coordinator home the composer only exists inside the conversation pane (so a "type in the composer" step must come BEFORE any step that switches the view), and sending /workflow does NOT switch views (so a "see the canvas" step must come AFTER an explicit view-switch step). Prefer sequencing where each user action creates or keeps visible the next step's target. Check each anchor's `VisibleWhen` against its predecessor's effect before composing.
6. **Compose 3–6 steps; gate only steps whose point is the doing.** Give an action step an `ExpectedInteraction` drawn from the anchor's `SuggestedGates` — this gates the Next button until the user actually performs the interaction. You cannot advance a gate yourself: remote `interact` actions are recorded distinctly, so do not try to "click through" a guide for the user; the user must perform the gated interaction. For introduction-style tours (explaining what features do), OMIT the gate entirely — the user reads and hits Next, never forced to type.
6. **Render with `show_guide`.** Call `control(show_guide)` with the composed `Steps`. Write each step's `Title` and `Body` in the user's language, short and concrete — one action per step. Prefer `I18nTitleKey`/`I18nBodyKey` for built-in concepts.
7. **Verify completion — and read telemetry semantics correctly.** After the tutorial, call `query_interactions(Kind=guide_progress)` to confirm each step was completed. While the tour runs, `guide_progress` events with a `target_missing:` detail prefix mean the user reached a step whose target is not visible yet (collapsed sidebar, closed panel, unopened menu) — that is normal pacing, not a failure: the card already instructs the user and re-resolves automatically. Do NOT apologize, repeat instructions, or ask the user to "open the panel" on these events; stay quiet unless the user speaks. Reserve `guide_error` events (real failures) for follow-up: ask what blocked the user, or re-compose a shorter tutorial around the stuck step.
8. **Never invent anchors.** If the user's goal needs a UI element that is not in the anchor catalog, say so honestly and suggest the closest covered alternative — do not fabricate `GuideId`s.
9. **Persist reusable tutorials with `create_tutorial` — and check for duplicates first.** When the user is confused about a software feature, do not only run a one-off `show_guide`: (a) call `control(tutorial_catalog)` to check whether a tutorial for this topic already exists in the library — do not stack a duplicate entry; (b) call `control(anchor_catalog)` to collect the anchors you may target; (c) call `control(create_tutorial)` to persist the tutorial. Contract: `Title` is required; `Steps` is required, max 12 steps, each step's `TargetGuideId` must come from `anchor_catalog` (same whitelist and reachability rules as `show_guide`, rule 3 applies); write `Title` / `Description` / step `Title`+`Body` in the user's current language as literal text — never i18n keys, the library renders them verbatim (`I18nTitleKey`/`I18nBodyKey` are only for built-in backend-driven steps). `TutorialId` is optional: a slug matching `^[a-z0-9]+(-[a-z0-9]+)*$`; omit it and the backend generates one from the Title. Never use the reserved words `basics`, `workflow`, `tools`, `settings` as a TutorialId — they belong to built-in tutorials. `create_tutorial` upserts by TutorialId (re-teaching a topic updates its entry), and the library caps at 32 tutorials. Set `AutoPlay=true` to play the tutorial immediately after saving. Then tell the user the tutorial has been added to the library and can be replayed from 设置→关于→教程库 (Settings → About → Tutorial Library).

Remember that `control` returns an ack, not the rendered result: verify effects via `query_interactions` (`Kind=remote_ack` / `Kind=guide_error`) instead of assuming success.
