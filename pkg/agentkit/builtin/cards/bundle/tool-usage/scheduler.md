---
id: builtin:bundle:scheduler
type: bundle
title: Scheduler Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: timer
  visual:
    icon: timer
    accent: indigo
    color: "#6366f1"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  modeManaged: true
  tools:
    - project.wiki_list_timers
    - project.wiki_toggle_timer
    - project.wiki_trigger_timer_card
---

## Scheduler Tools Usage

Manage the project's timer cards (`scheduler:*`) — the self-contained tasks the project runs on a cron, interval, or one-shot time. These tools inspect and steer what is scheduled; they do not author the schedule itself.

- **wiki_list_timers** — List every scheduled timer card with its schedule, next fire time, last run, run status and schedule type. Read-only. Use it to answer "what is scheduled?" and to find the exact card id before acting on one.
- **wiki_toggle_timer** — Set one scheduled card's `Enabled` flag by id. Disabling preserves the card and its schedule and only stops future fires; re-enable to resume. Use it to pause a recurring task (maintenance window, a task that has gone stale) rather than deleting it.
- **wiki_trigger_timer_card** — Fire one scheduled card once, immediately, without changing its schedule. Use it to run a task on demand or to backfill a missed fire.

### Running inside Scheduler Mode

This bundle mounts automatically with `builtin:mode:scheduler`, so a scheduled run can inspect and adjust the schedule it belongs to. Scheduled runs are unattended and self-contained — no user is present to confirm or redirect — so:

1. **Stay scoped** — do the task body's work only. Do not open unrelated conversations, and do not spawn child agents unless the task body explicitly asks.
2. **Be idempotent** — a fire can repeat or overlap; check whether the work was already done before redoing it.
3. **Record the outcome** — write the result where it is auditable (a project wiki card, or as the task body directs).
4. **Leave a clean state** — commit any changes so the project stays consistent and the next fire starts from a known point.

### Rules

1. **Resolve the card first** — `wiki_toggle_timer` and `wiki_trigger_timer_card` take a `scheduler:*` card id; call `wiki_list_timers` first when the id or the current enabled state is not already known.
2. **Toggle sets a value, it does not flip** — pass the intended `Enabled` state explicitly, not "the opposite of now".
3. **Trigger is a manual one-shot, not a scheduler** — use `wiki_trigger_timer_card` to backfill or run on demand; never call it in a loop or to create recurring work (that is a new `scheduler:*` card).
4. **Do not re-fire yourself** — a scheduled run must not trigger its own card repeatedly as a substitute for the timer.
