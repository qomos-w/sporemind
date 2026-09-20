package puppeteditor

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func agentCtx() *testutil.FakeCtx {
	return &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
}

func exploreReq(a *Actor, requestID string) gen.PuppetAgentExploreQueryReq {
	a.mu.RLock()
	id := a.state.Document.ID
	a.mu.RUnlock()
	return gen.PuppetAgentExploreQueryReq{
		Envelope: gen.PuppetAgentRequest{TargetGuid: id, RequestID: requestID, AuditSource: "tool.puppet"},
	}
}

// commitTextureForTest stages and commits a texture asset onto the node so
// HasTexture filters have something to match.
func commitTextureForTest(t *testing.T, a *Actor, nodeGuid string) {
	t.Helper()
	staged, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{
		Kind: assetKindTexture, DataRef: "sha256:" + strings.Repeat("a", 64), Name: "tex",
		SourceRef: "assets/generated/tex.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleAssetCommit(nil, gen.PuppetAssetCommitReq{AssetID: staged.Asset.ID, NodeGuid: nodeGuid}); err != nil {
		t.Fatal(err)
	}
}

func setMeshForTest(t *testing.T, a *Actor, nodeGuid, requestID string) {
	t.Helper()
	meshJSON := `{"Vertices":[0,0,10,0,0,10],"Indices":[0,1,2],"UVs":[0,0,1,0,0,1]}`
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdSetNodeMesh, TargetGuid: nodeGuid, RequestID: requestID, Params: map[string]string{"mesh": meshJSON},
	}})
	if err != nil || !resp.Applied {
		t.Fatalf("set_node_mesh failed: %v %s", err, resp.Detail)
	}
}

func TestAgentExploreQuery_KindFilter(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleAgentExploreQuery(agentCtx(), exploreReq(a, "q1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 4 {
		t.Fatalf("unfiltered query returned %d nodes, want 4", len(resp.Nodes))
	}

	req := exploreReq(a, "q2")
	req.Kind = "part"
	resp, err = a.handleAgentExploreQuery(agentCtx(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range resp.Nodes {
		if n.Kind != "part" {
			t.Fatalf("kind filter leaked %q node %q", n.Kind, n.Guid)
		}
		got[n.Guid] = true
	}
	if len(got) != 2 || !got["face"] || !got["eye-l"] {
		t.Fatalf("kind=part matched %v, want face and eye-l", got)
	}
}

func TestAgentExploreQuery_NamePatternIsCaseInsensitiveSubstring(t *testing.T) {
	a := editTestActor(t)
	req := exploreReq(a, "q1")
	req.NamePattern = "eye"
	resp, err := a.handleAgentExploreQuery(agentCtx(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 2 {
		t.Fatalf("pattern 'eye' matched %d nodes, want 2 (Eyes, Eye.L)", len(resp.Nodes))
	}
	for _, n := range resp.Nodes {
		if !strings.Contains(strings.ToLower(n.Name), "eye") {
			t.Fatalf("pattern matched non-matching node %q", n.Name)
		}
	}
}

func TestAgentExploreQuery_TextureAndMeshFilters(t *testing.T) {
	a := editTestActor(t)
	commitTextureForTest(t, a, "face")
	setMeshForTest(t, a, "face", "mesh-1")

	texReq := exploreReq(a, "q1")
	texReq.HasTexture = true
	resp, err := a.handleAgentExploreQuery(agentCtx(), texReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Guid != "face" {
		t.Fatalf("HasTexture matched %+v, want only face", resp.Nodes)
	}
	if resp.Nodes[0].TextureAssetID == "" {
		t.Fatal("projection lost TextureAssetId")
	}

	meshReq := exploreReq(a, "q2")
	meshReq.HasMesh = true
	resp, err = a.handleAgentExploreQuery(agentCtx(), meshReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Guid != "face" || !resp.Nodes[0].HasMesh {
		t.Fatalf("HasMesh matched %+v, want only face", resp.Nodes)
	}

	both := exploreReq(a, "q3")
	both.HasTexture = true
	both.HasMesh = true
	both.Kind = "composite"
	resp, err = a.handleAgentExploreQuery(agentCtx(), both)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Nodes) != 0 {
		t.Fatalf("combined filters matched %+v, want none (face is not composite)", resp.Nodes)
	}
}

func TestAgentExploreQuery_ProjectionCarriesDepthAndParent(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleAgentExploreQuery(agentCtx(), exploreReq(a, "q1"))
	if err != nil {
		t.Fatal(err)
	}
	byGuid := map[string]gen.PuppetNodeInfo{}
	for _, n := range resp.Nodes {
		byGuid[n.Guid] = n
	}
	if root := byGuid["root"]; root.Depth != 0 || root.ParentGuid != "" {
		t.Fatalf("root projection %+v has wrong depth/parent", root)
	}
	if eyes := byGuid["eyes"]; eyes.Depth != 1 || eyes.ParentGuid != "root" {
		t.Fatalf("eyes projection %+v has wrong depth/parent", eyes)
	}
	if eyeL := byGuid["eye-l"]; eyeL.Depth != 2 || eyeL.ParentGuid != "eyes" {
		t.Fatalf("eye-l projection %+v has wrong depth/parent", eyeL)
	}
}

func TestAgentExploreQuery_UnknownKindFilterRejected(t *testing.T) {
	a := editTestActor(t)
	req := exploreReq(a, "q1")
	req.Kind = "particle"
	if _, err := a.handleAgentExploreQuery(agentCtx(), req); err == nil {
		t.Fatal("unknown Kind filter accepted")
	}
}

func TestAgentExploreQuery_IsReadOnly(t *testing.T) {
	a := editTestActor(t)
	commitTextureForTest(t, a, "face")
	setMeshForTest(t, a, "face", "mesh-1")

	a.mu.RLock()
	keyBefore := documentContentKey(a.state.Document)
	revisionsBefore := len(a.state.Revisions)
	assetsBefore := len(a.state.Assets)
	snapshotsBefore := len(a.state.Snapshots)
	a.mu.RUnlock()

	req := exploreReq(a, "q1")
	resp, err := a.handleAgentExploreQuery(agentCtx(), req)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper with every returned projection: the actor must be unaffected.
	for i := range resp.Nodes {
		resp.Nodes[i].Name = "tampered"
		resp.Nodes[i].Kind = "mask"
		resp.Nodes[i].TextureAssetID = "forged"
		resp.Nodes[i].HasMesh = !resp.Nodes[i].HasMesh
	}

	a.mu.RLock()
	keyAfter := documentContentKey(a.state.Document)
	revisionsAfter := len(a.state.Revisions)
	assetsAfter := len(a.state.Assets)
	snapshotsAfter := len(a.state.Snapshots)
	a.mu.RUnlock()

	if keyBefore != keyAfter {
		t.Fatal("explore query mutated the document content")
	}
	if revisionsAfter != revisionsBefore || assetsAfter != assetsBefore || snapshotsAfter != snapshotsBefore {
		t.Fatalf("explore query changed actor state sizes: revisions %d->%d assets %d->%d snapshots %d->%d",
			revisionsBefore, revisionsAfter, assetsBefore, assetsAfter, snapshotsBefore, snapshotsAfter)
	}
	if n := nodeByName(a, "face"); n.Name == "tampered" || n.TextureAssetID == "forged" {
		t.Fatal("explore result aliases actor state")
	}
}

func TestAgentExploreQuery_PolicyDeniedForNonAgent(t *testing.T) {
	a := editTestActor(t)
	req := exploreReq(a, "q1")
	anon := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleAgentExploreQuery(anon, req); err == nil {
		t.Fatal("anonymous caller reached agent explore")
	}
	human := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleAgentExploreQuery(human, req); err == nil {
		t.Fatal("human caller reached agent explore")
	}
	badEnv := req
	badEnv.Envelope.AuditSource = ""
	if _, err := a.handleAgentExploreQuery(agentCtx(), badEnv); err == nil {
		t.Fatal("envelope without AuditSource accepted")
	}
}

// ---------------------------------------------------------------------------
// Viewport capture
// ---------------------------------------------------------------------------

func decodeCaptureUri(t *testing.T, uri string) string {
	t.Helper()
	const prefix = "data:" + viewportMimeSVG + ";base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("capture uri %q does not start with %q", uri, prefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("capture uri is not valid base64: %v", err)
	}
	return string(raw)
}

func TestViewportCapture_HonestDocumentReprojection(t *testing.T) {
	a := editTestActor(t)
	commitTextureForTest(t, a, "face")

	a.mu.RLock()
	keyBefore := documentContentKey(a.state.Document)
	revisionsBefore := len(a.state.Revisions)
	a.mu.RUnlock()

	resp, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Source != viewportSourceReprojection {
		t.Fatalf("Source=%q, want %q", resp.Source, viewportSourceReprojection)
	}
	if resp.MimeType != viewportMimeSVG {
		t.Fatalf("MimeType=%q, want %q", resp.MimeType, viewportMimeSVG)
	}
	if resp.Width != viewportDefaultWidth || resp.Height != viewportDefaultHeight {
		t.Fatalf("default size %dx%d, want %dx%d", resp.Width, resp.Height, viewportDefaultWidth, viewportDefaultHeight)
	}

	svg := decodeCaptureUri(t, resp.Uri)
	if !strings.Contains(svg, "document reprojection (not a canvas screenshot)") {
		t.Fatal("reprojection is not labeled as a reprojection")
	}
	if !strings.Contains(svg, "Face") || !strings.Contains(svg, "[tex]") {
		t.Fatal("reprojection does not project node names/texture markers")
	}
	if !strings.Contains(svg, "z=2") {
		t.Fatal("reprojection does not project z-order")
	}
	if !strings.HasSuffix(svg, "</svg>") {
		t.Fatal("reprojection svg is truncated")
	}

	// Deterministic: identical document renders identical bytes.
	again, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Uri != resp.Uri {
		t.Fatal("reprojection is not deterministic for an unchanged document")
	}

	// Read-only: no mutation, no revision.
	a.mu.RLock()
	keyAfter := documentContentKey(a.state.Document)
	revisionsAfter := len(a.state.Revisions)
	a.mu.RUnlock()
	if keyBefore != keyAfter || revisionsAfter != revisionsBefore {
		t.Fatal("viewport capture mutated document or appended a revision")
	}
}

func TestViewportCapture_RejectsRasterFormats(t *testing.T) {
	a := editTestActor(t)
	for _, format := range []string{"png", "jpeg"} {
		resp, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{Format: format})
		if err == nil {
			t.Fatalf("format %q accepted — fabricating raster screenshots is forbidden", format)
		}
		if !strings.Contains(err.Error(), "fabricated") {
			t.Fatalf("format %q error %q does not explain the no-fabrication policy", format, err.Error())
		}
		if resp.Uri != "" {
			t.Fatalf("format %q returned a uri despite rejection", format)
		}
	}
	if _, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{Format: "svg"}); err != nil {
		t.Fatalf("explicit svg rejected: %v", err)
	}
}

func TestViewportCapture_SizeNormalization(t *testing.T) {
	a := editTestActor(t)
	cases := []struct {
		reqWidth, reqHeight int32
		wantWidth, wantHeight int32
	}{
		{0, 0, viewportDefaultWidth, viewportDefaultHeight},
		{-5, -5, viewportDefaultWidth, viewportDefaultHeight},
		{10, 10, viewportMinDim, viewportMinDim},
		{99999, 2, viewportMaxDim, viewportMinDim},
		{1024, 768, 1024, 768},
	}
	for _, tc := range cases {
		resp, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{Width: tc.reqWidth, Height: tc.reqHeight})
		if err != nil {
			t.Fatalf("size %dx%d rejected: %v", tc.reqWidth, tc.reqHeight, err)
		}
		if resp.Width != tc.wantWidth || resp.Height != tc.wantHeight {
			t.Fatalf("size %dx%d normalized to %dx%d, want %dx%d",
				tc.reqWidth, tc.reqHeight, resp.Width, resp.Height, tc.wantWidth, tc.wantHeight)
		}
	}
}

func TestViewportCapture_RowOverflowMarksOmittedNodes(t *testing.T) {
	a := editTestActor(t)
	// 64px height fits at most one row; the remaining nodes must be counted,
	// not silently dropped.
	resp, err := a.handleViewportCapture(nil, gen.PuppetViewportCaptureReq{Width: 256, Height: viewportMinDim})
	if err != nil {
		t.Fatal(err)
	}
	svg := decodeCaptureUri(t, resp.Uri)
	if !strings.Contains(svg, "more nodes") {
		t.Fatalf("overflowed reprojection does not mark omitted nodes: %s", svg)
	}
}

func TestAgentExploreCapture_RequiresEnvelopeAndMarksSource(t *testing.T) {
	a := editTestActor(t)
	a.mu.RLock()
	docID := a.state.Document.ID
	a.mu.RUnlock()
	env := gen.PuppetAgentRequest{TargetGuid: docID, RequestID: "cap-1", AuditSource: "tool.puppet"}

	anon := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleAgentExploreCapture(anon, gen.PuppetAgentExploreCaptureReq{Envelope: env}); err == nil {
		t.Fatal("anonymous caller reached agent capture")
	}

	resp, err := a.handleAgentExploreCapture(agentCtx(), gen.PuppetAgentExploreCaptureReq{Envelope: env, Width: 320, Height: 240})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result.Source != viewportSourceReprojection {
		t.Fatalf("agent capture Source=%q, want %q", resp.Result.Source, viewportSourceReprojection)
	}
	if resp.Result.Width != 320 || resp.Result.Height != 240 {
		t.Fatalf("agent capture size %dx%d, want 320x240", resp.Result.Width, resp.Result.Height)
	}

	badEnv := env
	badEnv.TargetGuid = "other-document"
	if _, err := a.handleAgentExploreCapture(agentCtx(), gen.PuppetAgentExploreCaptureReq{Envelope: badEnv}); err == nil {
		t.Fatal("wrong document accepted")
	}
}

// TestPolicy_RegistrationVisibility pins the surface contract: the new
// callables keep the Internal+agent boundary for the agent surface and
// Public visibility for the frontend-facing revert/capture.
func TestPolicy_RegistrationVisibility(t *testing.T) {
	a := &Actor{store: nil, actorID: "policy-only"}
	seedForEditTest(a)
	registered := testutil.HumanCtx(testutil.GenActorID())
	registered.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(registered); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{agentExploreQuery, agentRevertTo, agentExploreCapture} {
		opts := registered.RegOpts[name]
		if opts == nil {
			t.Fatalf("%s not registered", name)
		}
		if v := actor.ResolveVisibility(opts...); v != actor.VisibilityInternal {
			t.Errorf("%s visibility=%v, want internal", name, v)
		}
		if !strings.Contains(actor.ResolveDescription(opts...), "Agent") {
			t.Errorf("%s has no policy description", name)
		}
	}
	for _, name := range []string{"puppet.document.revert_to", "puppet.viewport.capture"} {
		opts := registered.RegOpts[name]
		if opts == nil {
			t.Fatalf("%s not registered", name)
		}
		if v := actor.ResolveVisibility(opts...); v != actor.VisibilityPublic {
			t.Errorf("%s visibility=%v, want public", name, v)
		}
	}
}
