package workspace

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleAgentReadMessage_ForwardsAndReturnsItems(t *testing.T) {
	a, ctx := freshActor(t)
	agentID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{ID: "Coder#0001", ActorID: agentID.String(), LoadState: "loaded"}}

	wantResp := domain.AgentMessageReadResp{
		Items: []domain.AgentMessageReadItem{
			{Seq: 9, Role: "assistant", Content: "done"},
			{Seq: 8, Role: "user", Content: "continue"},
		},
		HasMore: true,
	}
	var forwarded domain.AgentMessageReadReq
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(agentID, func(callID string, payload any) any {
			if callID == "message_read" {
				forwarded = payload.(domain.AgentMessageReadReq)
				return wantResp
			}
			return nil
		}), true
	}

	got, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{
		ToAgentID:  "Coder#0001",
		Limit:      5,
		BeforeSeq:  10,
	})
	if err != nil {
		t.Fatalf("handleAgentReadMessage: %v", err)
	}
	if forwarded.Limit != 5 || forwarded.BeforeSeq != 10 {
		t.Errorf("forwarded req = %+v, want Limit=5 BeforeSeq=10", forwarded)
	}
	if len(got.Items) != 2 || got.Items[0].Seq != 9 || got.Items[1].Seq != 8 || !got.HasMore {
		t.Fatalf("resp = %+v, want passthrough of wantResp", got)
	}
}

func TestHandleAgentReadMessage_DecodesMapPayload(t *testing.T) {
	a, ctx := freshActor(t)
	agentID := testutil.GenActorID()
	a.Agents = []domain.AgentRef{{ID: "Coder#0001", ActorID: agentID.String(), LoadState: "loaded"}}

	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(agentID, func(callID string, payload any) any {
			if callID == "message_read" {
				// HasMore omitted -> decodes to false.
				return map[string]any{
					"Items": []any{map[string]any{"Seq": 3, "Role": "user", "Content": "hi"}},
				}
			}
			return nil
		}), true
	}

	got, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Coder#0001"})
	if err != nil {
		t.Fatalf("handleAgentReadMessage: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Seq != 3 || got.Items[0].Content != "hi" || got.HasMore {
		t.Fatalf("resp = %+v, want decoded item without HasMore", got)
	}
}

func TestHandleAgentReadMessage_TargetValidation(t *testing.T) {
	a, ctx := freshActor(t)

	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{}); err == nil {
		t.Fatal("expected error for empty ToAgentId")
	}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Ghost#0001"}); err == nil {
		t.Fatal("expected error for unknown target")
	}

	a.Agents = []domain.AgentRef{{ID: "Coder#0001", ActorID: "not-canonical", LoadState: "loaded"}}
	if _, err := a.handleAgentReadMessage(ctx, domain.AgentMessageReadReq{ToAgentID: "Coder#0001"}); err == nil {
		t.Fatal("expected error for non-canonical actor id")
	}
}
