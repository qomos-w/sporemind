package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// errSimulatedBuild short-circuits native_build right after source
// resolution, so the tests observe resolveProjectSource's outcome (the dir
// handed to the compiler) without running a real toolchain.
var errSimulatedBuild = errors.New("simulated build failure")

// countingProjectActor records project.info/project.read traffic so the
// SourceRoot tests can pin which resolution path resolveProjectSource took.
type countingProjectActor struct {
	mu        sync.Mutex
	root      string
	manifest  string
	infoCalls int
	readPaths []string
}

func (countingProjectActor) Type() string               { return "counting-project" }
func (countingProjectActor) OnInit(actor.Context) error { return nil }
func (countingProjectActor) OnStop(actor.Context) error { return nil }
func (p *countingProjectActor) OnStart(ctx actor.Context) error {
	ctx.RegisterDomain("project")
	_ = ctx.Register("project.info", func(actor.PureContext) (gen.ProjectInfoResp, error) {
		p.mu.Lock()
		p.infoCalls++
		p.mu.Unlock()
		return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "root", Path: p.root}}}, nil
	})
	_ = ctx.Register("project.read", func(_ actor.PureContext, req gen.FileSystemReadReq) (gen.FileSystemReadResp, error) {
		p.mu.Lock()
		p.readPaths = append(p.readPaths, req.Path)
		p.mu.Unlock()
		return gen.FileSystemReadResp{Content: p.manifest}, nil
	})
	return nil
}

func (p *countingProjectActor) calls() (int, []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.infoCalls, append([]string(nil), p.readPaths...)
}

// writeEntry drops a minimal main.gen.go into dir so CompileFromProject
// finds the default entry module.
func writeEntry(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.gen.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bootSourceTestApp spawns a fake project actor and a pluginhost actor whose
// go build is stubbed to capture the source root and fail fast. It returns
// the pluginhost ref, the fake project's canonical ID string, the fake
// project, and a pointer to the captured build dir.
func bootSourceTestApp(t *testing.T, projectRoot string, manifest string) (ref.Ref, string, *countingProjectActor, *string) {
	t.Helper()

	runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	a, err := app.New(
		app.WithNamespace("sporemind"),
		app.WithRootActor(func() actor.Actor { return laneTestRoot{} }),
	)
	if err != nil {
		t.Fatalf("app new: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(runCtx) }()
	if err := a.WaitForAllCellsStart(10 * time.Second); err != nil {
		t.Fatalf("app start: %v", err)
	}

	projectCID, err := identity.NewCanonicalID(uint64(time.Now().UnixMilli()), 1, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	fake := &countingProjectActor{root: projectRoot, manifest: manifest}
	projectProps := actor.PropsFromFunc(func() actor.Actor { return fake }).
		WithID(id.From(projectCID))
	if _, err := a.Spawn(projectProps, "project"); err != nil {
		t.Fatalf("spawn fake project: %v", err)
	}

	var builtDir string
	phActor := &Actor{}
	phActor.goBuildOverride = func(dir, outPath string, env []string) ([]byte, error) {
		builtDir = dir
		return nil, errSimulatedBuild
	}
	phProps := actor.PropsFromFunc(func() actor.Actor { return phActor }).WithPlanner()
	phRef, err := a.Spawn(phProps, "pluginhost")
	if err != nil {
		t.Fatalf("spawn pluginhost: %v", err)
	}
	return phRef, projectCID.String(), fake, &builtDir
}

// invokeNativeBuild invokes pluginhost.native_build, tolerating the startup
// race where the test fires before the actor finishes registering callables.
func invokeNativeBuild(t *testing.T, phRef ref.Ref, req gen.NativeBuildReq) error {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		call := phRef.Invoke(context.Background(), "pluginhost.native_build", req)
		_, err := call.Final(context.Background())
		if err == nil || !strings.Contains(err.Error(), "not registered") {
			return err
		}
		if time.Now().After(deadline) {
			t.Fatalf("pluginhost.native_build never became callable: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestResolveProjectSourceSourceRootSkipsProjectInfo pins the SourceRoot
// fast path: when the caller supplies a pre-resolved project root, pluginhost
// must use it verbatim (AppDir appended) and never consult project.info,
// while the manifest is still read and AppID-validated via project.read.
func TestResolveProjectSourceSourceRootSkipsProjectInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("real-app source test skipped in -short mode")
	}

	manifest, err := json.Marshal(gen.AppManifest{ID: "native.sourceroot", Name: "SR", Version: "1.0.0", Runtime: "native"})
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := filepath.Join(t.TempDir(), "worktree", "proj")
	writeEntry(t, sourceRoot)
	phRef, projectID, fake, builtDir := bootSourceTestApp(t, "/unused/info-root", string(manifest))

	err = invokeNativeBuild(t, phRef, gen.NativeBuildReq{
		ProjectID:  projectID,
		AppID:      "native.sourceroot",
		SourceRoot: sourceRoot + "/",
	})
	if err == nil {
		t.Fatal("native_build unexpectedly succeeded with a failing build override")
	}
	if *builtDir != sourceRoot {
		t.Fatalf("build dir = %q, want SourceRoot %q (trailing slash must be trimmed)", *builtDir, sourceRoot)
	}
	infoCalls, readPaths := fake.calls()
	if infoCalls != 0 {
		t.Fatalf("project.info called %d times, want 0 when SourceRoot is set", infoCalls)
	}
	if len(readPaths) != 1 || readPaths[0] != filepath.Join(sourceRoot, "app.manifest.json") {
		t.Fatalf("project.read paths = %v, want manifest under SourceRoot", readPaths)
	}
}

// TestResolveProjectSourceFallsBackToProjectInfo pins the fallback: an empty
// SourceRoot resolves the root via project.info exactly as before.
func TestResolveProjectSourceFallsBackToProjectInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("real-app source test skipped in -short mode")
	}

	manifest, err := json.Marshal(gen.AppManifest{ID: "native.fallback", Name: "FB", Version: "1.0.0", Runtime: "native"})
	if err != nil {
		t.Fatal(err)
	}
	infoRoot := t.TempDir()
	writeEntry(t, infoRoot)
	writeEntry(t, filepath.Join(infoRoot, "sub", "app"))
	phRef, projectID, fake, builtDir := bootSourceTestApp(t, infoRoot, string(manifest))

	err = invokeNativeBuild(t, phRef, gen.NativeBuildReq{
		ProjectID: projectID,
		AppID:     "native.fallback",
		AppDir:    "sub/app",
	})
	if err == nil {
		t.Fatal("native_build unexpectedly succeeded with a failing build override")
	}
	if *builtDir == "" {
		t.Fatalf("native_build failed before reaching the compiler: %v", err)
	}
	wantDir := infoRoot + "/sub/app"
	if *builtDir != wantDir {
		t.Fatalf("build dir = %q, want project.info root + AppDir = %q", *builtDir, wantDir)
	}
	infoCalls, readPaths := fake.calls()
	if infoCalls != 1 {
		t.Fatalf("project.info called %d times, want 1 on the fallback path", infoCalls)
	}
	if len(readPaths) != 1 || readPaths[0] != filepath.Join(wantDir, "app.manifest.json") {
		t.Fatalf("project.read paths = %v, want manifest under resolved root", readPaths)
	}
}
