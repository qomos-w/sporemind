package sporeapp

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/app"
	"github.com/qomos-w/spore/script"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// laneTestRoot is a trivial root actor for the minimal real-app harness used
// by the lane-isolation test.
type laneTestRoot struct{}

func (laneTestRoot) Type() string               { return "lane-test-root" }
func (laneTestRoot) OnInit(actor.Context) error { return nil }
func (laneTestRoot) OnStart(actor.Context) error {
	return nil
}
func (laneTestRoot) OnStop(actor.Context) error { return nil }

// TestRegistrationSurface_ExecLane pins the spore_exec lane wiring: user-script
// invoke and runtime reload run on the dedicated stateful lane so the owner
// lane keeps answering control/query callables while a script is in flight.
// The state query (sporeapp.state) is a stateless (PureContext) snapshot read;
// state_set stays on the owner lane as the single writer of the state table.
func TestRegistrationSurface_ExecLane(t *testing.T) {
	a := &Actor{Manifest: gen.AppManifest{ID: "x", Runtime: "spore"}, Modules: map[string]string{"main": "x"}, allowedCapabilities: map[string]struct{}{}}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.Loops = map[string]actor.HandlerMode{}
	ctx.RegOpts = map[string][]actor.RegisterOption{}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if got, ok := ctx.Loops["spore_exec"]; !ok || got != actor.ModeStateful {
		t.Errorf("spore_exec loop = %v (ok=%v), want ModeStateful", got, ok)
	}
	execLane := func(id string) {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			return
		}
		if got := actor.ResolveLoop(opts...); got != "spore_exec" {
			t.Errorf("%s loop = %q, want %q", id, got, "spore_exec")
		}
	}
	execLane("sporeapp.invoke")
	execLane("sporeapp.reload")

	// Neither state callable may be pinned to the script lane.
	for _, id := range []string{"sporeapp.state", "sporeapp.state_set"} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; state callables must not be pinned to the script lane", id, got)
		}
	}
	// sporeapp.state is a stateless snapshot read; state_set remains the
	// stateful single writer.
	pure := reflect.TypeOf((*actor.PureContext)(nil)).Elem()
	stateFn, ok := ctx.Regs["sporeapp.state"]
	if !ok {
		t.Fatal("sporeapp.state not registered")
	}
	if ft := reflect.TypeOf(stateFn); ft == nil || ft.NumIn() < 1 || ft.In(0) != pure {
		t.Errorf("sporeapp.state must be stateless (PureContext), got %T", stateFn)
	}
	setFn, ok := ctx.Regs["sporeapp.state_set"]
	if !ok {
		t.Fatal("sporeapp.state_set not registered")
	}
	if ft := reflect.TypeOf(setFn); ft != nil && ft.NumIn() > 0 && ft.In(0) == pure {
		t.Errorf("sporeapp.state_set must stay stateful (owner lane), got %T", setFn)
	}
}

// TestInvokeOnExecLaneKeepsQueriesResponsive runs a real actor cell and pins
// the Owner-Lane constraint end to end:
//
//   - a long-running user script occupies the spore_exec lane;
//   - sporeapp.state (stateless pure loop) still answers immediately (query
//     snapshot);
//   - sporeapp.reload issued mid-invoke queues on spore_exec instead of
//     deadlocking: it cannot start (and cannot flip the runtime table) until
//     the invoking script has returned, then completes normally.
func TestInvokeOnExecLaneKeepsQueriesResponsive(t *testing.T) {
	if testing.Short() {
		t.Skip("real-app lane test skipped in -short mode")
	}

	manifest := gen.AppManifest{
		ID: "lane.app", Name: "Lane", Version: "1.0.0", Runtime: "spore",
		Callables: []gen.AppCallableDescriptor{{ID: "slow"}, {ID: "answer"}},
	}
	// ~3.4s of pure compute on this machine — long enough for the assertions
	// below to run strictly inside the invoke while it is still in-flight, and
	// still well within the test budget even under -race.
	const slowIterations = 100000000
	// slow calls app.stateSet first (a deterministic "script entered" signal)
	// and then burns ~0.7s of pure compute — long enough for the assertions
	// below to run strictly inside the invoke while it is still in-flight.
	slowModule := fmt.Sprintf(`export fun slow(): int {
  var i: int = 0
  while i < %d {
    i = i + 1
  }
  return i
}
export fun answer(): int = 42`, slowIterations)
	nextModule := `export fun slow(): int = 0
export fun answer(): int = 43`
	hashV1 := (script.Package{AppID: "lane.app", Version: "1.0.0", EntryModule: "main", Modules: map[string]string{"main": slowModule}}).ComputeHash()
	hashV2 := (script.Package{AppID: "lane.app", Version: "1.0.0", EntryModule: "main", Modules: map[string]string{"main": nextModule}}).ComputeHash()

	appActor := &Actor{
		Manifest:            manifest,
		EntryModule:         "main",
		Modules:             map[string]string{"main": slowModule},
		PackageHash:         hashV1,
		State:               map[string]any{},
		allowedCapabilities: map[string]struct{}{},
	}

	runCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	ref, err := a.Spawn(actor.PropsFromFunc(func() actor.Actor { return appActor }), "lane-app")
	if err != nil {
		t.Fatalf("spawn sporeapp: %v", err)
	}

	// Long script runs on spore_exec.
	invokeDone := make(chan error, 1)
	invokeCall := ref.Invoke(runCtx, "sporeapp.invoke", gen.SporeAppInvokeReq{ID: "lane.app", Callable: "slow"})
	go func() {
		_, err := invokeCall.Final(runCtx)
		invokeDone <- err
	}()

	// Wait until the handler is provably in-flight (still running the loop).
	// A short settle lets the invoke reach the actor before we assert.
	time.Sleep(150 * time.Millisecond)
	select {
	case err := <-invokeDone:
		t.Fatalf("slow invoke already finished before assertions (machine too fast or invoke failed): %v", err)
	default:
	}

	// 1) Owner lane query answers promptly while the script occupies spore_exec.
	queryStart := time.Now()
	stateCall := ref.Invoke(runCtx, "sporeapp.state", gen.SporeAppStateReq{ID: "lane.app"})
	if _, err := stateCall.Final(runCtx); err != nil {
		t.Fatalf("sporeapp.state during invoke: %v", err)
	}
	if elapsed := time.Since(queryStart); elapsed > 2*time.Second {
		t.Fatalf("sporeapp.state took %s while invoke in-flight; owner lane blocked", elapsed)
	}

	// 2) reload issued mid-invoke must queue, not run concurrently and not
	// deadlock. Give a racing (wrong-lane) reload ample time to show itself.
	reloadDone := make(chan error, 1)
	reloadCall := ref.Invoke(runCtx, "sporeapp.reload", gen.SporeAppReloadReq{
		ID:          "lane.app",
		EntryModule: "main",
		Modules:     map[string]string{"main": nextModule},
		PackageHash: hashV2,
	})
	go func() {
		_, err := reloadCall.Final(runCtx)
		reloadDone <- err
	}()
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-reloadDone:
		t.Fatalf("reload completed while the invoking script was still in-flight; spore_exec lane not serialized (err=%v)", err)
	default:
	}
	appActor.mu.Lock()
	hashDuring := appActor.PackageHash
	appActor.mu.Unlock()
	if hashDuring != hashV1 {
		t.Fatalf("runtime table flipped to %q while script in-flight; reload did not queue", hashDuring)
	}

	// 3) Both complete — no deadlock — and reload lands last.
	select {
	case err := <-invokeDone:
		if err != nil {
			t.Fatalf("slow invoke failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("slow invoke did not return within 30s")
	}
	select {
	case err := <-reloadDone:
		if err != nil {
			t.Fatalf("reload after invoke failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("reload deadlocked behind completed invoke")
	}

	// 4) The reloaded package is live: new module answers, hash flipped.
	appActor.mu.Lock()
	hashAfter := appActor.PackageHash
	appActor.mu.Unlock()
	if hashAfter != hashV2 {
		t.Fatalf("PackageHash after reload = %q, want %q", hashAfter, hashV2)
	}
	answerCall := ref.Invoke(runCtx, "sporeapp.invoke", gen.SporeAppInvokeReq{ID: "lane.app", Callable: "answer"})
	answerResp, err := answerCall.Final(runCtx)
	if err != nil {
		t.Fatalf("post-reload answer invoke: %v", err)
	}
	payload := string(answerResp.(gen.SporeAppInvokeResp).Payload)
	if payload != "43" {
		t.Fatalf("post-reload answer = %q, want %q", payload, "43")
	}
}