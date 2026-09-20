package appmanager

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// blockingProjectActor is a minimal stand-in for the project actor whose
// "project.info" callable blocks until released. It lets us pin an
// appmanager orchestration handler (dev_gate / project_package) mid-flight on
// the dedicated appmanager_ops lane: the orchestration's first .Await()
// (project.info) parks it there, exactly as a long native build or file scan
// would. While it is parked we assert that owner-lane control-plane queries
// (list / get) still return promptly — proving the orchestration no longer
// blocks the owner lane.
type blockingProjectActor struct {
	entered chan struct{} // closed when project.info is entered
	release chan struct{} // closed to unblock project.info
}

func (b *blockingProjectActor) Type() string               { return "blocking-project" }
func (b *blockingProjectActor) OnInit(actor.Context) error { return nil }
func (b *blockingProjectActor) OnStop(actor.Context) error { return nil }
func (b *blockingProjectActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("project.info", func(_ actor.PureContext) (gen.ProjectInfoResp, error) {
		close(b.entered)
		<-b.release
		return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Path: "/fake/proj"}}}, nil
	}, actor.Public()); err != nil {
		return err
	}
	return nil
}

// laneTestRoot is a minimal root actor whose only job is to spawn the
// appmanager as a child WITH the Planner capability granted (Props
// .WithPlanner()), mirroring the production runtime topology where the
// runtime rootActor spawns appmanager with spec.Planner. Without the grant,
// ctx.Planner() inside appmanager handlers is nil and every orchestration
// chain bails at its first cross-actor call.
type laneTestRoot struct {
	appMgr *Actor
}

func (r *laneTestRoot) Type() string               { return "lane-test-root" }
func (r *laneTestRoot) OnInit(actor.Context) error { return nil }
func (r *laneTestRoot) OnStop(actor.Context) error { return nil }
func (r *laneTestRoot) OnStart(ctx actor.Context) error {
	props := actor.PropsFromFunc(func() actor.Actor { return r.appMgr }).WithPlanner()
	if _, err := ctx.Spawn(props, "appmanager"); err != nil {
		return fmt.Errorf("spawn appmanager: %w", err)
	}
	return nil
}

// bootOpsLaneApp boots a real app whose root is a laneTestRoot that spawns the
// appmanager as a Planner-capable child, with a seeded registered app so
// owner-lane queries (list/get) have something to read. It returns the running
// App and the appmanager ref. Caller must cancel ctx and wait on runDone
// (t.Cleanup handles both).
func bootOpsLaneApp(t *testing.T, ctx context.Context) (app.App, ref.Ref) {
	t.Helper()
	a := &Actor{
		store: persist.NewFSPersist(t.TempDir()),
		Apps: map[string]gen.AppManifest{
			"sample.app": {
				ID: "sample.app", Name: "Sample", Version: "1.0.0", Runtime: "spore",
				Namespace: "sporeapp.sample.app", ProtocolVersion: 1,
			},
		},
		Records: map[string]appRecord{
			"sample.app": {Manifest: gen.AppManifest{ID: "sample.app", Name: "Sample", Version: "1.0.0", Runtime: "spore", Namespace: "sporeapp.sample.app", ProtocolVersion: 1}, State: "registered"},
		},
	}
	root := &laneTestRoot{appMgr: a}
	ap, err := app.New(
		app.WithNamespace("sporemind"),
		app.WithRootActor(func() actor.Actor { return root }),
	)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- ap.Run(ctx) }()
	if err := ap.WaitForAllCellsStart(10 * time.Second); err != nil {
		t.Fatalf("WaitForAllCellsStart: %v", err)
	}
	// WaitForAllCellsStart can return before Run has registered the cells in
	// the app's cell map; poll until the appmanager service is exposed and its
	// callable surface is actually registered so the first invoke cannot race
	// the registration.
	deadline := time.Now().Add(10 * time.Second)
	var appRef ref.Ref
	for {
		if r, ok := ap.LookupService("appmanager"); ok && r != nil {
			if tbl, ok := ap.HandlerTableFor(r); ok {
				if _, found := tbl.Lookup("appmanager.dev_gate"); found {
					if _, found := tbl.Lookup("appmanager.list"); found {
						appRef = r
						break
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("appmanager callables never registered on appmanager cell")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Cleanup(func() {
		// ctx is cancelled by the caller, so Run will exit.
		select {
		case err := <-runDone:
			if err != nil {
				t.Logf("app Run exit: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("app did not shut down")
		}
	})
	return ap, appRef
}

// spawnBlockingProject spawns a child whose canonical ActorID equals the
// projectID string dev_gate/project_package resolve via ParseCanonicalID, so
// the orchestration's ctx.LookupID finds it. Returns the projectID string.
func spawnBlockingProject(t *testing.T, ap app.App, b *blockingProjectActor) string {
	t.Helper()
	cid, err := identity.NewCanonicalID(uint64(time.Now().UnixMilli()), 1, 0, uint64(time.Now().UnixNano()%100000))
	if err != nil {
		t.Fatalf("NewCanonicalID: %v", err)
	}
	projectID := cid.String()
	props := actor.PropsFromFunc(func() actor.Actor { return b }).WithID(id.From(cid))
	if _, err := ap.Spawn(props, "proj-"+projectID[:8]); err != nil {
		t.Fatalf("spawn blocking project: %v", err)
	}
	return projectID
}

// assertOwnerQueriesRespond asserts that appmanager.list and appmanager.get
// both complete within timeout while an orchestration is parked on the ops
// lane. If a query were routed onto the same lane as the parked orchestration
// it would hang until release and the timeout would fire.
func assertOwnerQueriesRespond(t *testing.T, appRef ref.Ref, timeout time.Duration) {
	t.Helper()
	start := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	listCall := appRef.Invoke(ctx, "appmanager.list", gen.AppManagerListReq{})
	if listCall == nil {
		t.Fatal("appmanager.list returned nil call")
	}
	if _, err := listCall.Final(ctx); err != nil {
		t.Fatalf("appmanager.list blocked or errored while orchestration in flight (took %v): %v", time.Since(start), err)
	}

	getCtx, getCancel := context.WithTimeout(context.Background(), timeout)
	defer getCancel()
	getCall := appRef.Invoke(getCtx, "appmanager.get", gen.AppManagerGetReq{ID: "sample.app"})
	if getCall == nil {
		t.Fatal("appmanager.get returned nil call")
	}
	if _, err := getCall.Final(getCtx); err != nil {
		t.Fatalf("appmanager.get blocked or errored while orchestration in flight: %v", err)
	}
	if elapsed := time.Since(start); elapsed > timeout {
		t.Fatalf("owner-lane queries did not respond within %v (took %v)", timeout, elapsed)
	}
}

// TestDevGateDoesNotBlockOwnerLane is the concurrency regression test for the
// dev_gate orchestration: while dev_gate is parked mid-flight on the
// appmanager_ops lane (its first .Await() is project.info), the owner-lane
// control-plane queries appmanager.list and appmanager.get return immediately.
func TestDevGateDoesNotBlockOwnerLane(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ap, appRef := bootOpsLaneApp(t, ctx)
	b := &blockingProjectActor{entered: make(chan struct{}), release: make(chan struct{})}
	projectID := spawnBlockingProject(t, ap, b)

	devCtx, devCancel := context.WithCancel(ctx)
	defer devCancel()
	done := make(chan error, 1)
	go func() {
		call := appRef.Invoke(devCtx, "appmanager.dev_gate", gen.AppManagerDevGateReq{ProjectID: projectID})
		if call == nil {
			done <- errors.New("appmanager.dev_gate returned nil call")
			return
		}
		_, err := call.Final(devCtx)
		done <- err
	}()

	// Wait until dev_gate is genuinely parked inside project.info (mid-Await).
	select {
	case err := <-done:
		t.Fatalf("dev_gate failed before entering project.info: %v", err)
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("dev_gate never entered the blocking project.info call")
	}

	// The whole point: control-plane queries must respond while dev_gate is
	// parked on the ops lane.
	assertOwnerQueriesRespond(t, appRef, 2*time.Second)

	close(b.release)
	if err := <-done; err != nil {
		t.Fatalf("dev_gate errored after release: %v", err)
	}
}

// TestProjectPackageDoesNotBlockOwnerLane is the same regression assertion for
// the project_package orchestration (the heaviest chain, 11x Await). It is
// AdminOnly, so the invoke carries an admin role header.
func TestProjectPackageDoesNotBlockOwnerLane(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ap, appRef := bootOpsLaneApp(t, ctx)
	b := &blockingProjectActor{entered: make(chan struct{}), release: make(chan struct{})}
	projectID := spawnBlockingProject(t, ap, b)

	adminHdr := map[string]string{"gospore.caller_role": "admin"}

	pkgCtx, pkgCancel := context.WithCancel(ctx)
	defer pkgCancel()
	done := make(chan error, 1)
	go func() {
		call := appRef.Invoke(pkgCtx, "appmanager.project_package", gen.AppManagerProjectPackageReq{ProjectID: projectID}, adminHdr)
		if call == nil {
			done <- errors.New("appmanager.project_package returned nil call")
			return
		}
		_, err := call.Final(pkgCtx)
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("project_package failed before entering project.info: %v", err)
	case <-b.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("project_package never entered the blocking project.info call")
	}

	assertOwnerQueriesRespond(t, appRef, 2*time.Second)

	close(b.release)
	// The stub project only implements project.info; the chain continues to
	// project.read and legitimately errors there. What matters is that the
	// orchestration COMPLETED (never hung) and the queries above stayed
	// responsive while it was parked.
	<-done
}
