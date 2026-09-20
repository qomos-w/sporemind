package puppeteditor

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// TestIntegration_VerticalSlice exercises the complete editor flow end-to-end
// through the actor's public callables, simulating the acceptance scenario:
//
//  1. Read the persisted document snapshot (bootstrapped by OnInit).
//  2. Submit a whitelisted edit (set_node_name) → revision appended.
//  3. Submit another whitelisted edit (set_node_z) → revision appended.
//  4. Stage a texture asset → asset enters "staged" state.
//  5. Commit the staged asset to an explicit node → document mutated +
//     revision appended.
//  6. Read the revision log → verify the full chain order and integrity.
//  7. Read the final snapshot → verify all mutations are reflected.
//
// This mirrors the user-visible flow: open editor → read document → edit →
// stage material → commit to node → see revision and document refresh.
func TestIntegration_VerticalSlice(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-integration"}

	// Bootstrap: simulate OnInit creating a default document.
	a.mu.Lock()
	a.state = puppetState{
		Document: gen.PuppetDocument{
			ID:       "doc-int",
			Revision: 0,
			Name:     "Integration",
			Root: gen.PuppetNode{
				Guid: "root", Name: "Root", Kind: "group", Enabled: true,
				Children: []gen.PuppetNode{
					{Guid: "body", Name: "Body", Kind: "part", Enabled: true, Z: 0, Children: []gen.PuppetNode{}},
					{Guid: "head", Name: "Head", Kind: "part", Enabled: true, Z: 1, Children: []gen.PuppetNode{}},
				},
			},
			CreatedAt: "2026-08-14T05:00:00Z",
			UpdatedAt: "2026-08-14T05:00:00Z",
		},
		Revisions: []gen.PuppetRevision{
			{ID: "rev-init", DocumentID: "doc-int", Sequence: 0, ParentRevisionID: "", Author: "system", CommandKind: "init", Timestamp: "2026-08-14T05:00:00Z"},
		},
		Assets:            []gen.PuppetStagedAsset{},
		ProcessedRequests: map[string]editResult{},
	}
	a.mu.Unlock()

	// ── Step 1: Read snapshot ──────────────────────────────────────────
	snapResp, err := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snap := snapResp.Snapshot
	if snap.Document.ID != "doc-int" {
		t.Fatalf("snapshot doc id = %q, want doc-int", snap.Document.ID)
	}
	if snap.RevisionCount != 1 {
		t.Errorf("snapshot revision count = %d, want 1", snap.RevisionCount)
	}
	if snap.StagedAssetCount != 0 {
		t.Errorf("snapshot staged asset count = %d, want 0", snap.StagedAssetCount)
	}

	// ── Step 2: Whitelist edit — set_node_name ─────────────────────────
	editResp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "head",
			Params:     map[string]string{"name": "Crown"},
		},
	})
	if err != nil {
		t.Fatalf("edit set_node_name: %v", err)
	}
	if !editResp.Applied {
		t.Fatalf("set_node_name not applied: %s", editResp.Detail)
	}
	if editResp.Revision.Sequence != 1 {
		t.Errorf("set_node_name revision sequence = %d, want 1", editResp.Revision.Sequence)
	}
	if editResp.Revision.CommandKind != "set_node_name" {
		t.Errorf("set_node_name revision kind = %q", editResp.Revision.CommandKind)
	}

	// ── Step 3: Whitelist edit — set_node_z ────────────────────────────
	editResp2, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeZ,
			TargetGuid: "head",
			Params:     map[string]string{"z": "5"},
		},
	})
	if err != nil {
		t.Fatalf("edit set_node_z: %v", err)
	}
	if !editResp2.Applied {
		t.Fatalf("set_node_z not applied: %s", editResp2.Detail)
	}
	if editResp2.Revision.Sequence != 2 {
		t.Errorf("set_node_z revision sequence = %d, want 2", editResp2.Revision.Sequence)
	}

	// Verify both edits are reflected in the document.
	headNode := findNode(&a.state.Document.Root, "head")
	if headNode.Name != "Crown" {
		t.Errorf("head name = %q, want Crown", headNode.Name)
	}
	if headNode.Z != 5 {
		t.Errorf("head z = %d, want 5", headNode.Z)
	}

	// ── Step 4: Stage a texture asset ──────────────────────────────────
	stageResp, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{
		Kind:             assetKindTexture,
		DataRef:          "sha256:abcd1234",
		Name:             "Generated Hair",
		Width:            512,
		Height:           512,
		SourceRef:        "gen-req-99",
		GenParamsSummary: "prompt=anime hair, model=gpt-image, size=512x512",
	})
	if err != nil {
		t.Fatalf("stage asset: %v", err)
	}
	stagedAsset := stageResp.Asset
	if stagedAsset.State != assetStateStaged {
		t.Errorf("staged asset state = %q, want staged", stagedAsset.State)
	}
	if stagedAsset.DataRef != "sha256:abcd1234" {
		t.Errorf("staged asset dataref = %q", stagedAsset.DataRef)
	}

	// ── Step 5: Commit the staged asset to an explicit node ────────────
	commitResp, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{
		AssetID:  stagedAsset.ID,
		NodeGuid: "head",
	})
	if err != nil {
		t.Fatalf("commit asset: %v", err)
	}
	if commitResp.Asset.State != assetStateCommitted {
		t.Errorf("committed asset state = %q, want committed", commitResp.Asset.State)
	}
	if commitResp.Asset.NodeGuid != "head" {
		t.Errorf("committed asset node = %q, want head", commitResp.Asset.NodeGuid)
	}
	if commitResp.Revision.CommandKind != "commit_asset" {
		t.Errorf("commit revision kind = %q, want commit_asset", commitResp.Revision.CommandKind)
	}
	if commitResp.Revision.Sequence != 3 {
		t.Errorf("commit revision sequence = %d, want 3", commitResp.Revision.Sequence)
	}

	// Verify the node now references the committed asset.
	headNode = findNode(&a.state.Document.Root, "head")
	if headNode.TextureAssetID != stagedAsset.ID {
		t.Errorf("head texture asset id = %q, want %q", headNode.TextureAssetID, stagedAsset.ID)
	}

	// ── Step 6: Read the revision log ──────────────────────────────────
	revResp, err := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{})
	if err != nil {
		t.Fatalf("revision log: %v", err)
	}
	revs := revResp.Revisions
	if len(revs) != 4 {
		t.Fatalf("revision log length = %d, want 4", len(revs))
	}
	// Verify chain order and command kinds.
	wantKinds := []string{"init", "set_node_name", "set_node_z", "commit_asset"}
	for i, want := range wantKinds {
		if revs[i].CommandKind != want {
			t.Errorf("revision[%d] kind = %q, want %q", i, revs[i].CommandKind, want)
		}
	}
	// Verify the parent chain links are contiguous.
	for i := 1; i < len(revs); i++ {
		if revs[i].ParentRevisionID != revs[i-1].ID {
			t.Errorf("revision[%d] parent = %q, want %q", i, revs[i].ParentRevisionID, revs[i-1].ID)
		}
	}

	// ── Step 7: Read the final snapshot ────────────────────────────────
	snapResp2, err := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if err != nil {
		t.Fatalf("final snapshot: %v", err)
	}
	snap2 := snapResp2.Snapshot
	if snap2.Document.Revision != 3 {
		t.Errorf("final document revision = %d, want 3", snap2.Document.Revision)
	}
	if snap2.RevisionCount != 4 {
		t.Errorf("final revision count = %d, want 4", snap2.RevisionCount)
	}
	if snap2.StagedAssetCount != 0 {
		t.Errorf("final staged asset count = %d, want 0 (asset committed)", snap2.StagedAssetCount)
	}

	// Verify the committed name is reflected in the snapshot.
	snapHead := findNode(&snap2.Document.Root, "head")
	if snapHead == nil {
		t.Fatal("head node not found in final snapshot")
	}
	if snapHead.Name != "Crown" {
		t.Errorf("snapshot head name = %q, want Crown", snapHead.Name)
	}
	if snapHead.Z != 5 {
		t.Errorf("snapshot head z = %d, want 5", snapHead.Z)
	}
	if snapHead.TextureAssetID != stagedAsset.ID {
		t.Errorf("snapshot head texture = %q, want %q", snapHead.TextureAssetID, stagedAsset.ID)
	}

	// Verify snapshot is a deep copy — mutating it must not affect the actor.
	snapHead.Name = "Tampered"
	actorHead := findNode(&a.state.Document.Root, "head")
	if actorHead.Name != "Crown" {
		t.Errorf("actor head name = %q, want Crown (snapshot isolation failed)", actorHead.Name)
	}
}

// TestIntegration_EditRejection verifies that a rejected edit returns
// Applied=false without appending a revision, so the document and revision
// chain remain unchanged.
func TestIntegration_EditRejection(t *testing.T) {
	a := editTestActor(t)

	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "nonexistent",
			Params:     map[string]string{"name": "Ghost"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if resp.Applied {
		t.Error("edit to nonexistent node should not be applied")
	}
	if resp.Detail == "" {
		t.Error("rejected edit should have a non-empty Detail")
	}

	// Document revision must not have advanced.
	if a.state.Document.Revision != 0 {
		t.Errorf("document revision = %d, want 0", a.state.Document.Revision)
	}

	// Revision log must still have only the init entry.
	revResp, _ := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{})
	if len(revResp.Revisions) != 1 {
		t.Errorf("revision count = %d, want 1", len(revResp.Revisions))
	}
}

// TestIntegration_AssetRejectNoDocumentChange verifies that rejecting a
// staged asset marks it terminal without mutating the document node tree.
func TestIntegration_AssetRejectNoDocumentChange(t *testing.T) {
	a := editTestActor(t)

	// Stage an asset.
	stageResp, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{
		Kind:    assetKindTexture,
		DataRef: "sha256:rejectme",
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	// Reject it.
	_, err = a.handleAssetReject(nil, gen.PuppetAssetRejectReq{
		AssetID: stageResp.Asset.ID,
		Reason:  "wrong color",
	})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}

	// Asset must be rejected.
	listResp, _ := a.handleAssetList(nil, gen.PuppetAssetListReq{State: assetStateRejected})
	if len(listResp.Assets) != 1 {
		t.Fatalf("rejected assets = %d, want 1", len(listResp.Assets))
	}
	if listResp.Assets[0].ReviewReason != "wrong color" {
		t.Errorf("reject reason = %q, want wrong color", listResp.Assets[0].ReviewReason)
	}

	// No node should have a TextureAssetId set.
	root := a.state.Document.Root
	assertNoTextureAttached(t, &root)
}

func assertNoTextureAttached(t *testing.T, n *gen.PuppetNode) {
	t.Helper()
	if n.TextureAssetID != "" {
		t.Errorf("node %q has TextureAssetId %q after reject (should be empty)", n.Guid, n.TextureAssetID)
	}
	for i := range n.Children {
		assertNoTextureAttached(t, &n.Children[i])
	}
}
