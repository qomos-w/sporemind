package puppeteditor

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func agentReq(a *Actor) gen.PuppetAgentRequest {
	a.mu.RLock()
	id := a.state.Document.ID
	a.mu.RUnlock()
	return gen.PuppetAgentRequest{TargetGuid: id, RequestID: "req-agent-1", AuditSource: "tool.puppet"}
}

func TestAgentPolicy_OnlyAgentRoleAndInternalSurface(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-policy"}
	seedForEditTest(a)
	anonymous := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.handleAgentSnapshot(anonymous, gen.PuppetAgentSnapshotReq{Envelope: agentReq(a)}); err == nil {
		t.Fatal("anonymous caller reached Agent snapshot")
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if _, err := a.handleAgentSnapshot(ctx, gen.PuppetAgentSnapshotReq{Envelope: agentReq(a)}); err == nil {
		t.Fatal("human caller reached Agent snapshot")
	}
	registered := testutil.HumanCtx(testutil.GenActorID())
	registered.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(registered); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{agentSnapshot, agentRevisionLog, agentAssetList, agentEdit, agentAssetStage, agentAssetReject, "puppet.agent.read.asset_read", agentExploreQuery, agentRevertTo, agentExploreCapture} {
		opts := registered.RegOpts[name]
		if actor.ResolveVisibility(opts...) != actor.VisibilityInternal {
			t.Errorf("%s is not internal", name)
		}
		if !strings.Contains(actor.ResolveDescription(opts...), "Agent") {
			t.Errorf("%s has no policy description", name)
		}
	}
	if _, ok := registered.RegOpts["puppet.agent.act.asset_commit"]; ok {
		t.Fatal("Agent asset commit must not be exposed")
	}
}

func TestAgentPolicy_EnvelopeAndIllegalCommandDenied(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-envelope"}
	seedForEditTest(a)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	req := agentReq(a)
	req.RequestID = ""
	if _, err := a.handleAgentSnapshot(ctx, gen.PuppetAgentSnapshotReq{Envelope: req}); err == nil {
		t.Fatal("missing RequestId accepted")
	}
	req = agentReq(a)
	resp, err := a.handleAgentEdit(ctx, gen.PuppetAgentEditReq{Envelope: req, Command: gen.PuppetEditCommand{Kind: "delete_everything", TargetGuid: req.TargetGuid, RequestID: req.RequestID}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Result.Applied {
		t.Fatal("illegal command applied")
	}
}

func TestAgentPolicy_AuthorizedEditAuditsSource(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-audit"}
	seedForEditTest(a)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	req := agentReq(a)
	resp, err := a.handleAgentEdit(ctx, gen.PuppetAgentEditReq{Envelope: req, Command: gen.PuppetEditCommand{Kind: cmdSetNodeName, TargetGuid: "face", RequestID: req.RequestID, Params: map[string]string{"name": "Draft"}}})
	if err != nil || !resp.Result.Applied {
		t.Fatalf("authorized edit failed: %v %+v", err, resp.Result)
	}
	if resp.Result.Revision.AuditSource != req.AuditSource {
		t.Fatalf("AuditSource=%q, want %q", resp.Result.Revision.AuditSource, req.AuditSource)
	}
}

func TestAgentRevisionProvenanceImmutableAtAppend(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-immutable"}
	seedForEditTest(a)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	req := agentReq(a)
	if _, err := a.handleAgentEdit(ctx, gen.PuppetAgentEditReq{Envelope: req, Command: gen.PuppetEditCommand{Kind: cmdSetNodeName, TargetGuid: "face", RequestID: req.RequestID, Params: map[string]string{"name": "Agent draft"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleEdit(nil, gen.PuppetEditReq{Command: gen.PuppetEditCommand{Kind: cmdSetNodeName, TargetGuid: "face", RequestID: "public-edit", Params: map[string]string{"name": "Public draft"}}}); err != nil {
		t.Fatal(err)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if got := a.state.Revisions[len(a.state.Revisions)-2].AuditSource; got != req.AuditSource {
		t.Fatalf("agent revision source=%q, want %q", got, req.AuditSource)
	}
	if got := a.state.Revisions[len(a.state.Revisions)-1].AuditSource; got != "" {
		t.Fatalf("public revision source=%q, want empty", got)
	}
}

func TestAgentRejectConcurrentProvenanceBoundToOwnRevision(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-reject-concurrent"}
	seedForEditTest(a)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	const count = 12
	assets := make([]gen.PuppetStagedAsset, count)
	for i := range assets {
		resp, err := a.handleAssetStage(nil, gen.PuppetAssetStageReq{Kind: assetKindTexture, DataRef: fmt.Sprintf("sha256:%064d", i), Name: fmt.Sprintf("asset-%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		assets[i] = resp.Asset
	}
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i, asset := range assets {
		wg.Add(1)
		go func(i int, asset gen.PuppetStagedAsset) {
			defer wg.Done()
			req := agentReq(a)
			req.RequestID = fmt.Sprintf("reject-%d", i)
			req.AuditSource = fmt.Sprintf("agent-source-%d", i)
			_, err := a.handleAgentAssetReject(ctx, gen.PuppetAgentAssetRejectReq{Envelope: req, AssetID: asset.ID, Reason: req.AuditSource})
			errs <- err
		}(i, asset)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, revision := range a.state.Revisions {
		if revision.CommandKind != "reject_asset" {
			continue
		}
		if !strings.Contains(revision.Summary, revision.AuditSource) {
			t.Fatalf("revision %s provenance %q does not match its own rejection summary %q", revision.ID, revision.AuditSource, revision.Summary)
		}
	}
}

func TestAgentPolicy_TargetAndRequestBinding(t *testing.T) {
	a := &Actor{store: persist.NewFSPersist(t.TempDir()), actorID: "agent-binding"}
	seedForEditTest(a)
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Kind: id.IdentityToken, Subject: "agent-1", Role: "agent"}}
	req := agentReq(a)
	cmd := gen.PuppetEditCommand{Kind: cmdSetNodeName, TargetGuid: "face", RequestID: "other", Params: map[string]string{"name": "x"}}
	if _, err := a.handleAgentEdit(ctx, gen.PuppetAgentEditReq{Envelope: req, Command: cmd}); err == nil {
		t.Fatal("mismatched RequestId accepted")
	}
	req.TargetGuid = "other-document"
	if _, err := a.handleAgentSnapshot(ctx, gen.PuppetAgentSnapshotReq{Envelope: req}); err == nil {
		t.Fatal("wrong document accepted")
	}
}
