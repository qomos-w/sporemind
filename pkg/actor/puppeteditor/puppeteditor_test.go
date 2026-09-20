package puppeteditor

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// newTestActor returns a fresh Actor backed by an in-memory FSPersist rooted in
// a temp dir, with actorID set so Save/Load key correctly.
func newTestActor(t *testing.T, dir string) *Actor {
	t.Helper()
	return &Actor{
		store:   persist.NewFSPersist(dir),
		actorID: "puppet-test",
	}
}

// mutateForTest seeds the actor with a non-trivial document, revision chain
// and staged assets so round-trip fidelity can be verified after restart.
func mutateForTest(a *Actor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state = puppetState{
		Document: gen.PuppetDocument{
			ID:         "doc-rt",
			Revision:   7,
			Name:       "Hero",
			SourcePath: "/proj/hero.inp",
			Root: gen.PuppetNode{
				Guid: "root", Name: "Root", Kind: "group", Enabled: true,
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
			},
			CreatedAt: "2026-08-14T05:00:00Z",
			UpdatedAt: "2026-08-14T06:00:00Z",
		},
		Revisions: []gen.PuppetRevision{
			{ID: "rev-0", DocumentID: "doc-rt", Sequence: 0, ParentRevisionID: "", Author: "system", CommandKind: "init", Timestamp: "2026-08-14T05:00:00Z"},
			{ID: "rev-1", DocumentID: "doc-rt", Sequence: 1, ParentRevisionID: "rev-0", Author: "coder", CommandKind: "add_node", Summary: "added Face", Timestamp: "2026-08-14T05:02:00Z"},
			{ID: "rev-2", DocumentID: "doc-rt", Sequence: 2, ParentRevisionID: "rev-1", Author: "coder", CommandKind: "commit_asset", Summary: "attached hair", Timestamp: "2026-08-14T05:05:00Z"},
		},
		Assets: []gen.PuppetStagedAsset{
			{ID: "asset-1", DocumentID: "doc-rt", State: "staged", Kind: "texture", DataRef: "sha256:abc", Name: "hair-gen", Width: 1024, Height: 1024, SourceRef: "gen-req-42", CreatedAt: "2026-08-14T05:01:00Z"},
			{ID: "asset-2", DocumentID: "doc-rt", State: "committed", Kind: "texture", DataRef: "sha256:def", NodeGuid: "face", ReviewedAt: "2026-08-14T05:05:00Z", CreatedAt: "2026-08-14T05:00:30Z"},
			{ID: "asset-3", DocumentID: "doc-rt", State: "rejected", Kind: "reference", DataRef: "sha256:ghi", ReviewedAt: "2026-08-14T05:06:00Z", CreatedAt: "2026-08-14T05:01:30Z"},
		},
	}
}

// ---------------------------------------------------------------------------
// Persistence — restart recovery
// ---------------------------------------------------------------------------

// TestPersist_RoundTrip verifies that Save then Load restores the exact same
// document, revision chain and staged assets — the core restart-recovery
// contract.
func TestPersist_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := newTestActor(t, dir)
	mutateForTest(a)

	if err := a.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Simulate a restart: a brand-new actor instance pointing at the same
	// persisted directory.
	b := newTestActor(t, dir)
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// Document identity and revision counter.
	if b.state.Document.ID != "doc-rt" {
		t.Errorf("Document.Id = %q, want doc-rt", b.state.Document.ID)
	}
	if b.state.Document.Revision != 7 {
		t.Errorf("Document.Revision = %d, want 7", b.state.Document.Revision)
	}
	if b.state.Document.Name != "Hero" {
		t.Errorf("Document.Name = %q, want Hero", b.state.Document.Name)
	}
	if b.state.Document.SourcePath != "/proj/hero.inp" {
		t.Errorf("Document.SourcePath = %q", b.state.Document.SourcePath)
	}

	// Recursive node tree fidelity.
	root := b.state.Document.Root
	if root.Guid != "root" || root.Kind != "group" {
		t.Errorf("Root = %+v", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("Root.Children len = %d, want 2", len(root.Children))
	}
	face := root.Children[0]
	if face.Guid != "face" || face.TextureAssetID != "asset-1" {
		t.Errorf("Face = %+v", face)
	}
	if face.Params["tint"] != "#FFFFFF" {
		t.Errorf("Face.Params[tint] = %q", face.Params["tint"])
	}
	eyes := root.Children[1]
	if len(eyes.Children) != 2 {
		t.Fatalf("Eyes.Children len = %d, want 2", len(eyes.Children))
	}
	if eyes.Children[1].Enabled != false {
		t.Errorf("Eye.R.Enabled = %v, want false", eyes.Children[1].Enabled)
	}

	// Revision chain.
	if len(b.state.Revisions) != 3 {
		t.Fatalf("Revisions len = %d, want 3", len(b.state.Revisions))
	}
	if b.state.Revisions[0].ParentRevisionID != "" {
		t.Errorf("rev-0 parent = %q, want empty", b.state.Revisions[0].ParentRevisionID)
	}
	if b.state.Revisions[2].ParentRevisionID != "rev-1" {
		t.Errorf("rev-2 parent = %q, want rev-1", b.state.Revisions[2].ParentRevisionID)
	}
	if b.state.Revisions[2].Author != "coder" {
		t.Errorf("rev-2 author = %q", b.state.Revisions[2].Author)
	}

	// Staged assets.
	if len(b.state.Assets) != 3 {
		t.Fatalf("Assets len = %d, want 3", len(b.state.Assets))
	}
	if b.state.Assets[1].State != "committed" || b.state.Assets[1].NodeGuid != "face" {
		t.Errorf("asset-2 = %+v", b.state.Assets[1])
	}
}

// TestPersist_LoadFirstStart verifies that a fresh actor (no prior state) loads
// without error and leaves the state at zero.
func TestPersist_LoadFirstStart(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	if err := a.Load(); err != nil {
		t.Fatalf("first-start load must succeed: %v", err)
	}
	if a.state.Document.ID != "" || len(a.state.Revisions) != 0 || len(a.state.Assets) != 0 {
		t.Errorf("state = %+v, want zero", a.state)
	}
}

// ---------------------------------------------------------------------------
// Snapshot callable
// ---------------------------------------------------------------------------

// TestSnapshot_ReturnsDocumentAndCounts verifies that the read-only snapshot
// carries the document, recursive node tree, and the derived staging/revision
// counts.
func TestSnapshot_ReturnsDocumentAndCounts(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, err := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	snap := resp.Snapshot

	if snap.Document.ID != "doc-rt" {
		t.Errorf("Document.Id = %q", snap.Document.ID)
	}
	if snap.Document.Revision != 7 {
		t.Errorf("Document.Revision = %d", snap.Document.Revision)
	}
	// Only one asset is in "staged" state in the seed data.
	if snap.StagedAssetCount != 1 {
		t.Errorf("StagedAssetCount = %d, want 1", snap.StagedAssetCount)
	}
	if snap.RevisionCount != 3 {
		t.Errorf("RevisionCount = %d, want 3", snap.RevisionCount)
	}
	// Recursive tree is present.
	if len(snap.Document.Root.Children) != 2 {
		t.Errorf("Root.Children len = %d, want 2", len(snap.Document.Root.Children))
	}
}

// TestSnapshot_ReturnsCopy verifies that the snapshot is a value copy — callers
// cannot mutate the actor's internal state through the returned document.
func TestSnapshot_ReturnsCopy(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, _ := a.handleSnapshot(nil, gen.PuppetDocumentSnapshotReq{})
	resp.Snapshot.Document.Revision = 999
	resp.Snapshot.Document.Root.Children[0].Guid = "tampered"

	// Internal state must be untouched.
	a.mu.RLock()
	doc := a.state.Document
	a.mu.RUnlock()
	if doc.Revision != 7 {
		t.Errorf("internal Document.Revision = %d, want 7 (snapshot mutation leaked)", doc.Revision)
	}
	if doc.Root.Children[0].Guid != "face" {
		t.Errorf("internal Root.Children[0].Guid = %q, want face", doc.Root.Children[0].Guid)
	}
}

// ---------------------------------------------------------------------------
// Revision log callable
// ---------------------------------------------------------------------------

// TestRevisionLog_All verifies the full log is returned when no limit is set.
func TestRevisionLog_All(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, err := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{})
	if err != nil {
		t.Fatalf("revision log: %v", err)
	}
	if len(resp.Revisions) != 3 {
		t.Fatalf("Revisions len = %d, want 3", len(resp.Revisions))
	}
	// Chain integrity: root has empty parent.
	if resp.Revisions[0].ParentRevisionID != "" {
		t.Errorf("rev-0 parent = %q", resp.Revisions[0].ParentRevisionID)
	}
}

// TestRevisionLog_Limited verifies that the Limit parameter returns the most
// recent N entries in chronological order.
func TestRevisionLog_Limited(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, err := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{Limit: 2})
	if err != nil {
		t.Fatalf("revision log: %v", err)
	}
	if len(resp.Revisions) != 2 {
		t.Fatalf("Revisions len = %d, want 2", len(resp.Revisions))
	}
	// Most recent 2 = rev-1 and rev-2 (in order).
	if resp.Revisions[0].ID != "rev-1" {
		t.Errorf("Revisions[0].Id = %q, want rev-1", resp.Revisions[0].ID)
	}
	if resp.Revisions[1].ID != "rev-2" {
		t.Errorf("Revisions[1].Id = %q, want rev-2", resp.Revisions[1].ID)
	}
}

// TestRevisionLog_LimitExceedsLength verifies a limit larger than the log
// returns everything.
func TestRevisionLog_LimitExceedsLength(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, _ := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{Limit: 100})
	if len(resp.Revisions) != 3 {
		t.Errorf("Revisions len = %d, want 3", len(resp.Revisions))
	}
}

// TestRevisionLog_ReturnsCopy verifies the returned slice is a copy — mutating
// it cannot affect the actor's internal revision history.
func TestRevisionLog_ReturnsCopy(t *testing.T) {
	a := newTestActor(t, t.TempDir())
	mutateForTest(a)

	resp, _ := a.handleRevisionLog(nil, gen.PuppetRevisionLogReq{})
	resp.Revisions[0].Author = "tampered"
	resp.Revisions = resp.Revisions[:0]

	a.mu.RLock()
	revs := a.state.Revisions
	a.mu.RUnlock()
	if len(revs) != 3 {
		t.Errorf("internal Revisions len = %d, want 3 (returned slice alias leaked)", len(revs))
	}
	if revs[0].Author != "system" {
		t.Errorf("internal rev-0 Author = %q, want system", revs[0].Author)
	}
}

// ---------------------------------------------------------------------------
// Author provenance
// ---------------------------------------------------------------------------

// TestAuthorFromContext_TokenIdentity verifies that an authenticated caller's
// token subject is used as the author, not any request field.
func TestAuthorFromContext_TokenIdentity(t *testing.T) {
	ctx := &testutil.FakeCtx{
		Identity_: idIdentity("real-agent", "agent"),
	}
	if got := authorFromContext(ctx); got != "real-agent" {
		t.Errorf("author = %q, want real-agent", got)
	}
}

// TestAuthorFromContext_AnonymousIsUnknown verifies that an unauthenticated
// external caller (zero identity) records "unknown", not a trusted identity.
func TestAuthorFromContext_AnonymousIsUnknown(t *testing.T) {
	ctx := &testutil.FakeCtx{Identity_: idIdentity("", "")}
	if got := authorFromContext(ctx); got != authorUnknown {
		t.Errorf("author = %q, want %q", got, authorUnknown)
	}
}

// TestAuthorFromContext_NilContextIsSystem verifies that internal actor-to-actor
// calls (nil context) record "system".
func TestAuthorFromContext_NilContextIsSystem(t *testing.T) {
	if got := authorFromContext(nil); got != authorSystem {
		t.Errorf("author = %q, want %q", got, authorSystem)
	}
}

// idIdentity builds a token Identity for the given subject/role, or a zero
// (anonymous) Identity when both are empty.
func idIdentity(subject, role string) id.Identity {
	if subject == "" && role == "" {
		return id.Identity{}
	}
	return id.Identity{Kind: id.IdentityToken, Subject: subject, Role: id.Role(role)}
}
