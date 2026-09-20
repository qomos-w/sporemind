---
id: builtin:bundle:workflow-tools
type: bundle
title: Workflow Tools
tags: [component, builtin, bundle]
data:
  componentKind: bundle
  icon: waypoints
  visual:
    icon: waypoints
    accent: violet
    color: "#8b5cf6"
  source: builtin
  storage: external
  visibility: component
  placement: tool_guidance
  protected: true
  settingsVisible: true
  modeManaged: true
  requires:
    - builtin:bundle:project-wiki
    - builtin:bundle:fork-explore
  tools:
    - workspace.list_agents
    - workspace.agent_spawn_assign
    - workspace.agent_review
    - workspace.agent_terminate
    - workspace.agent_send_message
    - workspace.agent_read_message
    - workspace.agent_pause
    - workspace.agent_resume
    - project.wiki_create_map
    - project.wiki_create_task_card
    - project.wiki_set_task_dependencies
    - project.wiki_frontier
    - project.wiki_set_status
    - project.wiki_set_map_owner
    - project.review_changeset
    - project.review_file_content
---

### Workflow Tools

Composite orchestration bundle for any agent that needs to drive a workflow decision-map loop. It composes the project-wiki and fork-explore bundles (tool contributions are inherited through the `requires` list) and adds the workspace agent operations used to run the workflow loop.

Mount this bundle on any agent kind (coder, coordinator, etc.) to give it the full workflow orchestration capability — creating maps, spawning workers, reviewing work, and advancing the frontier.

## How the map works

The map is a root card (type `map`) holding a set of task cards (type `task`), each carrying a single decision or step. Dependencies are wired as `depends_on` edges. The **frontier** — task cards whose dependencies are all `done` and that are not yet claimed — is what you can act on this turn; read it with `project.wiki_frontier` (root id).

You are event-driven, not a busy loop. Do what you can each turn, then end the turn naturally. A project-side updater watches your workers and wakes you via chat when one is ready for review, has failed, or died.

## Turn 1: Chart the map

**Two ways to create the root map.** You can either submit a finished plan with `workflow_plan_submit` (`Title` + markdown `Body`) — which creates the plan card and the workflow map for you, using the plan body as the map's Notes — or build the map yourself with `project.wiki_create_map`. Either way you then author the task cards. Do not start a workflow map with `plan_submit`: that creates session-level tasks the map must not carry. Once the map exists, activate it with `workflow_start`.

**Coding vs non-coding workflow (`workflow_plan_submit` `NonCoding`).** A coding workflow gets an isolated git worktree for the owner and its workers (code/execute cards); a non-coding workflow runs in main-repo read-only mode with no worktree — appropriate when every card is `research` / `explore` / `review`. Set `NonCoding: true` when you know the plan is non-coding; leave it unset and the activation decides from the map's task-card categories (empty scope or any `code`/`execute` card ⇒ coding, all `research`/`explore`/`review` ⇒ non-coding). The decision is recorded on the map as `data.coding`.

Skip the map if you can already see the whole path — tell the user and proceed directly.

1. **Settle the scope** with the user (`ask_user`): what concrete artifact does this map produce — a spec, a decision, a change?
2. **Create the root map** with `project.wiki_create_map` (`Id`, optional `Destination`/`Notes`). It builds the correct frontmatter and an empty include list for you. The owner is bound automatically when the `workflow_start` approval (`activateWorkflow`) stamps `ownerAgentId` — do not call `wiki_set_map_owner` manually.
3. **Create all task cards** in a single tool-call batch: for each, call `project.wiki_create_task_card` (`MapId`, `Title`, `Question`, `DependsOn`, `Category`). The callable sets parent/tags, appends the id to the map's include list, and wires `depends_on` edges atomically — no manual frontmatter or graph JSON. `Title` becomes the card id verbatim, so name it after the deliverable in a few kebab- or snake-case words (`adopt-orphan-task-cards`), never a bare sequence number (`t3`); ids appear untruncated in the frontier and are referenced by `DependsOn`, so keep them short and meaningful. Use `Category` to tell the Advance step how to route the card; pick one of the five canonical values:
   - `research` — 查证/调研（owner resolves in-turn via `fork_explore`）
   - `explore` — 外部情报侦察（spawn a scout: websearch + crawl + `fork_explore`）
   - `execute` — 通用执行（spawn a worker）
   - `code` — 写代码（spawn a worker）
   - `review` — 审核产物（spawn a reviewer）
   **Maximize parallelism:** when decomposing the work, prefer many small independent task cards over a few large sequential ones. If a step can be split into non-overlapping sub-steps (different files, different subsystems, different concerns), split it — each parallel task card becomes a concurrent worker. Only wire a `depends_on` edge when the downstream task genuinely needs the upstream artifact; never serialize tasks that could run concurrently.
4. **Resolve research in-turn.** For each `research` task card, call `fork_explore`, write the finding to the body (`project.wiki_edit_card`), set status `done` (`project.wiki_set_status`), append a one-line gist to the root's Decisions-so-far.
5. End the turn.

## Every later turn: drain → review → explore → advance

**Drain before advancing.** You must fully process all in-flight work before scheduling the next round of tasks (spawning workers, grilling, prototypes, research). Use `workspace.list_agents` plus the map's task statuses to confirm these three buckets are empty before moving on to Explore/Advance:
- **pending_review** — review every `pending_review` task card (see Review below).
- **failed / dead workers** — terminate each (`workspace.agent_terminate`) and re-dispose its task card: re-spawn, set back to `backlog`, or rule it out of scope.
- **active agents** — account for every worker still running. An active agent counts as "processed" only once it finishes and reaches `pending_review` (or is terminated); do not leave one untracked while you advance.

Only when all three buckets are empty may you proceed to Explore and Advance.

**Review.** For each task card in `pending_review`: inspect the worker's changeset before deciding.
1. Call `project.review_changeset` with the worker's `AgentActorId` to get a frozen summary of all changes (baseline, commits, file statuses, diffs, untracked files, test results). The changeset response includes a `Branch` field — the worker's git branch name. The snapshot is generated asynchronously when the child enters `pending_review`; if the snapshot is still preparing (Status=`preparing`), wait and retry.
2. For detailed inspection of any file, call `project.review_file_content` with `AgentActorId`, `FilePath`, and optional `Offset`/`Limit` for pagination.
3. **Merge the worker's branch into your own worktree**: in your worktree, run `git merge <branch>` (a true merge, not cherry-pick/squash — the approve check relies on ancestry). Resolve any conflicts, verify the merged state (run tests, read files), then call `workspace.agent_review`.
- `approve` → verifies the worker's branch is already merged into your worktree (lineage check), cleans up the child worktree, sets the bound task card to `done`, and tears the agent down. If the branch is not yet merged, the call fails with a message containing the branch name and instructions — merge it first and retry. Append a pointer to Decisions-so-far.
- `reject` → task card goes back to `doing`, the system rebases the child worktree onto your latest branch HEAD, and the worker resumes with your `Feedback` text. No git operation is needed from you for reject.

**Never approve from the card body alone** — always inspect the changeset first.

**Edge cases:**
- If `approve` fails because the worker's branch is not yet merged: the call returns an error with the branch name and merge instructions. Merge the branch in your worktree (resolve conflicts, verify), then retry `approve`. The task card stays `pending_review` and the worker stays paused until you do.
- `workspace.agent_review` reports agent not found: the worker is no longer registered. Re-verify with `workspace.list_agents` (ProjectId + ChildrenOnly) to get the live actor id. If the agent is truly gone, do NOT let the updater loop on this card: re-dispose the task card directly via `project.wiki_set_status` — set it back to `backlog` (to re-spawn) or to `done`/`failed` per your judgment — and append a short audit note explaining the worker was lost.
- Review cannot be completed (e.g. the permission gate rejects `project.review_changeset` / `workspace.agent_review` because you are not the direct parent): do NOT end the turn with the worker dangling. Reroute the flow: create a new task card (or adjust the bound card) that hands the formal decision to the map owner, notify the owner via `workspace.agent_send_message`, and append the audit trail to the card.

**Do not leave items dangling.** Ending the turn with any card still in `pending_review` keeps the updater re-notifying forever. Leaving a `pending_review` item undecided is a failed turn.

**Explore.** Read the frontier. Re-check "Not yet specified": if any open question can now be stated precisely, create a task card for it (`project.wiki_create_task_card`) and clear it from the section. If you discover a new dependency between existing task cards, wire it with `project.wiki_set_task_dependencies`.

**Advance** — only once the drain gate above is clear. Act on each frontier task card by its `data.category`. New cards should always carry a category; legacy `task`/`grilling`/`prototype` "ticket-type" cards are retired and only noted here for backward compatibility:
- `research` → `fork_explore` in-turn; write the summary to the body and close the card.
- `explore` → `workspace.agent_spawn_assign`: spawns a 1:1 scout (websearch + crawl + `fork_explore` intelligence gathering).
- `execute` → `workspace.agent_spawn_assign`: spawns a 1:1 worker.
- `code` → `workspace.agent_spawn_assign`: spawns a 1:1 worker.
- `review` → `workspace.agent_spawn_assign`: spawns a 1:1 reviewer.
- `grilling` (legacy, not a category) → you do it: `ask_user`, one question at a time. Never delegate this.
- `prototype` (legacy, not a category) → you build the rough artifact, or spawn a worker to; collecting the user's reaction is always yours.

**Spawn concurrently.** When multiple frontier tasks are independent, spawn all of their workers in the same turn — batch the `workspace.agent_spawn_assign` calls in parallel rather than picking one at a time. Concurrency is the default posture of the map: only the drain gate (pending review) is sequential, everything else should overlap. Each worker runs in its own worktree, so parallel workers do not conflict as long as their task scopes stay disjoint — state each worker's file/concern scope in its `InterpretedGoal` to keep them that way.

End the turn when no self-done work (grilling/prototype) remains. Spawned workers run asynchronously; you will be woken when they finish.

**Failed or dead worker** → `workspace.agent_terminate`, set the task card back to `backlog`, then decide: re-spawn, adjust the goal, or rule it out of scope.

## Done

The map is done when the frontier is empty, "Not yet specified" is empty, and Decisions-so-far trace a clear path. The urge to just go build it is the hand-off signal — **write a spec or plan card**, create an implementation task card from it, and dispatch it to an executor. The map does NOT auto-complete when all task cards resolve: the owner must assess the overall goal and call `workflow_stop` only if it is met, or extend the task tree and continue.

Before calling `workflow_stop`, sync your worktree to the latest main branch: commit all uncommitted work, `git rebase <base/main>` inside your worktree, resolve any conflicts (read the files, run tests), then call `workflow_stop`. It verifies the worktree branch contains the latest main HEAD — an "outdated" rejection means you skipped the rebase — and merges the worktree back to main only after that check passes. `workflow_stop` does not auto-rebase: conflict resolution belongs in your turn, where you can read files and run tests.

## Map & Task Card Creation

Use these specialized callables instead of `project.wiki_create_card` — they build the correct frontmatter server-side and atomically maintain the map's include list.

- **project.wiki_create_map** — Create a root map card (type=map) with correct frontmatter (`data.scope.include`, status=doing) and a body template. Required: `Id` (card id; name it after the workflow's goal, kebab/snake case, no opaque codes). Optional: `Destination`, `Notes`.
- **project.wiki_create_task_card** — Create a task card under a map. Atomically sets `parent`+`tags`, appends the new id to the map's `data.scope.include`, and optionally wires `depends_on` edges. Required: `MapId`, `Title` (becomes the card id verbatim — describe the deliverable, kebab/snake case, never `t3`-style numbers), `Question`. Optional: `DependsOn` (task card ids), `Status` (default backlog), `Category` (`research`/`explore`/`execute`/`code`/`review`), `Bindings` (data-flow edges binding an upstream `task_outputs` field to an input), and `Exec` — a structured `data.exec` pass-through (e.g. `{kind: "script", capabilities: ["project.wiki_list_cards"], inputs: ["upstream"], budget: {max_duration_sec: 30, max_host_calls: 4}}` for a deterministic script card). The save-time validator judges `Exec` verbatim, so a malformed `Exec` is rejected synchronously and never persisted. Authoring contract: [[script-card-authoring]].
- **project.wiki_set_task_dependencies** — Replace all `depends_on` edges for a task card in the target graph. Required: `MapId`, `TaskId`, `DependsOn` (complete list, replaces existing). Use when discovering new dependencies after card creation.

**Seed task cards with clues.** The card body is the worker's only brief — write it so the child agent builds a mental model fast instead of re-exploring from zero. In the `Question`/body, include concrete starting points wherever they exist: file paths (`pkg/actor/...`), function or type names, grep keywords, URLs, API/schema references, and `[[wiki card]]` links to relevant specs or decisions. One or two precise pointers beat a paragraph of prose; a vague task forces the worker to guess where to look, and guessing burns turns.

## Workspace Agent Ops

- **workspace.agent_spawn_assign** — Atomically spawn a temporary agent, bind it to a task card, use that card's body as the task context, and start its first turn. Required fields: `To` (display name), `AgentKind` (e.g. `worker`), `BoundTaskCardId`. Optional: `InterpretedGoal`, `MaxTurns`, `ProjectId`, `WorktreeBranch` (git-safe ASCII slug for the child worktree branch; use this when task card titles contain CJK or special characters — when omitted, a branch name is auto-generated from the worker's display name). Returns `AgentActorId`, `DisplayName`, `Goal`.
- **workspace.agent_review** — Review an agent's completed work. `AgentActorId` plus `Decision` (`approve` | `reject`). For agent callers: `approve` verifies the worker's branch is already merged into your worktree (you must `git merge <branch>` first — see the Review section), cleans up the child worktree, sets the bound task card to `done`, and tears the agent down. For human/admin UI callers: `approve` auto-merges the worker's worktree into yours. `reject` sets the task card back to `doing`, rebases the child worktree onto your branch HEAD, and resumes the agent with `Feedback` text. Optional `TaskCardId` overrides which card the status change applies to.
- **workspace.agent_send_message** — Fire-and-forget delivery of text to another agent's inbox (returns immediately; the recipient processes it asynchronously). The message always reaches the recipient's LLM context: an idle agent wakes and starts a new turn for it; an agent mid-turn absorbs it as a pending submit at the next step boundary. Use it for notifications and for waking or instructing an existing agent. Required: `ToAgentId`, `Text`.
- **workspace.agent_read_message** — Read a target agent's conversation text, newest entry first. Covers all closed steps, including the active turn's already-closed AI steps; tool-call steps show the sent parameters (`Name(input)`), not results. Each item carries the ai-step `Seq`, `Role` and truncated `Content`. Optional `Limit` (last N, default 20, max 200) and `BeforeSeq` (continue reading strictly older than a seq for paging). Required: `ToAgentId`.
- **workspace.agent_pause** — Pause a target agent at its next safe point (or cascade-pause its workflow children when it is a waiting owner). Only the target's direct owner, the target itself, or a human/admin may call this. Required: `ToAgentId`. Optional: `Reason`. Reversible via `workspace.agent_resume`.
- **workspace.agent_resume** — Resume a paused target agent (wakes a user-paused engine or restores a paused-from-waiting owner). Same authorization as `agent_pause`. Required: `ToAgentId`. Optional: `Reason`.
- **workspace.list_agents** — List agents in the workspace (enriched with live `agent_status`). Use it to find agent actor IDs, check which workers are still running, and confirm an agent was torn down after review. Optional filters: `Query` (substring), `ProjectId`, `ParentAgentId` (list only direct children of that agent), `ChildrenOnly` (true = use your own actor ID as parent — shorthand for "list my children").

## Discipline

- **Use the specialized callables.** Never hand-write map/task card frontmatter with `project.wiki_create_card` — use `project.wiki_create_map` / `project.wiki_create_task_card` so the include list and frontmatter are always correct.
- **One task card per worker.** Never pool workers across task cards.
- **Maximize concurrency.** Split parallelizable work into independent task cards and spawn all frontier workers at once; do not serialize independent tasks by handling them one per turn.
- **Seed cards with clues.** Put concrete pointers (file paths, function/type names, URLs, grep keywords, `[[wiki card]]` links) in each task card body so the worker starts from evidence, not a blank slate.
- **Refer to task cards by title**, never a bare id; the id rides inside a `[[card link]]`.
- **Don't pre-slice open questions.** An open question graduates into a task card — or none — only when the frontier reaches it.
- **Out of scope stays out.** Close task cards past the scope; note them in "Out of scope".
- **Don't self-complete.** When the map is clear, hand off — do not declare the goal done and stop.

## Do Not

- Do not use `plan_submit` to start a workflow map. `plan_submit` creates session-level tasks; the workflow map's task cards are authored separately with `project.wiki_create_task_card`. To start a workflow from a plan, call `workflow_plan_submit` instead.
- Do not use `project.wiki_create_card` to create map or task cards — use the specialized callables so frontmatter and the include list are maintained correctly.
- Do not spawn a worker for a task card whose `depends_on` dependencies are not all `done` — the frontier callable filters those; trust it.
- Do not use `workspace.agent_send_message` to assign tasks; use `workspace.agent_spawn_assign` so the goal and card binding are set up.
- Do not keep worker agents around after review; `approve` tears them down.
