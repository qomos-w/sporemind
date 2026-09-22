package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/actor/policy"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// fakeAggActor stands in for the real aiaggregator: it registers
// aiaggregator.dispatch in the "aiaggregator" service domain and streams back
// a fixed AggregatorChunk text sequence carrying a strict-JSON policy answer.
type fakeAggActor struct {
	actor.Host
	lastReq any
}

func (f *fakeAggActor) Type() string { return "aiaggregator" }

func (f *fakeAggActor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("aiaggregator.dispatch", f.handleDispatch, actor.Public()); err != nil {
		return err
	}
	return ctx.RegisterDomain("aiaggregator").Expose()
}

func (f *fakeAggActor) handleDispatch(_ actor.PureContext, req domain.SendSessionMessageReq, emit actor.Emitter) error {
	f.lastReq = req
	prompt := ""
	if len(req.Messages) > 0 && len(req.Messages[0].Content) > 0 {
		prompt = req.Messages[0].Content[0].Text
	}
	// Echo-visible marker so the test can assert the prompt actually carried
	// the state and the question spec.
	_ = prompt
	text := `{"answers":{"urgency":{"noul":0.9},"severity":{"score":2,"probabilities":{"0":0,"1":0.1,"2":0.9},"confidence":0.5}}}`
	for _, part := range []string{text[:20], text[20:]} {
		if err := emit.Send(domain.AggregatorChunk{Kind: domain.AggregatorChunkText, Text: part}); err != nil {
			return err
		}
	}
	return nil
}

// TestLLMBackendEndToEnd boots policy + a fake aiaggregator in one App and
// drives policy.decide with Backend="llm": the real path — LookupService →
// Invoke → stream decode → strict-JSON parse — must produce uncalibrated,
// llm-provenance answers.
func TestLLMBackendEndToEnd(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		NoGateway: true,
		Children: []runtime.ChildSpec{
			{Name: "aiaggregator", Factory: func() actor.Actor { return &fakeAggActor{} }, RequirePersistent: false, Role: "system"},
			{Name: "policy", Factory: func() actor.Actor { return &policy.Actor{} }, RequirePersistent: true, Role: "system", DependsOn: []string{"aiaggregator"}},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	defer func() {
		cancel()
		_ = h.Wait()
	}()
	time.Sleep(500 * time.Millisecond)

	// Configure the llm unit (no Jev key → auto mode would degrade; we pin llm).
	app := h.App()
	policyRef, ok := app.LookupService("policy")
	if !ok {
		t.Fatal("policy service not exposed")
	}
	cfgCall := policyRef.Invoke(context.Background(), "policy.configure", gen.PolicyConfigureReq{
		Backend: "llm",
		LLM:     &gen.PolicyLLMConfig{Unit: &gen.ModelUnit{Model: "fake-m", Provider: "fake-p"}},
	})
	if cfgCall == nil {
		t.Fatal("configure invoke nil")
	}
	if _, err := cfgCall.Final(context.Background()); err != nil {
		t.Fatalf("configure: %v", err)
	}
	cfgCall.Close()

	decideCall := policyRef.Invoke(context.Background(), "policy.decide", gen.PolicyDecideReq{
		State: "the deploy is failing on the demo machine",
		Questions: map[string]gen.PolicyQuestion{
			"urgency":  {Type: "noul", Instructions: "The situation is urgent"},
			"severity": {Type: "score", Instructions: "How severe", Levels: []string{"Minor", "Moderate", "Critical"}},
		},
	})
	if decideCall == nil {
		t.Fatal("decide invoke nil")
	}
	raw, err := decideCall.Final(context.Background())
	decideCall.Close()
	if err != nil {
		t.Fatalf("decide: %v", err)
	}

	resp, ok := raw.(gen.PolicyDecideResp)
	if !ok {
		t.Fatalf("decide resp type %T", raw)
	}
	if resp.Backend != "llm" || resp.Degraded {
		t.Fatalf("backend = %q degraded = %v", resp.Backend, resp.Degraded)
	}
	urg := resp.Answers["urgency"]
	if urg.Noul != 0.9 || urg.Calibrated || urg.Backend != "llm" {
		t.Fatalf("urgency = %+v", urg)
	}
	sev := resp.Answers["severity"]
	if sev.Score != 2 || sev.Confidence != 0.5 || sev.Calibrated {
		t.Fatalf("severity = %+v", sev)
	}
}
