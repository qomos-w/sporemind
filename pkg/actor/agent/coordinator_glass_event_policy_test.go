package agent

import (
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestCoordinatorGlassEventPolicy(t *testing.T) {
	cases := []struct {
		name        string
		req         gen.GlassEventDeliverReq
		wantOutcome string
		wantText    string
	}{
		{"non-agent preserved", gen.GlassEventDeliverReq{Source: "build", Kind: "completed"}, "handled", ""},
		{"started ignored", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.started"}, "ignored", ""},
		{"completed ignored", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.completed"}, "ignored", ""},
		{"failed notifies", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.failed", Payload: `{"title":"Indexer"}`}, "handled", "Indexer 执行失败"},
		{"blocked notifies", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.blocked", Payload: `{"title":"Coder"}`}, "handled", "Coder 等待你的处理"},
		{"invalid payload retries", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.failed", Payload: "{"}, "failed", ""},
		{"unknown ignored", gen.GlassEventDeliverReq{Source: "agent", Kind: "agent.other"}, "ignored", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outcome, text := coordinatorGlassEventPolicy(tc.req)
			if outcome != tc.wantOutcome || text != tc.wantText {
				t.Fatalf("policy = (%q, %q), want (%q, %q)", outcome, text, tc.wantOutcome, tc.wantText)
			}
		})
	}
}
