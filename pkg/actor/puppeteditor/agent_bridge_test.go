package puppeteditor

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// agentStageCtx returns a context carrying a valid agent identity.
func agentStageCtx() *testutil.FakeCtx {
	return &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-img-1", Role: "agent"}}
}

// stageActor returns a seeded actor backed by a temp project root, with the
// generated-assets directory already created. The project root is wired into
// projectRoots so the agent bridge can locate generated files.
func stageActor(t *testing.T) (*Actor, string) {
	t.Helper()
	root := t.TempDir()
	genDir := filepath.Join(root, "assets", "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-stage", projectRoots: []string{root}}
	seedForEditTest(a)
	return a, root
}

// stageReq builds a well-formed PuppetAgentAssetStageReq carrying all required
// provenance fields for an image-gen bridge call. DataRef is intentionally left
// empty: the actor computes it from the file.
func stageReq(a *Actor, sourceRef, genSummary string) gen.PuppetAgentAssetStageReq {
	a.mu.RLock()
	docID := a.state.Document.ID
	a.mu.RUnlock()
	return gen.PuppetAgentAssetStageReq{
		Envelope: gen.PuppetAgentRequest{
			TargetGuid:  docID,
			RequestID:   "img-req-1",
			AuditSource: "tool.image-gen",
		},
		Operation: gen.PuppetAssetStageReq{
			Kind:             assetKindTexture,
			SourceRef:        sourceRef,
			GenParamsSummary: genSummary,
		},
	}
}

// writeGeneratedFile writes content under <root>/assets/generated/<rel> and
// returns the expected sha256 DataRef.
func writeGeneratedFile(t *testing.T, root, rel string, content []byte) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash("assets/generated/"+rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := sha256.Sum256(content)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestAgentAssetStage_ComputesDataRefFromGeneratedFile(t *testing.T) {
	a, root := stageActor(t)
	content := []byte("fake-png-bytes")
	wantRef := writeGeneratedFile(t, root, "img_1234.png", content)

	resp, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/img_1234.png",
		`{"prompt":"a face","model":"gpt-image-2","size":"1K"}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	asset := resp.Result.Asset
	if asset.DataRef != wantRef {
		t.Errorf("DataRef=%q want %q", asset.DataRef, wantRef)
	}
	if !strings.HasPrefix(asset.DataRef, "sha256:") {
		t.Errorf("DataRef not content-addressed: %q", asset.DataRef)
	}
	if asset.SourceRef != "assets/generated/img_1234.png" {
		t.Errorf("SourceRef=%q", asset.SourceRef)
	}
	if asset.GenParamsSummary == "" || !strings.Contains(asset.GenParamsSummary, "gpt-image-2") {
		t.Errorf("GenParamsSummary=%q", asset.GenParamsSummary)
	}
	if asset.State != assetStateStaged {
		t.Errorf("State=%q want staged", asset.State)
	}
}

func TestAgentAssetStage_IgnoresAgentSuppliedDataRef(t *testing.T) {
	a, root := stageActor(t)
	wantRef := writeGeneratedFile(t, root, "img.png", []byte("data"))

	// Agent supplies a bogus DataRef; the actor must overwrite it with the
	// computed hash so the value cannot be forged.
	req := stageReq(a, "assets/generated/img.png", "summary")
	req.Operation.DataRef = "sha256:DEADBEEF"
	resp, err := a.handleAgentAssetStage(agentStageCtx(), req)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if resp.Result.Asset.DataRef != wantRef {
		t.Errorf("actor accepted forged DataRef: got %q want %q", resp.Result.Asset.DataRef, wantRef)
	}
}

func TestAgentAssetStage_PersistsGenParamsSummary(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	genDir := filepath.Join(root, "assets", "generated")
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "agent-stage-reload", projectRoots: []string{root}}
	seedForEditTest(a)
	writeGeneratedFile(t, root, "img_99.png", []byte("eyes"))

	_, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/img_99.png",
		`{"prompt":"eyes","model":"nano-banana"}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}

	b := &Actor{store: persist.NewFSPersist(dir), actorID: "agent-stage-reload"}
	if err := b.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.state.Assets) != 1 {
		t.Fatalf("restored assets=%d", len(b.state.Assets))
	}
	got := b.state.Assets[0]
	if got.SourceRef != "assets/generated/img_99.png" {
		t.Errorf("SourceRef=%q", got.SourceRef)
	}
	if got.GenParamsSummary == "" || !strings.HasPrefix(got.DataRef, "sha256:") {
		t.Errorf("persisted asset missing fields: %+v", got)
	}
}

func TestAgentAssetStage_RequiresGenParamsSummary(t *testing.T) {
	a, _ := stageActor(t)

	req := stageReq(a, "assets/generated/img_1.png", "")
	if _, err := a.handleAgentAssetStage(agentStageCtx(), req); err == nil {
		t.Fatal("empty GenParamsSummary accepted")
	}
	req = stageReq(a, "assets/generated/img_1.png", "   ")
	if _, err := a.handleAgentAssetStage(agentStageCtx(), req); err == nil {
		t.Fatal("whitespace-only GenParamsSummary accepted")
	}
}

func TestAgentAssetStage_RequiresProjectRelativeSourceRef(t *testing.T) {
	a, _ := stageActor(t)
	ctx := agentStageCtx()

	// Empty SourceRef
	if _, err := a.handleAgentAssetStage(ctx, stageReq(a, "", "summary")); err == nil {
		t.Fatal("empty SourceRef accepted")
	}
	// Absolute Unix path
	if _, err := a.handleAgentAssetStage(ctx, stageReq(a, "/home/user/project/assets/generated/img.png", "summary")); err == nil {
		t.Fatal("absolute path accepted")
	}
	// Absolute Windows path
	if _, err := a.handleAgentAssetStage(ctx, stageReq(a, "C:/project/assets/generated/img.png", "summary")); err == nil {
		t.Fatal("windows absolute path accepted")
	}
	// Parent traversal
	if _, err := a.handleAgentAssetStage(ctx, stageReq(a, "assets/generated/../../../etc/passwd", "summary")); err == nil {
		t.Fatal("traversal path accepted")
	}
	// Outside generated dir
	if _, err := a.handleAgentAssetStage(ctx, stageReq(a, "assets/textures/face.png", "summary")); err == nil {
		t.Fatal("non-generated path accepted")
	}
}

func TestAgentAssetStage_MissingFileRejectedNoStateChange(t *testing.T) {
	a, _ := stageActor(t)
	beforeAssets := len(a.state.Assets)
	beforeRevs := len(a.state.Revisions)

	// Valid generated path but the file does not exist on disk.
	_, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/does_not_exist.png", "summary"))
	if err == nil {
		t.Fatal("missing file accepted")
	}
	if len(a.state.Assets) != beforeAssets || len(a.state.Revisions) != beforeRevs {
		t.Fatalf("state mutated by missing-file rejection: assets=%d revs=%d", len(a.state.Assets), len(a.state.Revisions))
	}
}

func TestAgentAssetStage_NoProjectRootsRejectedNoStateChange(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-stage-noroots"}
	seedForEditTest(a)
	// projectRoots is empty (simulating workspace unavailable).
	beforeAssets := len(a.state.Assets)

	_, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/img.png", "summary"))
	if err == nil {
		t.Fatal("staging with no project roots accepted")
	}
	if len(a.state.Assets) != beforeAssets {
		t.Fatalf("state mutated by no-roots rejection: assets=%d", len(a.state.Assets))
	}
}

func TestAgentAssetStage_AcceptsValidGeneratedPath(t *testing.T) {
	a, root := stageActor(t)
	writeGeneratedFile(t, root, "img_1234.png", []byte("a"))
	writeGeneratedFile(t, root, filepath.ToSlash(filepath.Join("sub", "face.png")), []byte("b"))

	validPaths := []string{
		"assets/generated/img_1234.png",
		"assets/generated/sub/face.png",
		"./assets/generated/img_1234.png",
	}
	for _, p := range validPaths {
		if _, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a, p, "summary")); err != nil {
			t.Errorf("valid path %q rejected: %v", p, err)
		}
	}
}

func TestAgentAssetStage_DeniesAnonymousAndHumanCaller(t *testing.T) {
	a, _ := stageActor(t)
	req := stageReq(a, "assets/generated/img_1.png", "summary")

	anonymous := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleAgentAssetStage(anonymous, req); err == nil {
		t.Fatal("anonymous caller reached agent asset stage")
	}
	human := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleAgentAssetStage(human, req); err == nil {
		t.Fatal("human caller reached agent asset stage")
	}
}

func TestAgentAssetStage_DoesNotLeakMediaAccount(t *testing.T) {
	a, root := stageActor(t)
	writeGeneratedFile(t, root, "img_5.png", []byte("face"))

	resp, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/img_5.png",
		`{"prompt":"face"}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	asset := resp.Result.Asset
	for _, field := range []string{asset.ID, asset.DataRef, asset.SourceRef, asset.GenParamsSummary} {
		lower := strings.ToLower(field)
		if strings.Contains(lower, "apikey") || strings.Contains(lower, "bearer") || strings.Contains(lower, "secret") {
			t.Errorf("credential-like content in asset field: %q", field)
		}
	}
}

func TestAgentAssetStage_FailedValidationDoesNotMutateState(t *testing.T) {
	a, _ := stageActor(t)
	beforeRevs := len(a.state.Revisions)
	beforeAssets := len(a.state.Assets)
	beforeDocRev := a.state.Document.Revision

	// Every rejection path must leave state untouched.
	_, _ = a.handleAgentAssetStage(agentStageCtx(), stageReq(a, "", "summary"))
	_, _ = a.handleAgentAssetStage(agentStageCtx(), stageReq(a, "assets/generated/x.png", ""))
	_, _ = a.handleAgentAssetStage(agentStageCtx(), stageReq(a, "/abs/path.png", "summary"))
	_, _ = a.handleAgentAssetStage(agentStageCtx(), stageReq(a, "assets/generated/missing.png", "summary"))

	if len(a.state.Revisions) != beforeRevs || len(a.state.Assets) != beforeAssets || a.state.Document.Revision != beforeDocRev {
		t.Fatalf("state mutated by failed validation: revs=%d assets=%d docRev=%d",
			len(a.state.Revisions), len(a.state.Assets), a.state.Document.Revision)
	}
}

func TestAgentAssetStage_NoDocumentNodeMutation(t *testing.T) {
	a, root := stageActor(t)
	writeGeneratedFile(t, root, "face.png", []byte("face"))

	faceBefore := findNode(&a.state.Document.Root, "face").TextureAssetID
	_, err := a.handleAgentAssetStage(agentStageCtx(), stageReq(a,
		"assets/generated/face.png",
		`{"prompt":"face"}`))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	faceAfter := findNode(&a.state.Document.Root, "face").TextureAssetID
	if faceBefore != faceAfter {
		t.Fatalf("staging mutated document node texture: %q -> %q", faceBefore, faceAfter)
	}
}
