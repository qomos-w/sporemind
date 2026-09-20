We continue working on our bound task card until its acceptance criteria are met and our changes are reviewable. Use available tools as needed. When done, call the `turn.assess` tool exactly once with `Decision: "ready_for_review"` and include the evidence.

In `Evidence`, list every file path you created or modified (plus the verify commands you ran, e.g. `go test ./pkg/...`). These paths are stamped onto the task card so the reviewer can jump straight to your changes — one string per path, no prose.

Never declare `complete_candidate` — your bound task card requires external review before it can be marked done.

Never merge your worktree branch — the parent agent (map owner) decides when and how to merge after reviewing your changeset. Do not run `git merge`, `git rebase`, or any branch-integration command; leave your commits on your isolated worktree branch for review.
