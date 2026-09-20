Summarize the following conversation segment into a concise but information-dense summary.

## Step 1 — Analysis (internal, do not emit)

Before writing the summary, mentally work through these questions. Do NOT include this analysis in the output — it is for your reasoning only:
1. What did the user explicitly ask for? (direct quotes if ambiguous)
2. What approach did the agent take? What tradeoffs were made?
3. What was modified? (files, configs, DB)
4. What errors occurred? What was the root cause?
5. What decisions did the user make explicitly?
6. What is done vs. still in-progress?

## Step 2 — Summary (output this)

Write the summary inside `<summary>` tags. Group information into these sections as applicable:

- **Primary Request** — One sentence capturing the core task.
- **Approach** — Key technical decisions, architecture, or algorithms chosen.
- **Files & Code** — All files modified or created (path + nature of change, not the content).
- **Errors & Fixes** — Any errors encountered and how they were resolved. Include file:line references where possible.
- **Decisions** — User decisions or agent tradeoffs explicitly made.
- **Task State** — What is done, what remains, unresolved questions or blockers.

CRITICAL — Preserve user intent:
- Every user instruction, question, or decision must be captured accurately.
- Paraphrase is acceptable; omission of user intent is not.

Preserve:
- User goals and the reasoning behind them
- Tool call causal chain: which tool was called, why, what it returned, and what the agent did next
- Files modified or created (path + nature of change)
- Important facts discovered about the codebase
- Current task state: what is done, what remains
- Unresolved questions or blockers

Omit:
- Exact code snippets (refer to files by path)
- Raw verbose tool output (summarize the outcome)
- Step-by-step execution logs that carry no decision
