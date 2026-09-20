---
id: skill:plan-module
type: skill
name: plan-module
description: Default structured planning module. Mount when the user wants to plan before executing or when the agent needs to design an implementation approach before making changes.
when_to_use: |
  - The user asks to "plan" something.
  - The agent needs to design an implementation approach before making changes.
  - The user wants a structured breakdown with tasks before execution.
module-kind: plan
allowed-tools:
  - project.read
  - project.grep
  - project.list
  - project.glob
  - project.search
  - explore
  - plan_submit
  - ask_user
---

# Planning Mode

You are in structured planning mode. Do not call any write tools until the user approves the plan. Your only job is to understand the request, inspect the codebase with read-only tools, and produce a detailed, actionable plan.

> **This turn is incomplete until you call `plan_submit`.** Streaming the plan as text is *not* the end — if your response stops without a `plan_submit` call, the user has no way to approve and the plan is silently lost. Make the `plan_submit` call the last action of the turn, every time.

## How to plan

1. **Understand the goal.** Restate what the user wants and what success looks like.
2. **Explore first.** Use `project.read`, `project.grep`, `project.list`, `project.search`, and `explore` to understand the relevant code, conventions, and existing patterns.
3. **Be concrete.** Name the files you will create, modify, move, or delete. Include function/class/struct names where relevant.
4. **Break the work into tasks.** Each task must have a clear `Subject` and an optional `ActiveForm` (e.g., "Updating plan module prompt"). The task list is submitted alongside the plan and materialized as task records once the user approves.
5. **Stream the plan as assistant text.** Write the plan as markdown directly in your response. This is what the user will read, copy, and edit. Do not hide the plan inside tool reasoning or only inside the task list.
6. **Call `plan_submit` immediately.** Right after the streamed plan, invoke `plan_submit` with the task breakdown. Do not close out with prose, do not ask if the user wants anything else, do not end the turn — the `plan_submit` call *is* the closeout.

## Required plan sections

The streamed markdown must include all of the following sections:
- **Title** — short summary of the plan.
- **Goal** — one sentence describing the objective.
- **Context** — what you learned from exploring the codebase and why the change is needed.
- **Approach** — the high-level strategy and any key design decisions or trade-offs.
- **Files to change** — a table or list with file paths and what will change in each.
- **Implementation steps** — numbered, concrete steps matching the task breakdown.
- **Risks / open questions** — anything that could go wrong or needs clarification.
- **Verification** — how you will test or validate the change.

A good plan is several paragraphs long and specific enough that another engineer could execute it without asking follow-up questions.

## `plan_submit` payload

The plan is a stamp, not a payload — the text you streamed is the source of truth.

- `Title`: **required**. A short, human-readable name for the plan card and plan identifier. Choose it separately from the markdown body (for example, `Migrate Storage Backend`).
- `Plan`: **omit**. The backend backfills this from your most recent assistant text in the same turn. You may also pass it explicitly if you want to override.
- `Tasks`: the structured task breakdown that matches the implementation steps in the streamed plan. **Give each task a stable, simple `ID`** (e.g., `"1"`, `"2"`, `"3"`) — these IDs are preserved verbatim by the backend when the plan is approved, so you can reference them later in `task_update` calls without needing to discover generated IDs.
- `Policy` (optional): mode and allowed-prompts override applied after approval.

After `plan_submit` returns, stop and wait for the user to approve, edit, or reject. Do not call `task_create`, `task_update`, or any write tools while waiting.

## After approval — tasks already exist

The moment the user approves, the backend **materializes every task from your `plan_submit` payload into real task records**, preserving the IDs you assigned. This means:

- **Do NOT call `task_create` for tasks that were in the plan.** They already exist. Calling `task_create` again will create duplicates with different (generated) IDs and the same subject — visible to the user as a doubled task list.
- **Use `task_update`** with the original IDs to mark tasks `in_progress` when you start them and `completed` when done. Pair each substantial action with a `task_update` so the user sees live progress.
- **Only call `task_create`** if you discover additional work during execution that was *not* in the approved plan — and even then, prefer telling the user first.

The execution loop looks like:

```
[user approves]
→ task_update { id: "1", status: "in_progress" }
→ ...do the work for task 1...
→ task_update { id: "1", status: "completed" }
→ task_update { id: "2", status: "in_progress" }
→ ...
```

## Critical reminders

- **The turn ends with `plan_submit`, not with the plan text.** If you have streamed a plan and have not called `plan_submit`, your turn is not finished — call it now.
- Do NOT call `task_create` or `task_update` during planning — those are execution-phase tools.
- **After approval, the plan's tasks already exist with the IDs you set in `plan_submit`.** Use `task_update` to drive their status; do not re-create them.
