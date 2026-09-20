package appmanager

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// scaffoldAppDef returns an .appdef definition that matches the A2 scaffold
// manifest produced by nativeScaffoldManifestJSON. The manifest consistency gate
// derives the expected manifest from this source, so it must be semantically
// equivalent to the on-disk app.manifest.json used for the same app.
func scaffoldAppDef() string {
	return `// scaffold.appdef
app Scaffold {
	id:          "app.scaffold"
	name:        "scaffold"
	version:     "0.1.0"
	namespace:   "app.scaffold"
	permissions: []

	struct PingRequest {
	}

	struct PingResponse {
	}

	callable ping {
		request:  PingRequest
		response: PingResponse
	}

	entrypoint command scaffold.ping {
		title: "Ping"
		route: "ping"
	}
}
`
}

// nativeScaffoldGatedE2EPlanner is a configurable planner for register_project
// gate acceptance tests. It supplies .appdef, .go sources and a plugin dispatch
// registry, so the four inline gates are actually executed.
func nativeScaffoldGatedE2EPlanner(t *testing.T, calls *[]string, manifestJSON, appdefContent, handlersGo string, registeredCallables []string) lifecyclePlanner {
	t.Helper()
	const (
		// Subprocess (dev) build artifacts are executables: no extension on
		// Unix, ".exe" on Windows. The mocked path mirrors the Unix form.
		artifactPathV1 = "/test/build/plugin-app-scaffold"
		artifactHashV1 = "aabbccddeeff0011"
	)
	abi := scaffoldAbi()

	files := map[string]string{
		"app.manifest.json": manifestJSON,
		"scaffold.appdef":   appdefContent,
		"main.gen.go":       "package main\n\nfunc main() {}\n",
		"handlers.go":       handlersGo,
	}

	readBase64 := func(path string) string {
		name := path[strings.LastIndex(path, "/")+1:]
		content := files[name]
		return base64.StdEncoding.EncodeToString([]byte(content))
	}

	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		*calls = append(*calls, callID)
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "scaffold", Path: "/test/scaffold"}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			name := req.Path[strings.LastIndex(req.Path, "/")+1:]
			content, ok := files[name]
			if !ok {
				return gen.FileSystemReadResp{}, nil
			}
			return gen.FileSystemReadResp{Content: content}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			return gen.FileSystemReadBase64Resp{Content: readBase64(req.Path)}, nil
		case "project.list":
			req := payload.(gen.FileSystemListReq)
			// resolveSingleAppDef uses Depth:1 to find the .appdef file.
			if req.Depth == 1 {
				return listText(gen.FileEntry{Name: "scaffold.appdef", IsDir: false}), nil
			}
			// handleProjectPackage and collectGoSourceFiles use Depth:-1 to
			// enumerate source files. Exclude .appdef here because it would be
			// treated as a binary asset and require read_base64.
			var items []gen.FileEntry
			for name, isDir := range map[string]bool{"main.gen.go": false, "handlers.go": false} {
				items = append(items, gen.FileEntry{Name: name, IsDir: isDir})
			}
			return listText(items...), nil
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
		case "pluginhost.list_plugins":
			return pluginhostactor.ListPluginsResp{Plugins: []pluginhostactor.PluginDescriptor{
				{ID: "app.scaffold", Callables: registeredCallables},
			}}, nil
		case "pluginhost.artifact_unload":
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		default:
			t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

// nativeScaffoldGatedE2ECtx builds a FakeCtx wired with the gated planner.
func nativeScaffoldGatedE2ECtx(t *testing.T, calls *[]string, manifestJSON, appdefContent, handlersGo string, registeredCallables []string) *testutil.FakeCtx {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		// The project actor is looked up by canonical ID from projectCID.
		return projectRef, true
	}
	planner := nativeScaffoldGatedE2EPlanner(t, calls, manifestJSON, appdefContent, handlersGo, registeredCallables)
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

// TestNativeScaffoldE2ERegistrationWithGates_AllPass verifies that when an
// .appdef is present and every gate passes, register_project succeeds and the app
// is registered. This is the gate-positive path for the inline gate check.
func TestNativeScaffoldE2ERegistrationWithGates_AllPass(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 44)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := nativeScaffoldManifestJSON(t)
	appdef := scaffoldAppDef()

	var calls []string
	handlers := "package main\n\nfunc handlePing() {}\n"
	ctx := nativeScaffoldGatedE2ECtx(t, &calls, manifestJSON, appdef, handlers, []string{"ping"})
	a := newNativeScaffoldE2EActor(t)

	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("expected running state, got %q", regResp.Status.State)
	}
	if _, ok := a.Apps["app.scaffold"]; !ok {
		t.Fatal("app not registered after passing gates")
	}

	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.list_plugins") {
		t.Fatalf("expected coverage gate to query pluginhost.list_plugins, got %s", callSeq)
	}
	if !strings.Contains(callSeq, "pluginhost.artifact_load") {
		t.Fatalf("expected artifact_load, got %s", callSeq)
	}
}

// TestNativeScaffoldE2ERegistrationWithGates_StubFillingFails verifies that
// register_project is rejected when a source file still contains
// ErrNotImplemented, and the rejection names the stub_filling gate.
func TestNativeScaffoldE2ERegistrationWithGates_StubFillingFails(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 45)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := nativeScaffoldManifestJSON(t)
	appdef := scaffoldAppDef()

	var calls []string
	handlers := "package main\n\nfunc handlePing() { return ErrNotImplemented }\n"
	ctx := nativeScaffoldGatedE2ECtx(t, &calls, manifestJSON, appdef, handlers, []string{"ping"})
	a := newNativeScaffoldE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err == nil {
		t.Fatal("expected register_project to fail with unfilled stub")
	}
	msg := err.Error()
	if !strings.Contains(msg, "stub_filling") {
		t.Fatalf("expected stub_filling gate in error, got %q", msg)
	}
	if !strings.Contains(msg, "ErrNotImplemented") {
		t.Fatalf("expected ErrNotImplemented in error, got %q", msg)
	}

	if _, ok := a.Apps["app.scaffold"]; ok {
		t.Fatal("failed registration must not leave app in registry")
	}

	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload compensation after gate failure, got %s", callSeq)
	}
}

// TestNativeScaffoldE2ERegistrationWithGates_ManifestMismatchFails verifies that
// register_project is rejected when the on-disk app.manifest.json does not
// match the manifest derived from .appdef, and the error names the
// manifest_consistency gate.
func TestNativeScaffoldE2ERegistrationWithGates_ManifestMismatchFails(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 46)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()
	manifestJSON := nativeScaffoldManifestJSON(t)

	// appdef declares a different callable than the manifest, so the manifest
	// consistency gate must fail before coverage is checked.
	badAppDef := `// scaffold.appdef
app Scaffold {
	id:          "app.scaffold"
	name:        "scaffold"
	version:     "0.1.0"
	namespace:   "app.scaffold"
	permissions: []

	struct PongRequest {
	}

	struct PongResponse {
	}

	callable pong {
		request:  PongRequest
		response: PongResponse
	}

	entrypoint command scaffold.ping {
		title: "Ping"
		route: "ping"
	}
}
`

	var calls []string
	handlers := "package main\n\nfunc handlePong() {}\n"
	ctx := nativeScaffoldGatedE2ECtx(t, &calls, manifestJSON, badAppDef, handlers, []string{"pong"})
	a := newNativeScaffoldE2EActor(t)

	_, err = a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err == nil {
		t.Fatal("expected register_project to fail with manifest mismatch")
	}
	msg := err.Error()
	if !strings.Contains(msg, "manifest_consistency") {
		t.Fatalf("expected manifest_consistency gate in error, got %q", msg)
	}
	if !strings.Contains(msg, "ping") && !strings.Contains(msg, "pong") {
		t.Fatalf("expected mismatch detail about callable ids, got %q", msg)
	}

	if _, ok := a.Apps["app.scaffold"]; ok {
		t.Fatal("failed registration must not leave app in registry")
	}

	callSeq := strings.Join(calls, ",")
	if !strings.Contains(callSeq, "pluginhost.artifact_unload") {
		t.Fatalf("expected artifact_unload compensation after gate failure, got %s", callSeq)
	}
}
