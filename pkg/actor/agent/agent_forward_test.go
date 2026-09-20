package agent

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestForwardToPluginsInvokesPluginhostDeliver pins the agent→pluginhost
// forwarding wire: kind and JSON payload must arrive at
// pluginhost.event_deliver exactly as the event bus sees them, and a missing
// pluginhost service (headless/tests) must be a silent no-op.
func TestForwardToPluginsInvokesPluginhostDeliver(t *testing.T) {
	actorID := testutil.GenActorID()
	a := &Actor{actorID: actorID.String()}

	type delivered struct {
		kind    string
		payload string
	}
	got := make(chan delivered, 4)
	phRef := testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
		if callID != "pluginhost.event_deliver" {
			t.Errorf("unexpected callID %q", callID)
			return nil
		}
		req, ok := payload.(gen.PluginEventDeliverReq)
		if !ok {
			t.Errorf("payload type %T, want gen.PluginEventDeliverReq", payload)
			return nil
		}
		got <- delivered{kind: req.Kind, payload: req.Payload}
		return gen.PluginEventDeliverResp{Delivered: 1}
	})

	ctx := testutil.HumanCtx(actorID)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "pluginhost" {
			return phRef, true
		}
		return nil, false
	}

	a.forwardToPlugins(ctx, "step", domain.StepEvent{Kind: "step.opened", StepID: "s1", TurnID: "t1"})
	a.forwardToPlugins(ctx, "agent_message_received", domain.AgentMessage{ID: "m1", FromAgentID: "a2"})

	for i, wantKind := range []string{"step", "agent_message_received"} {
		d := <-got
		if d.kind != wantKind {
			t.Errorf("delivery %d kind = %q, want %q", i, d.kind, wantKind)
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(d.payload), &probe); err != nil {
			t.Errorf("delivery %d payload is not JSON: %v", i, err)
			continue
		}
		if wantKind == "step" && probe["StepId"] != "s1" {
			t.Errorf("step payload StepId = %v, want s1", probe["StepId"])
		}
		if wantKind == "agent_message_received" && probe["Id"] != "m1" {
			t.Errorf("message payload Id = %v, want m1", probe["Id"])
		}
	}

	// Missing pluginhost: silent no-op, no panic.
	noHostCtx := testutil.HumanCtx(actorID)
	noHostCtx.LookupServiceFn = func(string) (ref.Ref, bool) { return nil, false }
	a.forwardToPlugins(noHostCtx, "step", domain.StepEvent{Kind: "step.opened"})
}
