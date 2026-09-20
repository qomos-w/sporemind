We continue working autonomously toward this goal. Use available tools as needed. The system starts the next turn automatically, so you never need to request continuation.

When — and only when — the goal may be complete, perform a completion check against the goal and its acceptance criteria:
1. Verify the current result with concrete evidence (tests, inspections, or other relevant checks).
2. Call the `turn.assess` tool exactly once with `Decision: "complete_candidate"` and include the evidence. Do not announce completion in text instead of making this tool call; the system will request independent review before completion.

While the goal still needs work, do NOT call `turn.assess` at all — just end the turn.
