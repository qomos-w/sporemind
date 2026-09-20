package puppeteditor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

func assetTestActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "puppet-asset-test"}
	seedForEditTest(a)
	return a
}

func stageTexture(t *testing.T, a *Actor) gen.PuppetStagedAsset {
	t.Helper()
	resp, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{Kind: assetKindTexture, DataRef: "sha256:texture", Name: "face"})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	return resp.Asset
}

func TestAssetStageListAndPersistence(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-asset-persist"}
	seedForEditTest(a)

	staged, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{Kind: assetKindTexture, DataRef: "sha256:texture", SourceRef: "upload"})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if staged.Asset.State != assetStateStaged || staged.Asset.DocumentID != "doc-edit" || staged.Asset.CreatedAt == "" {
		t.Fatalf("staged asset = %+v", staged.Asset)
	}
	listed, err := a.handleAssetList(nil, gen.PuppetAssetListReq{State: assetStateStaged})
	if err != nil || len(listed.Assets) != 1 || listed.Assets[0].ID != staged.Asset.ID {
		t.Fatalf("list staged = %+v, %v", listed, err)
	}
	b := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-asset-persist"}
	if err := b.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.state.Assets) != 1 || b.state.Assets[0].State != assetStateStaged {
		t.Fatalf("restored assets = %+v", b.state.Assets)
	}
}

func TestAssetCommitRequiresExplicitNodeAndMutatesOnlyOnSuccess(t *testing.T) {
	a := assetTestActor(t)
	asset := stageTexture(t, a)

	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: asset.ID}); err == nil {
		t.Fatal("commit with empty NodeGuid succeeded")
	}
	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: asset.ID, NodeGuid: "missing"}); err == nil {
		t.Fatal("commit to missing node succeeded")
	}
	a.mu.RLock()
	if a.state.Assets[0].State != assetStateStaged || findNode(&a.state.Document.Root, "face").TextureAssetID != "" || len(a.state.Revisions) != 1 {
		t.Fatalf("failed commit mutated state: %+v", a.state)
	}
	a.mu.RUnlock()

	resp, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: asset.ID, NodeGuid: "face"})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if resp.Asset.State != assetStateCommitted || resp.Asset.NodeGuid != "face" || resp.Asset.ReviewedAt == "" {
		t.Fatalf("committed asset = %+v", resp.Asset)
	}
	if resp.Revision.CommandKind != "commit_asset" || resp.Revision.Sequence != 1 || resp.Revision.ParentRevisionID != "rev-init" {
		t.Fatalf("commit revision = %+v", resp.Revision)
	}
	if got := findNode(&a.state.Document.Root, "face").TextureAssetID; got != asset.ID {
		t.Errorf("face texture = %q, want %q", got, asset.ID)
	}
	if a.state.Document.Revision != 1 || len(a.state.Revisions) != 2 {
		t.Fatalf("document/revisions out of sync: revision=%d log=%d", a.state.Document.Revision, len(a.state.Revisions))
	}
}

func TestAssetRejectOnlyChangesAssetAndAuditsRevision(t *testing.T) {
	a := assetTestActor(t)
	asset := stageTexture(t, a)
	before := cloneDocument(a.state.Document)

	resp, err := a.handleAssetReject(nil, gen.PuppetAssetRejectReq{AssetID: asset.ID, Reason: "wrong crop"})
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if resp.Asset.State != assetStateRejected || resp.Asset.ReviewReason != "wrong crop" || resp.Asset.ReviewedAt == "" {
		t.Fatalf("rejected asset = %+v", resp.Asset)
	}
	if got := findNode(&a.state.Document.Root, "face").TextureAssetID; got != before.Root.Children[0].TextureAssetID {
		t.Errorf("reject changed node texture: %q", got)
	}
	if a.state.Document.Revision != 1 || len(a.state.Revisions) != 2 {
		t.Fatalf("reject did not append exactly one audit revision: document=%d log=%d", a.state.Document.Revision, len(a.state.Revisions))
	}
	rev := a.state.Revisions[1]
	if rev.CommandKind != "reject_asset" || rev.ParentRevisionID != "rev-init" {
		t.Fatalf("reject revision = %+v", rev)
	}
}

func TestAssetTerminalTransitionsAreRejected(t *testing.T) {
	a := assetTestActor(t)
	committed := stageTexture(t, a)
	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: committed.ID, NodeGuid: "face"}); err != nil {
		t.Fatalf("initial commit: %v", err)
	}
	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: committed.ID, NodeGuid: "face"}); err == nil {
		t.Fatal("duplicate commit succeeded")
	}
	if _, err := a.handleAssetReject(nil, gen.PuppetAssetRejectReq{AssetID: committed.ID}); err == nil {
		t.Fatal("reject after commit succeeded")
	}

	rejected := stageTexture(t, a)
	if _, err := a.handleAssetReject(nil, gen.PuppetAssetRejectReq{AssetID: rejected.ID}); err != nil {
		t.Fatalf("initial reject: %v", err)
	}
	if _, err := a.handleAssetReject(nil, gen.PuppetAssetRejectReq{AssetID: rejected.ID}); err == nil {
		t.Fatal("duplicate reject succeeded")
	}
	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: rejected.ID, NodeGuid: "face"}); err == nil {
		t.Fatal("commit after reject succeeded")
	}
	if len(a.state.Revisions) != 3 || a.state.Assets[0].State != assetStateCommitted || a.state.Assets[1].State != assetStateRejected {
		t.Fatalf("illegal transition mutated state: %+v", a.state)
	}
}

func TestAssetConcurrentCommitHasOneWinner(t *testing.T) {
	a := assetTestActor(t)
	asset := stageTexture(t, a)
	const callers = 16
	var wg sync.WaitGroup
	results := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: asset.ID, NodeGuid: "face"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent commits = %d, want 1", successes)
	}
	if a.state.Assets[0].State != assetStateCommitted || len(a.state.Revisions) != 2 || a.state.Document.Revision != 1 {
		t.Fatalf("concurrent commit state = %+v", a.state)
	}
}

func TestAssetRejectPersistsWithRevision(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-asset-reject-persist"}
	seedForEditTest(a)
	asset := stageTexture(t, a)
	if _, err := a.handleAssetReject(nil, gen.PuppetAssetRejectReq{AssetID: asset.ID, Reason: "unused"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	b := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-asset-reject-persist"}
	if err := b.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.state.Assets) != 1 || b.state.Assets[0].State != assetStateRejected || b.state.Assets[0].ReviewReason != "unused" {
		t.Fatalf("restored asset = %+v", b.state.Assets)
	}
	if b.state.Document.Revision != 1 || len(b.state.Revisions) != 2 || b.state.Revisions[1].CommandKind != "reject_asset" {
		t.Fatalf("restored audit state = %+v", b.state)
	}
}

// ---------------------------------------------------------------------------
// puppet.asset.read — DataRef → restricted content read with sha256 verification
// ---------------------------------------------------------------------------

// readTestActor returns a seeded actor backed by a temp project root with the
// generated-assets directory already created. The project root is wired into
// projectRoots so handleAssetRead can locate generated files.
func readTestActor(t *testing.T) (*Actor, string) {
	t.Helper()
	root := t.TempDir()
	genDir := filepath.Join(root, "assets", "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "asset-read-test", projectRoots: []string{root}}
	seedForEditTest(a)
	return a, root
}

// stageAndCommitTexture stages a texture asset with a real generated file on
// disk and commits it to the "face" node. Returns the committed asset.
func stageAndCommitTexture(t *testing.T, a *Actor, root, rel string, content []byte) gen.PuppetStagedAsset {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash("assets/generated/"+rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Use the agent bridge to stage (computes DataRef from file content).
	stageResp, err := a.handleAgentAssetStage(agentStageCtx(), gen.PuppetAgentAssetStageReq{
		Envelope: gen.PuppetAgentRequest{
			TargetGuid:  "doc-edit",
			RequestID:   "req-stage-" + rel,
			AuditSource: "tool.image-gen",
		},
		Operation: gen.PuppetAssetStageReq{
			Kind:             assetKindTexture,
			SourceRef:        "assets/generated/" + rel,
			GenParamsSummary: `{"prompt":"test"}`,
		},
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Commit to the "face" node.
	commitResp, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{
		AssetID:  stageResp.Result.Asset.ID,
		NodeGuid: "face",
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return commitResp.Asset
}

func TestAssetRead_ReturnsDataURLForCommittedAsset(t *testing.T) {
	a, root := readTestActor(t)
	content := []byte("fake-png-bytes-for-face")
	asset := stageAndCommitTexture(t, a, root, "face_01.png", content)

	resp, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: asset.ID})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.AssetID != asset.ID {
		t.Errorf("AssetId = %q, want %q", resp.AssetID, asset.ID)
	}
	if !strings.HasPrefix(resp.Uri, "data:image/png;base64,") {
		t.Errorf("Uri = %q, want data:image/png;base64,...", resp.Uri)
	}
	if resp.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", resp.MimeType)
	}
	// The data URL must not expose the raw file-system path.
	if strings.Contains(resp.Uri, root) || strings.Contains(resp.Uri, "assets/generated") {
		t.Errorf("Uri leaks file-system path: %q", resp.Uri)
	}
}

func TestAssetRead_UnknownAssetIDRejected(t *testing.T) {
	a, _ := readTestActor(t)

	_, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: "nonexistent"})
	if err == nil {
		t.Fatal("read of unknown asset succeeded")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err)
	}
}

func TestAssetRead_StagedAssetRejected(t *testing.T) {
	a, root := readTestActor(t)
	content := []byte("staged-only")
	abs := filepath.Join(root, "assets/generated/staged.png")
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stageResp, err := a.handleAgentAssetStage(agentStageCtx(), gen.PuppetAgentAssetStageReq{
		Envelope: gen.PuppetAgentRequest{
			TargetGuid:  "doc-edit",
			RequestID:   "req-staged",
			AuditSource: "tool.image-gen",
		},
		Operation: gen.PuppetAssetStageReq{
			Kind:             assetKindTexture,
			SourceRef:        "assets/generated/staged.png",
			GenParamsSummary: `{"prompt":"staged"}`,
		},
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	// Asset is staged (not committed) — read must be rejected.
	_, err = a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: stageResp.Result.Asset.ID})
	if err == nil {
		t.Fatal("read of staged asset succeeded")
	}
	if !strings.Contains(err.Error(), "not committed") {
		t.Errorf("error = %q, want 'not committed'", err)
	}
}

func TestAssetRead_NoProjectRootsRejected(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "asset-read-noroots", projectRoots: nil}
	seedForEditTest(a)
	// Manually insert a committed asset with a SourceRef so the lookup
	// reaches the roots check.
	a.mu.Lock()
	a.state.Assets = append(a.state.Assets, gen.PuppetStagedAsset{
		ID:        "asset-committed-noroots",
		DocumentID: "doc-edit",
		State:     assetStateCommitted,
		Kind:      assetKindTexture,
		DataRef:   "sha256:abc",
		SourceRef: "assets/generated/img.png",
		NodeGuid:  "face",
		CreatedAt: "2026-08-14T05:00:00Z",
	})
	a.mu.Unlock()

	_, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: "asset-committed-noroots"})
	if err == nil {
		t.Fatal("read with no project roots succeeded")
	}
}

func TestAssetRead_PathTraversalRejected(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "asset-read-traversal", projectRoots: []string{t.TempDir()}}
	seedForEditTest(a)
	// Manually insert a committed asset with a traversal SourceRef. The
	// resolveGeneratedFilePath check must reject it before any file is read.
	a.mu.Lock()
	a.state.Assets = append(a.state.Assets, gen.PuppetStagedAsset{
		ID:        "asset-traversal",
		DocumentID: "doc-edit",
		State:     assetStateCommitted,
		Kind:      assetKindTexture,
		DataRef:   "sha256:abc",
		SourceRef: "assets/generated/../../../etc/passwd",
		NodeGuid:  "face",
		CreatedAt: "2026-08-14T05:00:00Z",
	})
	a.mu.Unlock()

	_, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: "asset-traversal"})
	if err == nil {
		t.Fatal("read with traversal path succeeded")
	}
	if !strings.Contains(err.Error(), "traversal") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want traversal or not-found rejection", err)
	}
}

func TestAssetRead_Sha256MismatchRejected(t *testing.T) {
	a, root := readTestActor(t)
	content := []byte("original-content")
	asset := stageAndCommitTexture(t, a, root, "hash_mismatch.png", content)

	// Tamper with the file on disk after commit.
	abs := filepath.Join(root, "assets/generated/hash_mismatch.png")
	if err := os.WriteFile(abs, []byte("tampered-content"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: asset.ID})
	if err == nil {
		t.Fatal("read with sha256 mismatch succeeded")
	}
	if !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("error = %q, want 'hash mismatch'", err)
	}
}

func TestAssetRead_MissingFileRejected(t *testing.T) {
	a, root := readTestActor(t)
	content := []byte("temp-file")
	asset := stageAndCommitTexture(t, a, root, "temp_file.png", content)

	// Delete the file after commit.
	abs := filepath.Join(root, "assets/generated/temp_file.png")
	if err := os.Remove(abs); err != nil {
		t.Fatalf("remove: %v", err)
	}

	_, err := a.handleAssetRead(nil, gen.PuppetAssetReadReq{AssetID: asset.ID})
	if err == nil {
		t.Fatal("read of missing file succeeded")
	}
}

// ---------------------------------------------------------------------------
// handleAssetCommit — AffectedGuids on revision + snapshot contains texture
// ---------------------------------------------------------------------------

func TestAssetCommit_RevisionRecordsAffectedGuids(t *testing.T) {
	a, root := readTestActor(t)
	asset := stageAndCommitTexture(t, a, root, "affected.png", []byte("affected"))

	// The revision returned by commit must carry AffectedGuids.
	commitResp, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{
		AssetID:  asset.ID,
		NodeGuid: "face",
	})
	// This asset is already committed, so this should fail. Instead, stage
	// a fresh asset and commit it.
	_ = commitResp
	_ = err

	// Stage a second asset for a fresh commit.
	abs := filepath.Join(root, "assets/generated/affected2.png")
	if err := os.WriteFile(abs, []byte("affected2"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	stageResp, err := a.handleAgentAssetStage(agentStageCtx(), gen.PuppetAgentAssetStageReq{
		Envelope: gen.PuppetAgentRequest{
			TargetGuid:  "doc-edit",
			RequestID:   "req-affected2",
			AuditSource: "tool.image-gen",
		},
		Operation: gen.PuppetAssetStageReq{
			Kind:             assetKindTexture,
			SourceRef:        "assets/generated/affected2.png",
			GenParamsSummary: `{"prompt":"test2"}`,
		},
	})
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	resp, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{
		AssetID:  stageResp.Result.Asset.ID,
		NodeGuid: "face",
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if len(resp.Revision.AffectedGuids) != 1 || resp.Revision.AffectedGuids[0] != "face" {
		t.Fatalf("Revision.AffectedGuids = %v, want [face]", resp.Revision.AffectedGuids)
	}

	// The revision in the persisted state must also carry AffectedGuids.
	a.mu.RLock()
	lastRev := a.state.Revisions[len(a.state.Revisions)-1]
	a.mu.RUnlock()
	if len(lastRev.AffectedGuids) != 1 || lastRev.AffectedGuids[0] != "face" {
		t.Fatalf("persisted revision AffectedGuids = %v, want [face]", lastRev.AffectedGuids)
	}
}

func TestAssetCommit_SnapshotContainsTexture(t *testing.T) {
	a, root := readTestActor(t)
	asset := stageAndCommitTexture(t, a, root, "snap_texture.png", []byte("snap"))

	// Take a snapshot and verify the node carries the committed texture.
	snapResp, err := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	faceNode := findNode(&snapResp.Snapshot.Document.Root, "face")
	if faceNode == nil {
		t.Fatal("face node not found in snapshot")
	}
	if faceNode.TextureAssetID != asset.ID {
		t.Errorf("snapshot face texture = %q, want %q", faceNode.TextureAssetID, asset.ID)
	}
}
