package agent

import (
	"context"
	"strconv"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// coordinatorGlassCtx builds a Coordinator actor with a FakeCtx whose
// glassinteract service lookup resolves to a fake ref and whose planner records
// every glass_interact.event.complete call.
func coordinatorGlassCtx(t *testing.T, onComplete func(req gen.GlassEventCompleteReq)) (*testutil.FakeCtx, *Actor) {
	t.Helper()
	glassRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID == glassEventCompleteCallable {
			req, _ := payload.(gen.GlassEventCompleteReq)
			if onComplete != nil {
				onComplete(req)
			}
			return gen.GlassEventCompleteResp{EventID: req.EventID, State: "completed"}
		}
		return nil
	})
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "glass_interact" {
			return glassRef, true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID == glassEventCompleteCallable {
					req, _ := payload.(gen.GlassEventCompleteReq)
					if onComplete != nil {
						onComplete(req)
					}
					return gen.GlassEventCompleteResp{EventID: req.EventID, State: "completed"}, nil
				}
				return nil, nil
			},
		}
	}
	a := &Actor{
		agentKind:   string(domain.AgentKindCoordinator),
		DisplayName: "Coordinator",
		Session:     domain.Session{},
		RawSession:  domain.RawSession{},
	}
	return ctx, a
}

// TestHandleCoordinatorGlassEvent_Validation verifies the Coordinator intake
// rejects malformed deliveries with an explicit error (the glassinteract side
// treats that as a failed delivery and requeues with a bounded retry).
func TestHandleCoordinatorGlassEvent_Validation(t *testing.T) {
	ctx, a := coordinatorGlassCtx(t, nil)
	for _, tc := range []struct {
		name string
		req  gen.GlassEventDeliverReq
	}{
		{"empty event id", gen.GlassEventDeliverReq{Source: "turn", Kind: "complete"}},
		{"empty source", gen.GlassEventDeliverReq{EventID: "ge_1", Kind: "complete"}},
		{"empty kind", gen.GlassEventDeliverReq{EventID: "ge_1", Source: "turn"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.handleCoordinatorGlassEvent(ctx, tc.req); err == nil {
				t.Fatalf("handleCoordinatorGlassEvent(%+v) = nil error, want validation failure", tc.req)
			}
		})
	}
	if len(a.coordinatorGlassEventsSnapshot()) != 0 {
		t.Fatalf("invalid deliveries must not be recorded: %+v", a.coordinatorGlassEventsSnapshot())
	}
}

// TestHandleCoordinatorGlassEvent_AckRecordsAndCompletes verifies the real
// Coordinator intake: a valid delivery is acknowledged, recorded in the narrow
// Coordinator-only runtime record, and completed back through
// glass_interact.event.complete with the handled outcome.
func TestHandleCoordinatorGlassEvent_AckRecordsAndCompletes(t *testing.T) {
	t.Cleanup(func() { config.ResetForTest() })
	config.SetDataDirForTest(t.TempDir())
	var completes []gen.GlassEventCompleteReq
	ctx, a := coordinatorGlassCtx(t, func(req gen.GlassEventCompleteReq) {
		completes = append(completes, req)
	})

	resp, err := a.handleCoordinatorGlassEvent(ctx, gen.GlassEventDeliverReq{
		EventID:    "ge_1",
		Source:     "turn",
		Kind:       "complete",
		Priority:   "normal",
		Payload:    `{"turn":"t1"}`,
		Deliveries: 1,
	})
	if err != nil {
		t.Fatalf("handleCoordinatorGlassEvent: %v", err)
	}
	if !resp.Received || resp.EventID != "ge_1" {
		t.Fatalf("ack resp = %+v, want Received=true for ge_1", resp)
	}
	records := a.coordinatorGlassEventsSnapshot()
	if len(records) != 1 || records[0].EventID != "ge_1" || records[0].Payload != `{"turn":"t1"}` {
		t.Fatalf("coordinator records = %+v, want one ge_1 record", records)
	}
	if len(completes) != 1 {
		t.Fatalf("complete calls = %d, want 1", len(completes))
	}
	if completes[0].EventID != "ge_1" || completes[0].Outcome != coordinatorGlassEventOutcomeHandled {
		t.Fatalf("complete call = %+v, want ge_1 handled", completes[0])
	}
}

// TestHandleCoordinatorGlassEvent_RecordBounded verifies the runtime-only
// Coordinator record stays bounded.
func TestHandleCoordinatorGlassEvent_RecordBounded(t *testing.T) {
	ctx, a := coordinatorGlassCtx(t, nil)
	for i := 0; i < coordinatorGlassEventsMax+10; i++ {
		if _, err := a.handleCoordinatorGlassEvent(ctx, gen.GlassEventDeliverReq{
			EventID:  "ge_" + strconv.Itoa(i),
			Source:   "turn",
			Kind:     "complete",
			Priority: "normal",
			Payload:  "p",
		}); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
	records := a.coordinatorGlassEventsSnapshot()
	if len(records) != coordinatorGlassEventsMax {
		t.Fatalf("record len = %d, want bounded %d", len(records), coordinatorGlassEventsMax)
	}
	if records[0].EventID != "ge_10" {
		t.Fatalf("oldest retained record = %s, want ge_10 (tail kept)", records[0].EventID)
	}
}
