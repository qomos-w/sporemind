package appmanager

// End-to-end worktree dev loop: a real git repo + real project actor + real
// worktree checkout drive the full dev_generate → dev_gate → register_project
// → reload_project chain. The appmanager actor talks to the project actor
// through its registered handlers (recorded by testutil.FakeCtx.Register) and
// to a mocked pluginhost for the native build/load machinery. The test pins
// every card acceptance criterion: worktree-bound routing of every call,
// worktree-dimensioned GeneratedManifest key, register warnings naming the
// worktree, and zero main-tree pollution (git status --porcelain unchanged
// across the full loop).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	pluginhostactor "github.com/qomos-w/sporemind/pkg/actor/pluginhost"
	projectactor "github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// wtDevAppDef: minimal .appdef fixture for the worktree dev e2e. The
// filename in the worktree is "app.appdef" (NOT hidden; project.list filters
// names starting with ".").
const wtDevAppDef = `// app.appdef — worktree dev e2e fixture
app WtDev {
    id:          "app.wtdev"
    name:        "WtDev"
    version:     "0.1.0"
    namespace:   "wtdev"

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
        toolName: "wtdev-ping"
    }

    entrypoint view main {
        title: "WtDev"
        route: "/"
    }
}
`

// wtDevHandlersFilled replaces the dev_generate ErrNotImplemented stub after
// step 2 of the workflow, mirroring the bp7 fixture shape.
const wtDevHandlersFilled = `// handlers.go — agent-owned
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handlePing(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]string{"pong": "ok"}}, nil
}
`

// wtDevEnv assembles the worktree dev e2e environment: a real main git repo,
// a real project actor (so worktree enter/bound-check/file read/list/info are
// genuine), the appmanager actor under test, and a mocked pluginhost service
// (native build/load/reload/unload answered with worktree-local artifact
// paths). Project.* planner calls are dispatched to the project actor's
// registered handlers via reflection.
type wtDevEnv struct {
	t            *testing.T
	mainDir      string // absolute, forward slashes
	wtPath       string // bound agent's worktree checkout
	agentKey     string // canonical id string of the calling agent
	projectID    string // canonical id string of the project
	regs         map[string]any
	projAdminCtx *testutil.FakeCtx // admin ctx used to invoke project handlers
	am           *Actor
	amCtx        *testutil.FakeCtx // appmanager-facing ctx

	mu          sync.Mutex
	buildReqs   []gen.NativeBuildReq
	loaded      map[string][]string
	artifactSeq int
}

// wtProjectCID returns a project actor canonical id distinct from the
// reserved 61 (bp7) and the native scaffold 42/43 so the test never collides.
func wtProjectCID(t *testing.T) (identity.CanonicalID, string) {
	t.Helper()
	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 0x4e)
	if err != nil {
		t.Fatalf("newCanonicalID: %v", err)
	}
	return cid, cid.String()
}

// wtAgentCID returns a canonical id for the agent caller. The hex string is
// what appmanager injects as CallerAgentID; the cid is what the caller
// FakeCtx's ref ID carries so callerID(ctx) inside the project actor matches.
func wtAgentCID(t *testing.T) (identity.CanonicalID, string) {
	t.Helper()
	cid, err := identity.NewCanonicalID(1700000000000, 1, 2, 0x4e)
	if err != nil {
		t.Fatalf("newCanonicalID: %v", err)
	}
	return cid, cid.String()
}

// initGitRepoMain mirrors pkg/actor/project's initGitRepo helper (package
// internal to that test binary, so duplicated here for the appmanager test
// package).
func initGitRepoMain(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	util.HideWindow(cmd)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	for _, kv := range []string{"user.name test", "user.email test@test"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		util.HideWindow(cfg)
		cfg.Dir = dir
		if o, e := cfg.CombinedOutput(); e != nil {
			t.Fatalf("git config %s: %v\n%s", parts[0], e, o)
		}
	}
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	add := exec.Command("git", "add", ".")
	util.HideWindow(add)
	add.Dir = dir
	if o, e := add.CombinedOutput(); e != nil {
		t.Fatalf("git add: %v\n%s", e, o)
	}
	commit := exec.Command("git", "commit", "-m", "initial")
	util.HideWindow(commit)
	commit.Dir = dir
	if o, e := commit.CombinedOutput(); e != nil {
		t.Fatalf("git commit: %v\n%s", e, o)
	}
}

// gitStatusPorcelain returns git status --porcelain for dir. The e2e flow
// captures this string before dev_generate and after the full loop; any file
// the dev pipeline leaks into the main repo flips a line in the diff.
func gitStatusPorcelain(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	return string(out)
}

// newWtDevEnv wires the full environment: main repo + project actor +
// worktree enter + appmanager actor + planner dispatch. The callerAgentID is
// already bound to a fresh worktree by the time newWtDevEnv returns.
func newWtDevEnv(t *testing.T) *wtDevEnv {
	t.Helper()

	_, projectID := wtProjectCID(t)
	agentCID, agentKey := wtAgentCID(t)

	// Main tree: real git repo. Place it inside the package's cwd so the
	// SDK workspace fallback (walkUpForSDK from process cwd) finds the
	// in-repo sporemind-plugin-sdk.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	mainDir, err := os.MkdirTemp(wd, "wtdev-main-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(mainDir) })
	initGitRepoMain(t, mainDir)

	// Isolate the project actor's data dir so worktree checkouts land in a
	// temp directory, not the real data dir.
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)

	// Spin up the real project actor; record every registered handler in
	// regs so the planner can dispatch project.* calls into it without a
	// gospore runtime. OnInit is an optional Initializable hook that
	// gospore normally invokes before OnStart — we drive it manually here.
	pa := projectactor.NewActor(mainDir)()
	projAdminCtx := testutil.AdminCtx(testutil.GenActorID())
	if init, ok := pa.(interface{ OnInit(actor.Context) error }); ok {
		if err := init.OnInit(projAdminCtx); err != nil {
			t.Fatalf("project actor OnInit: %v", err)
		}
	}
	if err := pa.OnStart(projAdminCtx); err != nil {
		t.Fatalf("project actor OnStart: %v", err)
	}
	regs := projAdminCtx.Regs

	env := &wtDevEnv{
		t:            t,
		mainDir:      filepath.ToSlash(mainDir),
		agentKey:     agentKey,
		projectID:    projectID,
		regs:         regs,
		projAdminCtx: projAdminCtx,
		loaded:       map[string][]string{},
	}

	// Enter the agent's worktree through the real handler. The caller
	// ctx carries a fake ref whose ID matches agentKey so callerID(ctx)
	// inside the handler equals the agent's bind key.
	enterCtx := testutil.AdminCtx(testutil.GenActorID())
	enterCtx.CallerRef = testutil.NewFakeRef(id.From(agentCID), nil)
	resp, err := invokeProjectHandler(t, regs, "project.worktree_enter", enterCtx, gen.ProjectWorktreeEnterReq{Name: "wtdev-e2e"})
	if err != nil {
		t.Fatalf("project.worktree_enter: %v", err)
	}
	enterResp, ok := resp.(gen.ProjectWorktreeEnterResp)
	if !ok {
		t.Fatalf("project.worktree_enter: bad response type %T", resp)
	}
	if !enterResp.Created {
		t.Fatal("worktree enter: expected Created=true on first call")
	}
	env.wtPath = enterResp.Worktree.Path

	// Sanity: bound_check for the agent should report the worktree we just
	// created — this proves the dispatch reached the real project actor.
	bc, err := invokeProjectHandler(t, regs, "project.worktree_bound_check", projAdminCtx,
		gen.ProjectWorktreeBoundCheckReq{AgentActorID: agentKey})
	if err != nil {
		t.Fatalf("project.worktree_bound_check: %v", err)
	}
	bcResp, ok := bc.(gen.ProjectWorktreeBoundCheckResp)
	if !ok || !bcResp.Bound {
		t.Fatalf("expected bound after enter, got %+v", bcResp)
	}
	if bcResp.WorktreePath != env.wtPath {
		t.Fatalf("bound_check WorktreePath=%q, want %q", bcResp.WorktreePath, env.wtPath)
	}

	// Construct the appmanager actor + its FakeCtx.
	env.am = newBP7Actor(t)

	pluginRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	projectCIDID := id.From(parseCID(t, projectID))
	amCtx := testutil.HumanCtx(testutil.GenActorID())
	amCtx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return pluginRef, name == pluginhostServiceName
	}
	amCtx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(projectCIDID, nil), aid == projectCIDID
	}
	amCtx.PlannerFn = func() actor.Planner { return env.planner() }
	env.amCtx = amCtx

	return env
}

// parseCID parses a canonical id hex string into a CanonicalID.
func parseCID(t *testing.T, s string) identity.CanonicalID {
	t.Helper()
	cid, err := identity.ParseCanonicalID(s)
	if err != nil {
		t.Fatalf("parseCID %q: %v", s, err)
	}
	return cid
}

// writeWt writes content to a path relative to the worktree checkout (every
// caller-visible write stays inside the worktree, never the main repo).
func (e *wtDevEnv) writeWt(rel, content string) {
	e.t.Helper()
	full := filepath.Join(e.wtPath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// existsMain reports whether a relative path exists in the main repo.
func (e *wtDevEnv) existsMain(rel string) bool {
	_, err := os.Stat(filepath.Join(e.mainDir, filepath.FromSlash(rel)))
	return err == nil
}

// existsWt reports whether a relative path exists in the worktree.
func (e *wtDevEnv) existsWt(rel string) bool {
	_, err := os.Stat(filepath.Join(e.wtPath, filepath.FromSlash(rel)))
	return err == nil
}

// planner returns the appmanager-side planner: project.* routed to the real
// project actor's registered handlers (reflection), pluginhost.* answered by
// an in-memory mock that places the artifact inside the worktree.
func (e *wtDevEnv) planner() actor.Planner {
	e.t.Helper()
	abi := scaffoldAbi()
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		switch callID {
		case "project.info", "project.list", "project.read", "project.read_base64",
			"project.set_protected_files", "project.worktree_bound_check",
			"project.worktree_enter", "project.worktree_exit":
			return invokeProjectHandler(e.t, e.regs, callID, e.projAdminCtx, payload)
		case "pluginhost.native_build":
			req := payload.(gen.NativeBuildReq)
			e.mu.Lock()
			e.buildReqs = append(e.buildReqs, req)
			e.artifactSeq++
			seq := e.artifactSeq
			hash := fmt.Sprintf("wtdev%013x", seq)
			artifactPath := e.wtPath + "/.sporecode/build/plugin-wtdev"
			e.mu.Unlock()
			return gen.NativeBuildResp{
				Result:       gen.NativeBuildResult{Success: true, ArtifactPath: artifactPath, ArtifactHash: hash},
				ManifestPath: e.wtPath + "/app.manifest.json",
				Abi:          abi,
			}, nil
		case "pluginhost.artifact_load":
			req := payload.(gen.PluginArtifactLoadReq)
			e.mu.Lock()
			e.loaded[req.Manifest.ID] = []string{"ping"}
			e.mu.Unlock()
			return gen.PluginArtifactLoadResp{
				PluginID:     req.Manifest.ID,
				ArtifactHash: req.ArtifactHash,
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active"},
			}, nil
		case "pluginhost.artifact_unload":
			req := payload.(gen.PluginArtifactUnloadReq)
			e.mu.Lock()
			delete(e.loaded, req.PluginID)
			e.mu.Unlock()
			return gen.PluginArtifactUnloadResp{Removed: 1}, nil
		case "pluginhost.artifact_reload_prepare":
			req := payload.(gen.PluginArtifactReloadPrepareReq)
			return gen.PluginArtifactReloadPrepareResp{
				Token:        "reload-token",
				PluginID:     req.Manifest.ID,
				ArtifactHash: req.ArtifactHash,
				Status:       gen.AppStatus{ID: req.Manifest.ID, Runtime: "native", State: "active", Version: "0.2.0"},
			}, nil
		case "pluginhost.artifact_reload_commit":
			if p, ok := payload.(gen.PluginArtifactReloadCommitReq); !ok || p.Token != "reload-token" {
				e.t.Fatalf("reload_commit: wrong payload/token: %+v", payload)
			}
			return gen.PluginArtifactReloadCommitResp{
				PluginID:     "app.wtdev",
				ArtifactHash: "wtdev-commit-hash",
				Status:       gen.AppStatus{ID: "app.wtdev", Runtime: "native", State: "active", Version: "0.2.0"},
			}, nil
		case "pluginhost.artifact_reload_abort":
			return gen.PluginArtifactReloadAbortResp{}, nil
		case "pluginhost.list_plugins":
			e.mu.Lock()
			descs := make([]pluginhostactor.PluginDescriptor, 0, len(e.loaded))
			for id, calls := range e.loaded {
				descs = append(descs, pluginhostactor.PluginDescriptor{ID: id, Callables: calls})
			}
			e.mu.Unlock()
			return pluginhostactor.ListPluginsResp{Plugins: descs}, nil
		case "pluginhost.assets_put":
			return gen.PluginAssetsPutResp{}, nil
		default:
			e.t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

// invokeProjectHandler invokes a registered handler by reflect-calling the
// signature (PureContext[, payload]) → (Resp, error). Handlers that take
// only PureContext (project.info) are invoked with no payload.
func invokeProjectHandler(t *testing.T, regs map[string]any, callID string, ctx *testutil.FakeCtx, payload any) (any, error) {
	t.Helper()
	h, ok := regs[callID]
	if !ok {
		return nil, fmt.Errorf("project handler %s not registered", callID)
	}
	hv := reflect.ValueOf(h)
	if hv.Kind() != reflect.Func {
		return nil, fmt.Errorf("project handler %s is not a func (%T)", callID, h)
	}
	in := []reflect.Value{reflect.ValueOf(ctx)}
	if hv.Type().NumIn() == 2 {
		in = append(in, reflect.ValueOf(payload))
	}
	outs := hv.Call(in)
	var err error
	if e := outs[len(outs)-1]; !e.IsNil() {
		err = e.Interface().(error)
	}
	return outs[0].Interface(), err
}

// lastBuildReq returns the most recently captured native_build request
// (thread-safe). Returns ok=false when none have been recorded.
func (e *wtDevEnv) lastBuildReq() (gen.NativeBuildReq, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.buildReqs) == 0 {
		return gen.NativeBuildReq{}, false
	}
	return e.buildReqs[len(e.buildReqs)-1], true
}

// TestWorktreeDevLoopE2E exercises the full bound-caller dev loop the card
// spells out: real worktree enter → write .appdef into the worktree →
// dev_generate (assert artifacts land in the worktree, GeneratedManifests
// carries the worktree-dimensioned key, warnings name the worktree) →
// dev_gate (passes against the worktree's bundle) → register_project
// (artifact path lives inside the worktree, Warnings list the worktree, the
// recorded native_build carried SourceRoot=worktree) → reload_project (new
// artifact hash, same SourceRoot). Main-tree zero-pollution is asserted by
// diffing git status --porcelain snapshots around the whole loop.
func TestWorktreeDevLoopE2E(t *testing.T) {
	env := newWtDevEnv(t)
	ctx := env.amCtx
	agentKey := env.agentKey

	// Write the .appdef inside the worktree (step 2 of the agent dev loop).
	env.writeWt("app.appdef", wtDevAppDef)

	// Baseline snapshot AFTER actor bring-up + worktree enter + agent .appdef
	// write (those are prerequisites, not part of the loop the dev pipeline
	// drives). Everything below must reproduce this snapshot verbatim.
	baseline := gitStatusPorcelain(t, env.mainDir)

	// ── dev_generate ──
	genResp, err := env.am.handleDevGenerate(ctx, gen.AppManagerDevGenerateReq{
		ProjectID:     env.projectID,
		CallerAgentID: agentKey,
	})
	if err != nil {
		t.Fatalf("dev_generate: %v", err)
	}
	if genResp.Error != "" {
		t.Fatalf("dev_generate error: %s", genResp.Error)
	}
	// Bind warnings: at least one names the worktree, telling the agent the
	// artifacts and protected-files bucket die with the worktree. The
	// warning embeds the original worktree path verbatim (backslashes on
	// Windows); compare with slash-normalized form on both sides.
	wtSlash := filepath.ToSlash(env.wtPath)
	foundWorktreeWarning := false
	for _, w := range genResp.Warnings {
		if strings.Contains(filepath.ToSlash(w), wtSlash) {
			foundWorktreeWarning = true
			break
		}
	}
	if !foundWorktreeWarning {
		t.Fatalf("dev_generate Warnings = %v, want at least one naming the worktree path %s", genResp.Warnings, wtSlash)
	}
	// Artifacts must land in the worktree, never the main repo.
	for _, name := range []string{"app.manifest.json", "main.gen.go", "handlers.go", "schemas_gen.go", "client.gen.ts", "go.mod"} {
		if !env.existsWt(name) {
			t.Errorf("dev_generate: expected artifact %q in worktree", name)
		}
		if env.existsMain(name) {
			t.Errorf("dev_generate: artifact %q leaked into main repo", name)
		}
	}
	// GeneratedManifests must be keyed with the worktree dimension. The
	// unbound bare-project key must NOT be present — a worktree build must
	// neither clobber nor satisfy the main tree's drift record.
	wantKey := generatedManifestKey(env.projectID, "", env.wtPath)
	if !strings.HasPrefix(wantKey, "wt") || !strings.Contains(wantKey, "::"+env.projectID) {
		t.Fatalf("generatedManifestKey = %q, want wt<hash>::%s form", wantKey, env.projectID)
	}
	if _, ok := env.am.GeneratedManifests[wantKey]; !ok {
		t.Fatalf("GeneratedManifests missing worktree key %q; keys: %v", wantKey, keysOfGeneratedManifests(env.am))
	}
	if _, ok := env.am.GeneratedManifests[env.projectID]; ok {
		t.Fatalf("GeneratedManifests must not carry the bare project key for a worktree build; keys: %v", keysOfGeneratedManifests(env.am))
	}

	// ── dev_gate ──
	// Workflow step 3: fill the handlers stub. dev_generate emitted the
	// generated stub; the gate checks stub-filling, so a hand-written
	// implementation must precede the gate call.
	env.writeWt("handlers.go", wtDevHandlersFilled)

	gateResp, err := env.am.handleDevGate(ctx, gen.AppManagerDevGateReq{
		ProjectID:     env.projectID,
		CallerAgentID: agentKey,
	})
	if err != nil {
		t.Fatalf("dev_gate: %v", err)
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
	// The gate's native_build must target the worktree (not the main tree).
	if last, ok := env.lastBuildReq(); ok {
		if last.SourceRoot != env.wtPath {
			t.Errorf("dev_gate native_build SourceRoot = %q, want %q", last.SourceRoot, env.wtPath)
		}
	} else {
		t.Fatal("dev_gate did not invoke pluginhost.native_build")
	}

	// ── register_project ──
	regResp, err := env.am.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{
		ProjectID:     env.projectID,
		CallerAgentID: agentKey,
	})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if regResp.Status.State != stateRunning {
		t.Fatalf("register_project state = %q, want %q", regResp.Status.State, stateRunning)
	}
	// The artifact path the record now carries must live inside the worktree
	// — the bound caller's native_build pointed at the worktree, so the
	// artifact appmanager registered came from there.
	rec, ok := env.am.Records["app.wtdev"]
	if !ok {
		t.Fatal("register_project: app.wtdev not in Records")
	}
	if !strings.HasPrefix(rec.ArtifactPath, env.wtPath) {
		t.Errorf("record ArtifactPath = %q, want prefix %q (must be inside the bound worktree)", rec.ArtifactPath, env.wtPath)
	}
	if rec.Abi == nil || rec.ArtifactHash == "" {
		t.Fatalf("record missing artifact info: %+v", rec)
	}
	// The register leg's native_build (the second build, after the gate's)
	// must also target the worktree.
	if last, ok := env.lastBuildReq(); ok {
		if last.SourceRoot != env.wtPath {
			t.Errorf("register native_build SourceRoot = %q, want %q", last.SourceRoot, env.wtPath)
		}
	}
	// Bound-caller warning: the response must name the worktree and tell
	// the agent to re-run register after merge.
	regWorktreeWarn := false
	for _, w := range regResp.Warnings {
		if strings.Contains(filepath.ToSlash(w), wtSlash) {
			regWorktreeWarn = true
			break
		}
	}
	if !regWorktreeWarn {
		t.Fatalf("register_project Warnings = %v, want at least one naming the worktree path %s", regResp.Warnings, wtSlash)
	}
	// Zero-pollution midpoint: no new files in the main repo yet.
	if mid := gitStatusPorcelain(t, env.mainDir); mid != baseline {
		t.Fatalf("main tree changed after generate+gate+register:\nbefore:\n%s\nafter:\n%s", baseline, mid)
	}

	// ── reload_project ──
	preReloadHash := rec.ArtifactHash
	reloadResp, err := env.am.handleReloadProject(ctx, gen.AppManagerReloadProjectReq{
		ProjectID:     env.projectID,
		AppID:         "app.wtdev",
		CallerAgentID: agentKey,
	})
	if err != nil {
		t.Fatalf("reload_project: %v", err)
	}
	if reloadResp.Status.State != stateRunning {
		t.Fatalf("reload_project state = %q, want %q", reloadResp.Status.State, stateRunning)
	}
	postReload := env.am.Records["app.wtdev"]
	if postReload.ArtifactHash == "" || postReload.ArtifactHash == preReloadHash {
		t.Errorf("reload_project did not produce a new artifact hash: pre=%q post=%q", preReloadHash, postReload.ArtifactHash)
	}
	if !strings.HasPrefix(postReload.ArtifactPath, env.wtPath) {
		t.Errorf("reload artifact path %q is not inside the worktree %q", postReload.ArtifactPath, env.wtPath)
	}
	if last, ok := env.lastBuildReq(); ok {
		if last.SourceRoot != env.wtPath {
			t.Errorf("reload native_build SourceRoot = %q, want %q", last.SourceRoot, env.wtPath)
		}
	}

	// Final zero-pollution assertion: the entire loop must reproduce the
	// baseline porcelain exactly. Any deviation means a dev step wrote into
	// the main repo.
	if final := gitStatusPorcelain(t, env.mainDir); final != baseline {
		t.Fatalf("main tree changed after the full dev loop:\nbefore:\n%s\nafter:\n%s", baseline, final)
	}

	// ── exit + re-assert ──
	// Exiting the worktree must not pollute the main repo either. Discard
	// mode is the most aggressive cleanup; it removes the worktree and
	// clears the binding without touching the main repo's tree.
	exitCtx := testutil.AdminCtx(testutil.GenActorID())
	cid, _ := parseCIDEx(agentKey)
	exitCtx.CallerRef = testutil.NewFakeRef(id.From(cid), nil)
	if _, err := invokeProjectHandler(t, env.regs, "project.worktree_exit", exitCtx, gen.ProjectWorktreeExitReq{Mode: "discard", Force: true}); err != nil {
		t.Fatalf("worktree_exit: %v", err)
	}
	if final := gitStatusPorcelain(t, env.mainDir); final != baseline {
		t.Fatalf("main tree changed after worktree exit:\nbefore:\n%s\nafter:\n%s", baseline, final)
	}
	// And the binding is gone: the next appmanager call from this agent
	// must NOT be routed to a worktree.
	bc, err := invokeProjectHandler(t, env.regs, "project.worktree_bound_check", env.projAdminCtx,
		gen.ProjectWorktreeBoundCheckReq{AgentActorID: agentKey})
	if err != nil {
		t.Fatalf("bound_check post-exit: %v", err)
	}
	if bcResp, ok := bc.(gen.ProjectWorktreeBoundCheckResp); ok && bcResp.Bound {
		t.Fatalf("bound_check after discard still bound: %+v", bcResp)
	}
}

// parseCIDEx wraps identity.ParseCanonicalID outside the t-helper pattern.
func parseCIDEx(s string) (identity.CanonicalID, error) {
	return identity.ParseCanonicalID(s)
}

// keysOfGeneratedManifests flattens GeneratedManifests keys for diagnostic
// messages.
func keysOfGeneratedManifests(a *Actor) []string {
	out := make([]string, 0, len(a.GeneratedManifests))
	for k := range a.GeneratedManifests {
		out = append(out, k)
	}
	return out
}
