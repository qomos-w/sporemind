package puppeteditor

import (
	"encoding/json"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// cmdRevertTo is the CommandKind recorded on the new head revision a revert
// produces. The revision chain stays append-only: revert never deletes or
// rewrites history, it only restores an earlier recorded state under a fresh
// revision whose ParentRevisionId links to the previous head.
const cmdRevertTo = "revert_to"

// revertRequestPrefix namespaces agent revert_to RequestIds inside the shared
// ProcessedRequests idempotency cache so a revert replay can never collide
// with (or be mistaken for) a cached edit result under the same raw id.
const revertRequestPrefix = "revert:"

// handleRevertTo is the public puppet.document.revert_to callable: undo/redo
// on the immutable revision chain. The document is rebuilt from the snapshot
// recorded when the target revision was appended, a new "revert_to" head
// revision records the action, and the revision counter keeps climbing
// monotonically. Reverting to a state the document already carries is an
// idempotent no-op that appends nothing.
func (a *Actor) handleRevertTo(ctx actor.PureContext, req gen.PuppetDocumentRevertToReq) (gen.PuppetDocumentRevertToResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.revertToLocked(ctx, req, "")
}

// handleAgentRevertTo is the Internal agent bridge for revert. It enforces the
// agent-role + audited-envelope gate, then applies the same revert under one
// lock, with envelope RequestId idempotency: a replay returns the original
// result without re-applying or appending a duplicate revision.
func (a *Actor) handleAgentRevertTo(ctx actor.PureContext, req gen.PuppetAgentRevertToReq) (gen.PuppetAgentRevertToResp, error) {
	if err := a.validateAgentRequest(ctx, req.Envelope); err != nil {
		return gen.PuppetAgentRevertToResp{}, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if req.Envelope.RequestID != "" {
		key := revertRequestPrefix + req.Envelope.RequestID
		if pr, ok := a.state.ProcessedRequests[key]; ok {
			if cached, ok := a.cachedRevertRespLocked(pr); ok {
				return gen.PuppetAgentRevertToResp{Result: cached}, nil
			}
		}
	}

	result, err := a.revertToLocked(ctx, gen.PuppetDocumentRevertToReq{RevisionID: req.RevisionID}, req.Envelope.AuditSource)
	if err != nil {
		return gen.PuppetAgentRevertToResp{}, err
	}

	// Record the RequestId so a replay short-circuits above. Persisted with
	// the same save the revert already performed when it appended; a no-op
	// revert still persists the cache entry.
	if req.Envelope.RequestID != "" {
		if a.state.ProcessedRequests == nil {
			a.state.ProcessedRequests = make(map[string]editResult)
		}
		a.state.ProcessedRequests[revertRequestPrefix+req.Envelope.RequestID] = editResult{
			RevisionID:    result.Revision.ID,
			Detail:        result.Detail,
			AffectedGuids: result.Revision.AffectedGuids,
		}
		if err := a.saveLocked(); err != nil {
			return gen.PuppetAgentRevertToResp{}, fmt.Errorf("puppet.agent.act.revert_to: persist idempotency entry failed: %w", err)
		}
	}
	return gen.PuppetAgentRevertToResp{Result: result}, nil
}

// revertToLocked is the revert core. The caller must hold a.mu. Invalid
// targets are rejected with an error: empty ids, unknown revisions,
// revisions belonging to another document, and legacy revisions that predate
// snapshot recording (they cannot be rebuilt deterministically).
func (a *Actor) revertToLocked(ctx actor.PureContext, req gen.PuppetDocumentRevertToReq, auditSource string) (gen.PuppetDocumentRevertToResp, error) {
	if req.RevisionID == "" {
		return gen.PuppetDocumentRevertToResp{}, fmt.Errorf("puppet.document.revert_to: RevisionId must not be empty")
	}

	var target gen.PuppetRevision
	found := false
	for _, r := range a.state.Revisions {
		if r.ID == req.RevisionID {
			target = r
			found = true
			break
		}
	}
	if !found {
		return gen.PuppetDocumentRevertToResp{}, fmt.Errorf("puppet.document.revert_to: revision %q not found", req.RevisionID)
	}
	if target.DocumentID != a.state.Document.ID {
		return gen.PuppetDocumentRevertToResp{}, fmt.Errorf("puppet.document.revert_to: revision %q belongs to document %q, not the current document %q", req.RevisionID, target.DocumentID, a.state.Document.ID)
	}
	snap, ok := a.state.Snapshots[req.RevisionID]
	if !ok {
		return gen.PuppetDocumentRevertToResp{}, fmt.Errorf("puppet.document.revert_to: revision %q (sequence %d) predates per-revision snapshots and cannot be reverted to", req.RevisionID, target.Sequence)
	}

	// Idempotent no-op: the document already carries the target state (e.g.
	// reverting to the current head, or repeating a revert). Return Applied
	// with the current head and append nothing, so repeated reverts do not
	// spam the history.
	if documentContentKey(a.state.Document) == documentContentKey(snap) {
		head := a.state.Revisions[len(a.state.Revisions)-1]
		return gen.PuppetDocumentRevertToResp{
			Applied:  true,
			Document: cloneDocument(a.state.Document),
			Revision: head,
			Detail:   fmt.Sprintf("revert_to %s: document already carries the target state; no revision appended", req.RevisionID),
		}, nil
	}

	// Rebuild: restore a clone of the recorded snapshot, then record the
	// revert itself as the new head. The restored document keeps the CURRENT
	// revision counter so appendRevisionLocked continues the monotonic climb
	// (the snapshot's own counter is historical). appendRevisionLocked then
	// bumps the counter, stamps UpdatedAt, and snapshots the new state, so
	// the revert is itself revertible (redo).
	currentSeq := a.state.Document.Revision
	a.state.Document = cloneDocument(snap)
	a.state.Document.Revision = currentSeq
	detail := fmt.Sprintf("revert_to %s (sequence %d, %s by %s)", target.ID, target.Sequence, target.CommandKind, target.Author)
	rev := a.appendRevisionLocked(cmdRevertTo, authorFromContext(ctx), auditSource, detail)

	// Every node in the restored tree may have changed; mark them so the
	// frontend refreshes the whole projection.
	affected := collectNodeGuids(&a.state.Document.Root)
	rev.AffectedGuids = affected
	a.state.Revisions[len(a.state.Revisions)-1].AffectedGuids = affected

	if err := a.saveLocked(); err != nil {
		return gen.PuppetDocumentRevertToResp{}, fmt.Errorf("puppet.document.revert_to: persist failed: %w", err)
	}

	return gen.PuppetDocumentRevertToResp{
		Applied:  true,
		Document: cloneDocument(a.state.Document),
		Revision: rev,
		Detail:   detail,
	}, nil
}

// cachedRevertRespLocked rebuilds the replay response for a cached agent
// revert RequestId from the immutable revision it produced plus the snapshot
// recorded at that revision. The caller must hold a.mu. It reports ok=false
// when the cache entry cannot be reconstructed (should not happen for
// revisions appended after snapshot recording began); the caller then falls
// through to a fresh apply.
func (a *Actor) cachedRevertRespLocked(pr editResult) (gen.PuppetDocumentRevertToResp, bool) {
	var rev gen.PuppetRevision
	found := false
	for _, r := range a.state.Revisions {
		if r.ID == pr.RevisionID {
			rev = r
			found = true
			break
		}
	}
	if !found {
		return gen.PuppetDocumentRevertToResp{}, false
	}
	doc, ok := a.state.Snapshots[pr.RevisionID]
	if !ok {
		return gen.PuppetDocumentRevertToResp{}, false
	}
	return gen.PuppetDocumentRevertToResp{
		Applied:  true,
		Document: cloneDocument(doc),
		Revision: rev,
		Detail:   pr.Detail,
	}, true
}

// documentContentKey returns a stable content fingerprint of a document for
// idempotent revert no-op detection. The monotonic revision counter and the
// UpdatedAt bookkeeping stamp differ between "the state at revision N" and
// "the same state restored as revision M", so both are zeroed before
// marshalling; everything else (name, node tree, params, meshes, textures)
// participates.
func documentContentKey(doc gen.PuppetDocument) string {
	d := doc
	d.Revision = 0
	d.UpdatedAt = ""
	b, err := json.Marshal(d)
	if err != nil {
		// PuppetDocument is a plain JSON-tagged struct; marshal cannot fail.
		return ""
	}
	return string(b)
}

// collectNodeGuids gathers the guids of every node in the subtree in
// deterministic pre-order.
func collectNodeGuids(n *gen.PuppetNode) []string {
	if n == nil {
		return nil
	}
	guids := []string{n.Guid}
	for i := range n.Children {
		guids = append(guids, collectNodeGuids(&n.Children[i])...)
	}
	return guids
}
