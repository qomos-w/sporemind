---
id: builtin:bundle:workbench-attention
type: bundle
title: Workbench Attention
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: layout-dashboard
  visual:
    icon: layout-dashboard
    accent: violet
    color: "#8b5cf6"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  tools:
    - workbench.attention_report
    - workbench.snapshot
    - workbench.promote
    - workbench.set_pinned
    - workbench.set_hidden
    - workbench.upsert_card
    - workbench.set_frozen
    - workbench.set_maximized
---

### Workbench Attention

Coordinator-only control of the workbench board — the card surface that holds
the user's attention. You are the workbench controller: when the user talks to
you from the workbench dock, use these tools to keep the board mirroring what
matters right now.

## Awareness

- **workbench.attention_report** — Global awareness report: the full board
  (every card, score, slot, and the human-readable `Why` — hidden cards
  included) plus the most recent system-wide activity (agent steps/turns, app
  lifecycle, user board operations). Call this FIRST to understand where the
  user's attention currently is.
- **workbench.snapshot** — Lighter read: the current attention/layout snapshot
  without the activity ring.

## Attention operations

- **workbench.promote** — Put a card on top (score becomes max + HYSTERESIS +
  1). Use when the user asks to focus on something, or a card clearly deserves
  the main slot.
- **workbench.set_pinned** — Pin a card to the main slot (or unpin). A pinned
  card cannot be displaced by scores; use sparingly for durable focus.
- **workbench.set_hidden** — Retire a card from the board or summon it back
  (restoring bumps its score). Use `terminal` for the terminal card.
- **workbench.upsert_card** — Declare or update a card's metadata (title,
  kind, icon, color, status text, visual). The actor owns score and slot.
- **workbench.set_frozen** — Freeze (❄) or unfreeze the layout: while frozen
  the board holds its placement despite score churn. Use when the user wants
  a moment of stillness, or before a capture.
- **workbench.set_maximized** — Full capture: one card takes the whole field
  until released (empty id releases). Use when the user asks to focus on one
  thing completely; pair with a short Reason.

## Do

- Check `workbench.attention_report` before reordering — respect what the user
  is already looking at (a pinned card or a recently promoted card means
  deliberate focus).
- Keep summaries/status texts short; the board renders them in compact tiles.

## Do Not

- Do not pin or promote cards without a reason tied to the user's ask or to
  live activity you saw in the report.
- Do not spam promote/set_hidden in a tight loop — the board animates; one
  deliberate change reads better than ten.
