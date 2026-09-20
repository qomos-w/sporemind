package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// ---------------------------------------------------------------------------
// Fake event source
// ---------------------------------------------------------------------------

// fakeEventStream is a channel-backed eventWaitStream used as the fake event
// source: tests push payloads on events, errors on errs, or close either to
// end the stream.
type fakeEventStream struct {
	events   chan any
	errs     chan error
	cancelCh chan struct{}
	cancel   atomic.Bool
	nexts    atomic.Int32
}

func newFakeEventStream() *fakeEventStream {
	return &fakeEventStream{
		events:   make(chan any, 4),
		errs:     make(chan error, 4),
		cancelCh: make(chan struct{}),
	}
}

func (s *fakeEventStream) Next(ctx context.Context) (any, error) {
	s.nexts.Add(1)
	select {
	case v, ok := <-s.events:
		if !ok {
			return nil, io.EOF
		}
		return v, nil
	case err := <-s.errs:
		return nil, err
	case <-s.cancelCh:
		return nil, errors.New("fake stream cancelled")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *fakeEventStream) Cancel() {
	if s.cancel.CompareAndSwap(false, true) {
		close(s.cancelCh)
	}
}

// ---------------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------------

// eventWaitTestFixture mirrors toolcallTestFixture: a fresh workspace actor
// whose project actor is a fake recording status CAS flips and outputs, and
// whose event subscription is a controllable fake stream.
type eventWaitTestFixture struct {
	a               *Actor
	ctx             *testutil.FakeCtx
	projectID       string
	callerAgentID   string
	boundTaskCardID string

	// Fake project actor state.
	mu          sync.Mutex
	cardStatus  map[string]string // cardID → live status (starts "doing": activation runs post-claim)
	statusCalls []gen.WikiSetStatusReq
	outputsSet  map[string]map[string]any

	// Fake event source state.
	streams     []*fakeEventStream // every stream the executor subscribed
	subRequests []struct{ service, kind string }
	subFn       func(service, kind string) eventWaitStream
}

func newEventWaitTestFixture(t *testing.T) *eventWaitTestFixture {
	t.Helper()
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	f := &eventWaitTestFixture{
		a:               a,
		ctx:             ctx,
		projectID:       projectID,
		callerAgentID:   callerAgentID,
		boundTaskCardID: "task-eventwait-1",
		cardStatus:      map[string]string{},
		outputsSet:      map[string]map[string]any{},
	}
	f.cardStatus[f.boundTaskCardID] = "doing"

	ctx.LookupIDFn = f.lookupID
	a.eventWaitSubscribeFn = f.subscribe
	// Keep resubscribe backoff fast so error/retry tests stay snappy.
	t.Cleanup(func() { eventWaitResubscribeDelay = 2 * time.Second })
	eventWaitResubscribeDelay = 10 * time.Millisecond
	return f
}

func (f *eventWaitTestFixture) subscribe(service, kind string) eventWaitStream {
	f.mu.Lock()
	f.subRequests = append(f.subRequests, struct{ service, kind string }{service, kind})
	impl := f.subFn
	f.mu.Unlock()
	if impl != nil {
		return impl(service, kind)
	}
	s := newFakeEventStream()
	f.mu.Lock()
	f.streams = append(f.streams, s)
	f.mu.Unlock()
	return s
}

// subCount is the locked accessor for waitFor conditions.
func (f *eventWaitTestFixture) subCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subRequests)
}

// lastSubRequest is the locked accessor for the most recent (service, kind).
func (f *eventWaitTestFixture) lastSubRequest() struct{ service, kind string } {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subRequests[len(f.subRequests)-1]
}

// pushEvent delivers a payload on the given stream (index 0 = first
// subscription).
func (f *eventWaitTestFixture) pushEvent(i int, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.streams) {
		return
	}
	f.streams[i].events <- payload
}

// failStream makes the given stream's Next return err.
func (f *eventWaitTestFixture) failStream(i int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.streams) {
		return
	}
	f.streams[i].errs <- err
}

func (f *eventWaitTestFixture) lookupID(aid id.ActorID) (ref.Ref, bool) {
	aidStr := aid.String()
	if aidStr == f.callerAgentID {
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "agent_status" {
				return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
			}
			return nil
		}), true
	}
	return f.fakeProjectRef(aid), true
}

// fakeProjectRef implements the project actor's three callables used by the
// event_wait completion path. wiki_set_status honors the ExpectedStatus CAS
// exactly like the real project actor.
func (f *eventWaitTestFixture) fakeProjectRef(aid id.ActorID) ref.Ref {
	return testutil.NewFakeRef(aid, func(callID string, payload any) any {
		switch callID {
		case "project.wiki_get_card":
			req, ok := payload.(domain.WikiGetCardReq)
			if !ok {
				return nil
			}
			return domain.WikiGetCardResp{ID: req.ID, Raw: f.cardRaw()}
		case "project.wiki_set_status":
			req, ok := payload.(gen.WikiSetStatusReq)
			if !ok {
				return nil
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.statusCalls = append(f.statusCalls, req)
			current, tracked := f.cardStatus[req.ID]
			if !tracked {
				current = "doing"
			}
			if req.ExpectedStatus != "" && current != req.ExpectedStatus {
				return fmt.Errorf("project.wiki.set_status: expected status %q, got %q", req.ExpectedStatus, current)
			}
			f.cardStatus[req.ID] = req.Status
			return domain.WikiSetStatusResp{PreviousStatus: current}
		case "project.wiki_set_task_outputs":
			req, ok := payload.(domain.WikiSetTaskOutputsReq)
			if !ok {
				return nil
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			f.outputsSet[req.CardID] = req.Outputs
			return domain.WikiSetTaskOutputsResp{}
		}
		return nil
	})
}

func (f *eventWaitTestFixture) cardRaw() string {
	return "---\nid: " + f.boundTaskCardID + "\ntype: task\nstatus: doing\n" +
		"data:\n" +
		"  exec:\n" +
		"    kind: event_wait\n" +
		"    event_kind: toast.card_added\n" +
		"    event_service: toast\n" +
		"---\n\nEvent-wait task body."
}

func (f *eventWaitTestFixture) claimReq() ClaimReq {
	return ClaimReq{
		CallerAgentID:   f.callerAgentID,
		ProjectID:       f.projectID,
		BoundTaskCardID: f.boundTaskCardID,
		CardRaw:         f.cardRaw(),
		Claimed:         true,
		PreviousStatus:  "todo",
		Body:            "Event-wait task body.",
	}
}

func (f *eventWaitTestFixture) status() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cardStatus[f.boundTaskCardID]
}

func (f *eventWaitTestFixture) outputs() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.outputsSet[f.boundTaskCardID]
}

func (f *eventWaitTestFixture) waitCount() int {
	f.a.eventWaitMu.RLock()
	defer f.a.eventWaitMu.RUnlock()
	return len(f.a.eventWaits)
}

// waitFor polls cond until true or the deadline, failing the test on timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestEventWaitExecutor_Registered(t *testing.T) {
	f := newEventWaitTestFixture(t)
	exec, ok := f.a.execRegistry.Lookup(ExecKindEventWait)
	if !ok {
		t.Fatal("event_wait executor not registered after OnStart")
	}
	if exec.Kind() != ExecKindEventWait {
		t.Fatalf("expected kind event_wait, got %q", exec.Kind())
	}
}

func TestEventWaitConfig_Parsing(t *testing.T) {
	base := "---\nid: t\ntype: task\ndata:\n  exec:\n    kind: event_wait\n"
	req := func(yaml string) ClaimReq {
		return ClaimReq{BoundTaskCardID: "t", CardRaw: base + yaml + "---\nBody."}
	}

	if _, err := requireEventWaitConfig(req("")); err == nil || !strings.Contains(err.Error(), "event_kind is required") {
		t.Fatalf("missing event_kind: err = %v", err)
	}

	cfg, err := requireEventWaitConfig(req("    event_kind: toast.card_added\n"))
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.EventService != eventWaitDefaultService {
		t.Fatalf("default service = %q, want %q", cfg.EventService, eventWaitDefaultService)
	}
	if cfg.TimeoutSec != 0 {
		t.Fatalf("default timeout = %d, want 0", cfg.TimeoutSec)
	}

	cfg, err = requireEventWaitConfig(req("    event_kind: app_event\n    event_service: appmanager\n    timeout_s: '60'\n"))
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if cfg.EventService != "appmanager" || cfg.EventKind != "app_event" || cfg.TimeoutSec != 60 {
		t.Fatalf("explicit config = %+v", cfg)
	}

	// Clamps: below 1 becomes 1; above the 30-day cap becomes the cap.
	cfg, _ = requireEventWaitConfig(req("    event_kind: k\n    timeout_s: '-5'\n"))
	if cfg.TimeoutSec != 1 {
		t.Fatalf("negative timeout clamp = %d, want 1", cfg.TimeoutSec)
	}
	cfg, _ = requireEventWaitConfig(req("    event_kind: k\n    timeout_s: '99999999'\n"))
	if cfg.TimeoutSec != eventWaitMaxTimeoutSec {
		t.Fatalf("max timeout clamp = %d, want %d", cfg.TimeoutSec, eventWaitMaxTimeoutSec)
	}
}

func TestEventWaitExecutor_Preflight_Rejections(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	// Missing event_kind.
	_, err := e.Preflight(f.ctx, ClaimReq{
		CallerAgentID: f.callerAgentID,
		ProjectID:     f.projectID,
		CardRaw:       "---\nid: t\ntype: task\ndata:\n  exec:\n    kind: event_wait\n---\nBody.",
	})
	if err == nil || !strings.Contains(err.Error(), "event_kind is required") {
		t.Fatalf("missing event_kind: err = %v", err)
	}

	// No active workflow (no caller agent).
	noWorkflow := f.claimReq()
	noWorkflow.CallerAgentID = ""
	if _, err := e.Preflight(f.ctx, noWorkflow); err == nil {
		t.Fatal("missing caller agent must fail preflight")
	}

	// Happy path passes.
	pf, err := e.Preflight(f.ctx, f.claimReq())
	if err != nil {
		t.Fatalf("happy preflight: %v", err)
	}
	if pf.DisplayName != "EventWaitExecutor" {
		t.Fatalf("preflight display name = %q", pf.DisplayName)
	}
}

func TestEventWaitExecutor_Execute_ArmsWaitAndReturns(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	resp, err := e.Execute(f.ctx, req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if resp.AgentActorID != "" {
		t.Fatalf("event_wait spawns no agent, got AgentActorID %q", resp.AgentActorID)
	}
	if resp.DisplayName != "EventWaitExecutor" {
		t.Fatalf("display name = %q", resp.DisplayName)
	}
	if resp.Goal.BoundTaskCardID != f.boundTaskCardID {
		t.Fatalf("goal card = %q", resp.Goal.BoundTaskCardID)
	}

	// The wait is armed with one subscription; the card is still doing.
	waitFor(t, "subscription", func() bool { return f.subCount() == 1 })
	if got := f.lastSubRequest(); got.service != "toast" || got.kind != "toast.card_added" {
		t.Fatalf("subscribed (service,kind) = (%s,%s), want (toast,toast.card_added)", got.service, got.kind)
	}
	if f.waitCount() != 1 {
		t.Fatalf("wait count = %d, want 1", f.waitCount())
	}
	if f.status() != "doing" {
		t.Fatalf("card must stay doing while waiting, got %q", f.status())
	}
}

func TestEventWaitExecutor_EventCompletesCard(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitFor(t, "subscription", func() bool { return f.subCount() == 1 })

	f.pushEvent(0, map[string]any{"id": "toast-9", "title": "hello"})
	waitFor(t, "card done", func() bool { return f.status() == "done" })

	out := f.outputs()
	if out == nil {
		t.Fatal("task_outputs not written")
	}
	ev, _ := out["event"].(map[string]any)
	if ev == nil || ev["id"] != "toast-9" {
		t.Fatalf("outputs.event = %#v", out["event"])
	}
	if out["event_kind"] != "toast.card_added" || out["event_service"] != "toast" {
		t.Fatalf("outputs metadata = %#v", out)
	}

	// The flip was a doing→done CAS.
	f.mu.Lock()
	last := f.statusCalls[len(f.statusCalls)-1]
	f.mu.Unlock()
	if last.ExpectedStatus != "doing" || last.Status != "done" {
		t.Fatalf("flip call = %+v, want CAS doing→done", last)
	}

	// The wait is gone from the tracking map.
	waitFor(t, "wait removal", func() bool { return f.waitCount() == 0 })
}

func TestEventWaitExecutor_TimeoutFailsCard(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	raw := strings.Replace(f.cardRaw(), "    event_service: toast\n", "    event_service: toast\n    timeout_s: '1'\n", 1)
	req := f.claimReq()
	req.CardRaw = raw
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}

	waitFor(t, "timeout failure", func() bool { return f.status() == "failed" })
	out := f.outputs()
	if out == nil {
		t.Fatal("timeout outputs not written")
	}
	if note, _ := out["error"].(string); !strings.Contains(note, "timed out after 1s") || !strings.Contains(note, "toast.card_added") {
		t.Fatalf("timeout note = %#v", out["error"])
	}
	waitFor(t, "wait removal", func() bool { return f.waitCount() == 0 })
}

func TestEventWaitExecutor_CASMissDropsWait(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// The user cancelled the card while the wait was armed.
	f.mu.Lock()
	f.cardStatus[f.boundTaskCardID] = "cancelled"
	f.mu.Unlock()

	waitFor(t, "subscription", func() bool { return f.subCount() == 1 })
	f.pushEvent(0, map[string]any{"id": "late"})
	waitFor(t, "wait removal on CAS miss", func() bool { return f.waitCount() == 0 })

	// The card's terminal state is untouched by the executor.
	if f.status() != "cancelled" {
		t.Fatalf("card status = %q, want cancelled (CAS must not override)", f.status())
	}
}

func TestEventWaitExecutor_ReactivationReplacesWait(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	// Duplicate activation (orphan reclaim): replaces the wait and its stream.
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("second execute: %v", err)
	}
	waitFor(t, "second subscription", func() bool { return f.subCount() == 2 })
	if f.waitCount() != 1 {
		t.Fatalf("wait count after reactivation = %d, want 1", f.waitCount())
	}

	// An event on the ORPHANED first stream must not complete the card.
	f.pushEvent(0, map[string]any{"id": "stale"})
	time.Sleep(200 * time.Millisecond)
	if f.status() != "doing" {
		t.Fatalf("orphaned stream completed the card: status = %q", f.status())
	}

	// An event on the current stream completes it.
	f.pushEvent(1, map[string]any{"id": "fresh"})
	waitFor(t, "card done via current stream", func() bool { return f.status() == "done" })
	waitFor(t, "wait removal", func() bool { return f.waitCount() == 0 })
}

func TestEventWaitExecutor_StreamErrorResubscribesThenFails(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	// The stream fails twice, then a healthy stream delivers the event —
	// the wait must recover via resubscription and complete the card.
	var calls atomic.Int32
	f.mu.Lock()
	f.subFn = func(service, kind string) eventWaitStream {
		s := newFakeEventStream()
		f.streams = append(f.streams, s)
		n := calls.Add(1)
		if n <= 2 {
			s.errs <- errors.New("transient boom")
		}
		return s
	}
	f.mu.Unlock()

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}

	waitFor(t, "three subscriptions", func() bool { return calls.Load() == 3 })
	f.pushEvent(2, map[string]any{"id": "after-recovery"})
	waitFor(t, "card done after resubscribe", func() bool { return f.status() == "done" })
}

func TestEventWaitExecutor_StreamErrorExhaustedFailsCard(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	// Every stream fails: initial + 5 resubscribes, then the card fails.
	f.mu.Lock()
	f.subFn = func(service, kind string) eventWaitStream {
		s := newFakeEventStream()
		f.streams = append(f.streams, s)
		s.errs <- errors.New("event not found")
		return s
	}
	f.mu.Unlock()

	req := f.claimReq()
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}

	waitFor(t, "card failed after exhausted resubscribes", func() bool { return f.status() == "failed" })
	out := f.outputs()
	if note, _ := out["error"].(string); !strings.Contains(note, "event not found") {
		t.Fatalf("failure note = %#v", out["error"])
	}
	waitFor(t, "wait removal", func() bool { return f.waitCount() == 0 })
}

func TestEventWaitPersistence_RoundTripAndRearm(t *testing.T) {
	f := newEventWaitTestFixture(t)
	e := newEventWaitExecutor(f.a)

	raw := strings.Replace(f.cardRaw(), "    event_service: toast\n", "    event_service: toast\n    timeout_s: '3600'\n", 1)
	req := f.claimReq()
	req.CardRaw = raw
	req.Preflight = &PreflightResult{}
	if _, err := e.Execute(f.ctx, req); err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitFor(t, "subscription", func() bool { return f.subCount() == 1 })

	// Simulate a restart: a fresh actor Loads the persisted waits and
	// re-arms them in OnStart.
	b := &Actor{store: f.a.store, actorID: f.a.actorID, Mounts: f.a.Mounts}
	if err := b.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(b.eventWaits) != 1 {
		t.Fatalf("restored waits = %d, want 1", len(b.eventWaits))
	}
	var restored *eventWaitState
	for _, s := range b.eventWaits {
		restored = s
	}
	if restored.rec.EventKind != "toast.card_added" || restored.rec.EventService != "toast" {
		t.Fatalf("restored record = %+v", restored.rec)
	}
	if restored.deadline.IsZero() {
		t.Fatal("restored wait must keep its deadline")
	}
	if want := time.Until(restored.deadline); want <= 0 || want > time.Hour {
		t.Fatalf("restored deadline window = %v, want (0, 1h]", want)
	}

	// Re-arm against the restarted actor: a fresh subscription is armed and
	// an event completes the card.
	b.eventWaitSubscribeFn = f.subscribe
	b.rearmEventWaits(f.ctx)
	waitFor(t, "re-armed subscription", func() bool { return f.subCount() == 2 })
	f.pushEvent(1, map[string]any{"id": "post-restart"})
	waitFor(t, "card done after restart", func() bool { return f.status() == "done" })
	waitFor(t, "restarted wait removal", func() bool {
		b.eventWaitMu.RLock()
		defer b.eventWaitMu.RUnlock()
		return len(b.eventWaits) == 0
	})
}

func TestEventWaitRearm_ExpiredDeadlineFailsCard(t *testing.T) {
	f := newEventWaitTestFixture(t)

	// A persisted wait whose deadline is already in the past.
	past := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	f.a.eventWaits = map[string]*eventWaitState{
		eventWaitKey(f.projectID, f.boundTaskCardID): {
			rec: eventWaitRecord{
				ProjectID:    f.projectID,
				TaskCardID:   f.boundTaskCardID,
				EventKind:    "toast.card_added",
				EventService: "toast",
				TimeoutSec:   60,
				ActivatedAt:  past,
				Deadline:     past,
			},
			deadline: time.Now().UTC().Add(-time.Minute),
		},
	}
	f.a.eventWaitSubscribeFn = f.subscribe
	f.a.rearmEventWaits(f.ctx)

	waitFor(t, "expired deadline fails card", func() bool { return f.status() == "failed" })
	out := f.outputs()
	if note, _ := out["error"].(string); !strings.Contains(note, "timed out") {
		t.Fatalf("expired note = %#v", out["error"])
	}
	waitFor(t, "wait removal", func() bool { return f.waitCount() == 0 })
}
