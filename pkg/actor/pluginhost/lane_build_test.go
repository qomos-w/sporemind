package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	gosporeid "github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// laneTestRoot is a trivial root actor for the minimal real-app harness used
// by the lane-isolation tests.
type laneTestRoot struct{}

func (laneTestRoot) Type() string               { return "lane-test-root" }
func (laneTestRoot) OnInit(actor.Context) error { return nil }
func (laneTestRoot) OnStart(actor.Context) error {
	return nil
}
func (laneTestRoot) OnStop(actor.Context) error { return nil }

// fakeProjectActor answers the two callables pluginhost's native_build
// resolves through the planner (project.info + project.read) against a
// fixture source root. It stands in for the real project actor so the lane
// test exercises the full owner→planner→build chain without a project tree.
type fakeProjectActor struct {
	root     string
	manifest string
}

func (fakeProjectActor) Type() string               { return "fake-project" }
func (fakeProjectActor) OnInit(actor.Context) error { return nil }
func (fakeProjectActor) OnStop(actor.Context) error { return nil }
func (p fakeProjectActor) OnStart(ctx actor.Context) error {
	ctx.RegisterDomain("project")
	_ = ctx.Register("project.info", func(actor.PureContext) (gen.ProjectInfoResp, error) {
		return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "root", Path: p.root}}}, nil
	})
	_ = ctx.Register("project.read", func(_ actor.PureContext, req gen.FileSystemReadReq) (gen.FileSystemReadResp, error) {
		return gen.FileSystemReadResp{Content: p.manifest}, nil
	})
	return nil
}

// TestNativeBuildOnBuildLaneKeepsQueriesResponsive pins the Owner-Lane
// constraint: while native_build occupies the dedicated plugin_build loop (a
// compile that blocks inside the go build step), the owner lane must keep
// answering query callables immediately. Before the lane fix the build held
// the owner loop and list_plugins queued behind it.
func TestNativeBuildOnBuildLaneKeepsQueriesResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("real-app lane test skipped in -short mode")
	}

	// Fixture source tree the fake project actor reports.
	srcRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcRoot, "main.gen.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(gen.AppManifest{ID: "native.lane", Name: "Lane", Version: "1.0.0", Runtime: "native"})
	if err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
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

	// Fake project actor under a fixed canonical ID so resolveProjectSource
	// finds it via LookupID.
	projectCID, err := identity.NewCanonicalID(uint64(time.Now().UnixMilli()), 1, 1, 7)
	if err != nil {
		t.Fatal(err)
	}
	projectProps := actor.PropsFromFunc(func() actor.Actor { return fakeProjectActor{root: srcRoot, manifest: string(manifest)} }).
		WithID(gosporeid.From(projectCID))
	if _, err := a.Spawn(projectProps, "project"); err != nil {
		t.Fatalf("spawn fake project: %v", err)
	}

	// pluginhost actor with a go build that blocks until released — the
	// "slow command" the lane must absorb.
	phActor := &Actor{}
	entered := make(chan struct{})
	release := make(chan struct{})
	phActor.goBuildOverride = func(dir, outPath string, env []string) ([]byte, error) {
		close(entered)
		<-release
		return nil, errors.New("simulated slow build failure")
	}
	phProps := actor.PropsFromFunc(func() actor.Actor { return phActor }).WithPlanner()
	phRef, err := a.Spawn(phProps, "pluginhost")
	if err != nil {
		t.Fatalf("spawn pluginhost: %v", err)
	}

	buildDone := make(chan error, 1)
	buildCall := phRef.Invoke(runCtx, "pluginhost.native_build", gen.NativeBuildReq{
		ProjectID: projectCID.String(),
		AppID:     "native.lane",
	})
	go func() {
		_, err := buildCall.Final(runCtx)
		buildDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("go build override never entered — native_build did not reach the compile step")
	}

	// While the compile blocks the plugin_build lane, the owner lane must
	// answer a query callable well within the test budget.
	queryDone := make(chan struct{})
	go func() {
		defer close(queryDone)
		queryCall := phRef.Invoke(runCtx, "pluginhost.list_plugins", map[string]any{})
		if _, err := queryCall.Final(runCtx); err != nil {
			t.Errorf("list_plugins during build: %v", err)
		}
	}()
	select {
	case <-queryDone:
		// ok — fast reply
	case <-time.After(5 * time.Second):
		t.Fatal("list_plugins still queued after 5s while native_build held its lane")
	}

	// The build itself has not finished (gate still closed).
	select {
	case err := <-buildDone:
		t.Fatalf("native_build returned before release: %v", err)
	default:
	}

	close(release)
	select {
	case err := <-buildDone:
		if err == nil {
			t.Fatal("native_build unexpectedly succeeded with a failing build override")
		}
		if !strings.Contains(err.Error(), "simulated slow build failure") {
			t.Fatalf("native_build error = %q, want it to surface the simulated failure", err.Error())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native_build did not return after release")
	}
}
