package appmanager

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// nativeScaffoldManifestJSON returns a manifest matching the A1 scaffold
// template's app.manifest.json. The AgentBinding is now part of the template
// itself, so this mirrors it verbatim — no test-side binding workaround.
func nativeScaffoldManifestJSON(t *testing.T) string {
	t.Helper()
	m := gen.AppManifest{
		ID:              "app.scaffold",
		Name:            "scaffold",
		Version:         "0.1.0",
		Runtime:         "native",
		ProtocolVersion: 1,
		Namespace:       "app.scaffold",
		Permissions:     []string{},
		Schemas:         []gen.AppSchemaRef{},
		Callables: []gen.AppCallableDescriptor{
			{ID: "ping", RequestSchema: "PingRequest", ResponseSchema: "PingResponse", Service: "app.scaffold"},
		},
		Events:      []gen.AppEventDescriptor{},
		Entrypoints: []gen.AppEntrypoint{{Kind: "command", ID: "scaffold.ping", Title: "Ping", Route: "ping"}},
		AgentBinding: &gen.AppAgentBinding{
			Surface: &gen.AgentSurfaceBinding{
				AgentID:    "scaffold-agent",
				Entrypoint: "scaffold.ping",
			},
			Capability: &gen.AgentCapabilityBinding{
				Callables: []string{"ping"},
			},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(data)
}

// scaffoldAbi returns the ABI that handleNativeBuild would produce for a
// first-party native build. Subprocess (dev) is the default build mode, so
// the ABI carries the subprocess isolation.
func scaffoldAbi() gen.PluginAbi {
	return gen.PluginAbi{
		Name:            "spore-plugin",
		Version:         1,
		Encoding:        "binarycodec-v1",
		InvokeSymbol:    "PluginInvoke",
		ContractVersion: "1",
		Isolation:       "subprocess",
		TrustClass:      "first_party",
		Signer:          "sporemind.first-party",
	}
}

// nativeScaffoldE2EPlanner builds a lifecyclePlanner that mocks the project
// actor and pluginhost service, recording every callID for assertion.
func nativeScaffoldE2EPlanner(t *testing.T, calls *[]string, manifestJSON *string) lifecyclePlanner {
	t.Helper()
	const (
		// Subprocess (dev) build artifacts are executables: ".exe" on
		// Windows, no extension elsewhere. The mocked path mirrors the
		// Unix form.
		artifactPathV1 = "/test/build/plugin-app-scaffold"
		artifactHashV1 = "aabbccddeeff0011"
		artifactHashV2 = "aabbccddeeff0022"
	)
	abi := scaffoldAbi()
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		*calls = append(*calls, callID)
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "scaffold", Path: "/test/scaffold"}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			switch {
			case strings.HasSuffix(req.Path, "app.manifest.json"):
				return gen.FileSystemReadResp{Content: *manifestJSON}, nil
			case strings.HasSuffix(req.Path, "main.gen.go"):
				return gen.FileSystemReadResp{Content: "package main\n\nfunc main() {}\n"}, nil
			default:
				return gen.FileSystemReadResp{}, nil
			}
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			b64 := func(s string) gen.FileSystemReadBase64Resp {
				return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString([]byte(s))}
			}
			switch {
			case strings.HasSuffix(req.Path, "app.manifest.json"):
				return b64(*manifestJSON), nil
			case strings.HasSuffix(req.Path, "main.gen.go"):
				return b64("package main\n\nfunc main() {}\n"), nil
			default:
				return gen.FileSystemReadBase64Resp{}, nil
			}
		case "project.list":
			return "", nil
		case "pluginhost.native_build":
			return gen.NativeBuildResp{
				Result:       gen.NativeBuildResult{Success: true, ArtifactPath: artifactPathV1, ArtifactHash: artifactHashV1},
				ManifestPath: "/test/scaffold/app.manifest.json",
				Abi:          abi,
			}, nil
		case "pluginhost.artifact_load":
			return gen.PluginArtifactLoadResp{
				PluginID:     "app.scaffold",
				ArtifactHash: artifactHashV1,
				Status:       gen.AppStatus{ID: "app.scaffold", Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.invoke":
			return gen.PluginInvokeResp{Payload: []byte(`{"pong":"ok"}`)}, nil
		case "pluginhost.artifact_reload_prepare":
			return gen.PluginArtifactReloadPrepareResp{
				Token:        "reload-token",
				PluginID:     "app.scaffold",
				ArtifactHash: artifactHashV2,
				Status:       gen.AppStatus{ID: "app.scaffold", Runtime: "native", State: "active", Version: "0.2.0"},
			}, nil
		case "pluginhost.artifact_reload_commit":
			if p, ok := payload.(gen.PluginArtifactReloadCommitReq); !ok || p.Token != "reload-token" {
				t.Fatalf("commit: wrong payload: %+v", payload)
			}
			return gen.PluginArtifactReloadCommitResp{
				PluginID:     "app.scaffold",
				ArtifactHash: artifactHashV2,
				Status:       gen.AppStatus{ID: "app.scaffold", Runtime: "native", State: "active", Version: "0.2.0"},
			}, nil
		case "pluginhost.artifact_reload_abort":
			return gen.PluginArtifactReloadAbortResp{}, nil
		case "pluginhost.artifact_unload":
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

// nativeScaffoldE2ECtx builds a FakeCtx with project/pluginhost refs and a
// planner wired for the native scaffold e2e flow.
func nativeScaffoldE2ECtx(t *testing.T, calls *[]string, manifestJSON *string, projectCID identity.CanonicalID) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == id.From(projectCID)
	}
	planner := nativeScaffoldE2EPlanner(t, calls, manifestJSON)
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func newNativeScaffoldE2EActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-e2e",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

// TestNativeScaffoldE2ERegistration is the primary acceptance test for A2.
// It exercises the full plugin lifecycle through appmanager orchestration:
//
//  1. register_project (native_build → artifact_load → register) succeeds
//  2. invoke default callable ("ping") returns the plugin's payload
//  3. reload_project goes through prepare/commit (two-phase native reload)
//  4. unregister calls artifact_unload and cleans up all managed state
//
// The test uses a fake actor context + planner to mock the project actor and
// pluginhost service, isolating appmanager orchestration logic. The scaffold
// manifest, ABI, callable contract, and AgentBinding all match the A1 template
// verbatim — the template now ships a default AgentBinding so invoke works
// without any test-side binding workaround.
func TestNativeScaffoldE2ERegistration(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 42)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := nativeScaffoldManifestJSON(t)
	const appID = "app.scaffold"

	var calls []string
	ctx := nativeScaffoldE2ECtx(t, &calls, &manifestJSON, projectCID)
	a := newNativeScaffoldE2EActor(t)

	// ── Step 1: register_project ──
	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID: projectID,
	})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if regResp.Status.ID != appID {
		t.Fatalf("register status ID = %q, want %q", regResp.Status.ID, appID)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("register state = %q, want running", regResp.Status.State)
	}
	if regResp.Status.Runtime != "native" {
		t.Fatalf("register runtime = %q, want native", regResp.Status.Runtime)
	}

	// Verify app is in registry with native routing.
	if _, ok := a.Apps[appID]; !ok {
		t.Fatal("app not in Apps after register_project")
	}
	if target := a.children[appID]; target != pluginhostServiceName {
		t.Fatalf("native child routing = %q, want %q", target, pluginhostServiceName)
	}
	rec := a.Records[appID]
	if rec.ArtifactPath == "" || rec.ArtifactHash == "" || rec.Abi == nil {
		t.Fatalf("native record missing artifact info: %+v", rec)
	}
	if rec.State != "running" {
		t.Fatalf("record state = %q, want running", rec.State)
	}

	// Verify native_build and artifact_load were orchestrated.
	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.native_build") {
		t.Fatalf("expected native_build in register call sequence: %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.artifact_load") {
		t.Fatalf("expected artifact_load in register call sequence: %s", callSeq)
	}

	// ── Step 2: invoke default callable ──
	calls = nil
	invokeResp, err := a.handleInvoke(ctx, gen.AppManagerInvokeReq{
		ID:       appID,
		Callable: "ping",
		AgentID:  "scaffold-agent",
	})
	if err != nil {
		t.Fatalf("invoke ping: %v", err)
	}
	if string(invokeResp.Payload) != `{"pong":"ok"}` {
		t.Fatalf("invoke payload = %q, want {\"pong\":\"ok\"}", string(invokeResp.Payload))
	}
	if len(calls) == 0 || calls[len(calls)-1] != "pluginhost.invoke" {
		t.Fatalf("expected pluginhost.invoke as last call, got %v", calls)
	}

	// ── Step 3: reload_project (prepare/commit) ──
	// The developer bumps the manifest version on disk, then reloads: the
	// packaged candidate manifest is the source of the post-reload status.
	manifestJSON = strings.Replace(manifestJSON, `"Version":"0.1.0"`, `"Version":"0.2.0"`, 1)
	calls = nil
	reloadResp, err := a.handleReloadProject(ctx, gen.AppManagerReloadProjectReq{
		ProjectID: projectID,
		AppID:     appID,
	})
	if err != nil {
		t.Fatalf("reload_project: %v", err)
	}
	if reloadResp.Status.Version != "0.2.0" {
		t.Fatalf("reload version = %q, want 0.2.0", reloadResp.Status.Version)
	}
	if reloadResp.Status.State != stateRunning {
		t.Fatalf("reload state = %q, want running (pluginhost vocabulary normalized)", reloadResp.Status.State)
	}

	// Verify two-phase reload sequence.
	callSeq = strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_reload_prepare") {
		t.Fatalf("expected reload_prepare in reload sequence: %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.artifact_reload_commit") {
		t.Fatalf("expected reload_commit in reload sequence: %s", callSeq)
	}
	// reload must not unload the old artifact.
	if strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("reload must not unload; got unload in sequence: %s", callSeq)
	}
	// Verify record was updated with new artifact hash.
	updated := a.Records[appID]
	if updated.ArtifactHash != "aabbccddeeff0022" {
		t.Fatalf("reload should update artifact hash: got %q", updated.ArtifactHash)
	}
	if updated.Generation != rec.Generation+1 {
		t.Fatalf("reload should increment generation: got %d, want %d", updated.Generation, rec.Generation+1)
	}

	// ── Step 4: unregister ──
	calls = nil
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: appID}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	// Verify complete cleanup.
	if _, ok := a.Apps[appID]; ok {
		t.Fatal("app should be removed from Apps after unregister")
	}
	if _, ok := a.Records[appID]; ok {
		t.Fatal("record should be removed after unregister")
	}
	if _, ok := a.children[appID]; ok {
		t.Fatal("child routing should be removed after unregister")
	}
	// Verify artifact_unload was called for cleanup.
	callSeq = strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload in unregister sequence: %s", callSeq)
	}
}

// TestNativeScaffoldE2ERegistrationFailureCompensates verifies that when
// handleRegister fails after the artifact has been loaded (e.g. duplicate
// callable IDs), the loaded artifact is unloaded as compensation. This tests
// the rollback path in finishProjectRegistration.
func TestNativeScaffoldE2ERegistrationFailureCompensates(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 43)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	// Manifest with duplicate callable IDs → handleRegister will reject it.
	badManifest := gen.AppManifest{
		ID:              "app.bad",
		Name:            "bad",
		Version:         "0.1.0",
		Runtime:         "native",
		ProtocolVersion: 1,
		Namespace:       "app.bad",
		Callables: []gen.AppCallableDescriptor{
			{ID: "dup", RequestSchema: "A", ResponseSchema: "B"},
			{ID: "dup", RequestSchema: "A", ResponseSchema: "B"},
		},
		Entrypoints: []gen.AppEntrypoint{{Kind: "command", ID: "bad.dup", Title: "Dup", Route: "dup"}},
	}
	badJSON, _ := json.Marshal(badManifest)

	var calls []string
	badJSONStr := string(badJSON)
	ctx := nativeScaffoldE2ECtx(t, &calls, &badJSONStr, projectCID)
	a := newNativeScaffoldE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID: projectID,
	})
	if err == nil {
		t.Fatal("expected registration failure for duplicate callables")
	}

	// The artifact_load should have succeeded, then the failed register
	// must trigger artifact_unload as compensation.
	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_load") {
		t.Fatalf("expected artifact_load before failure: %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload compensation after register failure: %s", callSeq)
	}

	// App must not be in the registry.
	if _, ok := a.Apps["app.bad"]; ok {
		t.Fatal("failed registration must not leave app in registry")
	}
}
