package aimanager

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// invokeRecorder is a ref.Ref that records callIDs and the last
// aiaggregator.config_apply payload pushed through it.
type invokeRecorder struct {
	mu      sync.Mutex
	ids     []string
	lastCfg *domain.AIManagerAggregatorResolveResp
}

func (r *invokeRecorder) ID() id.ActorID           { return id.ActorID{} }
func (r *invokeRecorder) Service() (string, bool)  { return "", false }
func (r *invokeRecorder) Invoke(_ context.Context, callID string, payload any, _ ...map[string]string) *invoke.Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, callID)
	if cfg, ok := payload.(domain.AIManagerAggregatorResolveResp); ok {
		r.lastCfg = &cfg
	}
	return invoke.NewCall(invoke.CallModeUnary, invoke.NewErrorStream(io.EOF))
}

func (r *invokeRecorder) has(callID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.ids {
		if c == callID {
			return true
		}
	}
	return false
}

func (r *invokeRecorder) lastApply() *domain.AIManagerAggregatorResolveResp {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastCfg
}

// TestHandleAggregatorSetDisabled_NotifiesEmbeddingParents verifies that
// toggling a child aggregator's Disabled flag pushes a fresh config not only to
// the child itself but to every parent aggregator whose pool embeds a reference
// to it. Without the parent push, parents keep a stale Disabled=false snapshot
// for up to one 15s poll cycle and keep dispatching to the disabled child.
func TestHandleAggregatorSetDisabled_NotifiesEmbeddingParents(t *testing.T) {
	childRef := &invokeRecorder{}
	parentRef := &invokeRecorder{}
	unrelatedRef := &invokeRecorder{}

	a := &Actor{
		actorID: "test-aimanager",
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"parent": {ID: "parent", Name: "Parent", Units: []domain.ManualCallableUnit{
				{AggregatorID: "child"},
				{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://openai", Protocol: "openai"},
			}},
			"child": {ID: "child", Name: "Child", Units: []domain.ManualCallableUnit{
				{Model: "claude-opus-4-7", ProviderName: "anthropic", Endpoint: "https://anthropic", Protocol: "anthropic"},
			}},
			"unrelated": {ID: "unrelated", Name: "Unrelated", Units: []domain.ManualCallableUnit{
				{Model: "m", ProviderName: "p", Endpoint: "https://p", Protocol: "openai"},
			}},
		},
		aggRefs: map[string]ref.Ref{
			"parent":    parentRef,
			"child":     childRef,
			"unrelated": unrelatedRef,
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		configVersion:   1,
	}
	a.store = &memStore{data: make(map[string][]byte)}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleAggregatorSetDisabled(ctx, domain.AIManagerAggregatorSetDisabledReq{
		ID:       "child",
		Disabled: true,
	})
	if err != nil || !resp.Ok {
		t.Fatalf("handleAggregatorSetDisabled failed: err=%v resp=%+v", err, resp)
	}

	if !childRef.has("aiaggregator.config_apply") {
		t.Fatal("child aggregator must receive a config push")
	}
	if !parentRef.has("aiaggregator.config_apply") {
		t.Fatal("parent embedding the child must receive a config push")
	}
	if unrelatedRef.has("aiaggregator.config_apply") {
		t.Fatal("unrelated aggregator must not be notified")
	}

	// The parent's pushed config must project the child's Disabled flag onto
	// the aggregator-ref entry, so its next selection hard-skips the child.
	pushed := parentRef.lastApply()
	if pushed == nil || len(pushed.Units) < 1 {
		t.Fatalf("parent config_apply payload missing units: %+v", pushed)
	}
	if pushed.Units[0].AggregatorID != "child" || !pushed.Units[0].Disabled {
		t.Fatalf("aggregator-ref entry not projected disabled, got %+v", pushed.Units[0])
	}
}
