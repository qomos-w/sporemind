package puppeteditor

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// applyEdit is a helper that applies one successful whitelisted edit and
// returns the appended revision.
func applyEdit(t *testing.T, a *Actor, requestID, target, name string) gen.PuppetRevision {
	t.Helper()
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdSetNodeName, TargetGuid: target, RequestID: requestID,
		Params: map[string]string{"name": name},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Applied {
		t.Fatalf("edit %s not applied: %s", requestID, resp.Detail)
	}
	return resp.Revision
}

func nodeByName(a *Actor, guid string) *gen.PuppetNode {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return findNode(&a.state.Document.Root, guid)
}

func revisionCount(a *Actor) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.state.Revisions)
}

func TestRevertTo_RestoresSnapshotStateAndAppendsHead(t *testing.T) {
	a := editTestActor(t)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	applyEdit(t, a, "r2", "face", "Second")
	if got := nodeByName(a, "face").Name; got != "Second" {
		t.Fatalf("precondition: face name=%q, want Second", got)
	}

	resp, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Applied {
		t.Fatalf("revert not applied: %s", resp.Detail)
	}
	if got := nodeByName(a, "face").Name; got != "First" {
		t.Fatalf("after revert face name=%q, want First", got)
	}

	// New head revision records the revert: kind, parent = previous head,
	// and the document counter keeps climbing monotonically.
	if resp.Revision.CommandKind != cmdRevertTo {
		t.Fatalf("revision kind=%q, want %q", resp.Revision.CommandKind, cmdRevertTo)
	}
	if resp.Revision.ParentRevisionID == "" || strings.Contains(resp.Revision.ParentRevisionID, resp.Revision.ID) {
		t.Fatalf("revert revision parent=%q must link to the previous head", resp.Revision.ParentRevisionID)
	}
	if resp.Document.Revision != resp.Revision.Sequence {
		t.Fatalf("document revision %d must equal new head sequence %d", resp.Document.Revision, resp.Revision.Sequence)
	}
	if resp.Document.Revision <= 2 {
		t.Fatalf("document revision counter %d must stay monotonic (was 2 before revert)", resp.Document.Revision)
	}

	// History is never deleted: init + 2 edits + revert all present.
	a.mu.RLock()
	defer a.mu.RUnlock()
	if len(a.state.Revisions) != 4 {
		t.Fatalf("revision count=%d, want 4 (history must be append-only)", len(a.state.Revisions))
	}
	// AffectedGuids covers the restored tree so the frontend can refresh.
	if len(resp.Revision.AffectedGuids) == 0 {
		t.Fatal("revert revision carries no AffectedGuids")
	}
}

func TestRevertTo_IdempotentNoOpOnRepeat(t *testing.T) {
	a := editTestActor(t)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	applyEdit(t, a, "r2", "face", "Second")

	first, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev1.ID})
	if err != nil || !first.Applied {
		t.Fatalf("first revert failed: %v %+v", err, first)
	}
	countAfterFirst := revisionCount(a)
	keyAfterFirst := documentContentKey(func() gen.PuppetDocument {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.state.Document
	}())

	second, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Applied {
		t.Fatalf("repeated revert not applied: %s", second.Detail)
	}
	if got := revisionCount(a); got != countAfterFirst {
		t.Fatalf("repeated revert appended a revision: %d -> %d", countAfterFirst, got)
	}
	if second.Revision.ID != first.Revision.ID {
		t.Fatalf("repeated revert returned head %q, want unchanged %q", second.Revision.ID, first.Revision.ID)
	}
	if !strings.Contains(second.Detail, "no revision appended") {
		t.Fatalf("no-op detail=%q must explain that nothing was appended", second.Detail)
	}
	if key := documentContentKey(func() gen.PuppetDocument {
		a.mu.RLock()
		defer a.mu.RUnlock()
		return a.state.Document
	}()); key != keyAfterFirst {
		t.Fatal("repeated revert changed the document content")
	}
}

func TestRevertTo_CurrentHeadIsNoOp(t *testing.T) {
	a := editTestActor(t)
	head := applyEdit(t, a, "r1", "face", "First")
	before := revisionCount(a)

	resp, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: head.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Applied {
		t.Fatalf("revert-to-head not applied: %s", resp.Detail)
	}
	if got := revisionCount(a); got != before {
		t.Fatalf("revert-to-head appended a revision: %d -> %d", before, got)
	}
	if resp.Revision.ID != head.ID {
		t.Fatalf("returned revision %q, want current head %q", resp.Revision.ID, head.ID)
	}
}

func TestRevertTo_RedoForwardViaSecondRevert(t *testing.T) {
	a := editTestActor(t)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	rev2 := applyEdit(t, a, "r2", "face", "Second")

	// Undo: back to First.
	if _, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev1.ID}); err != nil {
		t.Fatal(err)
	}
	if got := nodeByName(a, "face").Name; got != "First" {
		t.Fatalf("undo: face=%q, want First", got)
	}
	// Redo: forward to Second through a new revert revision (not by deleting
	// history).
	if _, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev2.ID}); err != nil {
		t.Fatal(err)
	}
	if got := nodeByName(a, "face").Name; got != "Second" {
		t.Fatalf("redo: face=%q, want Second", got)
	}
	if got := revisionCount(a); got != 5 {
		t.Fatalf("revision count=%d, want 5 (init + 2 edits + undo + redo)", got)
	}
}

func TestRevertTo_InvalidRevisionRejected(t *testing.T) {
	a := editTestActor(t)
	applyEdit(t, a, "r1", "face", "First")

	cases := []struct {
		name string
		rev  string
		want string
	}{
		{"empty id", "", "RevisionId must not be empty"},
		{"unknown id", "rev_missing", "not found"},
		{"foreign document", "rev-foreign", "belongs to document"},
	}
	for _, tc := range cases {
		if tc.name == "foreign document" {
			a.mu.Lock()
			a.state.Revisions = append(a.state.Revisions, gen.PuppetRevision{
				ID: "rev-foreign", DocumentID: "doc-other", Sequence: 99, CommandKind: "set_node_name", Timestamp: "2026-08-14T05:00:00Z",
			})
			a.mu.Unlock()
		}
		resp, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: tc.rev})
		if err == nil || resp.Applied {
			t.Fatalf("%s: revert accepted (err=%v applied=%v)", tc.name, err, resp.Applied)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %q does not mention %q", tc.name, err.Error(), tc.want)
		}
	}
	// init + one edit + the foreign revision appended by the fixture above;
	// none of the rejected reverts may add anything.
	if got := revisionCount(a); got != 3 {
		t.Fatalf("rejected reverts must not append revisions, got %d", got)
	}
}

func TestRevertTo_LegacyRevisionWithoutSnapshotRejected(t *testing.T) {
	a := editTestActor(t)
	// seedForEditTest's init revision predates snapshot recording, exactly
	// like state persisted before this feature: revert to it must fail loudly
	// rather than guess.
	resp, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: "rev-init"})
	if err == nil || resp.Applied {
		t.Fatal("revert to snapshot-less revision accepted")
	}
	if !strings.Contains(err.Error(), "predates") {
		t.Fatalf("error %q must explain the missing snapshot", err.Error())
	}
}

func TestRevertTo_SnapshotsPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "revert-restart"}
	seedForEditTest(a)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	applyEdit(t, a, "r2", "face", "Second")
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	// Fresh actor instance restores from the same store; snapshots must
	// survive so undo still works after a restart.
	b := &Actor{store: persist.NewFSPersist(dir), actorID: "revert-restart"}
	if err := b.Load(); err != nil {
		t.Fatal(err)
	}
	resp, err := b.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: rev1.ID})
	if err != nil || !resp.Applied {
		t.Fatalf("revert after restart failed: %v", err)
	}
	if got := nodeByName(b, "face").Name; got != "First" {
		t.Fatalf("after restart revert face=%q, want First", got)
	}
}

func TestAgentRevertTo_AuditsSourceAndReplayIsIdempotent(t *testing.T) {
	a := editTestActor(t)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	applyEdit(t, a, "r2", "face", "Second")
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	req := gen.PuppetAgentRevertToReq{
		Envelope:   gen.PuppetAgentRequest{TargetGuid: "doc-edit", RequestID: "rev-req-1", AuditSource: "tool.puppet"},
		RevisionID: rev1.ID,
	}

	resp, err := a.handleAgentRevertTo(ctx, req)
	if err != nil || !resp.Result.Applied {
		t.Fatalf("agent revert failed: %v %+v", err, resp.Result)
	}
	if resp.Result.Revision.AuditSource != req.Envelope.AuditSource {
		t.Fatalf("AuditSource=%q, want %q", resp.Result.Revision.AuditSource, req.Envelope.AuditSource)
	}
	if resp.Result.Revision.Author != "agent-1" {
		t.Fatalf("Author=%q, want agent subject", resp.Result.Revision.Author)
	}
	countAfter := revisionCount(a)

	// Same RequestId replay: cached result, identical revision, nothing appended.
	replay, err := a.handleAgentRevertTo(ctx, req)
	if err != nil || !replay.Result.Applied {
		t.Fatalf("agent revert replay failed: %v", err)
	}
	if replay.Result.Revision.ID != resp.Result.Revision.ID {
		t.Fatalf("replay revision %q != original %q", replay.Result.Revision.ID, resp.Result.Revision.ID)
	}
	if got := revisionCount(a); got != countAfter {
		t.Fatalf("replay appended a revision: %d -> %d", countAfter, got)
	}
}

func TestAgentRevertTo_PolicyDeniedForNonAgent(t *testing.T) {
	a := editTestActor(t)
	rev1 := applyEdit(t, a, "r1", "face", "First")
	env := gen.PuppetAgentRequest{TargetGuid: "doc-edit", RequestID: "rev-req-2", AuditSource: "tool.puppet"}
	anon := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleAgentRevertTo(anon, gen.PuppetAgentRevertToReq{Envelope: env, RevisionID: rev1.ID}); err == nil {
		t.Fatal("anonymous caller reached agent revert")
	}
	human := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleAgentRevertTo(human, gen.PuppetAgentRevertToReq{Envelope: env, RevisionID: rev1.ID}); err == nil {
		t.Fatal("human caller reached agent revert")
	}
	agent := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	badEnv := env
	badEnv.RequestID = ""
	if _, err := a.handleAgentRevertTo(agent, gen.PuppetAgentRevertToReq{Envelope: badEnv, RevisionID: rev1.ID}); err == nil {
		t.Fatal("envelope without RequestId accepted")
	}
}

// TestRevertTo_RevertedMeshAndTextureStateSurvives checks the snapshot restore
// covers the whole document payload, not just scalar fields: an explicitly set
// mesh disappears when reverting past the revision that set it.
func TestRevertTo_RevertedMeshDisappears(t *testing.T) {
	a := editTestActor(t)
	before := applyEdit(t, a, "r1", "face", "First")
	meshJSON := `{"Vertices":[0,0,10,0,0,10],"Indices":[0,1,2],"UVs":[0,0,1,0,0,1]}`
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdSetNodeMesh, TargetGuid: "face", RequestID: "r2", Params: map[string]string{"mesh": meshJSON},
	}})
	if err != nil || !resp.Applied {
		t.Fatalf("set_node_mesh failed: %v %s", err, resp.Detail)
	}
	if nodeByName(a, "face").Mesh == nil {
		t.Fatal("precondition: mesh not stored")
	}

	if _, err := a.handleRevertTo(nil, gen.PuppetDocumentRevertToReq{RevisionID: before.ID}); err != nil {
		t.Fatal(err)
	}
	if nodeByName(a, "face").Mesh != nil {
		t.Fatal("revert did not restore the pre-mesh state")
	}
}
