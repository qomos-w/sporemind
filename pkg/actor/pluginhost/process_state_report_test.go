package pluginhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// appManagerCapture returns an actor whose appmanager service resolves to a
// fake ref recording every invoke (callID + payload).
func appManagerCapture(t *testing.T, calls *[][]any) *Actor {
	t.Helper()
	a := &Actor{}
	a.actorCtx = &testutil.FakeCtx{
		LookupServiceFn: func(name string) (ref.Ref, bool) {
			if name != "appmanager" {
				return nil, false
			}
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				*calls = append(*calls, []any{callID, payload})
				return nil
			}), true
		},
	}
	return a
}

// TestTransitionTriggersProcessStateReport pins the T2 wiring: an actual
// state transition fires a fire-and-forget report_process_state tell to
// appmanager with the verbatim state + crash cause; a repeat of the current
// state reports nothing.
func TestTransitionTriggersProcessStateReport(t *testing.T) {
	var calls [][]any
	a := appManagerCapture(t, &calls)
	a.processStateOnTransition = a.reportProcessStateToAppManager

	a.setProcessState("app.x", "running", "", "127.0.0.1:4001", 0)
	a.setProcessState("app.x", "running", "", "127.0.0.1:4001", 0) // repeat: no report
	a.setProcessState("app.x", "crashed", "boom", "", 0)

	if len(calls) != 2 {
		t.Fatalf("reports = %d, want 2 (running + crashed)", len(calls))
	}
	if calls[0][0] != "appmanager.report_process_state" {
		t.Fatalf("callID = %v, want appmanager.report_process_state", calls[0][0])
	}
	req, ok := calls[0][1].(gen.AppManagerReportProcessStateReq)
	if !ok || req.PluginID != "app.x" || req.State != "running" || req.CrashCause != "" {
		t.Fatalf("running report payload = %+v, want app.x running", calls[0][1])
	}
	if req.HttpAddr != "127.0.0.1:4001" {
		t.Fatalf("running report must carry the listener addr for gateway re-attach: %+v", req)
	}
	req, ok = calls[1][1].(gen.AppManagerReportProcessStateReq)
	if !ok || req.PluginID != "app.x" || req.State != "crashed" || req.CrashCause != "boom" {
		t.Fatalf("crashed report payload = %+v, want app.x crashed/boom", calls[1][1])
	}
}

// TestRestoreRunningReportCarriesLiveSecret pins the cold-start drift heal's
// report half: the restore-time "running" report must carry the session
// secret embedded in the persisted ArtifactLoads entry the process was
// spawned from, so appmanager can adopt it when its own record drifted
// (two actors, two persisted files, crash between the Saves). Non-running
// reports and secret-less entries send an empty secret.
func TestRestoreRunningReportCarriesLiveSecret(t *testing.T) {
	var calls [][]any
	a := appManagerCapture(t, &calls)
	a.processStateOnTransition = a.reportProcessStateToAppManager

	a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{
		"app.live": {OnLoadConfig: []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"s-live"}`)},
		"app.bare": {},
	}
	a.setProcessState("app.live", "running", "", "127.0.0.1:40999", 0)
	a.setProcessState("app.live", "crashed", "boom", "", 0)
	a.setProcessState("app.bare", "running", "", "127.0.0.1:40998", 0)

	var liveRunning, liveCrashed, bareRunning *gen.AppManagerReportProcessStateReq
	for _, c := range calls {
		req, ok := c[1].(gen.AppManagerReportProcessStateReq)
		if !ok || c[0] != "appmanager.report_process_state" {
			continue
		}
		switch {
		case req.PluginID == "app.live" && req.State == "running":
			liveRunning = &req
		case req.PluginID == "app.live" && req.State == "crashed":
			liveCrashed = &req
		case req.PluginID == "app.bare" && req.State == "running":
			bareRunning = &req
		}
	}
	if liveRunning == nil || liveRunning.SessionSecret != "s-live" {
		t.Fatalf("running report must carry the live process secret, got %+v", liveRunning)
	}
	if liveCrashed == nil || liveCrashed.SessionSecret != "" {
		t.Fatalf("crashed report must not carry a secret, got %+v", liveCrashed)
	}
	if bareRunning == nil || bareRunning.SessionSecret != "" {
		t.Fatalf("secret-less entry must report an empty secret, got %+v", bareRunning)
	}
}

// TestReportProcessStateToleratesMissingAppManager pins the boot-order
// tolerance: when appmanager is not exposed yet (pluginhostFirst start), the
// report is dropped with a log line — no panic, no retry, no block.
func TestReportProcessStateToleratesMissingAppManager(t *testing.T) {
	a := &Actor{}
	a.actorCtx = &testutil.FakeCtx{} // LookupService misses everything
	a.processStateOnTransition = a.reportProcessStateToAppManager

	done := make(chan struct{})
	go func() {
		a.setProcessState("app.x", "crashed", "boom", "", 0)
		close(done)
	}()
	<-done

	state, crash := a.processState("app.x")
	if state != "crashed" || crash != "boom" {
		t.Fatalf("local state = %q/%q, want crashed/boom", state, crash)
	}
}

// TestOnStartWiresProcessStateReport verifies the seam is connected at
// OnStart time (production wiring, not just tests).
func TestOnStartWiresProcessStateReport(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if a.processStateOnTransition == nil {
		t.Fatal("processStateOnTransition not wired by OnStart")
	}
	// appmanager is not exposed in this bare ctx: the report must be
	// dropped silently (startup-order tolerance), state still recorded.
	a.setProcessState("app.y", "crashed", "boom", "", 0)
	state, _ := a.processState("app.y")
	if state != "crashed" {
		t.Fatalf("local state = %q, want crashed", state)
	}
}

// TestRestoreSpawnReportReachesAppManager pins the OnStart wiring order: the
// process-state seam must be live BEFORE the restore loop, because the
// restored subprocesses report their post-OnLoad "running" (with the fresh
// listener addr) from inside loader.Load. A restore-time spawn report that
// fires while the seam is still nil is dropped, leaving appmanager's record
// on the stale pre-restart backend with no gateway attach.
func TestRestoreSpawnReportReachesAppManager(t *testing.T) {
	var calls [][]any
	a := appManagerCapture(t, &calls)

	wiredAtOpen := false
	opener := &reportingStubOpener{
		onOpen: func() {
			// Mirror what the real subprocess transport does once its OnLoad
			// binds the listener — this runs inside the restore loop's
			// loader.Load, exactly like production.
			wiredAtOpen = a.processStateOnTransition != nil
			a.ReportProcessState("app.live", "running", "", "127.0.0.1:40999", a.NextProcessGeneration("app.live"))
		},
	}
	SetOpenerOverrideForTest(a, opener)
	stubPath := filepath.Join(t.TempDir(), "stub.bin")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{
		"app.live": {
			Manifest:     gen.AppManifest{ID: "app.live", Name: "Live", Version: "1.0.0", Runtime: "native", Namespace: "ns.live", ProtocolVersion: 2},
			Abi:          subprocessAbi(),
			ArtifactPath: stubPath,
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "appmanager" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			calls = append(calls, []any{callID, payload})
			return nil
		}), true
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	if !wiredAtOpen {
		t.Fatal("processStateOnTransition must be wired before the restore loop's loader.Load runs")
	}
	var running *gen.AppManagerReportProcessStateReq
	for _, c := range calls {
		if req, ok := c[1].(gen.AppManagerReportProcessStateReq); ok && c[0] == "appmanager.report_process_state" && req.State == "running" {
			running = &req
		}
	}
	if running == nil {
		t.Fatalf("restore-time running report never reached appmanager: %v", calls)
	}
	if running.PluginID != "app.live" || running.HttpAddr != "127.0.0.1:40999" {
		t.Fatalf("running report = %+v, want app.live with listener addr", running)
	}
}

// reportingStubOpener is a minimal ArtifactOpener whose Open delegates to a
// callback before returning inert invoke/closer functions.
type reportingStubOpener struct {
	onOpen func()
}

func (o *reportingStubOpener) Open(_ gen.PluginAbi, _ string, _ string, _ string, _ map[string]struct{}, _ []byte) (func(ctx context.Context, callable string, request []byte) ([]byte, error), pluginhost.InvokeStreamFunc, func() error, string, error) {
	o.onOpen()
	noop := func(ctx context.Context, callable string, request []byte) ([]byte, error) { return nil, nil }
	return noop, nil, func() error { return nil }, "127.0.0.1:40999", nil
}

// TestRestoreFailureReportsFailedState pins the restore-time observability
// hole: when a persisted artifact fails to load at OnStart (e.g. the recorded
// path points at an unavailable drive), pluginhost must tell appmanager the
// plugin is "failed" with the load error — otherwise the registry keeps the
// pre-restart "running" and every invoke dies with "callable not declared"
// while the frontend shows a healthy app.
func TestRestoreFailureReportsFailedState(t *testing.T) {
	var calls [][]any
	a := appManagerCapture(t, &calls)
	a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{
		"app.gone": {
			Manifest:     gen.AppManifest{ID: "app.gone", Name: "Gone", Version: "1.0.0", Runtime: "native", Namespace: "ns.gone", ProtocolVersion: 2},
			Abi:          subprocessAbi(),
			ArtifactPath: filepath.Join(t.TempDir(), "does-not-exist.exe"),
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				calls = append(calls, []any{callID, payload})
				return nil
			}), true
		}
		return nil, false
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	var failedReport *gen.AppManagerReportProcessStateReq
	for _, c := range calls {
		if c[0] != "appmanager.report_process_state" {
			continue
		}
		if req, ok := c[1].(gen.AppManagerReportProcessStateReq); ok && req.PluginID == "app.gone" && req.State == "failed" {
			failedReport = &req
		}
	}
	if failedReport == nil {
		t.Fatalf("no failed report for app.gone among %v", calls)
	}
	if !strings.Contains(failedReport.CrashCause, "does-not-exist.exe") {
		t.Fatalf("failed report cause = %q, want the load error", failedReport.CrashCause)
	}
}

// TestOnStartNotifiesAppManagerOnlineAfterRestore pins the deterministic
// cold-start proxy reconciliation trigger: OnStart must fire
// appmanager.pluginhost_online AFTER the restore loop (so the running reports
// it processes cannot yet resolve this unexposed pluginhost for their attach
// tells) and after the domain is exposed. The online tell is what lets
// appmanager re-attach gateway routes for running native apps — without it,
// every cold start leaves /plugin/{id}/invoke|events 405/404 until a reload.
func TestOnStartNotifiesAppManagerOnlineAfterRestore(t *testing.T) {
	var calls [][]any
	a := appManagerCapture(t, &calls)

	openIndex := -1
	opener := &reportingStubOpener{
		onOpen: func() {
			// Runs inside the restore loop's loader.Load, before the online
			// tell: record the call index at spawn time.
			a.ReportProcessState("app.live", "running", "", "127.0.0.1:40999", a.NextProcessGeneration("app.live"))
		},
	}
	SetOpenerOverrideForTest(a, opener)
	stubPath := filepath.Join(t.TempDir(), "stub.bin")
	if err := os.WriteFile(stubPath, []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.ArtifactLoads = map[string]gen.PluginArtifactLoadReq{
		"app.live": {
			Manifest:     gen.AppManifest{ID: "app.live", Name: "Live", Version: "1.0.0", Runtime: "native", Namespace: "ns.live", ProtocolVersion: 2},
			Abi:          subprocessAbi(),
			ArtifactPath: stubPath,
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != "appmanager" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			calls = append(calls, []any{callID, payload})
			return nil
		}), true
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	onlineIndex := -1
	for i, c := range calls {
		if c[0] == "appmanager.report_process_state" {
			openIndex = i
		}
		if c[0] == "appmanager.pluginhost_online" {
			onlineIndex = i
			if _, ok := c[1].(gen.AppManagerPluginhostOnlineReq); !ok {
				t.Fatalf("online tell payload = %T, want AppManagerPluginhostOnlineReq", c[1])
			}
		}
	}
	if onlineIndex == -1 {
		t.Fatalf("OnStart must fire appmanager.pluginhost_online, got %v", calls)
	}
	if openIndex == -1 {
		t.Fatal("restore-time running report missing — test setup drift")
	}
	if onlineIndex < openIndex {
		t.Fatalf("online tell (index %d) must fire after the restore-time running report (index %d)", onlineIndex, openIndex)
	}
}
