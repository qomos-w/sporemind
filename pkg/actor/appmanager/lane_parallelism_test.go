package appmanager

import (
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestLockAppSerializesSameAppOnly pins the per-app transaction lock
// semantics: different apps take independent locks, the same app serializes.
func TestLockAppSerializesSameAppOnly(t *testing.T) {
	a := &Actor{}
	release := a.lockApp("app.a")

	otherDone := make(chan struct{})
	go func() { a.lockApp("app.b")(); close(otherDone) }()
	select {
	case <-otherDone:
	case <-time.After(2 * time.Second):
		t.Fatal("lockApp(app.b) blocked behind lockApp(app.a): per-app locks must be independent")
	}

	sameDone := make(chan struct{})
	go func() { a.lockApp("app.a")(); close(sameDone) }()
	select {
	case <-sameDone:
		t.Fatal("lockApp(app.a) re-entered while held: per-app lock must serialize")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-sameDone:
	case <-time.After(2 * time.Second):
		t.Fatal("lockApp(app.a) never acquired after release")
	}
}

// TestPluginLoadOtherAppDoesNotQueueBehindSlowReload is the regression test
// for the old shared appmanager_ops lane: a reload of app A parked inside
// pluginhost.artifact_reload_prepare must not queue an unrelated app B's
// plugin_load. plugin_load is registered stateless and takes only B's
// per-app lock, so B's load completes while A's reload is still in flight.
func TestPluginLoadOtherAppDoesNotQueueBehindSlowReload(t *testing.T) {
	a := newPluginLifecycleActor(t)
	abiA := &gen.PluginAbi{Name: "spore-plugin", Version: 1, Encoding: "binarycodec-v1", InvokeSymbol: "PluginInvoke", ContractVersion: "1", Isolation: "subprocess", TrustClass: "first_party", Signer: "sporemind.first-party"}
	manifestA := gen.AppManifest{ID: "app.slow", Name: "Slow", Version: "1.0.0", Runtime: "native", ProtocolVersion: 1, Namespace: "slow"}
	a.Apps["app.slow"] = manifestA
	a.Records["app.slow"] = appRecord{Manifest: manifestA, ArtifactPath: "/tmp/slow.so", ArtifactHash: "aa000000000001", Abi: abiA, State: "running", PackageHash: "old-package"}
	a.children["app.slow"] = pluginhostServiceName
	seedPlugin(a, "stopped")

	prepareEntered := make(chan struct{})
	releasePrepare := make(chan struct{})
	reloadDone := make(chan error, 1)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return pluginRef, name == pluginhostServiceName }
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			switch callID {
			case "pluginhost.artifact_reload_prepare":
				close(prepareEntered)
				<-releasePrepare
				req := payload.(gen.PluginArtifactReloadPrepareReq)
				return gen.PluginArtifactReloadPrepareResp{Token: "tok", PluginID: req.Manifest.ID, ArtifactHash: req.ArtifactHash, Status: gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "running", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_reload_commit":
				return gen.PluginArtifactReloadCommitResp{PluginID: "app.slow", ArtifactHash: "new-artifact", Status: gen.AppStatus{ID: "app.slow", Runtime: "native", State: "running", Version: "2.0.0"}}, nil
			case "pluginhost.artifact_load":
				req := payload.(gen.PluginArtifactLoadReq)
				return gen.PluginArtifactLoadResp{PluginID: req.Manifest.ID, ArtifactHash: req.ArtifactHash}, nil
			case "pluginhost.assets_put":
				return gen.PluginAssetsPutResp{}, nil
			default:
				t.Errorf("unexpected planner call %s", callID)
				return nil, nil
			}
		}}
	}

	candidateA := manifestA
	candidateA.Version = "2.0.0"
	go func() {
		_, err := a.handleReload(ctx, gen.AppManagerReloadReq{ID: "app.slow", PackageHash: "new-package", CandidateManifest: &candidateA, CandidateAbi: abiA, CandidateArtifactPath: "/tmp/slow-new.so", CandidateArtifactHash: "new-artifact"})
		reloadDone <- err
	}()

	<-prepareEntered
	loadDone := make(chan error, 1)
	go func() {
		_, err := a.handlePluginLoad(ctx, gen.AppManagerPluginLoadReq{ID: "app.demo"})
		loadDone <- err
	}()
	select {
	case err := <-loadDone:
		if err != nil {
			t.Fatalf("plugin_load of unrelated app failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plugin_load(app.demo) queued behind app.slow's in-flight reload: stateless lifecycle ops must not share the reload lane")
	}

	close(releasePrepare)
	if err := <-reloadDone; err != nil {
		t.Fatalf("reload: %v", err)
	}
}
