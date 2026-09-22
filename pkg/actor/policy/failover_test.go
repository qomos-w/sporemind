package policy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// newTestActor builds an Actor with fake backends for chain-level tests. The
// injected backends never touch the PureContext, so nil is acceptable there.
func newTestActor(mode string) *Actor {
	a := &Actor{lifecycleCtx: context.Background(), actorID: "policy-test"}
	a.state.Backend = mode
	a.jevBackend = func(_ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, nil // replaced per test
	}
	a.llmBackend = func(_ actor.PureContext, _ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, nil // replaced per test
	}
	return a
}

func okAnswers(backend string) map[string]gen.PolicyAnswer {
	return map[string]gen.PolicyAnswer{
		"urgency": {Type: "noul", Noul: 0.9, Backend: backend, Calibrated: backend == backendJev},
	}
}

// TestHandleDecideFailoverJevToLLM: jev availability fault (429) degrades to
// the llm backend with Degraded=true and provenance on every answer.
func TestHandleDecideFailoverJevToLLM(t *testing.T) {
	a := newTestActor(backendAuto)
	a.jevBackend = func(_ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, unavailable("jev: http 429")
	}
	a.llmBackend = func(_ actor.PureContext, _ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return okAnswers(backendLLM), nil
	}

	// narrow the request to one noul question (backends are fakes)
	req := gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{
		"urgency": {Type: "noul", Instructions: "urgent?"},
	}}
	resp, err := a.handleDecide(nil, req)
	if err != nil {
		t.Fatalf("handleDecide: %v", err)
	}
	if resp.Backend != backendLLM || !resp.Degraded {
		t.Errorf("resp backend = %q degraded = %v", resp.Backend, resp.Degraded)
	}
	if resp.Answers["urgency"].Backend != backendLLM || resp.Answers["urgency"].Calibrated {
		t.Errorf("answer provenance wrong: %+v", resp.Answers["urgency"])
	}
}

// TestHandleDecideFailOpenOnAllBackendsDown: every backend unavailable →
// empty answers + Error; no panic, no partial answers.
func TestHandleDecideFailOpenOnAllBackendsDown(t *testing.T) {
	a := newTestActor(backendAuto)
	a.jevBackend = func(_ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, unavailable("jev: no key")
	}
	a.llmBackend = func(_ actor.PureContext, _ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, unavailable("llm: no unit")
	}
	resp, err := a.handleDecide(nil, gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{
		"urgency": {Type: "noul", Instructions: "urgent?"},
	}})
	if err != nil {
		t.Fatalf("handleDecide must fail open, got handler error: %v", err)
	}
	if len(resp.Answers) != 0 || resp.Error == "" {
		t.Errorf("expected empty answers + Error, got %+v", resp)
	}
	if !strings.Contains(resp.Error, "jev") || !strings.Contains(resp.Error, "llm") {
		t.Errorf("error should carry last backend reason: %q", resp.Error)
	}
}

// TestHandleDecideContractFaultAbortsChain: a non-availability error from the
// primary backend must NOT be masked by the fallback.
func TestHandleDecideContractFaultAbortsChain(t *testing.T) {
	a := newTestActor(backendAuto)
	a.jevBackend = func(_ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return nil, fmt.Errorf("jev: http 400: bad request")
	}
	llmCalled := false
	a.llmBackend = func(_ actor.PureContext, _ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		llmCalled = true
		return okAnswers(backendLLM), nil
	}
	resp, err := a.handleDecide(nil, gen.PolicyDecideReq{State: "s", Questions: map[string]gen.PolicyQuestion{
		"urgency": {Type: "noul", Instructions: "urgent?"},
	}})
	if err != nil {
		t.Fatalf("handleDecide: %v", err)
	}
	if llmCalled {
		t.Error("contract fault must not failover to llm")
	}
	if len(resp.Answers) != 0 || !strings.Contains(resp.Error, "400") {
		t.Errorf("expected loud contract error, got %+v", resp)
	}
}

// TestHandleDecidePinnedBackend: per-call override skips the primary even in
// auto mode, and Degraded stays false (explicit choice, not a fault).
func TestHandleDecidePinnedBackend(t *testing.T) {
	a := newTestActor(backendAuto)
	jevCalled := false
	a.jevBackend = func(_ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		jevCalled = true
		return okAnswers(backendJev), nil
	}
	a.llmBackend = func(_ actor.PureContext, _ context.Context, _ PolicySnapshot, _ gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return okAnswers(backendLLM), nil
	}
	resp, err := a.handleDecide(nil, gen.PolicyDecideReq{
		State:     "s",
		Backend:   backendLLM,
		Questions: map[string]gen.PolicyQuestion{"urgency": {Type: "noul", Instructions: "urgent?"}},
	})
	if err != nil {
		t.Fatalf("handleDecide: %v", err)
	}
	if jevCalled {
		t.Error("pinned llm must skip jev")
	}
	if resp.Backend != backendLLM || resp.Degraded {
		t.Errorf("pinned call must not be marked degraded: %+v", resp)
	}
}

// TestConfigureRedactedEditFlow: empty ApiKey preserves the stored secret;
// status never echoes the key.
func TestConfigureRedactedEditFlow(t *testing.T) {
	a := newTestActor(backendAuto)
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	store, err := persist.New(config.PersistConfig("policy"))
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	a.store = store

	resp, err := a.handleConfigure(nil, gen.PolicyConfigureReq{
		Backend: backendAuto,
		Jev:     &gen.PolicyJevConfig{APIKey: "secret-1", Model: "jev-latest"},
		LLM:     &gen.PolicyLLMConfig{Unit: &gen.ModelUnit{Model: "m1", Provider: "p1"}},
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	if !resp.Status.JevConfigured || !resp.Status.LLMConfigured {
		t.Errorf("status = %+v", resp.Status)
	}

	// Redacted edit: no key field → stored key survives.
	if _, err := a.handleConfigure(nil, gen.PolicyConfigureReq{
		Jev: &gen.PolicyJevConfig{Model: "jev-1.13.0"},
	}); err != nil {
		t.Fatalf("configure edit: %v", err)
	}
	st, err := a.handleStatus(nil, gen.PolicyStatusReq{})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.JevModel != "jev-1.13.0" {
		t.Errorf("model update lost: %+v", st)
	}
	a.mu.RLock()
	key := a.state.Jev.APIKey
	a.mu.RUnlock()
	if key != "secret-1" {
		t.Errorf("stored key must survive redacted edit, got %q", key)
	}

	// Unknown backend mode is rejected loudly.
	if _, err := a.handleConfigure(nil, gen.PolicyConfigureReq{Backend: "grok"}); err == nil {
		t.Error("unknown backend must error")
	}
}

// TestConfigureClearUnit pins the frontend contract: PolicyConfigureReq.LLM
// present with no Unit clears the fallback unit (the settings panel sends
// LLM:{} for "remove unit"); LLM omitted entirely preserves it.
func TestConfigureClearUnit(t *testing.T) {
	a := newTestActor(backendAuto)
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	store, err := persist.New(config.PersistConfig("policy"))
	if err != nil {
		t.Fatalf("persist.New: %v", err)
	}
	a.store = store

	if _, err := a.handleConfigure(nil, gen.PolicyConfigureReq{
		LLM: &gen.PolicyLLMConfig{Unit: &gen.ModelUnit{Model: "m", Provider: "p"}},
	}); err != nil {
		t.Fatalf("set unit: %v", err)
	}
	if st, _ := a.handleStatus(nil, gen.PolicyStatusReq{}); !st.LLMConfigured {
		t.Fatal("unit should be configured")
	}

	// LLM:{} → clear
	if _, err := a.handleConfigure(nil, gen.PolicyConfigureReq{
		LLM: &gen.PolicyLLMConfig{},
	}); err != nil {
		t.Fatalf("clear unit: %v", err)
	}
	if st, _ := a.handleStatus(nil, gen.PolicyStatusReq{}); st.LLMConfigured {
		t.Fatal("empty LLM object must clear the unit")
	}
}
