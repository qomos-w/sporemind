package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
	"github.com/qomos-w/sporemind/pkg/util"
)

// ── Frozen review changeset ──
//
// When a workflow child enters pending_review, the project actor can generate
// a complete, read-only snapshot of the child's worktree state. The snapshot
// is frozen: it is generated once and cached so the parent reviewer always
// sees the same data, even if the child makes further commits or the worktree
// is torn down.
//
// Authorization: only the child's direct parent agent (the orchestrator that
// spawned it) or a trusted human/admin caller may read the changeset. The
// parent mapping is recorded during project.spawn_agent and stored under
// worktreeParentMu.

// reviewChangeset is the frozen in-memory snapshot.
type reviewChangeset struct {
	agentActorID   string
	worktreeID     string
	taskCardID     string
	generation     int32 // per-freeze token; increments on each freeze, used for stale-job detection
	generatedAt    string
	status         string // preparing | ready | failed
	errorMsg       string
	baseline       string
	head           string
	branch         string
	isDirty        bool
	commits        []gen.ProjectReviewCommitInfo
	files          []gen.ProjectReviewFileEntry
	untrackedFiles []gen.ProjectReviewUntrackedFile
	testResult     gen.ProjectReviewTestResult
	stats          gen.ProjectReviewChangesetStats
	// Cached file content per path (diff text for tracked, full text for
	// untracked). Stored so paginated reads always hit the frozen snapshot.
	fileContents map[string]string
}

// reviewChangesetStatusPreparing means the async generation job is in flight.
// reviewChangesetStatusReady means the snapshot is complete and frozen.
// reviewChangesetStatusFailed means the generation job encountered an error.
const (
	reviewChangesetStatusPreparing = "preparing"
	reviewChangesetStatusReady     = "ready"
	reviewChangesetStatusFailed    = "failed"
)

// reviewChangesetGenSem limits concurrent changeset generation goroutines so a
// burst of freeze requests does not spawn unbounded git processes.
const reviewChangesetFileCap = 512 * 1024 // 512 KB per file
const reviewChangesetPreviewLines = 20
const reviewChangesetDefaultPageLimit = 200
const reviewChangesetDefaultTestTimeout = 60 * time.Second
const reviewChangesetMaxTestTimeout = 5 * time.Minute

// handleReviewChangesetFreeze is an Internal callable invoked by the agent's
// turn engine when the child enters pending_review. It kicks off async
// generation immediately: it writes a preparing-state entry into the task
// card's custom data (the authoritative snapshot) and launches a goroutine to
// collect git state and optionally run tests. The handler returns
// immediately — it does NOT block the project owner loop on git or test
// execution.
//
// If a ready snapshot already exists in the card data and no force-refresh is
// requested, the existing snapshot is preserved. ForceRefresh forces
// re-generation.
//
// The generation counter lives in a persisted review-binding record
// (persist.Persist) so stale finalize dispatches are rejected across process
// restarts. The goroutine does NOT write the card store directly — it calls
// project.review_changeset_finalize via async self-invoke so the result is
// applied on the project actor's owner loop.
func (a *Actor) handleReviewChangesetFreeze(ctx actor.PureContext, req gen.ProjectReviewChangesetReq) error {
	if req.AgentActorID == "" {
		return fmt.Errorf("project.review.changeset_freeze: AgentActorID is required")
	}
	// Guard the persisted binding key too: freezing under a display name
	// would fragment the binding/generation state from the hex-keyed rows.
	if err := requireCanonicalAgentActorID("project.review.changeset_freeze", req.AgentActorID); err != nil {
		return err
	}

	// Depth-in-defense: a no-worktree worker writes directly into the main
	// repo (no isolated child branch), so there is no changeset to freeze
	// and no preparing placeholder to write. The agent-side trigger guards
	// the same condition via a.activeWorkflowWorktreeID(); this guard
	// covers direct freeze invocations from tests / out-of-band callers
	// that bypass the agent-side check. The card custom data is left
	// untouched so the parent reviewer sees no spurious "preparing" entry.
	wtID, wtPath, wtErr := a.resolveWorktreeForAgent(req.AgentActorID)
	if wtErr != nil || wtID == "" {
		return nil
	}

	// Resolve the task card: prefer the request, fall back to the persisted
	// binding (the previous freeze's target).
	taskCardID := req.TaskCardID
	binding := a.loadReviewBinding(req.AgentActorID)
	if taskCardID == "" {
		taskCardID = binding.TaskCardID
	}

	// If a ready snapshot already exists and no force-refresh, keep it.
	if !req.ForceRefresh && taskCardID != "" {
		if data, ok := a.readReviewChangesetByTask(taskCardID); ok &&
			data.Status == reviewChangesetStatusReady && data.AgentActorID == req.AgentActorID {
			return nil
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Assign the next freeze generation from the persisted counter. The async
	// goroutine carries this token back via the finalize request; the
	// finalize handler only applies the result when both the card data and
	// the binding counter match, preventing stale goroutines from
	// resurrecting cleared snapshots or overwriting newer freeze results.
	binding.Gen++
	binding.TaskCardID = taskCardID
	a.saveReviewBinding(req.AgentActorID, binding)
	gen := binding.Gen

	// Write preparing state to the task card custom data (owner loop write).
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, gen, req.AgentActorID))

	// Capture self-reference for the goroutine to call back via self-invoke.
	// The goroutine must NOT touch the card store or the file-contents cache
	// directly — it dispatches the result through the owner loop.
	selfRef := ctx.Self()
	baseBranch := a.reviewBaseBranch(wtID)
	testCmd := req.TestCommand
	testTimeout := reviewChangesetDefaultTestTimeout
	if req.TestTimeoutMs > 0 {
		testTimeout = time.Duration(req.TestTimeoutMs) * time.Millisecond
		if testTimeout > reviewChangesetMaxTestTimeout {
			testTimeout = reviewChangesetMaxTestTimeout
		}
	}

	go generateReviewChangesetAsync(
		selfRef, req.AgentActorID, wtID, taskCardID,
		gen, wtPath, baseBranch, testCmd, testTimeout, now,
	)

	return nil
}

// generateReviewChangesetAsync runs the actual git collection and optional test
// execution in a goroutine, bounded by reviewChangesetGenSem. When done, it
// dispatches the result to the project actor's owner loop via self-invoke
// (project.review_changeset_finalize). It does NOT touch any actor state.
func generateReviewChangesetAsync(
	selfRef ref.Ref, agentActorID, worktreeID, taskCardID string,
	generation int32, wtPath, baseBranch, testCmd string,
	testTimeout time.Duration, generatedAt string,
) {
	// Acquire semaphore slot (bounded concurrency).
	reviewChangesetGenSem <- struct{}{}
	defer func() { <-reviewChangesetGenSem }()

	cs, err := generateReviewChangesetSync(agentActorID, wtPath, baseBranch, testCmd, testTimeout)
	if err != nil {
		// Dispatch failure to the owner loop.
		finalizeReq := gen.ProjectReviewChangesetFinalizeReq{
			AgentActorID: agentActorID,
			TaskCardID:   taskCardID,
			WorktreeID:   worktreeID,
			Generation:   generation,
			ErrMsg:       err.Error(),
		}
		dispatchFinalize(selfRef, finalizeReq)
		return
	}

	cs.worktreeID = worktreeID
	cs.generatedAt = generatedAt
	summary := cs.toSummaryResp()

	// Dispatch success to the owner loop.
	finalizeReq := gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentActorID,
		TaskCardID:   taskCardID,
		WorktreeID:   worktreeID,
		Generation:   generation,
		Summary:      &summary,
		FileContents: cs.fileContents,
	}
	dispatchFinalize(selfRef, finalizeReq)
}

// dispatchFinalize sends the finalize request to the project actor via
// self-invoke. Fire-and-forget: the goroutine does not wait for the result.
func dispatchFinalize(selfRef ref.Ref, req gen.ProjectReviewChangesetFinalizeReq) {
	if selfRef == nil {
		return
	}
	finalizeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	call := selfRef.Invoke(finalizeCtx, "project.review_changeset_finalize", req)
	if call != nil {
		_ = call.Close()
	}
}

// handleReviewChangesetFinalize is an Internal callable that applies the async
// generation result on the project actor's owner loop. It reads the task
// card's changeset entry (the authoritative snapshot) for the stale
// generation check, then writes the full summary back into the card data and
// populates the transient file-contents cache (a rebuildable cache — cache
// misses re-derive diffs from git). Called by the goroutine via async
// self-invoke — never directly.
func (a *Actor) handleReviewChangesetFinalize(_ actor.PureContext, req gen.ProjectReviewChangesetFinalizeReq) error {
	if req.AgentActorID == "" {
		return fmt.Errorf("project.review.changeset_finalize: AgentActorID is required")
	}

	// Resolve the target task card: prefer the request, fall back to the
	// persisted binding.
	taskCardID := req.TaskCardID
	binding := a.loadReviewBinding(req.AgentActorID)
	if taskCardID == "" {
		taskCardID = binding.TaskCardID
	}

	// Stale detection: the card data must still be in preparing state with a
	// matching generation AND the binding counter must match (a clear
	// increments the counter without touching the card, so an in-flight
	// goroutine is rejected even when its card entry was later recreated by a
	// newer freeze that happened to reuse the number).
	data, ok := a.readReviewChangesetByTask(taskCardID)
	stale := !ok ||
		data.Status != reviewChangesetStatusPreparing ||
		data.AgentActorID != req.AgentActorID ||
		(req.Generation != 0 && data.Generation != req.Generation) ||
		(req.Generation != 0 && binding.Gen != req.Generation)

	// Also verify worktreeID matches if both are set — a worktree release +
	// rebind to a different worktree should not be resurrected by an old job.
	if !stale && req.WorktreeID != "" {
		if wtID, _, err := a.resolveWorktreeForAgent(req.AgentActorID); err == nil && wtID != req.WorktreeID {
			stale = true
		}
	}

	if stale {
		// Drop the result silently — the freeze that started this goroutine
		// has been cleared or superseded.
		return nil
	}

	if req.ErrMsg != "" {
		// Generation failed — write the failed state into the card data.
		a.writeReviewChangesetCardData(taskCardID, failedCardData(time.Now().UTC().Format(time.RFC3339), req.ErrMsg))
		return nil
	}

	if req.Summary == nil {
		return fmt.Errorf("project.review.changeset_finalize: neither ErrMsg nor Summary provided")
	}

	// Apply the ready snapshot into the card data (owner loop write).
	a.writeReviewChangesetCardData(taskCardID, summaryToCardData(req.Summary, req.Generation))

	// Populate the transient file-contents cache (rebuildable; the card data
	// remains the authoritative summary).
	if len(req.FileContents) > 0 {
		a.reviewFileContentsMu.Lock()
		if a.reviewFileContents == nil {
			a.reviewFileContents = make(map[string]map[string]string)
		}
		a.reviewFileContents[req.AgentActorID] = req.FileContents
		a.reviewFileContentsMu.Unlock()
	}

	return nil
}

// summaryToChangeset reconstructs a reviewChangeset from a SummaryResp for
// storage in the reviewChangesets map. File contents are NOT included (the
// finalize path only carries the summary); file_content reads will need the
// original goroutine's cache or will fall back to re-generation.
func summaryToChangeset(s *gen.ProjectReviewChangesetSummaryResp) *reviewChangeset {
	return &reviewChangeset{
		agentActorID:   s.AgentActorID,
		worktreeID:     s.WorktreeID,
		generatedAt:    s.GeneratedAt,
		status:         s.Status,
		errorMsg:       s.ErrorMsg,
		baseline:       s.Baseline,
		head:           s.Head,
		branch:         s.Branch,
		isDirty:        s.IsDirty,
		commits:        s.Commits,
		files:          s.Files,
		untrackedFiles: s.UntrackedFiles,
		testResult:     s.TestResult,
		stats:          s.Stats,
		fileContents:   map[string]string{}, // not carried via finalize
	}
}

// requireCanonicalAgentActorID enforces the one format the project-side
// review state is keyed by. Workspace review callables accept either the
// canonical hex actor id or the display name (AgentRef.ID); the project's
// agentWorktree/agentParent maps and the persisted review binding are keyed
// by the hex id only. A display name reaching those lookups produced
// misleading errors (authorization failure, "not bound to a worktree") that
// look like binding bugs — fail fast with the format contract instead.
func requireCanonicalAgentActorID(callable, v string) error {
	if _, err := identity.ParseCanonicalID(v); err != nil {
		return fmt.Errorf(
			"%s: AgentActorId must be the child's canonical hex actor id (the \"actor:\" value from the workflow notification or workspace.list_agents), got %q — display names are accepted by workspace.agent_review/agent_terminate but not here",
			callable, v)
	}
	return nil
}

// handleReviewChangesetSummary returns the changeset summary from the task
// card's custom data (the authoritative snapshot). If no snapshot exists yet,
// auto-freeze so human callers or the parent agent can trigger generation on
// first read. Only the child's direct parent or a trusted human/admin may
// call this.
func (a *Actor) handleReviewChangesetSummary(ctx actor.PureContext, req gen.ProjectReviewChangesetReq) (gen.ProjectReviewChangesetSummaryResp, error) {
	if req.AgentActorID == "" {
		return gen.ProjectReviewChangesetSummaryResp{}, fmt.Errorf("project.review.changeset: AgentActorID is required")
	}
	if err := requireCanonicalAgentActorID("project.review.changeset", req.AgentActorID); err != nil {
		return gen.ProjectReviewChangesetSummaryResp{}, err
	}
	if err := a.authorizeReviewChangeset(ctx, req.AgentActorID, req.CallerAgentID); err != nil {
		return gen.ProjectReviewChangesetSummaryResp{}, err
	}

	// Resolve the task card from the request or persisted binding.
	taskCardID := req.TaskCardID
	if taskCardID == "" {
		binding := a.loadReviewBinding(req.AgentActorID)
		taskCardID = binding.TaskCardID
	}

	// Read the authoritative snapshot from the card data.
	if data, ok := a.readReviewChangesetByTask(taskCardID); ok && data.AgentActorID == req.AgentActorID {
		return cardDataToSummaryResp(data, req.AgentActorID), nil
	}

	// ForceRefresh: kick off a new async generation.
	if req.ForceRefresh {
		// Security: only trusted developer callers may set a test command.
		// Non-developer callers (agents) cannot inject arbitrary shell commands.
		if err := policy.RequireDeveloper(ctx.Identity().Role); err != nil {
			req.TestCommand = ""
		}
		if err := a.handleReviewChangesetFreeze(ctx, req); err != nil {
			return gen.ProjectReviewChangesetSummaryResp{}, err
		}
	}

	// No snapshot at all — auto-freeze so developer callers or the parent agent
	// can trigger generation on first read if the pending_review freeze was
	// missed. This validates worktree binding and returns an error if the
	// agent is not bound.
	if data, ok := a.readReviewChangesetByTask(taskCardID); !ok || data.AgentActorID != req.AgentActorID {
		if err := a.handleReviewChangesetFreeze(ctx, req); err != nil {
			return gen.ProjectReviewChangesetSummaryResp{}, err
		}
	}

	// Return the preparing placeholder.
	return gen.ProjectReviewChangesetSummaryResp{
		AgentActorID: req.AgentActorID,
		Status:       reviewChangesetStatusPreparing,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

// handleReviewChangesetFileContent returns paginated file content from the
// changeset. For tracked modified files the content is the unified diff; for
// untracked files it is the full file content. Content is served from the
// transient finalize-time cache; on cache miss (e.g. after a process
// restart) the diff is re-derived from git using the frozen baseline/head in
// the card data.
func (a *Actor) handleReviewChangesetFileContent(ctx actor.PureContext, req gen.ProjectReviewFileContentReq) (gen.ProjectReviewFileContentResp, error) {
	if req.AgentActorID == "" {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: AgentActorID is required")
	}
	if err := requireCanonicalAgentActorID("project.review.file_content", req.AgentActorID); err != nil {
		return gen.ProjectReviewFileContentResp{}, err
	}
	if req.FilePath == "" {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: FilePath is required")
	}
	if err := a.authorizeReviewChangeset(ctx, req.AgentActorID, req.CallerAgentID); err != nil {
		return gen.ProjectReviewFileContentResp{}, err
	}

	// Read the authoritative snapshot from the card data (via the persisted
	// binding — the file-content request does not carry a task card ID).
	binding := a.loadReviewBinding(req.AgentActorID)
	taskCardID := binding.TaskCardID
	data, ok := a.readReviewChangesetByTask(taskCardID)
	if !ok || data.AgentActorID != req.AgentActorID {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: no changeset for agent %q; call review.changeset first", req.AgentActorID)
	}
	if data.Status != reviewChangesetStatusReady {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: changeset is %s, not ready", data.Status)
	}

	// Find the file in the frozen file lists.
	fileStatus, isBinary, isGenerated, isUntracked := "", false, false, false
	found := false
	for _, f := range data.Files {
		if f.Path == req.FilePath {
			fileStatus, isBinary, isGenerated, found = f.Status, f.IsBinary, f.IsGenerated, true
			break
		}
	}
	if !found {
		for _, f := range data.UntrackedFiles {
			if f.Path == req.FilePath {
				fileStatus, isBinary, found, isUntracked = "untracked", f.IsBinary, true, true
				break
			}
		}
	}
	if !found {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: file %q not found in changeset", req.FilePath)
	}
	if isBinary {
		return gen.ProjectReviewFileContentResp{}, fmt.Errorf("project.review.file_content: file %q is binary", req.FilePath)
	}

	// Resolve content: transient cache first, then re-derive from git.
	content, exists := a.lookupReviewFileContent(req.AgentActorID, req.FilePath)
	if !exists {
		derived, err := a.deriveReviewFileContent(req.AgentActorID, req.FilePath, data, isUntracked)
		if err != nil {
			return gen.ProjectReviewFileContentResp{}, err
		}
		content = derived
	}

	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1] // drop trailing empty line from split
	}
	totalLines := int32(len(lines))

	offset := int(req.Offset)
	if offset <= 0 {
		offset = 1
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = reviewChangesetDefaultPageLimit
	}

	startIdx := offset - 1
	if startIdx >= len(lines) {
		startIdx = len(lines)
	}
	endIdx := startIdx + limit
	if endIdx > len(lines) {
		endIdx = len(lines)
	}

	pageLines := lines[startIdx:endIdx]
	pageContent := strings.Join(pageLines, "\n")

	return gen.ProjectReviewFileContentResp{
		FilePath:    req.FilePath,
		Status:      fileStatus,
		Content:     pageContent,
		IsBinary:    isBinary,
		IsGenerated: isGenerated,
		StartLine:   int32(offset),
		NumLines:    int32(len(pageLines)),
		TotalLines:  totalLines,
		Truncated:   endIdx < len(lines),
	}, nil
}

// lookupReviewFileContent reads from the transient finalize-time cache.
func (a *Actor) lookupReviewFileContent(agentActorID, path string) (string, bool) {
	a.reviewFileContentsMu.RLock()
	defer a.reviewFileContentsMu.RUnlock()
	if a.reviewFileContents == nil {
		return "", false
	}
	contents, ok := a.reviewFileContents[agentActorID]
	if !ok {
		return "", false
	}
	c, ok := contents[path]
	return c, ok
}

// deriveReviewFileContent re-derives the content for one file from the live
// worktree using the frozen baseline/head recorded in the card data. For
// tracked files this is the committed diff baseline..head; for untracked
// files it reads the file from disk.
func (a *Actor) deriveReviewFileContent(agentActorID, path string, data reviewChangesetCardData, isUntracked bool) (string, error) {
	wtID, wtPath, err := a.resolveWorktreeForAgent(agentActorID)
	if err != nil {
		return "", fmt.Errorf("project.review.file_content: worktree unavailable for %q: %w", agentActorID, err)
	}
	_ = wtID
	if isUntracked {
		b, err := os.ReadFile(filepath.Join(wtPath, path))
		if err != nil {
			return "", fmt.Errorf("project.review.file_content: read untracked file %q: %w", path, err)
		}
		text := string(b)
		if len(text) > reviewChangesetFileCap {
			text = text[:reviewChangesetFileCap]
		}
		return text, nil
	}
	diffArgs := []string{"-c", "core.quotepath=false", "diff", "--find-renames"}
	if data.Baseline != "" {
		diffArgs = append(diffArgs, data.Baseline+".."+data.Head, "--", path)
	} else {
		diffArgs = append(diffArgs, data.Head, "--", path)
	}
	out, err := gitRun(nil, wtPath, diffArgs...)
	if err != nil {
		return "", fmt.Errorf("project.review.file_content: derive diff for %q: %w", path, err)
	}
	return out, nil
}

// handleReviewChangesetClear drops the changeset for an agent: removes the
// card-data entry from the task card, drops the file-contents cache, and
// bumps the persisted binding generation so any in-flight finalize goroutine
// is rejected as stale. The binding record itself is kept (as a tombstone
// counter) so subsequent freezes continue from the bumped generation; the
// worktree teardown deletes it outright.
func (a *Actor) handleReviewChangesetClear(_ actor.PureContext, req gen.ProjectReviewChangesetReq) error {
	if req.AgentActorID == "" {
		return fmt.Errorf("project.review.changeset_clear: AgentActorID is required")
	}

	// Bump the persisted generation counter: any in-flight finalize from the
	// previous freeze now mismatches and is dropped by the finalize handler.
	binding := a.loadReviewBinding(req.AgentActorID)
	binding.Gen++
	if req.TaskCardID != "" {
		binding.TaskCardID = req.TaskCardID
	}
	a.saveReviewBinding(req.AgentActorID, binding)

	// Remove the authoritative card-data entry from the task card.
	taskCardID := req.TaskCardID
	if taskCardID == "" {
		taskCardID = binding.TaskCardID
	}
	if taskCardID != "" {
		if data, ok := a.readReviewChangesetByTask(taskCardID); ok && data.AgentActorID == req.AgentActorID {
			a.clearReviewChangesetCardData(taskCardID)
		}
	}

	// Drop the transient file-contents cache.
	a.reviewFileContentsMu.Lock()
	delete(a.reviewFileContents, req.AgentActorID)
	a.reviewFileContentsMu.Unlock()

	return nil
}

// authorizeReviewChangeset verifies the caller is the child's direct parent or
// a trusted developer/admin. Mirrors the pattern in workspace.requireDirectParent.
func (a *Actor) authorizeReviewChangeset(ctx actor.PureContext, childAgentID, callerAgentID string) error {
	// Trusted human/admin callers (UI) bypass parent authorization.
	if policy.RequireDeveloper(ctx.Identity().Role) == nil {
		return nil
	}
	if callerAgentID == "" {
		return fmt.Errorf("project.review.changeset: caller identity is not trusted (role=%q) and no CallerAgentId was injected", ctx.Identity().Role)
	}
	a.worktreeParentMu.RLock()
	parent := a.agentParent[childAgentID]
	a.worktreeParentMu.RUnlock()
	if parent == "" || callerAgentID != parent {
		return fmt.Errorf("project.review.changeset: only the direct parent agent may read this child's changeset")
	}
	return nil
}

// generateReviewChangesetSync builds the full frozen snapshot from the child's
// worktree. It captures both committed and uncommitted state: committed
// commits since the merge-base, working-tree diffs, and untracked files.
// This is a pure function with no Actor dependencies — it runs in a goroutine.
func generateReviewChangesetSync(agentActorID, wtPath, baseBranch, testCmd string, testTimeout time.Duration) (*reviewChangeset, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Baseline = merge-base of worktree HEAD with the default branch.
	baseline := ""
	if baseBranch != "" {
		if out, err := gitRun(nil, wtPath, "merge-base", baseBranch, "HEAD"); err == nil {
			baseline = strings.TrimSpace(out)
		}
	}

	head, err := gitRevParseHead(wtPath)
	if err != nil {
		return nil, err
	}
	branch, _ := gitBranchShowCurrent(wtPath)
	dirty, _ := gitWorktreeHasUncommitted(wtPath)

	// Commits since the merge-base (or since HEAD~50 as fallback).
	commitRange := baseline
	if commitRange == "" {
		commitRange = "HEAD~50"
	}
	commits := collectReviewCommits(wtPath, commitRange)

	// Tracked file changes (committed + working-tree combined).
	trackedFiles, fileContents, stats := collectReviewTrackedFiles(wtPath, baseline)

	// Untracked files.
	untrackedFiles, untrackedContents := collectReviewUntrackedFiles(wtPath)

	// Merge file contents.
	allContents := make(map[string]string)
	for k, v := range fileContents {
		allContents[k] = v
	}
	for k, v := range untrackedContents {
		allContents[k] = v
	}
	stats.Untracked = int32(len(untrackedFiles))

	// Test result: only run if a test command was explicitly provided.
	var testResult gen.ProjectReviewTestResult
	if testCmd != "" {
		testResult = runReviewTestCommand(wtPath, testCmd, testTimeout)
	} else {
		testResult = gen.ProjectReviewTestResult{Skipped: true}
	}

	return &reviewChangeset{
		agentActorID:   agentActorID,
		generatedAt:    now,
		status:         reviewChangesetStatusReady,
		baseline:       baseline,
		head:           head,
		branch:         branch,
		isDirty:        dirty,
		commits:        commits,
		files:          trackedFiles,
		untrackedFiles: untrackedFiles,
		testResult:     testResult,
		stats:          stats,
		fileContents:   allContents,
	}, nil
}

// reviewBaseBranch returns the diff baseline branch for a worker's review
// changeset: the parent worktree's current branch for workflow workers
// (whose delta must be measured against the owner branch they forked from
// and are rebased onto), otherwise the repo's default branch. Using the
// default branch for a workflow worker would either fold in sibling
// workers' already-merged work or, when the owner branch is an ancestor of
// the default, yield an empty diff against the fork point.
func (a *Actor) reviewBaseBranch(wtID string) string {
	root, _ := a.rootPath()
	base, _ := defaultBranch(root)
	a.worktreeParentMu.RLock()
	wt, ok := a.worktrees[wtID]
	if !ok || wt.ParentWorktreeID == "" {
		a.worktreeParentMu.RUnlock()
		return base
	}
	parent, pok := a.worktrees[wt.ParentWorktreeID]
	a.worktreeParentMu.RUnlock()
	if !pok {
		return base
	}
	if branch, err := gitBranchShowCurrent(parent.Path); err == nil && branch != "" {
		return branch
	}
	return base
}

// resolveWorktreeForAgent looks up the worktree bound to the given agent.
func (a *Actor) resolveWorktreeForAgent(agentActorID string) (string, string, error) {
	a.bindingMu.RLock()
	wtID, ok := a.agentWorktree[agentActorID]
	a.bindingMu.RUnlock()
	if !ok {
		return "", "", fmt.Errorf("agent %q is not bound to a worktree", agentActorID)
	}
	a.worktreeParentMu.RLock()
	wt, exists := a.worktrees[wtID]
	a.worktreeParentMu.RUnlock()
	if !exists {
		return "", "", fmt.Errorf("worktree %q for agent %q not found", wtID, agentActorID)
	}
	if wt.Status != "active" {
		return "", "", fmt.Errorf("worktree %q is %s, not active", wtID, wt.Status)
	}
	return wtID, wt.Path, nil
}

// toSummaryResp converts the frozen snapshot into the summary response.
func (cs *reviewChangeset) toSummaryResp() gen.ProjectReviewChangesetSummaryResp {
	commits := cs.commits
	if commits == nil {
		commits = []gen.ProjectReviewCommitInfo{}
	}
	files := cs.files
	if files == nil {
		files = []gen.ProjectReviewFileEntry{}
	}
	untracked := cs.untrackedFiles
	if untracked == nil {
		untracked = []gen.ProjectReviewUntrackedFile{}
	}
	return gen.ProjectReviewChangesetSummaryResp{
		AgentActorID:   cs.agentActorID,
		WorktreeID:     cs.worktreeID,
		GeneratedAt:    cs.generatedAt,
		Status:         cs.status,
		ErrorMsg:       cs.errorMsg,
		Baseline:       cs.baseline,
		Head:           cs.head,
		Branch:         cs.branch,
		IsDirty:        cs.isDirty,
		Commits:        commits,
		Files:          files,
		UntrackedFiles: untracked,
		TestResult:     cs.testResult,
		Stats:          cs.stats,
	}
}

// ── Git collection helpers ──

// collectReviewCommits returns commit info from git log starting at commitRange.
func collectReviewCommits(wtPath, commitRange string) []gen.ProjectReviewCommitInfo {
	out, err := gitRun(nil, wtPath, "log", "--oneline", "--no-decorate", commitRange+"..HEAD")
	if err != nil {
		// Fallback: just get last 50 commits.
		out, err = gitRun(nil, wtPath, "log", "--oneline", "--no-decorate", "-50")
		if err != nil {
			return []gen.ProjectReviewCommitInfo{}
		}
	}
	var commits []gen.ProjectReviewCommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: <hash> <message>
		spaceIdx := strings.IndexByte(line, ' ')
		if spaceIdx < 0 {
			continue
		}
		hash := line[:spaceIdx]
		msg := line[spaceIdx+1:]
		short := hash
		if len(short) > 7 {
			short = short[:7]
		}
		commits = append(commits, gen.ProjectReviewCommitInfo{
			Hash:    hash,
			Short:   short,
			Message: msg,
		})
	}
	// Enrich with author/time via --format.
	if len(commits) > 0 {
		detailed, err := gitRun(nil, wtPath, "log", "--format=%H%x09%an%x09%at", commitRange+"..HEAD")
		if err == nil {
			detailMap := make(map[string]struct{ author, when string })
			for _, line := range strings.Split(strings.TrimSpace(detailed), "\n") {
				parts := strings.Split(line, "\t")
				if len(parts) >= 3 {
					ts, _ := parseUnixTimestamp(parts[2])
					detailMap[parts[0]] = struct{ author, when string }{parts[1], ts}
				}
			}
			for i := range commits {
				if d, ok := detailMap[commits[i].Hash]; ok {
					commits[i].Author = d.author
					commits[i].When = d.when
				}
			}
		}
	}
	return commits
}

func parseUnixTimestamp(s string) (string, error) {
	var ts int64
	if _, err := fmt.Sscanf(s, "%d", &ts); err != nil {
		return "", err
	}
	return time.Unix(ts, 0).Local().Format(time.RFC3339), nil
}

// collectReviewTrackedFiles collects tracked file changes (both committed since
// baseline and uncommitted working-tree changes). Returns the file summary
// list, cached diff contents, and aggregate stats.
func collectReviewTrackedFiles(wtPath, baseline string) ([]gen.ProjectReviewFileEntry, map[string]string, gen.ProjectReviewChangesetStats) {
	// `git diff --name-status` for committed changes (baseline..HEAD) and
	// working-tree changes (HEAD..working-tree). We use a combined approach:
	// `git diff --name-status <baseline> -- .` covers committed changes, and
	// `git status --porcelain` covers working-tree changes.
	committedStatus := ""
	if baseline != "" {
		// -z: NUL-separated fields with bare (never quoted) paths; newline
		// mode double-quotes non-ASCII paths.
		if out, err := gitRun(nil, wtPath, "diff", "--name-status", "-z", "--find-renames", baseline+"..HEAD"); err == nil {
			committedStatus = out
		}
	}
	workingStatus := ""
	if out, err := gitRun(nil, wtPath, "status", "-z", "--porcelain"); err == nil {
		workingStatus = out
	}

	// Merge into a unified map: path → (status, oldPath).
	type fileKey struct {
		status  string
		oldPath string
	}
	fileMap := make(map[string]fileKey)

	// Parse committed: <status>\0<path>\0 or <status>\0<oldPath>\0<path>\0 (rename)
	if committedStatus != "" {
		fields := strings.Split(strings.TrimRight(committedStatus, "\x00"), "\x00")
		for i := 0; i < len(fields); i++ {
			code := fields[i]
			if code == "" {
				continue
			}
			if code[0] == 'R' || code[0] == 'C' {
				if i+2 < len(fields) {
					fileMap[fields[i+2]] = fileKey{status: diffStatusCodeToWord(code), oldPath: fields[i+1]}
				}
				i += 2
			} else if i+1 < len(fields) {
				fileMap[fields[i+1]] = fileKey{status: diffStatusCodeToWord(code), oldPath: ""}
				i++
			}
		}
	}

	// Parse working-tree records from -z porcelain (rename originals arrive
	// as a second bare field, consumed by parseGitPorcelainZ).
	for _, rec := range parseGitPorcelainZ(workingStatus) {
		// Untracked files (??) are handled separately.
		if rec.staging == "?" && rec.worktree == "?" {
			continue
		}
		existing, exists := fileMap[rec.path]
		if !exists {
			status := porcelainToStatus(rec.staging + rec.worktree)
			fileMap[rec.path] = fileKey{status: status, oldPath: rec.oldPath}
		} else if rec.oldPath != "" && existing.oldPath == "" {
			existing.oldPath = rec.oldPath
			fileMap[rec.path] = existing
		}
	}

	// Build sorted file list and collect diffs.
	paths := make([]string, 0, len(fileMap))
	for p := range fileMap {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var files []gen.ProjectReviewFileEntry
	fileContents := make(map[string]string)
	var stats gen.ProjectReviewChangesetStats

	for _, p := range paths {
		info := fileMap[p]
		isBinary := isBinaryFile(filepath.Join(wtPath, p))
		isGen := isGeneratedFile(p)
		entry := gen.ProjectReviewFileEntry{
			Path:        p,
			Status:      info.status,
			OldPath:     info.oldPath,
			IsBinary:    isBinary,
			IsGenerated: isGen,
		}

		if !isBinary {
		// Get the full diff for this file (committed + working-tree).
		diffArgs := []string{"-c", "core.quotepath=false", "diff", "--find-renames"}
			if baseline != "" {
				diffArgs = append(diffArgs, baseline+"..HEAD", "--", p)
			} else {
				diffArgs = append(diffArgs, "HEAD", "--", p)
			}
			// Also include working-tree diff.
			diffOut, err := gitRun(nil, wtPath, diffArgs...)
			if err == nil && strings.TrimSpace(diffOut) != "" {
				entry.DiffLines = int32(strings.Count(diffOut, "\n"))
				if len(diffOut) <= reviewChangesetFileCap {
					fileContents[p] = diffOut
				}
				stats.TotalDiffLines += entry.DiffLines
				stats.AddedLines += int32(countDiffLines(diffOut, "+"))
				stats.DeletedLines += int32(countDiffLines(diffOut, "-"))
			}
		// Also check for uncommitted changes.
		wdOut, err := gitRun(nil, wtPath, "-c", "core.quotepath=false", "diff", "--", p)
			if err == nil && strings.TrimSpace(wdOut) != "" {
				// Merge working-tree diff into cached content.
				merged := diffOut + "\n" + wdOut
				entry.DiffLines += int32(strings.Count(wdOut, "\n"))
				if len(merged) <= reviewChangesetFileCap {
					fileContents[p] = merged
				}
				stats.TotalDiffLines += int32(strings.Count(wdOut, "\n"))
			}
		}

		files = append(files, entry)
		stats.TrackedChanged++
	}

	return files, fileContents, stats
}

// collectReviewUntrackedFiles collects untracked files from `git status`.
func collectReviewUntrackedFiles(wtPath string) ([]gen.ProjectReviewUntrackedFile, map[string]string) {
	out, err := gitRun(nil, wtPath, "status", "-z", "--porcelain")
	if err != nil {
		return []gen.ProjectReviewUntrackedFile{}, nil
	}
	var files []gen.ProjectReviewUntrackedFile
	contents := make(map[string]string)
	for _, rec := range parseGitPorcelainZ(out) {
		if rec.staging != "?" || rec.worktree != "?" {
			continue
		}
		path := rec.path
		if path == "" {
			continue
		}
		fullPath := filepath.Join(wtPath, path)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			continue
		}
		size := int32(info.Size())
		isBinary := isBinaryFile(fullPath)
		uf := gen.ProjectReviewUntrackedFile{
			Path:     path,
			Size:     size,
			IsBinary: isBinary,
			IsText:   !isBinary && size <= reviewChangesetFileCap,
		}
		if uf.IsText {
			data, err := os.ReadFile(fullPath)
			if err == nil {
				text := string(data)
				// Preview: first N lines.
				lines := strings.Split(text, "\n")
				if len(lines) > reviewChangesetPreviewLines {
					uf.Preview = strings.Join(lines[:reviewChangesetPreviewLines], "\n") + "\n..."
				} else {
					uf.Preview = text
				}
				if len(text) <= reviewChangesetFileCap {
					contents[path] = text
				}
			}
		}
		files = append(files, uf)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, contents
}

// runReviewTestCommand runs the given test command in the worktree with the
// specified timeout. Returns a result with Skipped=true if no command is given.
// The command runs via the system shell (sh -c / cmd /c) to support pipes and
// redirects. Output is truncated to the last 100 lines.
func runReviewTestCommand(wtPath, command string, timeout time.Duration) gen.ProjectReviewTestResult {
	if command == "" {
		return gen.ProjectReviewTestResult{Skipped: true}
	}
	if timeout <= 0 {
		timeout = reviewChangesetDefaultTestTimeout
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := util.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = wtPath
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	duration := time.Since(start)
	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}
	output = tailLines(output, 100)
	success := err == nil
	exitCode := 0
	errorMsg := ""
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			errorMsg = fmt.Sprintf("test timed out after %s", timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return gen.ProjectReviewTestResult{
		Command:  command,
		ExitCode: int32(exitCode),
		Output:   output,
		Duration: duration.Round(time.Millisecond).String(),
		Success:  success,
		ErrorMsg: errorMsg,
	}
}

// ── Utility functions ──

func diffStatusCodeToWord(code string) string {
	if len(code) == 0 {
		return "modified"
	}
	switch code[0] {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type-changed"
	case 'M':
		return "modified"
	default:
		return code
	}
}

func porcelainToStatus(xy string) string {
	staging := byte(' ')
	worktree := byte(' ')
	if len(xy) >= 1 {
		staging = xy[0]
	}
	if len(xy) >= 2 {
		worktree = xy[1]
	}
	// Priority: D > R > A > M
	for _, c := range []byte{staging, worktree} {
		switch c {
		case 'D':
			return "deleted"
		case 'R':
			return "renamed"
		case 'A':
			return "added"
		case 'M':
			return "modified"
		case 'T':
			return "type-changed"
		}
	}
	return "modified"
}

func isGeneratedFile(path string) bool {
	lower := strings.ToLower(path)
	for _, pat := range generatedFilePatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func countDiffLines(diff string, prefix string) int32 {
	var count int32
	for _, line := range strings.Split(diff, "\n") {
		if len(line) > 0 && line[0] == prefix[0] && !strings.HasPrefix(line, prefix+prefix) {
			// Skip "+++" / "---" header lines.
			if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
				continue
			}
			count++
		}
	}
	return count
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func fileStatusInChangeset(cs *reviewChangeset, path string) string {
	for _, f := range cs.files {
		if f.Path == path {
			return f.Status
		}
	}
	for _, f := range cs.untrackedFiles {
		if f.Path == path {
			return "untracked"
		}
	}
	return ""
}

func fileFlagsInChangeset(cs *reviewChangeset, path string) (bool, bool) {
	for _, f := range cs.files {
		if f.Path == path {
			return f.IsBinary, f.IsGenerated
		}
	}
	for _, f := range cs.untrackedFiles {
		if f.Path == path {
			return f.IsBinary, isGeneratedFile(path)
		}
	}
	return false, false
}
