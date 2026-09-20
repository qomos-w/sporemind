package aiaggregator

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// oracleStubCtx extends stubActorCtx with LookupService so resolveOracleRef
// can discover a fake oracle actor. byService misses return not-found, so
// existing nested-dispatch behavior is unchanged.
type oracleStubCtx struct {
	stubActorCtx
	byService map[string]ref.Ref
}

func (c *oracleStubCtx) LookupService(name string) (ref.Ref, bool) {
	r, ok := c.byService[name]
	return r, ok
}

// capturedInvoke records one ref.Invoke call.
type capturedInvoke struct {
	callID  string
	payload any
}

// capturingRef is a ref.Ref that records every invocation and answers with an
// immediately-closed empty stream (fire-and-forget friendly).
type capturingRef struct {
	mu    sync.Mutex
	calls []capturedInvoke
}

func (r *capturingRef) ID() id.ActorID          { return id.ActorID{} }
func (r *capturingRef) Service() (string, bool) { return "oracle", true }
func (r *capturingRef) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	r.mu.Lock()
	r.calls = append(r.calls, capturedInvoke{callID: callID, payload: payload})
	r.mu.Unlock()
	return invoke.NewCall(invoke.CallModeUnary, &fakeResultStream{})
}
func (r *capturingRef) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
func (r *capturingRef) lastDiagnostic(t *testing.T) domain.OracleReportDiagnosticReq {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		t.Fatalf("no oracle invocations recorded")
	}
	last := r.calls[len(r.calls)-1]
	req, ok := last.payload.(domain.OracleReportDiagnosticReq)
	if !ok {
		t.Fatalf("oracle payload type = %T, want domain.OracleReportDiagnosticReq", last.payload)
	}
	return req
}

// newDiagnosticTestActor wires an Actor whose actorCtx resolves "oracle" to
// the given capturingRef and aimanager to a fake (structured reports land
// there without network).
func newDiagnosticTestActor(t *testing.T, oracle *capturingRef) *Actor {
	t.Helper()
	return &Actor{
		id:           "diag-agg",
		lifecycleCtx: context.Background(),
		actorCtx: &oracleStubCtx{
			stubActorCtx: stubActorCtx{},
			byService:    map[string]ref.Ref{"oracle": oracle},
		},
		strategy:     NewFallbackStrategy(),
		aimanagerRef: &fakeRef{},
	}
}

// TestApplyStreamOpenFailure_ReportsOracleDiagnosticOnce verifies acceptance
// item 1+2: the first degradation-emitting failure sends exactly one
// oracle.report_diagnostic with Severity/Source/Unit/HttpStatus derived from
// the error, while llmclient.RecordFailure still applies the cooldown.
func TestApplyStreamOpenFailure_ReportsOracleDiagnosticOnce(t *testing.T) {
	resetGlobalHealth(t)
	oracle := &capturingRef{}
	a := newDiagnosticTestActor(t, oracle)

	unit := &CallableUnit{ID: "p::m", ProviderName: "p", Model: "m"}
	err429 := &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}

	// First 429: healthy → cooling_down, one diagnostic expected.
	a.applyStreamOpenFailure(unit, err429, llmclient.ClassifyStreamOpenError(err429))

	if got := oracle.count(); got != 1 {
		t.Fatalf("oracle calls after first 429 = %d, want 1", got)
	}
	req := oracle.lastDiagnostic(t)
	if req.Severity != "warning" {
		t.Errorf("Severity = %q, want warning (4xx)", req.Severity)
	}
	if req.Source != "aiaggregator" {
		t.Errorf("Source = %q, want aiaggregator", req.Source)
	}
	if req.Unit == nil || req.Unit.Provider != "p" || req.Unit.Model != "m" {
		t.Errorf("Unit = %+v, want p/m", req.Unit)
	}
	if req.HttpStatus != 429 {
		t.Errorf("HttpStatus = %d, want 429", req.HttpStatus)
	}
	if !strings.Contains(req.Message, "rate limited") || !strings.Contains(req.RawData, "rate limited") {
		t.Errorf("Message/RawData must carry the upstream message, got %q / %q", req.Message, req.RawData)
	}

	// RecordFailure side effect intact: the unit is globally cooled now.
	if llmclient.IsAvailable("p", "m", time.Now()) {
		t.Errorf("RecordFailure did not apply cooldown — routing policy changed")
	}

	// Second 429 on the already-cooling unit: debounced, no new report.
	a.applyStreamOpenFailure(unit, err429, llmclient.ClassifyStreamOpenError(err429))
	if got := oracle.count(); got != 1 {
		t.Fatalf("oracle calls after repeat 429 = %d, want still 1 (debounce)", got)
	}
}

// TestApplyStreamOpenFailure_DebounceScenarios covers acceptance item 3:
// pre-cooled units never report, ClassFailover reports only when
// AvailabilityThreshold is crossed, and ClassStop errors never report.
func TestApplyStreamOpenFailure_DebounceScenarios(t *testing.T) {
	resetGlobalHealth(t)

	// Pre-cooled unit: RecordFailure outside the failure path first; the
	// before-snapshot is not healthy, so no diagnostic may be emitted.
	oraclePre := &capturingRef{}
	aPre := newDiagnosticTestActor(t, oraclePre)
	preCooled := &CallableUnit{ID: "pc::m", ProviderName: "pc", Model: "m"}
	llmclient.RecordFailure(preCooled.ProviderName, preCooled.Model, &llmclient.UpstreamError{StatusCode: 429})
	err401 := &llmclient.UpstreamError{StatusCode: 401, Message: "unauthorized"}
	aPre.applyStreamOpenFailure(preCooled, err401, llmclient.ClassifyStreamOpenError(err401))
	if got := oraclePre.count(); got != 0 {
		t.Fatalf("pre-cooled unit produced %d oracle calls, want 0", got)
	}

	// ClassFailover: threshold 2 — first 503 keeps the unit healthy (no
	// report), second crosses into cooling_down (one report), third is
	// suppressed by the debounce.
	restorePolicy := setAvailabilityThreshold(t, 2)
	defer restorePolicy()

	oracleFF := &capturingRef{}
	aFF := newDiagnosticTestActor(t, oracleFF)
	ffUnit := &CallableUnit{ID: "ff::m", ProviderName: "ff", Model: "m"}
	err503 := &llmclient.UpstreamError{StatusCode: 503, Message: "upstream unavailable"}

	aFF.applyStreamOpenFailure(ffUnit, err503, llmclient.ClassifyStreamOpenError(err503))
	if got := oracleFF.count(); got != 0 {
		t.Fatalf("below-threshold 503 produced %d oracle calls, want 0", got)
	}

	aFF.applyStreamOpenFailure(ffUnit, err503, llmclient.ClassifyStreamOpenError(err503))
	if got := oracleFF.count(); got != 1 {
		t.Fatalf("threshold-crossing 503 produced %d oracle calls, want 1", got)
	}
	req := oracleFF.lastDiagnostic(t)
	if req.Severity != "error" {
		t.Errorf("Severity = %q, want error (5xx)", req.Severity)
	}

	aFF.applyStreamOpenFailure(ffUnit, err503, llmclient.ClassifyStreamOpenError(err503))
	if got := oracleFF.count(); got != 1 {
		t.Fatalf("post-transition 503 produced %d oracle calls, want still 1", got)
	}

	// ClassStop (400): request-level, RecordFailure no-ops, state stays
	// healthy — never reported as a problem.
	oracleStop := &capturingRef{}
	aStop := newDiagnosticTestActor(t, oracleStop)
	stopUnit := &CallableUnit{ID: "st::m", ProviderName: "st", Model: "m"}
	err400 := &llmclient.UpstreamError{StatusCode: 400, Message: "bad request"}
	aStop.applyStreamOpenFailure(stopUnit, err400, llmclient.ClassifyStreamOpenError(err400))
	if got := oracleStop.count(); got != 0 {
		t.Fatalf("ClassStop 400 produced %d oracle calls, want 0", got)
	}

	// Network error without HTTP status: severity error, HttpStatus omitted.
	// Threshold 1 so a single availability-class failure degrades the unit.
	restoreThreshold := setAvailabilityThreshold(t, 1)
	defer restoreThreshold()
	oracleNet := &capturingRef{}
	aNet := newDiagnosticTestActor(t, oracleNet)
	netUnit := &CallableUnit{ID: "net::m", ProviderName: "net", Model: "m"}
	errNet := context.DeadlineExceeded
	aNet.applyStreamOpenFailure(netUnit, errNet, llmclient.ClassifyStreamOpenError(errNet))
	if got := oracleNet.count(); got < 1 {
		t.Fatalf("network timeout produced %d oracle calls, want >=1", got)
	}
	netReq := oracleNet.lastDiagnostic(t)
	if netReq.Severity != "error" {
		t.Errorf("network Severity = %q, want error", netReq.Severity)
	}
	if netReq.HttpStatus != 0 {
		t.Errorf("network HttpStatus = %d, want 0", netReq.HttpStatus)
	}
}

// TestReportOracleDiagnostic_TruncatesPayloads verifies the hard truncation
// rule: Message/RawData are capped at diagnosticRawDataLimit bytes with a
// visible marker before crossing the actor boundary.
func TestReportOracleDiagnostic_TruncatesPayloads(t *testing.T) {
	resetGlobalHealth(t)
	// Threshold 1 so the single availability-class 500 degrades and reports.
	restoreThreshold := setAvailabilityThreshold(t, 1)
	defer restoreThreshold()
	oracle := &capturingRef{}
	a := newDiagnosticTestActor(t, oracle)

	unit := &CallableUnit{ID: "big::m", ProviderName: "big", Model: "m"}
	longBody := strings.Repeat("x", 2000)
	err500 := &llmclient.UpstreamError{StatusCode: 500, Message: longBody}

	a.applyStreamOpenFailure(unit, err500, llmclient.ClassifyStreamOpenError(err500))

	req := oracle.lastDiagnostic(t)
	limit := diagnosticRawDataLimit + len("...(truncated)")
	if len(req.RawData) > limit {
		t.Fatalf("RawData length = %d, want <= %d", len(req.RawData), limit)
	}
	if len(req.Message) > limit {
		t.Fatalf("Message length = %d, want <= %d", len(req.Message), limit)
	}
	if !strings.HasSuffix(req.RawData, "...(truncated)") {
		t.Errorf("RawData = %q..., want ...(truncated) suffix", req.RawData[:32])
	}
	raw := err500.Error()
	if req.RawData != raw[:diagnosticRawDataLimit]+"...(truncated)" {
		t.Errorf("RawData must be the unmodified first %d bytes of %q plus the marker", diagnosticRawDataLimit, raw[:24])
	}
}

// TestDispatchRotation_StillRotatesAndReportsOnce is the end-to-end acceptance
// check: within a real dispatch the failed candidate still rotates to the next
// unit exactly once (routing policy unchanged), exactly one diagnostic is
// emitted for the failed unit, and a follow-up dispatch that skips the cooled
// unit emits nothing more.
func TestDispatchRotation_StillRotatesAndReportsOnce(t *testing.T) {
	resetGlobalHealth(t)
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	var attemptsA atomic.Int32
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "fail429",
		Factory: func(_, _ string) llmclient.Client {
			return &countingErrorClient{attempts: &attemptsA, streamErr: &llmclient.UpstreamError{StatusCode: 429, Message: "rate limited"}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory:  func(_, _ string) llmclient.Client { return &okClient{} },
	})
	regValue := *reg

	oracle := &capturingRef{}
	a := newDiagnosticTestActor(t, oracle)
	a.aimanagerRef = tokenFakeRef()
	a.registry = regValue
	a.units = []CallableUnit{
		{ID: "a::m1", Model: "m1", ProviderName: "a", Protocol: "fail429"},
		{ID: "b::m2", Model: "m2", ProviderName: "b", Protocol: "ok"},
	}
	ctx := &testPureCtx{done: make(chan struct{})}

	emit := &collectingEmitter{done: make(chan struct{})}
	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	if ru := emit.resolvedUnit(); ru == nil || ru.Provider != "b" || ru.Model != "m2" {
		t.Fatalf("first dispatch resolved unit = %+v, want b/m2 (rotation intact)", ru)
	}
	if got := attemptsA.Load(); got != 1 {
		t.Fatalf("a::m1 attempted %d times, want exactly 1 (triedIDs rotation)", got)
	}
	if got := oracle.count(); got != 1 {
		t.Fatalf("oracle calls after rotating dispatch = %d, want 1", got)
	}
	req := oracle.lastDiagnostic(t)
	if req.Unit == nil || req.Unit.Provider != "a" || req.Unit.Model != "m1" || req.HttpStatus != 429 {
		t.Errorf("diagnostic Unit/HttpStatus = %+v/%d, want a/m1/429", req.Unit, req.HttpStatus)
	}

	// Second dispatch: cooled a::m1 is excluded before any attempt — no new
	// failure handling, no new diagnostic.
	emit2 := &collectingEmitter{done: make(chan struct{})}
	if err := a.handleDispatch(ctx, domain.SendSessionMessageReq{}, emit2); err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if got := attemptsA.Load(); got != 1 {
		t.Fatalf("a::m1 attempted %d times across dispatches, want still 1", got)
	}
	if got := oracle.count(); got != 1 {
		t.Fatalf("oracle calls after second dispatch = %d, want still 1", got)
	}
}

// setAvailabilityThreshold swaps in a cooldown policy with the given
// AvailabilityThreshold, returning a restore function.
func setAvailabilityThreshold(t *testing.T, threshold int) func() {
	t.Helper()
	orig := llmclient.DefaultProviderHealth.Policy()
	llmclient.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: threshold,
		BaseCooldown:          30 * time.Millisecond,
		BackoffFactor:         1.5,
		MaxCooldown:           time.Second,
		RateLimitFloor:        50 * time.Millisecond,
		RateLimitCeiling:      time.Second,
		QuotaCooldown:         100 * time.Millisecond,
	})
	return func() { llmclient.SetCooldownPolicy(orig) }
}
