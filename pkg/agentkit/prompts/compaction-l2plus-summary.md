Synthesize the following summary segments into a single higher-level summary.

Each segment is marked with its source range and level. Merge them into a coherent narrative.

## Step 1 — Analysis (internal, do not emit)

Before writing, mentally work through:
1. What is the overall goal across all segments? Has it shifted?
2. Which files/components have been touched repeatedly? What is the current state of each?
3. What decisions were made, and were any reversed later?
4. What is still open or incomplete?

Do NOT include this analysis in the output — it is for your reasoning only.

## Step 2 — Synthesis (output this)

Write the synthesis inside `<summary>` tags. Group information into these sections as applicable:

- **Overall Goal** — The user's primary objective as it stands now (may have evolved across segments).
- **Key Decisions** — Architectural choices and tradeoffs, deduplicated across segments.
- **Current State** — Files and components touched, their current status (done/in-progress/planned).
- **Open Items** — Unresolved blockers, pending questions, or remaining work.
- **Recent Progress** — What happened in the most recent segments (prioritize recency).

Goals:
- Deduplicate: if multiple segments mention the same file, task, or decision, merge into one mention
- Abstract: replace detailed step-by-step tool chains with their outcomes and decisions
- Preserve: user goals, architectural decisions, unresolved blockers, and current task state
- Remove: temporal sequencing ("then", "next", "after that") unless causality matters
- Reconcile conflicts: if two segments disagree on a fact, prefer the later one
