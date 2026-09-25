package domain

// Protocol tests for the Puppet Editor document protocol (task card
// "定义Puppet编辑器协议与代码生成"). These validate that the newly defined wire
// shapes — document snapshot, staged-asset workflow, revision history and the
// auditable command-pattern edit — are registered in the static schema fragment
// and survive the binary + JSON wire formats exactly as the runtime encodes them,
// the same guarantee the agent lifecycle protocol test upholds.

import (
	"encoding/json"
	"reflect"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// wireEqual compares two values for data-content equality across the binary
// wire boundary. The codec decodes absent optional collections as empty
// (non-nil), so nil and empty slices/maps are treated as equal at every depth —
// this captures the real contract (no data lost or corrupted) without being
// brittle to Go's nil-vs-empty allocation detail.
func wireEqual(got, want any) bool {
	return valEqual(reflect.ValueOf(got), reflect.ValueOf(want))
}

func valEqual(a, b reflect.Value) bool {
	if !a.IsValid() {
		return !b.IsValid()
	}
	if !b.IsValid() {
		return false
	}
	if a.Kind() == reflect.Ptr || a.Kind() == reflect.Interface {
		if a.IsNil() {
			return (b.Kind() == reflect.Ptr || b.Kind() == reflect.Interface) && b.IsNil()
		}
		a = a.Elem()
	}
	if b.Kind() == reflect.Ptr || b.Kind() == reflect.Interface {
		if b.IsNil() {
			return false // a is non-nil after the check above
		}
		b = b.Elem()
	}
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case reflect.Slice:
		if a.Len() != b.Len() { // nil and empty both report len 0
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !valEqual(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		for _, k := range a.MapKeys() {
			bv := b.MapIndex(k)
			if !bv.IsValid() || !valEqual(a.MapIndex(k), bv) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !valEqual(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	default:
		return a.Interface() == b.Interface()
	}
}

// samplePuppetNodeTree builds a small recursive node tree so the round-trip
// exercises the self-referential array<PuppetNode> wire shape.
func samplePuppetNodeTree() gen.PuppetNode {
	return gen.PuppetNode{
		Guid:    "root",
		Name:    "Root",
		Kind:    "group",
		Enabled: true,
		Z:       0,
		Params:  map[string]string{"opacity": "1.0"},
		Children: []gen.PuppetNode{
			{
				Guid: "face", Name: "Face", Kind: "part", Enabled: true, Z: 2,
				TextureAssetID: "asset-1",
				Params:         map[string]string{"tint": "#FFFFFF"},
				Children:       []gen.PuppetNode{},
			},
			{
				Guid: "eyes", Name: "Eyes", Kind: "composite", Enabled: true, Z: 3,
				Children: []gen.PuppetNode{
					{Guid: "eye-l", Name: "Eye.L", Kind: "part", Enabled: true, Z: 4, Children: []gen.PuppetNode{}},
					{Guid: "eye-r", Name: "Eye.R", Kind: "part", Enabled: false, Z: 4, Children: []gen.PuppetNode{}},
				},
			},
		},
	}
}

// TestProtocol_PuppetSchemaIDsRegistered asserts every Puppet protocol struct is
// present in the static schema fragment under a stable schema ID. This guards
// against accidental renumbering or a missing codegen step.
func TestProtocol_PuppetSchemaIDsRegistered(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	want := map[string]uint64{
		"PuppetNode":                 4688,
		"PuppetDocument":             4689,
		"PuppetDocumentSnapshot":     4690,
		"PuppetDocumentSnapshotReq":  4691,
		"PuppetDocumentSnapshotResp": 4692,
		"PuppetStagedAsset":          4693,
		"PuppetAssetListReq":         4694,
		"PuppetAssetListResp":        4695,
		"PuppetAssetStageReq":        4696,
		"PuppetAssetStageResp":       4697,
		"PuppetAssetCommitReq":       4698,
		"PuppetAssetCommitResp":      4699,
		"PuppetAssetRejectReq":       4700,
		"PuppetAssetRejectResp":      4701,
		"PuppetRevision":             4702,
		"PuppetRevisionLogReq":       4703,
		"PuppetRevisionLogResp":      4704,
		"PuppetEditCommand":          4705,
		"PuppetEditReq":              4706,
		"PuppetEditResp":             4707,
	}
	for name, id := range want {
		got := lookupProtocolID(t, ss, name)
		if got != id {
			t.Errorf("schema %q: got ID %d, want %d", name, got, id)
		}
	}
}

// TestProtocol_PuppetDocumentSnapshotRoundTrip covers the document snapshot read
// path: the recursive node tree, monotonic revision counter and derived counts
// must survive the binary wire format exactly.
func TestProtocol_PuppetDocumentSnapshotRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	root := samplePuppetNodeTree()
	snap := gen.PuppetDocumentSnapshot{
		Document: gen.PuppetDocument{
			ID:         "doc-1",
			Revision:   7,
			Name:       "Hero",
			SourcePath: "/proj/hero.inp",
			Root:       root,
			CreatedAt:  "2026-08-14T05:00:00Z",
			UpdatedAt:  "2026-08-14T06:00:00Z",
		},
		StagedAssetCount: 3,
		RevisionCount:    7,
	}

	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetDocumentSnapshot"), snap).(gen.PuppetDocumentSnapshot)
	protocolMustEqual(t, "Document.Id", decoded.Document.ID, "doc-1")
	protocolMustEqual(t, "Document.Revision", decoded.Document.Revision, int64(7))
	protocolMustEqual(t, "Document.SourcePath", decoded.Document.SourcePath, "/proj/hero.inp")
	protocolMustEqual(t, "StagedAssetCount", decoded.StagedAssetCount, int32(3))
	protocolMustEqual(t, "RevisionCount", decoded.RevisionCount, int64(7))

	// Recursive tree fidelity: nested grand-children survive with all attributes.
	// Use the wire-tolerant comparator because the codec normalizes absent
	// optional collections (nil Params on inner nodes) to empty on decode.
	if !wireEqual(decoded.Document.Root, root) {
		t.Errorf("Document.Root: got %#v, want %#v", decoded.Document.Root, root)
	}
	eyes := decoded.Document.Root.Children[1]
	protocolMustEqual(t, "Root.Children[1].Guid", eyes.Guid, "eyes")
	protocolMustEqual(t, "Root.Children[1].Children.len", len(eyes.Children), 2)
	protocolMustEqual(t, "Root.Children[1].Children[1].Enabled", eyes.Children[1].Enabled, false)
	// Map field (Params) round-trips element-for-element.
	protocolMustEqual(t, "Root.Params", decoded.Document.Root.Params, map[string]string{"opacity": "1.0"})
	protocolMustEqual(t, "Face.TextureAssetId", decoded.Document.Root.Children[0].TextureAssetID, "asset-1")

	// The snapshot resp wraps the snapshot identically.
	resp := gen.PuppetDocumentSnapshotResp{Snapshot: snap}
	decodedResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetDocumentSnapshotResp"), resp).(gen.PuppetDocumentSnapshotResp)
	protocolMustEqual(t, "Resp.Snapshot.Document.Id", decodedResp.Snapshot.Document.ID, "doc-1")
}

// TestProtocol_PuppetStagedAssetStates covers the three-state asset lifecycle
// (staged -> committed / rejected) and the provenance fields that make the
// staging stream auditable (SourceRef, NodeGuid, ReviewedAt).
func TestProtocol_PuppetStagedAssetStates(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	staged := gen.PuppetStagedAsset{
		ID: "asset-1", DocumentID: "doc-1", State: "staged", Kind: "texture",
		DataRef: "sha256:abc", Name: "hair-gen", Width: 1024, Height: 1024,
		SourceRef: "gen-req-42", CreatedAt: "2026-08-14T05:01:00Z",
	}
	committed := staged
	committed.ID = "asset-1"
	committed.State = "committed"
	committed.NodeGuid = "face"
	committed.ReviewedAt = "2026-08-14T05:05:00Z"
	rejected := staged
	rejected.ID = "asset-2"
	rejected.State = "rejected"
	rejected.ReviewedAt = "2026-08-14T05:06:00Z"

	for _, a := range []gen.PuppetStagedAsset{staged, committed, rejected} {
		decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetStagedAsset"), a).(gen.PuppetStagedAsset)
		protocolMustEqual(t, "Id", decoded.ID, a.ID)
		protocolMustEqual(t, "State", decoded.State, a.State)
		protocolMustEqual(t, "Kind", decoded.Kind, a.Kind)
		protocolMustEqual(t, "DataRef", decoded.DataRef, a.DataRef)
		protocolMustEqual(t, "SourceRef", decoded.SourceRef, a.SourceRef)
		protocolMustEqual(t, "NodeGuid", decoded.NodeGuid, a.NodeGuid)
		protocolMustEqual(t, "ReviewedAt", decoded.ReviewedAt, a.ReviewedAt)
		protocolMustEqual(t, "Width", decoded.Width, a.Width)
	}

	// The list response carries the full staging stream.
	listResp := gen.PuppetAssetListResp{Assets: []gen.PuppetStagedAsset{staged, committed, rejected}}
	decodedList := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAssetListResp"), listResp).(gen.PuppetAssetListResp)
	protocolMustEqual(t, "Assets.len", len(decodedList.Assets), 3)
	protocolMustEqual(t, "Assets[1].State", decodedList.Assets[1].State, "committed")
}

// TestProtocol_PuppetRevisionChain covers the immutable revision history. Each
// revision records author + command kind + the parent that forms the chain.
func TestProtocol_PuppetRevisionChain(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	log := gen.PuppetRevisionLogResp{Revisions: []gen.PuppetRevision{
		{ID: "rev-0", DocumentID: "doc-1", Sequence: 0, ParentRevisionID: "", Author: "system", CommandKind: "init", Timestamp: "2026-08-14T05:00:00Z"},
		{ID: "rev-1", DocumentID: "doc-1", Sequence: 1, ParentRevisionID: "rev-0", Author: "coder", CommandKind: "add_node", Summary: "added Face", Timestamp: "2026-08-14T05:02:00Z"},
		{ID: "rev-2", DocumentID: "doc-1", Sequence: 2, ParentRevisionID: "rev-1", Author: "coder", CommandKind: "commit_asset", Summary: "attached hair", Timestamp: "2026-08-14T05:05:00Z"},
	}}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetRevisionLogResp"), log).(gen.PuppetRevisionLogResp)
	protocolMustEqual(t, "Revisions.len", len(decoded.Revisions), 3)
	protocolMustEqual(t, "rev-0.ParentRevisionId", decoded.Revisions[0].ParentRevisionID, "")
	protocolMustEqual(t, "rev-1.ParentRevisionId", decoded.Revisions[1].ParentRevisionID, "rev-0")
	protocolMustEqual(t, "rev-2.CommandKind", decoded.Revisions[2].CommandKind, "commit_asset")
	protocolMustEqual(t, "rev-2.Author", decoded.Revisions[2].Author, "coder")
	protocolMustEqual(t, "rev-1.Summary", decoded.Revisions[1].Summary, "added Face")
}

// TestProtocol_PuppetEditCommand covers the auditable edit envelope: provenance
// (Author), the Params map that carries command-specific args, and the
// idempotency RequestId. The edit response returns the appended revision plus
// the affected-node guids the frontend uses for partial refresh.
func TestProtocol_PuppetEditCommand(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	cmd := gen.PuppetEditCommand{
		Kind:       "set_node_property",
		TargetGuid: "face",
		Author:     "coder",
		Params:     map[string]string{"property": "opacity", "value": "0.5"},
		RequestID:  "req-99",
	}
	req := gen.PuppetEditReq{Command: cmd}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetEditReq"), req).(gen.PuppetEditReq)
	protocolMustEqual(t, "Command.Kind", decodedReq.Command.Kind, "set_node_property")
	protocolMustEqual(t, "Command.TargetGuid", decodedReq.Command.TargetGuid, "face")
	protocolMustEqual(t, "Command.Author", decodedReq.Command.Author, "coder")
	protocolMustEqual(t, "Command.Params", decodedReq.Command.Params, map[string]string{"property": "opacity", "value": "0.5"})
	protocolMustEqual(t, "Command.RequestId", decodedReq.Command.RequestID, "req-99")

	resp := gen.PuppetEditResp{
		Applied:  true,
		Revision: gen.PuppetRevision{ID: "rev-3", DocumentID: "doc-1", Sequence: 3, ParentRevisionID: "rev-2", Author: "coder", CommandKind: "set_node_property", Timestamp: "2026-08-14T05:07:00Z"},
		Detail:   "opacity set to 0.5",
		AffectedGuids: []string{"face"},
	}
	decodedResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetEditResp"), resp).(gen.PuppetEditResp)
	protocolMustEqual(t, "Applied", decodedResp.Applied, true)
	protocolMustEqual(t, "Revision.CommandKind", decodedResp.Revision.CommandKind, "set_node_property")
	protocolMustEqual(t, "AffectedGuids", decodedResp.AffectedGuids, []string{"face"})
}

// TestProtocol_PuppetAssetCommitRoundTrip covers the commit response which pairs
// the updated (committed) asset with the revision it produced — the audit link
// between staging and history.
func TestProtocol_PuppetAssetCommitRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	resp := gen.PuppetAssetCommitResp{
		Asset: gen.PuppetStagedAsset{
			ID: "asset-1", DocumentID: "doc-1", State: "committed", Kind: "texture",
			DataRef: "sha256:abc", NodeGuid: "face", ReviewedAt: "2026-08-14T05:05:00Z", CreatedAt: "2026-08-14T05:01:00Z",
		},
		Revision: gen.PuppetRevision{ID: "rev-2", DocumentID: "doc-1", Sequence: 2, ParentRevisionID: "rev-1", Author: "coder", CommandKind: "commit_asset", Timestamp: "2026-08-14T05:05:00Z"},
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAssetCommitResp"), resp).(gen.PuppetAssetCommitResp)
	protocolMustEqual(t, "Asset.State", decoded.Asset.State, "committed")
	protocolMustEqual(t, "Asset.NodeGuid", decoded.Asset.NodeGuid, "face")
	protocolMustEqual(t, "Revision.CommandKind", decoded.Revision.CommandKind, "commit_asset")
	protocolMustEqual(t, "Revision.Sequence", decoded.Revision.Sequence, int64(2))
}

// TestProtocol_PuppetAllTypesBinaryRoundTrip table-drives a binary round-trip
// over every Puppet request/response struct to catch any field that fails the
// wire codec — the core contract that the generated clients depend on.
func TestProtocol_PuppetAllTypesBinaryRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	root := samplePuppetNodeTree()
	rev := gen.PuppetRevision{ID: "rev-1", DocumentID: "doc-1", Sequence: 1, ParentRevisionID: "rev-0", Author: "coder", CommandKind: "add_node", Timestamp: "2026-08-14T05:02:00Z"}
	asset := gen.PuppetStagedAsset{ID: "asset-1", DocumentID: "doc-1", State: "staged", Kind: "texture", DataRef: "sha256:abc", CreatedAt: "2026-08-14T05:01:00Z"}

	cases := []struct {
		name  string
		value any
	}{
		{"PuppetNode", root},
		{"PuppetDocument", gen.PuppetDocument{ID: "doc-1", Revision: 1, Name: "Hero", Root: root, CreatedAt: "t", UpdatedAt: "t"}},
		{"PuppetDocumentSnapshot", gen.PuppetDocumentSnapshot{Document: gen.PuppetDocument{ID: "doc-1", Revision: 1, Name: "Hero", Root: root, CreatedAt: "t", UpdatedAt: "t"}, StagedAssetCount: 1, RevisionCount: 1}},
		{"PuppetDocumentSnapshotReq", gen.PuppetDocumentSnapshotReq{}},
		{"PuppetDocumentSnapshotResp", gen.PuppetDocumentSnapshotResp{Snapshot: gen.PuppetDocumentSnapshot{StagedAssetCount: 1}}},
		{"PuppetStagedAsset", asset},
		{"PuppetAssetListReq", gen.PuppetAssetListReq{State: "staged"}},
		{"PuppetAssetListResp", gen.PuppetAssetListResp{Assets: []gen.PuppetStagedAsset{asset}}},
		{"PuppetAssetStageReq", gen.PuppetAssetStageReq{Kind: "texture", DataRef: "sha256:abc", Width: 512, Height: 512, SourceRef: "gen-1"}},
		{"PuppetAssetStageResp", gen.PuppetAssetStageResp{Asset: asset}},
		{"PuppetAssetCommitReq", gen.PuppetAssetCommitReq{AssetID: "asset-1", NodeGuid: "face"}},
		{"PuppetAssetCommitResp", gen.PuppetAssetCommitResp{Asset: asset, Revision: rev}},
		{"PuppetAssetRejectReq", gen.PuppetAssetRejectReq{AssetID: "asset-1", Reason: "wrong"}},
		{"PuppetAssetRejectResp", gen.PuppetAssetRejectResp{Asset: asset}},
		{"PuppetRevision", rev},
		{"PuppetRevisionLogReq", gen.PuppetRevisionLogReq{Limit: 10}},
		{"PuppetRevisionLogResp", gen.PuppetRevisionLogResp{Revisions: []gen.PuppetRevision{rev}}},
		{"PuppetEditCommand", gen.PuppetEditCommand{Kind: "add_node", TargetGuid: "root", Author: "coder", Params: map[string]string{"name": "Hair"}}},
		{"PuppetEditReq", gen.PuppetEditReq{Command: gen.PuppetEditCommand{Kind: "add_node", TargetGuid: "root", Author: "coder"}}},
		{"PuppetEditResp", gen.PuppetEditResp{Applied: true, Revision: rev, AffectedGuids: []string{"hair"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, tc.name), tc.value)
			// wireEqual treats nil and empty collections as equal: the codec
			// normalizes absent optional maps/slices to empty on decode.
			if !wireEqual(decoded, tc.value) {
				t.Errorf("round-trip mismatch:\n  got:  %#v\n  want: %#v", decoded, tc.value)
			}
		})
	}
}

// TestProtocol_PuppetOptionalFieldsOmitted verifies the optional Puppet fields
// are omitted from JSON when empty, matching the optional semantics the TS
// generator emits as `T | undefined`. This keeps the Go↔TS contract consistent.
func TestProtocol_PuppetOptionalFieldsOmitted(t *testing.T) {
	node := gen.PuppetNode{Guid: "root", Name: "Root", Kind: "group", Enabled: true, Children: []gen.PuppetNode{}}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	// Required fields are always present.
	for _, k := range []string{"Guid", "Name", "Kind", "Enabled", "Children"} {
		if _, ok := m[k]; !ok {
			t.Errorf("PuppetNode JSON missing required field %q (raw: %s)", k, data)
		}
	}
	// Optional fields must be omitted when empty.
	for _, k := range []string{"Z", "TextureAssetId", "Mesh", "Params"} {
		if _, ok := m[k]; ok {
			t.Errorf("PuppetNode JSON should omit empty field %q (raw: %s)", k, data)
		}
	}

	// PuppetDocument optional SourcePath is omitted when empty.
	doc := gen.PuppetDocument{ID: "d", Revision: 0, Name: "n", Root: node, CreatedAt: "t", UpdatedAt: "t"}
	docData, _ := json.Marshal(doc)
	var dm map[string]any
	_ = json.Unmarshal(docData, &dm)
	if _, ok := dm["SourcePath"]; ok {
		t.Errorf("PuppetDocument JSON should omit empty SourcePath (raw: %s)", docData)
	}
}

// ---------------------------------------------------------------------------
// Phase 2 protocol tests — mesh, params/binding, asset read, revert, explore,
// viewport capture. These validate the newly defined wire shapes survive the
// binary + JSON wire formats and that every schema ID is registered and unique.
// ---------------------------------------------------------------------------

// TestProtocol_PuppetPhase2SchemaIDsRegistered asserts every Phase 2 Puppet
// protocol struct is present in the static schema fragment under the expected
// stable schema ID (4577-4602). This guards against accidental renumbering.
func TestProtocol_PuppetPhase2SchemaIDsRegistered(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	want := map[string]uint64{
		"PuppetMesh":                        4721,
		"PuppetGenerateNodeMeshReq":         4722,
		"PuppetGenerateNodeMeshResp":        4723,
		"PuppetAssetReadReq":                4724,
		"PuppetAssetReadResp":               4725,
		"PuppetDocumentRevertToReq":         4726,
		"PuppetDocumentRevertToResp":        4727,
		"PuppetParam":                       4728,
		"PuppetParamBinding":                4729,
		"PuppetParamBindingReq":             4730,
		"PuppetParamBindingResp":            4731,
		"PuppetParamListReq":                4732,
		"PuppetParamListResp":               4733,
		"PuppetAgentExploreQueryReq":        4734,
		"PuppetNodeInfo":                    4735,
		"PuppetAgentExploreQueryResp":       4736,
		"PuppetViewportCaptureReq":          4737,
		"PuppetViewportCaptureResp":         4738,
		"PuppetAgentGenerateNodeMeshReq":    4739,
		"PuppetAgentGenerateNodeMeshResp":   4740,
		"PuppetAgentAssetReadReq":           4741,
		"PuppetAgentAssetReadResp":          4742,
		"PuppetAgentRevertToReq":            4743,
		"PuppetAgentRevertToResp":           4744,
		"PuppetAgentExploreCaptureReq":      4745,
		"PuppetAgentExploreCaptureResp":     4746,
	}
	for name, id := range want {
		got := lookupProtocolID(t, ss, name)
		if got != id {
			t.Errorf("Phase 2 schema %q: got ID %d, want %d", name, got, id)
		}
	}
}

// TestProtocol_PuppetSchemaIDUniqueness verifies that no two Puppet protocol
// structs share the same schema ID across both Phase 1 and Phase 2. This is the
// ID uniqueness guard the codegen contract depends on.
func TestProtocol_PuppetSchemaIDUniqueness(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	allNames := []string{
		// Phase 1
		"PuppetNode", "PuppetDocument", "PuppetDocumentSnapshot",
		"PuppetDocumentSnapshotReq", "PuppetDocumentSnapshotResp",
		"PuppetStagedAsset", "PuppetAssetListReq", "PuppetAssetListResp",
		"PuppetAssetStageReq", "PuppetAssetStageResp",
		"PuppetAssetCommitReq", "PuppetAssetCommitResp",
		"PuppetAssetRejectReq", "PuppetAssetRejectResp",
		"PuppetRevision", "PuppetRevisionLogReq", "PuppetRevisionLogResp",
		"PuppetEditCommand", "PuppetEditReq", "PuppetEditResp",
		"PuppetAgentRequest", "PuppetAgentSnapshotReq", "PuppetAgentSnapshotResp",
		"PuppetAgentRevisionLogReq", "PuppetAgentRevisionLogResp",
		"PuppetAgentAssetListReq", "PuppetAgentAssetListResp",
		"PuppetAgentEditReq", "PuppetAgentEditResp",
		"PuppetAgentAssetStageReq", "PuppetAgentAssetStageResp",
		"PuppetAgentAssetRejectReq", "PuppetAgentAssetRejectResp",
		// Phase 2
		"PuppetMesh", "PuppetGenerateNodeMeshReq", "PuppetGenerateNodeMeshResp",
		"PuppetAssetReadReq", "PuppetAssetReadResp",
		"PuppetDocumentRevertToReq", "PuppetDocumentRevertToResp",
		"PuppetParam", "PuppetParamBinding", "PuppetParamBindingReq",
		"PuppetParamBindingResp", "PuppetParamListReq", "PuppetParamListResp",
		"PuppetAgentExploreQueryReq", "PuppetNodeInfo", "PuppetAgentExploreQueryResp",
		"PuppetViewportCaptureReq", "PuppetViewportCaptureResp",
		"PuppetAgentGenerateNodeMeshReq", "PuppetAgentGenerateNodeMeshResp",
		"PuppetAgentAssetReadReq", "PuppetAgentAssetReadResp",
		"PuppetAgentRevertToReq", "PuppetAgentRevertToResp",
		"PuppetAgentExploreCaptureReq", "PuppetAgentExploreCaptureResp",
	}
	seen := make(map[uint64]string)
	for _, name := range allNames {
		id := lookupProtocolID(t, ss, name)
		if prev, ok := seen[id]; ok {
			t.Errorf("schema ID %d collision: %q and %q", id, prev, name)
		}
		seen[id] = name
	}
}

// samplePuppetMesh builds a small mesh with a quad (4 vertices, 2 triangles).
func samplePuppetMesh() gen.PuppetMesh {
	return gen.PuppetMesh{
		Vertices: []float32{0, 0, 1, 0, 1, 1, 0, 1},
		Indices:  []int32{0, 1, 2, 0, 2, 3},
		UVs:      []float32{0, 0, 1, 0, 1, 1, 0, 1},
	}
}

// samplePuppetNodeTreeWithMesh builds a node tree where the "face" node has an
// explicit mesh, exercising the PuppetNode.Mesh pointer field on the wire.
func samplePuppetNodeTreeWithMesh() gen.PuppetNode {
	mesh := samplePuppetMesh()
	return gen.PuppetNode{
		Guid:    "root",
		Name:    "Root",
		Kind:    "group",
		Enabled: true,
		Children: []gen.PuppetNode{
			{
				Guid: "face", Name: "Face", Kind: "part", Enabled: true, Z: 2,
				TextureAssetID: "asset-1",
				Mesh:           &mesh,
				Params:         map[string]string{"tint": "#FFFFFF"},
				Children:       []gen.PuppetNode{},
			},
		},
	}
}

// TestProtocol_PuppetMeshRoundTrip verifies the mesh geometry (vertices,
// indices, uv float arrays) survives the binary wire format exactly.
func TestProtocol_PuppetMeshRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	mesh := samplePuppetMesh()
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetMesh"), mesh).(gen.PuppetMesh)
	protocolMustEqual(t, "Vertices", decoded.Vertices, mesh.Vertices)
	protocolMustEqual(t, "Indices", decoded.Indices, mesh.Indices)
	protocolMustEqual(t, "UVs", decoded.UVs, mesh.UVs)
}

// TestProtocol_PuppetNodeWithMeshRoundTrip verifies the PuppetNode.Mesh pointer
// field round-trips through the wire codec — both when present and when absent.
func TestProtocol_PuppetNodeWithMeshRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	// With mesh
	root := samplePuppetNodeTreeWithMesh()
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetNode"), root).(gen.PuppetNode)
	if decoded.Children[0].Mesh == nil {
		t.Fatal("Children[0].Mesh is nil after round-trip; expected non-nil")
	}
	protocolMustEqual(t, "Mesh.Vertices", decoded.Children[0].Mesh.Vertices, root.Children[0].Mesh.Vertices)
	protocolMustEqual(t, "Mesh.Indices", decoded.Children[0].Mesh.Indices, root.Children[0].Mesh.Indices)
	protocolMustEqual(t, "Mesh.UVs", decoded.Children[0].Mesh.UVs, root.Children[0].Mesh.UVs)

	// Without mesh — the Mesh pointer should be nil on both sides
	plainNode := gen.PuppetNode{Guid: "n", Name: "N", Kind: "part", Enabled: true, Children: []gen.PuppetNode{}}
	decodedPlain := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetNode"), plainNode).(gen.PuppetNode)
	if decodedPlain.Mesh != nil {
		t.Errorf("Mesh should be nil for a node without mesh; got %+v", decodedPlain.Mesh)
	}
}

// TestProtocol_PuppetGenerateNodeMeshRoundTrip covers the mesh generation
// request/response, including the produced mesh and revision record.
func TestProtocol_PuppetGenerateNodeMeshRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	mesh := samplePuppetMesh()
	rev := gen.PuppetRevision{ID: "rev-5", DocumentID: "doc-1", Sequence: 5, ParentRevisionID: "rev-4", Author: "coder", CommandKind: "generate_node_mesh", Timestamp: "t"}
	resp := gen.PuppetGenerateNodeMeshResp{
		Applied:  true,
		NodeGuid: "face",
		Mesh:     mesh,
		Revision: rev,
		Detail:   "generated contour mesh",
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetGenerateNodeMeshResp"), resp).(gen.PuppetGenerateNodeMeshResp)
	protocolMustEqual(t, "Applied", decoded.Applied, true)
	protocolMustEqual(t, "NodeGuid", decoded.NodeGuid, "face")
	protocolMustEqual(t, "Mesh.Vertices", decoded.Mesh.Vertices, mesh.Vertices)
	protocolMustEqual(t, "Revision.CommandKind", decoded.Revision.CommandKind, "generate_node_mesh")
	protocolMustEqual(t, "Detail", decoded.Detail, "generated contour mesh")

	req := gen.PuppetGenerateNodeMeshReq{NodeGuid: "face", Method: "contour", SampleDensity: 2.5, RequestID: "req-1"}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetGenerateNodeMeshReq"), req).(gen.PuppetGenerateNodeMeshReq)
	protocolMustEqual(t, "NodeGuid", decodedReq.NodeGuid, "face")
	protocolMustEqual(t, "Method", decodedReq.Method, "contour")
	protocolMustEqual(t, "RequestId", decodedReq.RequestID, "req-1")
}

// TestProtocol_PuppetAssetReadRoundTrip covers the asset read request/response
// that resolves a DataRef to a loadable URI.
func TestProtocol_PuppetAssetReadRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	resp := gen.PuppetAssetReadResp{
		AssetID:  "asset-1",
		Uri:      "data:image/png;base64,iVBOR...",
		MimeType: "image/png",
		Width:    512,
		Height:   512,
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAssetReadResp"), resp).(gen.PuppetAssetReadResp)
	protocolMustEqual(t, "AssetId", decoded.AssetID, "asset-1")
	protocolMustEqual(t, "Uri", decoded.Uri, "data:image/png;base64,iVBOR...")
	protocolMustEqual(t, "MimeType", decoded.MimeType, "image/png")
	protocolMustEqual(t, "Width", decoded.Width, int32(512))

	req := gen.PuppetAssetReadReq{AssetID: "asset-1"}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAssetReadReq"), req).(gen.PuppetAssetReadReq)
	protocolMustEqual(t, "AssetId", decodedReq.AssetID, "asset-1")
}

// TestProtocol_PuppetDocumentRevertToRoundTrip covers the revert-to
// request/response that rebuilds the document from the revision chain.
func TestProtocol_PuppetDocumentRevertToRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	root := samplePuppetNodeTreeWithMesh()
	doc := gen.PuppetDocument{ID: "doc-1", Revision: 3, Name: "Hero", Root: root, CreatedAt: "t", UpdatedAt: "t"}
	rev := gen.PuppetRevision{ID: "rev-3", DocumentID: "doc-1", Sequence: 3, ParentRevisionID: "rev-2", Author: "coder", CommandKind: "revert_to", Timestamp: "t"}
	resp := gen.PuppetDocumentRevertToResp{Applied: true, Document: doc, Revision: rev, Detail: "reverted to rev-2"}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetDocumentRevertToResp"), resp).(gen.PuppetDocumentRevertToResp)
	protocolMustEqual(t, "Applied", decoded.Applied, true)
	protocolMustEqual(t, "Document.Id", decoded.Document.ID, "doc-1")
	protocolMustEqual(t, "Revision.CommandKind", decoded.Revision.CommandKind, "revert_to")

	req := gen.PuppetDocumentRevertToReq{RevisionID: "rev-2"}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetDocumentRevertToReq"), req).(gen.PuppetDocumentRevertToReq)
	protocolMustEqual(t, "RevisionId", decodedReq.RevisionID, "rev-2")
}

// TestProtocol_PuppetParamAndBindingRoundTrip covers the param model and
// binding structs, including the binding edit request/response.
func TestProtocol_PuppetParamAndBindingRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	param := gen.PuppetParam{
		ID: "param-1", Name: "Mouth Open", Kind: "float", Default: "0.0",
		Min: "0.0", Max: "1.0", Step: "0.01", Group: "Face",
	}
	decodedParam := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetParam"), param).(gen.PuppetParam)
	protocolMustEqual(t, "Id", decodedParam.ID, "param-1")
	protocolMustEqual(t, "Name", decodedParam.Name, "Mouth Open")
	protocolMustEqual(t, "Kind", decodedParam.Kind, "float")
	protocolMustEqual(t, "Default", decodedParam.Default, "0.0")
	protocolMustEqual(t, "Group", decodedParam.Group, "Face")

	binding := gen.PuppetParamBinding{
		ParamID: "param-1", NodeGuid: "mouth", Property: "opacity",
		Multiplier: 2.0, Offset: -0.5,
	}
	decodedBinding := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetParamBinding"), binding).(gen.PuppetParamBinding)
	protocolMustEqual(t, "ParamId", decodedBinding.ParamID, "param-1")
	protocolMustEqual(t, "NodeGuid", decodedBinding.NodeGuid, "mouth")
	protocolMustEqual(t, "Property", decodedBinding.Property, "opacity")

	rev := gen.PuppetRevision{ID: "rev-4", DocumentID: "doc-1", Sequence: 4, ParentRevisionID: "rev-3", Author: "coder", CommandKind: "set_param_binding", Timestamp: "t"}
	bindingResp := gen.PuppetParamBindingResp{Applied: true, Revision: rev, Detail: "added binding"}
	decodedBindingResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetParamBindingResp"), bindingResp).(gen.PuppetParamBindingResp)
	protocolMustEqual(t, "Applied", decodedBindingResp.Applied, true)
	protocolMustEqual(t, "Revision.CommandKind", decodedBindingResp.Revision.CommandKind, "set_param_binding")

	// Param list response carries params and bindings together.
	listResp := gen.PuppetParamListResp{
		Params:   []gen.PuppetParam{param},
		Bindings: []gen.PuppetParamBinding{binding},
	}
	decodedList := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetParamListResp"), listResp).(gen.PuppetParamListResp)
	protocolMustEqual(t, "Params.len", len(decodedList.Params), 1)
	protocolMustEqual(t, "Bindings.len", len(decodedList.Bindings), 1)
	protocolMustEqual(t, "Params[0].Id", decodedList.Params[0].ID, "param-1")

	// PuppetDocument carries the param model, explicit values, and the
	// binding table (Phase 2 storage); all three must survive the wire.
	doc := gen.PuppetDocument{
		ID: "doc-1", Revision: 7, Name: "Hero",
		Root:         gen.PuppetNode{Guid: "root", Name: "Root", Kind: "group", Enabled: true, Children: []gen.PuppetNode{}},
		Params:       []gen.PuppetParam{param},
		ParamValues:  map[string]string{"param-1": "0.75"},
		Bindings:     []gen.PuppetParamBinding{binding},
		CreatedAt:    "t0", UpdatedAt: "t1",
	}
	decodedDoc := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetDocument"), doc).(gen.PuppetDocument)
	protocolMustEqual(t, "Document.Params.len", len(decodedDoc.Params), 1)
	protocolMustEqual(t, "Document.Params[0].Id", decodedDoc.Params[0].ID, "param-1")
	protocolMustEqual(t, "Document.ParamValues[param-1]", decodedDoc.ParamValues["param-1"], "0.75")
	protocolMustEqual(t, "Document.Bindings.len", len(decodedDoc.Bindings), 1)
	protocolMustEqual(t, "Document.Bindings[0].NodeGuid", decodedDoc.Bindings[0].NodeGuid, "mouth")
}

// TestProtocol_PuppetAgentExploreQueryRoundTrip covers the agent explore
// query request/response with filter criteria and flat node projections.
func TestProtocol_PuppetAgentExploreQueryRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	env := gen.PuppetAgentRequest{TargetGuid: "doc-1", RequestID: "req-1", AuditSource: "agent-tool"}
	req := gen.PuppetAgentExploreQueryReq{
		Envelope:    env,
		Kind:        "part",
		NamePattern: "Face",
		HasTexture:  true,
		HasMesh:     false,
	}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAgentExploreQueryReq"), req).(gen.PuppetAgentExploreQueryReq)
	protocolMustEqual(t, "Kind", decodedReq.Kind, "part")
	protocolMustEqual(t, "NamePattern", decodedReq.NamePattern, "Face")
	protocolMustEqual(t, "HasTexture", decodedReq.HasTexture, true)
	protocolMustEqual(t, "HasMesh", decodedReq.HasMesh, false)

	// NodeInfo flat projection
	nodeInfo := gen.PuppetNodeInfo{
		Guid: "face", Name: "Face", Kind: "part", Enabled: true, Z: 2,
		TextureAssetID: "asset-1", HasMesh: true, Depth: 1, ParentGuid: "root",
	}
	decodedInfo := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetNodeInfo"), nodeInfo).(gen.PuppetNodeInfo)
	protocolMustEqual(t, "Guid", decodedInfo.Guid, "face")
	protocolMustEqual(t, "HasMesh", decodedInfo.HasMesh, true)
	protocolMustEqual(t, "Depth", decodedInfo.Depth, int32(1))
	protocolMustEqual(t, "ParentGuid", decodedInfo.ParentGuid, "root")

	resp := gen.PuppetAgentExploreQueryResp{Nodes: []gen.PuppetNodeInfo{nodeInfo}}
	decodedResp := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetAgentExploreQueryResp"), resp).(gen.PuppetAgentExploreQueryResp)
	protocolMustEqual(t, "Nodes.len", len(decodedResp.Nodes), 1)
	protocolMustEqual(t, "Nodes[0].Guid", decodedResp.Nodes[0].Guid, "face")
}

// TestProtocol_PuppetViewportCaptureRoundTrip covers the viewport capture
// request/response that returns a rendered canvas image as a URI.
func TestProtocol_PuppetViewportCaptureRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	resp := gen.PuppetViewportCaptureResp{
		Uri: "data:image/png;base64,iVBOR...", Width: 256, Height: 256, MimeType: "image/png",
	}
	decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetViewportCaptureResp"), resp).(gen.PuppetViewportCaptureResp)
	protocolMustEqual(t, "Uri", decoded.Uri, "data:image/png;base64,iVBOR...")
	protocolMustEqual(t, "Width", decoded.Width, int32(256))
	protocolMustEqual(t, "Height", decoded.Height, int32(256))
	protocolMustEqual(t, "MimeType", decoded.MimeType, "image/png")

	req := gen.PuppetViewportCaptureReq{Width: 512, Height: 512, Format: "jpeg", Quality: 0.8}
	decodedReq := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, "PuppetViewportCaptureReq"), req).(gen.PuppetViewportCaptureReq)
	protocolMustEqual(t, "Format", decodedReq.Format, "jpeg")
}

// TestProtocol_PuppetPhase2AllTypesBinaryRoundTrip table-drives a binary
// round-trip over every Phase 2 Puppet request/response struct to catch any
// field that fails the wire codec.
func TestProtocol_PuppetPhase2AllTypesBinaryRoundTrip(t *testing.T) {
	ss := loadProtocolSchemaSet(t)
	c := protocolCodec(t, ss)

	mesh := samplePuppetMesh()
	rev := gen.PuppetRevision{ID: "rev-1", DocumentID: "doc-1", Sequence: 1, ParentRevisionID: "rev-0", Author: "coder", CommandKind: "set_node_mesh", Timestamp: "t"}
	env := gen.PuppetAgentRequest{TargetGuid: "doc-1", RequestID: "req-1", AuditSource: "agent"}

	cases := []struct {
		name  string
		value any
	}{
		{"PuppetMesh", mesh},
		{"PuppetGenerateNodeMeshReq", gen.PuppetGenerateNodeMeshReq{NodeGuid: "face", Method: "contour", SampleDensity: 1.5}},
		{"PuppetGenerateNodeMeshResp", gen.PuppetGenerateNodeMeshResp{Applied: true, NodeGuid: "face", Mesh: mesh, Revision: rev}},
		{"PuppetAssetReadReq", gen.PuppetAssetReadReq{AssetID: "asset-1"}},
		{"PuppetAssetReadResp", gen.PuppetAssetReadResp{AssetID: "asset-1", Uri: "data:url", Width: 512, Height: 512}},
		{"PuppetDocumentRevertToReq", gen.PuppetDocumentRevertToReq{RevisionID: "rev-2"}},
		{"PuppetDocumentRevertToResp", gen.PuppetDocumentRevertToResp{Applied: true, Revision: rev}},
		{"PuppetParam", gen.PuppetParam{ID: "p1", Name: "X", Kind: "float", Default: "0"}},
		{"PuppetParamBinding", gen.PuppetParamBinding{ParamID: "p1", NodeGuid: "face", Property: "opacity", Multiplier: 1.0}},
		{"PuppetParamBindingReq", gen.PuppetParamBindingReq{Action: "add", Binding: gen.PuppetParamBinding{ParamID: "p1", NodeGuid: "face", Property: "opacity"}}},
		{"PuppetParamBindingResp", gen.PuppetParamBindingResp{Applied: true, Revision: rev}},
		{"PuppetParamListReq", gen.PuppetParamListReq{}},
		{"PuppetParamListResp", gen.PuppetParamListResp{Params: []gen.PuppetParam{{ID: "p1", Name: "X", Kind: "float", Default: "0"}}, Bindings: []gen.PuppetParamBinding{{ParamID: "p1", NodeGuid: "face", Property: "opacity"}}}},
		{"PuppetAgentExploreQueryReq", gen.PuppetAgentExploreQueryReq{Envelope: env, Kind: "part"}},
		{"PuppetNodeInfo", gen.PuppetNodeInfo{Guid: "face", Name: "Face", Kind: "part", Enabled: true, Depth: 1}},
		{"PuppetAgentExploreQueryResp", gen.PuppetAgentExploreQueryResp{Nodes: []gen.PuppetNodeInfo{{Guid: "face", Name: "Face", Kind: "part", Enabled: true}}}},
		{"PuppetViewportCaptureReq", gen.PuppetViewportCaptureReq{Width: 256, Height: 256, Format: "png"}},
		{"PuppetViewportCaptureResp", gen.PuppetViewportCaptureResp{Uri: "data:url", Width: 256, Height: 256}},
		{"PuppetAgentGenerateNodeMeshReq", gen.PuppetAgentGenerateNodeMeshReq{Envelope: env, Operation: gen.PuppetGenerateNodeMeshReq{NodeGuid: "face"}}},
		{"PuppetAgentGenerateNodeMeshResp", gen.PuppetAgentGenerateNodeMeshResp{Result: gen.PuppetGenerateNodeMeshResp{Applied: true, NodeGuid: "face", Mesh: mesh, Revision: rev}}},
		{"PuppetAgentAssetReadReq", gen.PuppetAgentAssetReadReq{Envelope: env, AssetID: "asset-1"}},
		{"PuppetAgentAssetReadResp", gen.PuppetAgentAssetReadResp{Result: gen.PuppetAssetReadResp{AssetID: "asset-1", Uri: "data:url"}}},
		{"PuppetAgentRevertToReq", gen.PuppetAgentRevertToReq{Envelope: env, RevisionID: "rev-2"}},
		{"PuppetAgentRevertToResp", gen.PuppetAgentRevertToResp{Result: gen.PuppetDocumentRevertToResp{Applied: true, Revision: rev}}},
		{"PuppetAgentExploreCaptureReq", gen.PuppetAgentExploreCaptureReq{Envelope: env, Width: 256, Height: 256}},
		{"PuppetAgentExploreCaptureResp", gen.PuppetAgentExploreCaptureResp{Result: gen.PuppetViewportCaptureResp{Uri: "data:url", Width: 256, Height: 256}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoded := protocolRoundTrip(t, c, ss, lookupProtocolID(t, ss, tc.name), tc.value)
			if !wireEqual(decoded, tc.value) {
				t.Errorf("round-trip mismatch:\n  got:  %#v\n  want: %#v", decoded, tc.value)
			}
		})
	}
}

// TestProtocol_PuppetNodeWithMeshJSONOptional verifies the PuppetNode.Mesh
// field is omitted from JSON when nil, matching the optional pointer semantics.
func TestProtocol_PuppetNodeWithMeshJSONOptional(t *testing.T) {
	node := gen.PuppetNode{Guid: "n", Name: "N", Kind: "part", Enabled: true, Children: []gen.PuppetNode{}}
	data, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["Mesh"]; ok {
		t.Errorf("PuppetNode JSON should omit nil Mesh (raw: %s)", data)
	}

	// With a mesh, the Mesh field should be present.
	mesh := samplePuppetMesh()
	node.Mesh = &mesh
	dataWithMesh, _ := json.Marshal(node)
	var m2 map[string]any
	_ = json.Unmarshal(dataWithMesh, &m2)
	if _, ok := m2["Mesh"]; !ok {
		t.Errorf("PuppetNode JSON should include Mesh when present (raw: %s)", dataWithMesh)
	}
}
