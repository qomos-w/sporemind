package puppeteditor

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// ---------------------------------------------------------------------------
// Mesh test fixtures
// ---------------------------------------------------------------------------

// solidSquareTexture returns a 256x256 fully-transparent image with an opaque
// 200x200 square at offset (28, 28) — enough alpha structure for the automesh
// contour pipeline (even on the coarse default sampling preset) to produce a
// non-degenerate mesh.
func solidSquareTexture() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for y := 28; y < 228; y++ {
		for x := 28; x < 228; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	return img
}

// transparentTexture returns a fully transparent image — valid PNG, but the
// automesh pipeline finds no contours and must reject it as degenerate.
func transparentTexture() *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, 16, 16))
}

// meshTestActor seeds the edit-test document with a committed texture asset on
// the "face" node, backed by a real PNG file under a temporary project root so
// the controlled read path (SourceRef resolution + sha256 verification)
// exercises the production code. It returns the actor and the project root.
func meshTestActor(t *testing.T, img image.Image) (*Actor, string) {
	t.Helper()

	root := t.TempDir()
	rel := "assets/generated/face.png"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	dataRef, err := computeSHA256(abs)
	if err != nil {
		t.Fatalf("sha256: %v", err)
	}

	a := editTestActor(t)
	a.mu.Lock()
	face := findNode(&a.state.Document.Root, "face")
	face.TextureAssetID = "asset-face"
	b := img.Bounds()
	a.state.Assets = append(a.state.Assets, gen.PuppetStagedAsset{
		ID:         "asset-face",
		DocumentID: "doc-edit",
		State:      assetStateCommitted,
		Kind:       assetKindTexture,
		DataRef:    dataRef,
		Name:       "face.png",
		Width:      int32(b.Dx()),
		Height:     int32(b.Dy()),
		SourceRef:  rel,
		NodeGuid:   "face",
		CreatedAt:  "2026-08-14T05:00:00Z",
		ReviewedAt: "2026-08-14T05:00:00Z",
	})
	a.projectRoots = []string{root}
	a.mu.Unlock()
	return a, root
}

// meshJSON is a minimal valid explicit mesh: two triangles over four vertices.
func meshJSON() string {
	return `{"Vertices":[0,0, 10,0, 10,10, 0,10],"Indices":[0,1,2, 0,2,3],"UVs":[0,0, 1,0, 1,1, 0,1]}`
}

// assertMeshWellFormed checks the structural contract every stored mesh must
// satisfy: even vertex/UV pair lists, triangle index triples in range, and
// (when present) UVs matching the vertex count.
func assertMeshWellFormed(t *testing.T, m *gen.PuppetMesh) {
	t.Helper()
	if m == nil {
		t.Fatal("mesh is nil")
	}
	if len(m.Vertices) < 6 {
		t.Fatalf("Vertices = %v, want at least 3 (x,y) pairs", m.Vertices)
	}
	if len(m.Vertices)%2 != 0 {
		t.Fatalf("Vertices length %d is not even", len(m.Vertices))
	}
	if len(m.UVs)%2 != 0 {
		t.Fatalf("UVs length %d is not even", len(m.UVs))
	}
	if len(m.UVs) != 0 && len(m.UVs) != len(m.Vertices) {
		t.Fatalf("UVs length %d != Vertices length %d", len(m.UVs), len(m.Vertices))
	}
	if len(m.Indices) < 3 || len(m.Indices)%3 != 0 {
		t.Fatalf("Indices = %v, want at least one triangle triple", m.Indices)
	}
	points := len(m.Vertices) / 2
	for _, idx := range m.Indices {
		if idx < 0 || int(idx) >= points {
			t.Fatalf("index %d out of range (0..%d)", idx, points-1)
		}
	}
	for _, uv := range m.UVs {
		if uv < 0 || uv > 1 {
			t.Fatalf("UV %f outside [0,1]", uv)
		}
	}
}

// ---------------------------------------------------------------------------
// set_node_mesh
// ---------------------------------------------------------------------------

func TestEdit_SetNodeMesh_Success(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeMesh,
			TargetGuid: "face",
			Params:     map[string]string{"mesh": meshJSON()},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	if resp.Revision.CommandKind != cmdSetNodeMesh {
		t.Errorf("CommandKind = %q, want %q", resp.Revision.CommandKind, cmdSetNodeMesh)
	}
	if resp.Revision.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", resp.Revision.Sequence)
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}

	node := findNode(&a.state.Document.Root, "face")
	assertMeshWellFormed(t, node.Mesh)
	if len(node.Mesh.Indices) != 6 {
		t.Errorf("Indices len = %d, want 6 (two triangles)", len(node.Mesh.Indices))
	}
	if node.Mesh.Vertices[0] != 0 || node.Mesh.Vertices[1] != 0 || node.Mesh.Vertices[2] != 10 {
		t.Errorf("Vertices = %v, want the submitted values", node.Mesh.Vertices)
	}
}

func TestEdit_SetNodeMesh_Clear(t *testing.T) {
	a := editTestActor(t)
	// Attach a mesh first.
	if resp, _ := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdSetNodeMesh, TargetGuid: "face", Params: map[string]string{"mesh": meshJSON()},
	}}); !resp.Applied {
		t.Fatalf("setup mesh failed: %q", resp.Detail)
	}
	// An all-empty mesh clears it.
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeMesh,
			TargetGuid: "face",
			Params:     map[string]string{"mesh": "{}"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	if node := findNode(&a.state.Document.Root, "face"); node.Mesh != nil {
		t.Errorf("node.Mesh = %v, want nil after clear", node.Mesh)
	}
	if !strings.Contains(resp.Detail, "cleared") {
		t.Errorf("Detail = %q, want it to mention clearing", resp.Detail)
	}
}

func TestEdit_SetNodeMesh_NodeNotFound(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeMesh,
			TargetGuid: "nonexistent",
			Params:     map[string]string{"mesh": meshJSON()},
		},
	})
	if err != nil {
		t.Fatalf("structured failure expected, got error: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for missing node")
	}
	if len(a.state.Revisions) != 1 {
		t.Errorf("Revisions len = %d, want 1 (unchanged)", len(a.state.Revisions))
	}
}

func TestEdit_SetNodeMesh_InvalidPayloads(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]string
	}{
		{"missing mesh param", nil},
		{"invalid json", map[string]string{"mesh": "not-json"}},
		{"odd vertex list", map[string]string{"mesh": `{"Vertices":[0,0,10]}`}},
		{"too few vertices", map[string]string{"mesh": `{"Vertices":[0,0, 10,0, 0,10, 10]}`}},
		{"uv length mismatch", map[string]string{"mesh": `{"Vertices":[0,0, 10,0, 10,10],"UVs":[0,0, 1,0, 1,1, 0,1]}`}},
		{"indices without vertices", map[string]string{"mesh": `{"Indices":[0,1,2]}`}},
		{"index out of range", map[string]string{"mesh": `{"Vertices":[0,0, 10,0, 10,10],"Indices":[0,1,7]}`}},
		{"indices not triples", map[string]string{"mesh": `{"Vertices":[0,0, 10,0, 10,10],"Indices":[0,1]}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := editTestActor(t)
			resp, err := a.handleEdit(nil, gen.PuppetEditReq{
				Command: gen.PuppetEditCommand{
					Kind:       cmdSetNodeMesh,
					TargetGuid: "face",
					Params:     tc.params,
				},
			})
			if err != nil {
				t.Fatalf("structured failure expected, got error: %v", err)
			}
			if resp.Applied {
				t.Errorf("Applied = true for %s", tc.name)
			}
			if resp.Detail == "" {
				t.Error("Detail should describe the failure")
			}
			if node := findNode(&a.state.Document.Root, "face"); node.Mesh != nil {
				t.Error("node.Mesh should stay nil on rejection")
			}
		})
	}
}

func TestEdit_SetNodeMesh_IdempotentReplay(t *testing.T) {
	a := editTestActor(t)
	req := gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind:       cmdSetNodeMesh,
		TargetGuid: "face",
		Params:     map[string]string{"mesh": meshJSON()},
		RequestID:  "mesh-req-001",
	}}
	resp1, _ := a.handleEdit(nil, req)
	if !resp1.Applied {
		t.Fatalf("first call failed: %q", resp1.Detail)
	}
	resp2, _ := a.handleEdit(nil, req)
	if !resp2.Applied {
		t.Fatalf("replay failed: %q", resp2.Detail)
	}
	if resp2.Revision.ID != resp1.Revision.ID {
		t.Errorf("replay revision = %q, want %q", resp2.Revision.ID, resp1.Revision.ID)
	}
	if len(a.state.Revisions) != 2 { // init + 1 edit
		t.Errorf("Revisions len = %d, want 2", len(a.state.Revisions))
	}
}

func TestEdit_SetNodeMesh_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-mesh"}
	seedForEditTest(a)
	if resp, _ := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdSetNodeMesh, TargetGuid: "face", Params: map[string]string{"mesh": meshJSON()},
	}}); !resp.Applied {
		t.Fatalf("set_node_mesh failed: %q", resp.Detail)
	}
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-mesh"}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := findNode(&b.state.Document.Root, "face").Mesh
	if got == nil {
		t.Fatal("mesh lost across restart")
	}
	assertMeshWellFormed(t, got)
	if len(got.Vertices) != 8 || len(got.Indices) != 6 {
		t.Errorf("mesh = %v/%v, want 8 vertices and 6 indices", got.Vertices, got.Indices)
	}
}

// ---------------------------------------------------------------------------
// generate_node_mesh
// ---------------------------------------------------------------------------

func TestEdit_GenerateNodeMesh_Success(t *testing.T) {
	a, _ := meshTestActor(t, solidSquareTexture())
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "face",
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	if resp.Revision.CommandKind != cmdGenerateNodeMesh {
		t.Errorf("CommandKind = %q, want %q", resp.Revision.CommandKind, cmdGenerateNodeMesh)
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}
	if a.state.Document.Revision != 1 {
		t.Errorf("Document.Revision = %d, want 1", a.state.Document.Revision)
	}

	mesh := findNode(&a.state.Document.Root, "face").Mesh
	assertMeshWellFormed(t, mesh)
	// UVs are derived for generated meshes and must be present.
	if len(mesh.UVs) != len(mesh.Vertices) {
		t.Fatalf("UVs len = %d, want %d (derived)", len(mesh.UVs), len(mesh.Vertices))
	}
	if resp.Detail == "" {
		t.Error("Detail should describe the generated mesh")
	}
}

func TestEdit_GenerateNodeMesh_MethodAndPresets(t *testing.T) {
	for _, tc := range []struct {
		method string
		preset string
		wantOK bool
	}{
		{"", "", true},
		{"contour", "", true},
		{"contour", "detailed", true},
		{"contour", "thin", true},
		{"grid", "", false},
		{"skeleton", "", false},
		{"contour", "ultra", false},
	} {
		t.Run("method="+tc.method+"/preset="+tc.preset, func(t *testing.T) {
			a, _ := meshTestActor(t, solidSquareTexture())
			resp, err := a.handleEdit(nil, gen.PuppetEditReq{
				Command: gen.PuppetEditCommand{
					Kind:       cmdGenerateNodeMesh,
					TargetGuid: "face",
					Params:     map[string]string{"method": tc.method, "preset": tc.preset},
				},
			})
			if err != nil {
				t.Fatalf("handleEdit: %v", err)
			}
			if resp.Applied != tc.wantOK {
				t.Fatalf("Applied = %v, want %v, Detail = %q", resp.Applied, tc.wantOK, resp.Detail)
			}
		})
	}
}

func TestEdit_GenerateNodeMesh_NodeNotFound(t *testing.T) {
	a, _ := meshTestActor(t, solidSquareTexture())
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("structured failure expected, got error: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for missing node")
	}
	if len(a.state.Revisions) != 1 {
		t.Errorf("Revisions len = %d, want 1 (unchanged)", len(a.state.Revisions))
	}
}

func TestEdit_GenerateNodeMesh_NoTextureAttached(t *testing.T) {
	a, _ := meshTestActor(t, solidSquareTexture())
	// eye-l has no TextureAssetId.
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "eye-l",
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for node without texture")
	}
	if !strings.Contains(resp.Detail, "no attached texture") {
		t.Errorf("Detail = %q, want no-attached-texture explanation", resp.Detail)
	}
}

func TestEdit_GenerateNodeMesh_AssetNotCommitted(t *testing.T) {
	a, _ := meshTestActor(t, solidSquareTexture())
	a.mu.Lock()
	a.state.Assets[0].State = assetStateStaged
	a.mu.Unlock()

	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "face",
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for non-committed asset")
	}
	if !strings.Contains(resp.Detail, "not committed") {
		t.Errorf("Detail = %q, want not-committed explanation", resp.Detail)
	}
}

func TestEdit_GenerateNodeMesh_DegenerateInputRejected(t *testing.T) {
	a, _ := meshTestActor(t, transparentTexture())
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "face",
		},
	})
	if err != nil {
		t.Fatalf("structured failure expected, got error: %v", err)
	}
	if resp.Applied {
		t.Fatal("Applied = true for fully transparent texture")
	}
	if !strings.Contains(resp.Detail, "degenerate") {
		t.Errorf("Detail = %q, want degenerate rejection", resp.Detail)
	}
	if node := findNode(&a.state.Document.Root, "face"); node.Mesh != nil {
		t.Error("node.Mesh must stay nil on degenerate rejection")
	}
	if len(a.state.Revisions) != 1 {
		t.Errorf("Revisions len = %d, want 1 (unchanged)", len(a.state.Revisions))
	}
}

func TestEdit_GenerateNodeMesh_TamperedTextureRejected(t *testing.T) {
	a, root := meshTestActor(t, solidSquareTexture())
	abs := filepath.Join(root, filepath.FromSlash("assets/generated/face.png"))
	if err := os.WriteFile(abs, []byte("tampered-not-a-png"), 0o644); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdGenerateNodeMesh,
			TargetGuid: "face",
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if resp.Applied {
		t.Fatal("Applied = true for tampered texture")
	}
	if !strings.Contains(resp.Detail, "hash mismatch") {
		t.Errorf("Detail = %q, want hash-mismatch explanation", resp.Detail)
	}
}

func TestEdit_GenerateNodeMesh_IdempotentReplay(t *testing.T) {
	a, root := meshTestActor(t, solidSquareTexture())
	req := gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind:       cmdGenerateNodeMesh,
		TargetGuid: "face",
		RequestID:  "gen-mesh-001",
	}}
	resp1, _ := a.handleEdit(nil, req)
	if !resp1.Applied {
		t.Fatalf("first call failed: %q", resp1.Detail)
	}

	// Delete the texture file: a genuine replay must be served from the
	// idempotency cache without re-reading pixel data.
	if err := os.Remove(filepath.Join(root, filepath.FromSlash("assets/generated/face.png"))); err != nil {
		t.Fatalf("remove texture: %v", err)
	}
	resp2, err := a.handleEdit(nil, req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !resp2.Applied {
		t.Fatalf("replay failed after file removal: %q", resp2.Detail)
	}
	if resp2.Revision.ID != resp1.Revision.ID {
		t.Errorf("replay revision = %q, want %q", resp2.Revision.ID, resp1.Revision.ID)
	}
	if len(a.state.Revisions) != 2 { // init + 1 edit
		t.Errorf("Revisions len = %d, want 2", len(a.state.Revisions))
	}
}

func TestEdit_GenerateNodeMesh_DifferentRequestIdsBothApply(t *testing.T) {
	a, _ := meshTestActor(t, solidSquareTexture())
	r1, _ := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdGenerateNodeMesh, TargetGuid: "face", RequestID: "gen-a",
	}})
	r2, _ := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdGenerateNodeMesh, TargetGuid: "face", Params: map[string]string{"preset": "detailed"}, RequestID: "gen-b",
	}})
	if !r1.Applied || !r2.Applied {
		t.Fatalf("both should apply: %q / %q", r1.Detail, r2.Detail)
	}
	if r1.Revision.ID == r2.Revision.ID {
		t.Error("different RequestIds produced the same revision")
	}
	if r2.Revision.Sequence != 2 {
		t.Errorf("second revision Sequence = %d, want 2", r2.Revision.Sequence)
	}
}

func TestEdit_GenerateNodeMesh_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a, _ := meshTestActor(t, solidSquareTexture())
	a.store = persist.NewFSPersist(dir)
	a.actorID = "puppet-genmesh"
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{
		Kind: cmdGenerateNodeMesh, TargetGuid: "face",
	}})
	if !resp.Applied {
		t.Fatalf("generate failed: %q", resp.Detail)
	}
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	b := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-genmesh"}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	mesh := findNode(&b.state.Document.Root, "face").Mesh
	if mesh == nil {
		t.Fatal("generated mesh lost across restart")
	}
	assertMeshWellFormed(t, mesh)
	if len(mesh.Indices) == 0 {
		t.Error("triangles lost across restart")
	}
}
