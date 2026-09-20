package puppeteditor

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// seedForEditTest initialises the actor with a minimal document and a single
// "init" revision at sequence 0, mirroring the OnInit bootstrap. This gives
// edit tests a predictable starting point: the first applied edit must produce
// sequence 1.
func seedForEditTest(a *Actor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = puppetState{
		Document: gen.PuppetDocument{
			ID:       "doc-edit",
			Revision: 0,
			Name:     "Test",
			Root: gen.PuppetNode{
				Guid: "root", Name: "Root", Kind: "group", Enabled: true,
				Children: []gen.PuppetNode{
					{Guid: "face", Name: "Face", Kind: "part", Enabled: true, Z: 2, Children: []gen.PuppetNode{}},
					{
						Guid: "eyes", Name: "Eyes", Kind: "composite", Enabled: true, Z: 1,
						Children: []gen.PuppetNode{
							{Guid: "eye-l", Name: "Eye.L", Kind: "part", Enabled: true, Z: 5, Children: []gen.PuppetNode{}},
						},
					},
				},
			},
			CreatedAt: "2026-08-14T05:00:00Z",
			UpdatedAt: "2026-08-14T05:00:00Z",
		},
		Revisions: []gen.PuppetRevision{
			{ID: "rev-init", DocumentID: "doc-edit", Sequence: 0, ParentRevisionID: "", Author: "system", CommandKind: "init", Timestamp: "2026-08-14T05:00:00Z"},
		},
		Assets:            []gen.PuppetStagedAsset{},
		ProcessedRequests: map[string]editResult{},
	}
}

// editTestActor returns a fresh seeded actor.
func editTestActor(t *testing.T) *Actor {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "puppet-edit-test"}
	seedForEditTest(a)
	return a
}

// ---------------------------------------------------------------------------
// Success cases
// ---------------------------------------------------------------------------

func TestEdit_SetNodeName_Success(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "face",
			Params:     map[string]string{"name": "Forehead"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	if resp.Revision.CommandKind != cmdSetNodeName {
		t.Errorf("CommandKind = %q, want %q", resp.Revision.CommandKind, cmdSetNodeName)
	}
	if resp.Revision.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", resp.Revision.Sequence)
	}
	if resp.Detail == "" {
		t.Error("Detail should not be empty on success")
	}
	if len(resp.AffectedGuids) != 1 || resp.AffectedGuids[0] != "face" {
		t.Errorf("AffectedGuids = %v, want [face]", resp.AffectedGuids)
	}

	// The document was actually mutated.
	node := findNode(&a.state.Document.Root, "face")
	if node.Name != "Forehead" {
		t.Errorf("node.Name = %q, want Forehead", node.Name)
	}
	if a.state.Document.Revision != 1 {
		t.Errorf("Document.Revision = %d, want 1", a.state.Document.Revision)
	}
}

func TestEdit_SetNodeEnabled_Success(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeEnabled,
			TargetGuid: "face",
			Params:     map[string]string{"enabled": "false"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	node := findNode(&a.state.Document.Root, "face")
	if node.Enabled {
		t.Error("node.Enabled = true, want false")
	}
}

func TestEdit_SetNodeZ_Success(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeZ,
			TargetGuid: "eye-l",
			Params:     map[string]string{"z": "42"},
		},
	})
	if err != nil {
		t.Fatalf("handleEdit: %v", err)
	}
	if !resp.Applied {
		t.Fatalf("Applied = false, Detail = %q", resp.Detail)
	}
	node := findNode(&a.state.Document.Root, "eye-l")
	if node.Z != 42 {
		t.Errorf("node.Z = %d, want 42", node.Z)
	}
}

func TestEdit_DeepNestedNode(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "eye-l",
			Params:     map[string]string{"name": "Left Eye"},
		},
	})
	if !resp.Applied {
		t.Fatalf("deep nested node not found, Detail = %q", resp.Detail)
	}
}

// ---------------------------------------------------------------------------
// Failure cases — all return structured results (Applied=false), never errors
// ---------------------------------------------------------------------------

func TestEdit_UnknownCommandKind(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       "delete_everything",
			TargetGuid: "face",
			Params:     map[string]string{"name": "x"},
		},
	})
	if err != nil {
		t.Fatalf("unknown kind should return structured failure, not error: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for unknown kind")
	}
	if resp.Detail == "" {
		t.Error("Detail should describe the failure")
	}
	// No revision should have been appended.
	if len(a.state.Revisions) != 1 {
		t.Errorf("Revisions len = %d, want 1 (unchanged)", len(a.state.Revisions))
	}
}

func TestEdit_NodeNotFound(t *testing.T) {
	a := editTestActor(t)
	resp, err := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "nonexistent",
			Params:     map[string]string{"name": "X"},
		},
	})
	if err != nil {
		t.Fatalf("node-not-found should return structured failure, not error: %v", err)
	}
	if resp.Applied {
		t.Error("Applied = true for missing node")
	}
}

func TestEdit_EmptyTargetGuid(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:   cmdSetNodeName,
			Params: map[string]string{"name": "X"},
		},
	})
	if resp.Applied {
		t.Error("Applied = true for empty TargetGuid")
	}
}

func TestEdit_MissingParam_Name(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "face",
		},
	})
	if resp.Applied {
		t.Error("Applied = true for missing name param")
	}
}

func TestEdit_EmptyParam_Name(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "face",
			Params:     map[string]string{"name": ""},
		},
	})
	if resp.Applied {
		t.Error("Applied = true for empty name param")
	}
}

func TestEdit_InvalidParam_Enabled(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeEnabled,
			TargetGuid: "face",
			Params:     map[string]string{"enabled": "maybe"},
		},
	})
	if resp.Applied {
		t.Error("Applied = true for invalid enabled param")
	}
}

func TestEdit_MissingParam_Z(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeZ,
			TargetGuid: "face",
		},
	})
	if resp.Applied {
		t.Error("Applied = true for missing z param")
	}
}

func TestEdit_InvalidParam_Z(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeZ,
			TargetGuid: "face",
			Params:     map[string]string{"z": "not-a-number"},
		},
	})
	if resp.Applied {
		t.Error("Applied = true for invalid z param")
	}
}

// ---------------------------------------------------------------------------
// Idempotency
// ---------------------------------------------------------------------------

func TestEdit_Idempotent_DuplicateRequestId(t *testing.T) {
	a := editTestActor(t)
	cmd := gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "face",
			Params:     map[string]string{"name": "First"},
			RequestID:  "req-001",
		},
	}

	resp1, _ := a.handleEdit(nil, cmd)
	if !resp1.Applied {
		t.Fatalf("first call should succeed, Detail = %q", resp1.Detail)
	}
	revCountAfter1 := len(a.state.Revisions)

	// Replay with the same RequestId — must not append a new revision.
	resp2, _ := a.handleEdit(nil, cmd)
	if !resp2.Applied {
		t.Fatalf("replay should return Applied=true, Detail = %q", resp2.Detail)
	}
	if len(a.state.Revisions) != revCountAfter1 {
		t.Errorf("replay appended a revision: len = %d, want %d", len(a.state.Revisions), revCountAfter1)
	}
	// The replayed response returns the same revision.
	if resp2.Revision.ID != resp1.Revision.ID {
		t.Errorf("replay revision = %q, want %q (same as original)", resp2.Revision.ID, resp1.Revision.ID)
	}
	if resp2.Revision.Sequence != resp1.Revision.Sequence {
		t.Errorf("replay sequence = %d, want %d", resp2.Revision.Sequence, resp1.Revision.Sequence)
	}
}

func TestEdit_Idempotent_DifferentRequestId(t *testing.T) {
	a := editTestActor(t)

	// Two identical commands with different RequestIds both apply.
	r1, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "A"}, RequestID: "req-a",
		},
	})
	r2, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "B"}, RequestID: "req-b",
		},
	})
	if r1.Revision.ID == r2.Revision.ID {
		t.Error("different RequestIds produced the same revision")
	}
	if r2.Revision.Sequence != 2 {
		t.Errorf("second revision Sequence = %d, want 2", r2.Revision.Sequence)
	}
}

func TestEdit_NoRequestId_AlwaysApplies(t *testing.T) {
	a := editTestActor(t)
	for i := 0; i < 3; i++ {
		resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
			Command: gen.PuppetEditCommand{
				Kind: cmdSetNodeName, TargetGuid: "face",
				Params: map[string]string{"name": "X"},
			},
		})
		if !resp.Applied {
			t.Fatalf("call %d should apply (no RequestId = no dedup)", i)
		}
	}
	if len(a.state.Revisions) != 4 { // 1 init + 3 edits
		t.Errorf("Revisions len = %d, want 4", len(a.state.Revisions))
	}
}

// ---------------------------------------------------------------------------
// Revision chain integrity
// ---------------------------------------------------------------------------

func TestEdit_RevisionChain_SequenceAndParent(t *testing.T) {
	a := editTestActor(t)

	// Apply three edits.
	r1, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{Kind: cmdSetNodeName, TargetGuid: "face", Params: map[string]string{"name": "A"}},
	})
	r2, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{Kind: cmdSetNodeEnabled, TargetGuid: "face", Params: map[string]string{"enabled": "false"}},
	})
	r3, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{Kind: cmdSetNodeZ, TargetGuid: "face", Params: map[string]string{"z": "99"}},
	})

	// Sequences are 1, 2, 3.
	if r1.Revision.Sequence != 1 {
		t.Errorf("r1.Sequence = %d, want 1", r1.Revision.Sequence)
	}
	if r2.Revision.Sequence != 2 {
		t.Errorf("r2.Sequence = %d, want 2", r2.Revision.Sequence)
	}
	if r3.Revision.Sequence != 3 {
		t.Errorf("r3.Sequence = %d, want 3", r3.Revision.Sequence)
	}

	// Parent chain: r1 -> init, r2 -> r1, r3 -> r2.
	if r1.Revision.ParentRevisionID != "rev-init" {
		t.Errorf("r1.ParentRevisionID = %q, want rev-init", r1.Revision.ParentRevisionID)
	}
	if r2.Revision.ParentRevisionID != r1.Revision.ID {
		t.Errorf("r2.ParentRevisionID = %q, want %q", r2.Revision.ParentRevisionID, r1.Revision.ID)
	}
	if r3.Revision.ParentRevisionID != r2.Revision.ID {
		t.Errorf("r3.ParentRevisionID = %q, want %q", r3.Revision.ParentRevisionID, r2.Revision.ID)
	}

	// All revisions reference the same document.
	for _, r := range []gen.PuppetRevision{r1.Revision, r2.Revision, r3.Revision} {
		if r.DocumentID != "doc-edit" {
			t.Errorf("revision DocumentID = %q, want doc-edit", r.DocumentID)
		}
	}

	// Document counter is in sync.
	if a.state.Document.Revision != 3 {
		t.Errorf("Document.Revision = %d, want 3", a.state.Document.Revision)
	}
}

// ---------------------------------------------------------------------------
// Author provenance — client-supplied Author is always overridden
// ---------------------------------------------------------------------------

func TestEdit_AuthorOverride_FromContext(t *testing.T) {
	a := editTestActor(t)
	ctx := &testutil.FakeCtx{
		Identity_: id.Identity{Kind: id.IdentityToken, Subject: "real-caller", Role: id.Role("agent")},
	}
	resp, _ := a.handleEdit(ctx, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind:       cmdSetNodeName,
			TargetGuid: "face",
			Params:     map[string]string{"name": "X"},
			// Client tries to forge the author.
			Author: "forged-attacker",
		},
	})
	if !resp.Applied {
		t.Fatalf("edit should succeed, Detail = %q", resp.Detail)
	}
	if resp.Revision.Author != "real-caller" {
		t.Errorf("Author = %q, want real-caller (context overrides client field)", resp.Revision.Author)
	}
}

func TestEdit_AuthorOverride_NilContextIsSystem(t *testing.T) {
	a := editTestActor(t)
	resp, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "X"}, Author: "forged",
		},
	})
	if resp.Revision.Author != authorSystem {
		t.Errorf("Author = %q, want %q", resp.Revision.Author, authorSystem)
	}
}

func TestEdit_AuthorOverride_AnonymousIsUnknown(t *testing.T) {
	a := editTestActor(t)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{}}
	resp, _ := a.handleEdit(ctx, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "X"},
		},
	})
	if resp.Revision.Author != authorUnknown {
		t.Errorf("Author = %q, want %q", resp.Revision.Author, authorUnknown)
	}
}

// ---------------------------------------------------------------------------
// Persistence round-trip of ProcessedRequests
// ---------------------------------------------------------------------------

func TestEdit_Idempotency_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	a := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-restart"}
	seedForEditTest(a)

	resp1, _ := a.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "Persisted"}, RequestID: "req-survive",
		},
	})
	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Simulate restart: fresh actor, same store.
	b := &Actor{store: persist.NewFSPersist(dir), actorID: "puppet-restart"}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Replay the same RequestId after restart.
	resp2, _ := b.handleEdit(nil, gen.PuppetEditReq{
		Command: gen.PuppetEditCommand{
			Kind: cmdSetNodeName, TargetGuid: "face",
			Params: map[string]string{"name": "Persisted"}, RequestID: "req-survive",
		},
	})
	// Should be deduped — same revision, no new append.
	if resp2.Revision.ID != resp1.Revision.ID {
		t.Errorf("post-restart replay revision = %q, want %q", resp2.Revision.ID, resp1.Revision.ID)
	}
	if len(b.state.Revisions) != 2 { // init + 1 edit
		t.Errorf("post-restart Revisions len = %d, want 2", len(b.state.Revisions))
	}
}

// ---------------------------------------------------------------------------
// findNode helper
// ---------------------------------------------------------------------------

func TestFindNode_Root(t *testing.T) {
	root := &gen.PuppetNode{Guid: "root"}
	if findNode(root, "root") == nil {
		t.Error("findNode(root, root) = nil")
	}
	if findNode(root, "missing") != nil {
		t.Error("findNode(root, missing) should be nil")
	}
}

func TestFindNode_NilRoot(t *testing.T) {
	if findNode(nil, "x") != nil {
		t.Error("findNode(nil, x) should be nil")
	}
}
