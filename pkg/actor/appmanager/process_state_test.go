package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newProcessStateTestActor(t *testing.T) *Actor {
	t.Helper()
	manifest := gen.AppManifest{
		ID: "app.native", Runtime: "native", Version: "1.0.0", Namespace: "native.test",
	}
	return &Actor{
		actorID:           "appmanager",
		Apps:              map[string]gen.AppManifest{manifest.ID: manifest},
		Records:           map[string]appRecord{manifest.ID: {Manifest: manifest, State: stateRunning}},
		children:          map[string]string{},
		bindings:          appbinding.NewRegistry(),
		FreeAgentPolicies: map[string]appbinding.FreeAgentPolicy{},
		store:             persist.NewFSPersist(t.TempDir()),
	}
}

// oracleCapture returns a LookupServiceFn that resolves only the oracle
// service and captures the report_diagnostic payload.
func oracleCapture(calls *[]string, diag *domain.OracleReportDiagnosticReq) func(string) (ref.Ref, bool) {
	return func(name string) (ref.Ref, bool) {
		if name != "oracle" {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			*calls = append(*calls, callID)
			if req, ok := payload.(domain.OracleReportDiagnosticReq); ok {
				*diag = req
			}
			return nil
		}), true
	}
}

func TestReportProcessStateCrashedUpdatesEmitsAndReportsProblem(t *testing.T) {
	a := newProcessStateTestActor(t)
	longCause := strings.Repeat("x", 600)
	wantCause := longCause[:processStateCauseLimit] + "...(truncated)"

	var oracleCalls []string
	var diag domain.OracleReportDiagnosticReq
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = oracleCapture(&oracleCalls, &diag)

	err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateCrashed, CrashCause: longCause,
	})
	if err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}

	rec := a.Records["app.native"]
	if rec.State != stateCrashed {
		t.Fatalf("record.State = %q, want %q", rec.State, stateCrashed)
	}
	if rec.Error != wantCause {
		t.Fatalf("record.Error = %q, want truncated cause %q", rec.Error, wantCause)
	}

	// list/get projection carries the crashed state verbatim.
	status := a.statusFromRecord(a.Apps["app.native"], rec)
	if status.State != stateCrashed || status.Error != wantCause {
		t.Fatalf("projection = %+v, want crashed with truncated error", status)
	}

	// app_lifecycle event emitted with the truncated cause in Error.
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("emitted events = %d, want 1", len(ctx.EmittedEvents))
	}
	ev := ctx.EmittedEvents[0]
	if ev.Kind != "app_lifecycle" {
		t.Fatalf("event kind = %q, want app_lifecycle", ev.Kind)
	}
	le, ok := ev.Payload.(gen.AppLifecycleEvent)
	if !ok {
		t.Fatalf("payload type = %T, want AppLifecycleEvent", ev.Payload)
	}
	if le.Kind != stateCrashed || le.State != stateCrashed || le.ID != "app.native" || le.Error != wantCause {
		t.Fatalf("lifecycle event = %+v, want crashed with truncated error", le)
	}

	// Crash recorded as an oracle problem (visible via get_problems).
	if len(oracleCalls) != 1 || oracleCalls[0] != "oracle.report_diagnostic" {
		t.Fatalf("oracle calls = %v, want one report_diagnostic", oracleCalls)
	}
	if diag.Severity != "error" || diag.Source != "appmanager" {
		t.Fatalf("diagnostic severity/source = %q/%q, want error/appmanager", diag.Severity, diag.Source)
	}
	if diag.RawData != wantCause {
		t.Fatalf("diagnostic RawData not pre-truncated: len=%d", len(diag.RawData))
	}
	if !strings.Contains(diag.Message, "app.native") {
		t.Fatalf("diagnostic Message = %q, want plugin id inside", diag.Message)
	}
}

// TestReportProcessStateFailedPersistsCauseAndEventizes pins the restore /
// load-failure path: a "failed" report (pluginhost could not restore an
// artifact after a host restart) persists the cause, emits the lifecycle
// event, and records the oracle problem exactly like a crash — otherwise the
// registry keeps the stale persisted "running" and the frontend shows a
// healthy app whose every invoke fails.
func TestReportProcessStateFailedPersistsCauseAndEventizes(t *testing.T) {
	a := newProcessStateTestActor(t)

	var oracleCalls []string
	var diag domain.OracleReportDiagnosticReq
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = oracleCapture(&oracleCalls, &diag)

	cause := "open F:\\gone\\plugin.exe: The system cannot find the path specified."
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateFailed, CrashCause: cause,
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}

	// The persisted/projection cause is redacted: the host run directory must
	// not reach record.Error, the app_lifecycle event, or the frontend panel.
	redacted := "open [path]: The system cannot find the path specified."
	rec := a.Records["app.native"]
	if rec.State != stateFailed || rec.Error != redacted {
		t.Fatalf("record = %q/%q, want failed with redacted cause", rec.State, rec.Error)
	}
	status := a.statusFromRecord(a.Apps["app.native"], rec)
	if status.State != stateFailed || status.Error != redacted {
		t.Fatalf("projection = %+v, want failed with redacted error", status)
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("emitted events = %d, want 1", len(ctx.EmittedEvents))
	}
	le, ok := ctx.EmittedEvents[0].Payload.(gen.AppLifecycleEvent)
	if !ok || le.Kind != stateFailed || le.State != stateFailed || le.Error != redacted {
		t.Fatalf("lifecycle event = %+v, want failed with redacted cause", ctx.EmittedEvents[0].Payload)
	}
	if len(oracleCalls) != 1 {
		t.Fatalf("oracle calls = %v, want one report_diagnostic", oracleCalls)
	}
	if !strings.Contains(diag.Message, "failed") || !strings.Contains(diag.Message, "app.native") {
		t.Fatalf("diagnostic Message = %q, want state + plugin id inside", diag.Message)
	}
}

func TestReportProcessStateStoppedWritesWithoutEventOrProblem(t *testing.T) {
	a := newProcessStateTestActor(t)
	// Seed a stale crash error; a stopped report must clear it.
	rec := a.Records["app.native"]
	rec.Error = "stale"
	a.Records["app.native"] = rec

	var oracleCalls []string
	var diag domain.OracleReportDiagnosticReq
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = oracleCapture(&oracleCalls, &diag)

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: "stopped",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	rec = a.Records["app.native"]
	if rec.State != "stopped" {
		t.Fatalf("record.State = %q, want stopped", rec.State)
	}
	if rec.Error != "" {
		t.Fatalf("record.Error = %q, want cleared", rec.Error)
	}
	if len(ctx.EmittedEvents) != 0 {
		t.Fatalf("stopped must not emit lifecycle events, got %v", ctx.EmittedEvents)
	}
	if len(oracleCalls) != 0 {
		t.Fatalf("stopped must not report problems, got %v", oracleCalls)
	}
}

func TestReportProcessStateUnknownPluginIsNoOp(t *testing.T) {
	a := newProcessStateTestActor(t)
	var oracleCalls []string
	var diag domain.OracleReportDiagnosticReq
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = oracleCapture(&oracleCalls, &diag)

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.unknown", State: stateCrashed, CrashCause: "boom",
	}); err != nil {
		t.Fatalf("unknown plugin report must be a no-op, got %v", err)
	}
	if len(ctx.EmittedEvents) != 0 || len(oracleCalls) != 0 {
		t.Fatalf("unknown plugin report must not emit events or problems")
	}
}

func TestReportProcessStateEmptyRequestIgnored(t *testing.T) {
	a := newProcessStateTestActor(t)
	ctx := &testutil.FakeCtx{}
	for _, req := range []gen.AppManagerReportProcessStateReq{
		{State: stateCrashed},
		{PluginID: "app.native"},
	} {
		if err := a.handleReportProcessState(ctx, req); err != nil {
			t.Fatalf("empty-field report must be ignored, got %v", err)
		}
	}
	if a.Records["app.native"].State != stateRunning {
		t.Fatalf("record state changed on empty report: %q", a.Records["app.native"].State)
	}
}

// TestRedactProcessCause pins the run-directory scrub: causes surfaced to the
// frontend (record.Error, app_lifecycle Error, error panel) must not carry
// absolute filesystem paths, while URLs, ports and diagnostics survive.
func TestRedactProcessCause(t *testing.T) {
	cases := []struct{ in, want string }{
		// Windows drive path from a PathError / exec failure.
		{"open F:\\dev\\sporemind\\apps\\example.exe: Access is denied", "open [path]: Access is denied"},
		// Windows path with forward slashes (Go error strings may normalize).
		{"stat D:/dev/example/index.html: no such file", "stat [path]: no such file"},
		// UNC share.
		{"open \\\\server\\share\\apps\\app.exe: error", "open [path]: error"},
		// Unix roots.
		{"fork/exec /home/user/sporemind/novel: no such file", "fork/exec [path]: no such file"},
		{"write /tmp/plugin-abc/session.json: full disk", "write [path]: full disk"},
		// URLs and host:port pairs must survive (no letter directly before colon).
		{"connect http://127.0.0.1:60616 refused", "connect http://127.0.0.1:60616 refused"},
		{"dial tcp 192.168.1.4:22: timeout", "dial tcp 192.168.1.4:22: timeout"},
		// Plain diagnostics untouched.
		{"plugin process exited: exit status 1", "plugin process exited: exit status 1"},
	}
	for _, c := range cases {
		if got := redactProcessCause(c.in); got != c.want {
			t.Errorf("redactProcessCause(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := redactProcessCause(longCause); !strings.HasSuffix(got, "...(truncated)") {
		t.Errorf("truncation lost: %q", got)
	}
}

var longCause = "plugin process exited: " + strings.Repeat("x", 600) + " F:\\leak\\path.exe"

// seedBackend installs a live backend on the app's record so a subsequent
// running report with a different addr exercises the switch path.
func seedBackend(a *Actor, pluginID, secret, addr string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := a.Records[pluginID]
	setBackendFromLoad(&rec, secret, addr)
	rec.State = stateRunning
	a.Records[pluginID] = rec
}

// TestReportProcessStateRunningSwitchesBackendCold pins the un-guarded
// behavior: a running report carrying a fresh HttpAddr (cold-start restore)
// refreshes the record's backend and re-attaches the proxy.
func TestReportProcessStateRunningSwitchesBackendCold(t *testing.T) {
	a := newProcessStateTestActor(t)
	seedBackend(a, "app.native", "aabb", "127.0.0.1:60601")

	var proxyCalls []string
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != pluginhostServiceName {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			proxyCalls = append(proxyCalls, callID)
			return nil
		}), true
	}

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60602",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	rec := a.Records["app.native"]
	if rec.BackendUrl != "http://127.0.0.1:60602" {
		t.Fatalf("BackendUrl = %q, want switched to 60602", rec.BackendUrl)
	}
	if rec.SessionSecret != "aabb" {
		t.Fatalf("SessionSecret = %q, want reused", rec.SessionSecret)
	}
	if got := countCalls(proxyCalls, "pluginhost.proxy_attach"); got != 1 {
		t.Fatalf("proxy calls = %v, want one proxy_attach", proxyCalls)
	}
}

// countCalls counts occurrences of callID in the invoke log captured by a
// test fake (the log now interleaves proxy_attach with lifecycle
// event_deliver forwards).
func countCalls(calls []string, callID string) int {
	n := 0
	for _, c := range calls {
		if c == callID {
			n++
		}
	}
	return n
}

// TestReportProcessStateAttachBumpsGenerationAndEventizes pins the cold-start
// remount signal: a running report that adopts a fresh backend bumps the
// record's session generation and emits an app_lifecycle event carrying it.
// Restored panels may have mounted while /plugin/{id} still served the static
// assets handler; the bumped generation is what makes PluginIframe remount
// through the live proxy without a manual refresh. A steady-state report
// (same addr, same secret) must not bump or emit.
func TestReportProcessStateAttachBumpsGenerationAndEventizes(t *testing.T) {
	a := newProcessStateTestActor(t)
	seedBackend(a, "app.native", "aabb", "127.0.0.1:60601")
	rec := a.Records["app.native"]
	rec.Generation = 4
	a.Records["app.native"] = rec

	var proxyCalls []string
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != pluginhostServiceName {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			proxyCalls = append(proxyCalls, callID)
			return nil
		}), true
	}

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60602",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	if got := a.Records["app.native"].Generation; got != 5 {
		t.Fatalf("Generation = %d, want bumped to 5", got)
	}
	if len(ctx.EmittedEvents) != 1 {
		t.Fatalf("emitted events = %d, want 1", len(ctx.EmittedEvents))
	}
	le, ok := ctx.EmittedEvents[0].Payload.(gen.AppLifecycleEvent)
	if !ok {
		t.Fatalf("payload type = %T, want AppLifecycleEvent", ctx.EmittedEvents[0].Payload)
	}
	if le.Kind != "reloaded" || le.ID != "app.native" || le.State != stateRunning || le.Generation != 5 {
		t.Fatalf("lifecycle event = %+v, want reloaded/running with generation 5", le)
	}
	if got := countCalls(proxyCalls, "pluginhost.proxy_attach"); got != 1 {
		t.Fatalf("proxy calls = %v, want one proxy_attach", proxyCalls)
	}

	// Steady state: same addr and matching secret must not bump or emit.
	ctx2 := &testutil.FakeCtx{}
	ctx2.LookupServiceFn = ctx.LookupServiceFn
	if err := a.handleReportProcessState(ctx2, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60602", SessionSecret: "aabb",
	}); err != nil {
		t.Fatalf("handleReportProcessState (steady): %v", err)
	}
	if got := a.Records["app.native"].Generation; got != 5 {
		t.Fatalf("Generation = %d, want stable at 5", got)
	}
	if len(ctx2.EmittedEvents) != 0 {
		t.Fatalf("steady-state report must not emit, got %v", ctx2.EmittedEvents)
	}
}

// TestReportProcessStateRunningAdoptsLiveSecret pins the cold-start drift
// heal: a running report whose SessionSecret differs from the persisted
// record (crash between the appmanager record Save and pluginhost's
// ArtifactLoads Save) makes the record ADOPT the live process's secret and
// re-attach the proxy — even when the listener addr already matches, the
// stale record secret would 401 every gateway-proxied request until a manual
// unload/load.
func TestReportProcessStateRunningAdoptsLiveSecret(t *testing.T) {
	a := newProcessStateTestActor(t)
	seedBackend(a, "app.native", "stale-record-secret", "127.0.0.1:60601")

	var proxyCalls []string
	var attachToken string
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != pluginhostServiceName {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			proxyCalls = append(proxyCalls, callID)
			if req, ok := payload.(gen.PluginProxyAttachReq); ok {
				attachToken = req.Token
			}
			return nil
		}), true
	}

	// Same addr as the record, different secret: pure drift.
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60601", SessionSecret: "aa11bb22cc33",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	rec := a.Records["app.native"]
	if rec.SessionSecret != "aa11bb22cc33" {
		t.Fatalf("SessionSecret = %q, want the live process secret adopted", rec.SessionSecret)
	}
	if countCalls(proxyCalls, "pluginhost.proxy_attach") != 1 {
		t.Fatalf("proxy calls = %v, want one proxy_attach on secret adoption", proxyCalls)
	}
	if attachToken == "" {
		t.Fatal("proxy attach must carry a token minted from the adopted secret")
	}

	// Matching secret afterwards must NOT re-attach (stable steady state).
	proxyCalls = nil
	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60601", SessionSecret: "aa11bb22cc33",
	}); err != nil {
		t.Fatalf("handleReportProcessState (steady): %v", err)
	}
	if len(proxyCalls) != 0 {
		t.Fatalf("proxy calls = %v, want none in steady state", proxyCalls)
	}
}

// TestReportProcessStateRunningDefersSwitchDuringReload pins the 401-window
// fix: when a native reload is in flight (reloadingApps guard set), a running
// report from the candidate process must NOT switch the record's backend or
// attach the proxy — the record still carries the pre-reload secret, so an
// early switch mints a proxy token the new-secret backend rejects. The state
// field itself still updates so status surfaces stay current.
func TestReportProcessStateRunningDefersSwitchDuringReload(t *testing.T) {
	a := newProcessStateTestActor(t)
	seedBackend(a, "app.native", "oldsecret", "127.0.0.1:60601")
	a.reloadingApps = map[string]bool{"app.native": true}

	var proxyCalls []string
	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != pluginhostServiceName {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			proxyCalls = append(proxyCalls, callID)
			return nil
		}), true
	}

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60699",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	rec := a.Records["app.native"]
	if rec.BackendUrl != "http://127.0.0.1:60601" {
		t.Fatalf("BackendUrl = %q, want unchanged 60601 during reload", rec.BackendUrl)
	}
	if rec.SessionSecret != "oldsecret" {
		t.Fatalf("SessionSecret = %q, want unchanged during reload", rec.SessionSecret)
	}
	if rec.State != stateRunning {
		t.Fatalf("State = %q, want running still recorded", rec.State)
	}
	if len(proxyCalls) != 0 {
		t.Fatalf("proxy calls = %v, want none during reload", proxyCalls)
	}
}

// TestReportProcessStateRunningResumesAfterGuardCleared pins the recovery:
// once the reload commits (guard cleared), a running report with a fresh addr
// switches the backend again — the normal cold-start path is intact.
func TestReportProcessStateRunningResumesAfterGuardCleared(t *testing.T) {
	a := newProcessStateTestActor(t)
	seedBackend(a, "app.native", "newsecret", "127.0.0.1:60602")
	// Guard was set during the reload and has been cleared by the defer.
	a.reloadingApps = map[string]bool{}

	ctx := &testutil.FakeCtx{}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name != pluginhostServiceName {
			return nil, false
		}
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			return nil
		}), true
	}

	if err := a.handleReportProcessState(ctx, gen.AppManagerReportProcessStateReq{
		PluginID: "app.native", State: stateRunning, HttpAddr: "127.0.0.1:60603",
	}); err != nil {
		t.Fatalf("handleReportProcessState: %v", err)
	}
	rec := a.Records["app.native"]
	if rec.BackendUrl != "http://127.0.0.1:60603" {
		t.Fatalf("BackendUrl = %q, want switched to 60603 after guard cleared", rec.BackendUrl)
	}
}
