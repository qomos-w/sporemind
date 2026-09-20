package appmanager

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// This file covers BP7: dev_generate / dev_gate / register_project must all
// accept an optional AppDir (subdirectory of the project root) so that apps
// like plugin-dev-example/ are reachable through the official 5-step
// flow. The default (no AppDir) must behave exactly as before.

const subdirDemoAppDef = `// app.appdef — BP7 subdir test fixture
app SubdirDemo {
    id:          "app.subdirdemo"
    name:        "SubdirDemo"
    version:     "0.1.0"
    namespace:   "subdirdemo"

    struct PingRequest {
        optional Message: string
    }
    struct PingResponse {
        Pong: string
    }

    callable ping {
        request:  PingRequest
        response: PingResponse
        effect:   "read"
        toolName: "subdirdemo-ping"
    }

    entrypoint view main {
        title: "Subdir Demo"
        route: "/"
    }
}
`

// subdirHandlersFilled replaces the generated ErrNotImplemented stub after
// dev_generate, mirroring workflow step 3 (implement handlers).
const subdirHandlersFilled = `// handlers.go — agent-owned
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handlePing(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]string{"pong": "ok"}}, nil
}
`

// bp7Env is a real-filesystem-backed project fixture plus the mock planner
// wiring for project.* and pluginhost.* callables.
type bp7Env struct {
	t              *testing.T
	root           string // absolute, forward slashes
	mu             sync.Mutex
	protectedFiles []string
	buildAppDirs   []string // AppDir values seen by pluginhost.native_build
	buildReqs      []gen.NativeBuildReq
	loadedPlugins  map[string][]string // pluginID → callables, tracked by artifact_load/unload
}

// newBP7Project creates the project root under the current package directory
// so that codegen.EnsureDevSDKWorkspace can walk up and find the in-repo
// sporemind-plugin-sdk (mirrors testSDKPath in pkg/codegen tests). The root
// is removed on cleanup.
func newBP7Project(t *testing.T, name string) *bp7Env {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(wd, name+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return &bp7Env{t: t, root: filepath.ToSlash(root), loadedPlugins: map[string][]string{}}
}

func (e *bp7Env) write(rel, content string) {
	e.t.Helper()
	path := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *bp7Env) exists(rel string) bool {
	_, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(rel)))
	return err == nil
}

func (e *bp7Env) readRel(rel string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(data), true
}

// listDir emulates project.list over the real filesystem: Depth 1 lists
// one level, Depth -1 lists recursively with slash-relative names. Hidden
// entries are skipped (matching the default even with NoIgnore).
func (e *bp7Env) listDir(dir string, depth int) []gen.FileEntry {
	e.t.Helper()
	// The caller may pass either an absolute root path (with or without
	// trailing slash) or a path inside the fixture root.
	rel := strings.TrimPrefix(dir, e.root)
	rel = strings.TrimPrefix(rel, "/")
	abs := e.root
	if rel != "" {
		abs = filepath.Join(e.root, filepath.FromSlash(rel))
	}
	var out []gen.FileEntry
	var walk func(d, rel string, levels int)
	walk = func(d, rel string, levels int) {
		entries, err := os.ReadDir(d)
		if err != nil {
			return
		}
		for _, en := range entries {
			name := en.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			if en.IsDir() {
				if depth == -1 || levels < depth {
					walk(filepath.Join(d, name), childRel, levels+1)
				}
				continue
			}
			out = append(out, gen.FileEntry{Name: childRel, IsDir: false})
		}
	}
	walk(abs, "", 1)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// planner returns a lifecyclePlanner backed by the fixture filesystem. Only
// the callables used by dev_generate/dev_gate/register_project are answered.
func (e *bp7Env) planner() lifecyclePlanner {
	abi := scaffoldAbi()
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "bp7", Path: e.root}}}, nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadResp{}, err
			}
			return gen.FileSystemReadResp{Content: string(data)}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadBase64Resp{}, err
			}
			return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
		case "project.list":
			req := payload.(gen.FileSystemListReq)
			return listText(e.listDir(req.Path, int(req.Depth))...), nil
		case "project.set_protected_files":
			req := payload.(gen.SetProtectedFilesReq)
			e.mu.Lock()
			e.protectedFiles = append([]string{}, req.Files...)
			e.mu.Unlock()
			return gen.SetProtectedFilesResp{Count: int64(len(req.Files))}, nil
		case "pluginhost.native_build":
			req := payload.(gen.NativeBuildReq)
			e.mu.Lock()
			e.buildReqs = append(e.buildReqs, req)
			e.buildAppDirs = append(e.buildAppDirs, req.AppDir)
			e.mu.Unlock()
			return gen.NativeBuildResp{
				Result:       gen.NativeBuildResult{Success: true, ArtifactPath: e.root + "/.sporecode/build/plugin-test", ArtifactHash: "bp7000000000001"},
				ManifestPath: e.root + "/app.manifest.json",
				Abi:          abi,
			}, nil
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			e.mu.Lock()
			e.loadedPlugins[req.Manifest.ID] = []string{"ping"}
			e.mu.Unlock()
			return gen.PluginArtifactLoadResp{
				PluginID:     req.Manifest.ID,
				ArtifactHash: "bp7000000000001",
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.artifact_unload":
			req := payload.(gen.PluginArtifactUnloadReq)
			e.mu.Lock()
			delete(e.loadedPlugins, req.PluginID)
			e.mu.Unlock()
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		case "pluginhost.assets_put":
			// BP5: live native registration pushes the packaged asset bundle
			// to the pluginhost for /plugin/{id}/ HTTP serving.
			return gen.PluginAssetsPutResp{}, nil
		case "pluginhost.artifact_reload_prepare":
			req := payload.(gen.PluginArtifactReloadPrepareReq)
			return gen.PluginArtifactReloadPrepareResp{
				Token:        "reload-token",
				PluginID:     req.Manifest.ID,
				ArtifactHash: req.ArtifactHash,
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.artifact_reload_commit":
			req := payload.(gen.PluginArtifactReloadCommitReq)
			if req.Token != "reload-token" {
				e.t.Fatalf("commit: wrong token %q", req.Token)
			}
			return gen.PluginArtifactReloadCommitResp{
				PluginID:     "app.subdirdemo",
				ArtifactHash: "bp7000000000001",
				Status:       gen.AppStatus{ID: "app.subdirdemo", Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.artifact_reload_abort":
			return gen.PluginArtifactReloadAbortResp{}, nil
		case "pluginhost.list_plugins":
			e.mu.Lock()
			descs := make([]pluginhostactor.PluginDescriptor, 0, len(e.loadedPlugins))
			for id, calls := range e.loadedPlugins {
				descs = append(descs, pluginhostactor.PluginDescriptor{ID: id, Callables: calls})
			}
			e.mu.Unlock()
			return pluginhostactor.ListPluginsResp{Plugins: descs}, nil
		default:
			e.t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

func (e *bp7Env) ctx() *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 61)
	if err != nil {
		e.t.Fatal(err)
	}
	projectActorID := id.From(projectCID)
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return projectRef, aid == projectActorID
	}
	planner := e.planner()
	ctx.PlannerFn = func() actor.Planner { return planner }
	return ctx
}

func newBP7Actor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-bp7-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
	}
}

func bp7ProjectID(t *testing.T) string {
	t.Helper()
	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 61)
	if err != nil {
		t.Fatal(err)
	}
	return cid.String()
}

// --- Acceptance: dev_generate lands artifacts in the AppDir subdirectory ---

func TestDevGenerateSubdirArtifactsLandInSubdir(t *testing.T) {
	env := newBP7Project(t, "bp7-subdir-")
	env.write("subdir/app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "subdir",
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("dev_generate error: %s", resp.Error)
	}

	// Artifacts must exist inside subdir, not in the root.
	for _, name := range []string{"main.gen.go", "handlers.go", "app.manifest.json", "schemas_gen.go", "client.gen.ts", "go.mod"} {
		if !env.exists("subdir/" + name) {
			t.Errorf("expected artifact subdir/%s to exist", name)
		}
		if env.exists(name) {
			t.Errorf("artifact %s leaked into project root", name)
		}
	}

	// Reported paths are project-root relative.
	for _, f := range resp.Files {
		if !strings.HasPrefix(f.Path, "subdir/") {
			t.Errorf("reported file %q is not subdir-prefixed", f.Path)
		}
	}

	// Protected files must be root-relative so project.write guards the
	// files under the subdirectory.
	env.mu.Lock()
	protected := append([]string{}, env.protectedFiles...)
	env.mu.Unlock()
	want := map[string]bool{"subdir/main.gen.go": false, "subdir/main_run.gen.go": false, "subdir/server.gen.go": false, "subdir/app.manifest.json": false, "subdir/schemas_gen.go": false, "subdir/app.descriptors.json": false, "subdir/client.gen.ts": false}
	for _, p := range protected {
		if _, ok := want[p]; !ok {
			t.Errorf("unexpected protected file %q", p)
			continue
		}
		want[p] = true
	}
	for p, found := range want {
		if !found {
			t.Errorf("protected files missing %q; got %v", p, protected)
		}
	}

	// Manifest is persisted under the appdir-scoped key.
	if _, ok := a.GeneratedManifests[bp7ProjectID(t)+"::subdir"]; !ok {
		t.Errorf("GeneratedManifests missing subdir key; keys: %v", mapKeys(a.GeneratedManifests))
	}
	if _, ok := a.GeneratedManifests[bp7ProjectID(t)]; ok {
		t.Error("GeneratedManifests must not use the bare project key for a subdir app")
	}
}

// --- Acceptance: dev_gate runs against the subdirectory and forwards AppDir ---

func TestDevGateSubdirPassesAfterGenerate(t *testing.T) {
	env := newBP7Project(t, "bp7-gate-")
	env.write("subdir/app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()

	genResp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "subdir",
	})
	if err != nil || genResp.Error != "" {
		t.Fatalf("dev_generate: err=%v resp=%+v", err, genResp)
	}

	// Workflow step 3: fill the handler stubs.
	env.write("subdir/handlers.go", subdirHandlersFilled)

	gateResp, err := a.handleDevGate(ctx, gen.AppManagerDevGateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "subdir",
	})
	if err != nil {
		t.Fatalf("handleDevGate: %v", err)
	}
	if gateResp.Error != "" {
		t.Fatalf("dev_gate error: %s", gateResp.Error)
	}
	if !gateResp.Passed {
		for _, r := range gateResp.Results {
			if !r.Passed {
				t.Errorf("gate %s failed: %+v", r.Gate, r.Error)
			}
		}
		t.Fatal("dev_gate did not pass")
	}

	// The native build request must carry the AppDir.
	env.mu.Lock()
	buildDirs := append([]string{}, env.buildAppDirs...)
	env.mu.Unlock()
	if len(buildDirs) == 0 || buildDirs[len(buildDirs)-1] != "subdir" {
		t.Errorf("native_build AppDir forwarded = %v; want last = subdir", buildDirs)
	}
}

// --- Acceptance: register_project works from the subdirectory ---

func TestRegisterProjectSubdirRegistersApp(t *testing.T) {
	env := newBP7Project(t, "bp7-reg-")
	env.write("subdir/app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()

	genResp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "subdir",
	})
	if err != nil || genResp.Error != "" {
		t.Fatalf("dev_generate: err=%v resp=%+v", err, genResp)
	}
	env.write("subdir/handlers.go", subdirHandlersFilled)

	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID: bp7ProjectID(t),
		AppDir:    "subdir",
	})
	if err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("expected running state, got %q", regResp.Status.State)
	}
	if _, ok := a.Apps["app.subdirdemo"]; !ok {
		t.Fatal("app.subdirdemo not registered")
	}

	env.mu.Lock()
	buildDirs := append([]string{}, env.buildAppDirs...)
	env.mu.Unlock()
	if len(buildDirs) == 0 || buildDirs[len(buildDirs)-1] != "subdir" {
		t.Errorf("native_build AppDir forwarded = %v; want last = subdir", buildDirs)
	}
}

// --- Acceptance: reload_project works from the subdirectory ---

func TestReloadProjectSubdirReloadsApp(t *testing.T) {
	env := newBP7Project(t, "bp7-reload-")
	env.write("subdir/app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()
	projectID := bp7ProjectID(t)

	genResp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: projectID,
		AppDir:    "subdir",
	})
	if err != nil || genResp.Error != "" {
		t.Fatalf("dev_generate: err=%v resp=%+v", err, genResp)
	}
	env.write("subdir/handlers.go", subdirHandlersFilled)

	regResp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID: projectID,
		AppDir:    "subdir",
	})
	if err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	if regResp.Status.State != "running" {
		t.Fatalf("expected running state, got %q", regResp.Status.State)
	}

	reloadResp, err := a.handleReloadProject(ctx, gen.AppManagerReloadProjectReq{
		ProjectID: projectID,
		AppID:     "app.subdirdemo",
		AppDir:    "subdir",
	})
	if err != nil {
		t.Fatalf("handleReloadProject: %v", err)
	}
	if reloadResp.Status.State != stateRunning {
		t.Fatalf("expected running reload state (pluginhost vocabulary normalized), got %q", reloadResp.Status.State)
	}

	env.mu.Lock()
	buildDirs := append([]string{}, env.buildAppDirs...)
	env.mu.Unlock()
	if len(buildDirs) == 0 || buildDirs[len(buildDirs)-1] != "subdir" {
		t.Errorf("native_build AppDir forwarded = %v; want last = subdir", buildDirs)
	}
}

// --- Regression: default (no AppDir) targets the project root unchanged ---

func TestDevGenerateDefaultTargetsRoot(t *testing.T) {
	env := newBP7Project(t, "bp7-root-")
	env.write("app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()

	resp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID: bp7ProjectID(t),
	})
	if err != nil {
		t.Fatalf("handleDevGenerate: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("dev_generate error: %s", resp.Error)
	}
	for _, name := range []string{"main.gen.go", "app.manifest.json", "handlers.go"} {
		if !env.exists(name) {
			t.Errorf("expected artifact %s at project root", name)
		}
	}
	for _, f := range resp.Files {
		if strings.Contains(f.Path, "/") {
			t.Errorf("default-path file %q must not be prefixed", f.Path)
		}
	}
	env.mu.Lock()
	protected := append([]string{}, env.protectedFiles...)
	env.mu.Unlock()
	for _, p := range protected {
		if strings.Contains(p, "/") {
			t.Errorf("default-path protected file %q must not be prefixed", p)
		}
	}
	if _, ok := a.GeneratedManifests[bp7ProjectID(t)]; !ok {
		t.Errorf("GeneratedManifests missing bare project key; keys: %v", mapKeys(a.GeneratedManifests))
	}
}

// --- Path safety: AppDir rejects traversal and absolute paths everywhere ---

func TestAppDirRejectedOnAllFaces(t *testing.T) {
	env := newBP7Project(t, "bp7-invalid-")
	env.write("subdir/app.appdef", subdirDemoAppDef)
	a := newBP7Actor(t)
	ctx := env.ctx()
	projectID := bp7ProjectID(t)

	for _, bad := range []string{"..", "../evil", "/abs", "C:\\abs", "a\\b", "sub/.."} {
		genResp, err := a.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{ProjectID: projectID, AppDir: bad})
		if err != nil {
			t.Fatalf("AppDir %q: unexpected transport error: %v", bad, err)
		}
		if genResp.Error == "" || !strings.Contains(genResp.Error, "AppDir") {
			t.Errorf("AppDir %q: dev_generate Error = %q, want AppDir rejection", bad, genResp.Error)
		}

		gateResp, err := a.handleDevGate(ctx, gen.AppManagerDevGateReq{ProjectID: projectID, AppDir: bad})
		if err != nil {
			t.Fatalf("AppDir %q: unexpected transport error: %v", bad, err)
		}
		if gateResp.Error == "" || !strings.Contains(gateResp.Error, "AppDir") {
			t.Errorf("AppDir %q: dev_gate Error = %q, want AppDir rejection", bad, gateResp.Error)
		}

		if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID, AppDir: bad}); err == nil || !strings.Contains(err.Error(), "AppDir") {
			t.Errorf("AppDir %q: register_project err = %v, want AppDir rejection", bad, err)
		}

		if _, err := a.handleReloadProject(ctx, gen.AppManagerReloadProjectReq{ProjectID: projectID, AppID: "app.subdirdemo", AppDir: bad}); err == nil || !strings.Contains(err.Error(), "AppDir") {
			t.Errorf("AppDir %q: reload_project err = %v, want AppDir rejection", bad, err)
		}
	}

	// Nothing may have been written outside the fixture.
	if env.exists("evil") || env.exists("../evil") {
		t.Error("traversal AppDir wrote outside the project root")
	}
}

func mapKeys(m map[string]codegen.GeneratedManifest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
