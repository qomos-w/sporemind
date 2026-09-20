package aiaggregator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// capturingClient records the llmclient.Request it served and replays fixed
// events, so tests can lock both the recognition request shape (image block +
// prompt) and the answer text.
type capturingClient struct {
	mu      sync.Mutex
	reqs    []llmclient.Request
	events  []llmclient.Event
	streams int
}

func (c *capturingClient) Stream(_ context.Context, req llmclient.Request) (llmclient.Stream, error) {
	c.mu.Lock()
	c.reqs = append(c.reqs, req)
	c.streams++
	events := c.events
	c.mu.Unlock()
	ch := make(chan llmclient.Event, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &okStream{ch: ch}, nil
}

func (c *capturingClient) lastRequest() llmclient.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.reqs) == 0 {
		return llmclient.Request{}
	}
	return c.reqs[len(c.reqs)-1]
}

// recognizeRegistry registers:
//   - "reject-image": stream-open 400 with an image-unsupported marker
//   - "ok": a text answer via a shared capturingClient
func recognizeRegistry(capture *capturingClient) llmclient.Registry {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "reject-image",
		Factory: func(_, _ string) llmclient.Client {
			return &errorClient{streamErr: &llmclient.UpstreamError{
				StatusCode: 400,
				Message:    "this model does not support image input",
			}}
		},
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "ok",
		Factory: func(_, _ string) llmclient.Client { return capture },
	})
	return *reg
}

func newRecognizeActor(reg llmclient.Registry, units ...CallableUnit) *Actor {
	return &Actor{
		id:            "test-agg",
		configLoaded:  true,
		units:         units,
		strategy:      NewFallbackStrategy(),
		registry:      reg,
		aimanagerRef:  tokenFakeRef(),
		lifecycleCtx:  context.Background(),
		noImageInput:  map[string]bool{},
	}
}

func visionUnit(id, provider, protocol string) CallableUnit {
	return CallableUnit{ID: id, Model: id, ProviderName: provider, Protocol: protocol}
}

// ── selection contract ──

// TestSelectVisionUnit_FiltersNonVisionCandidates locks the vision-capable
// filter: image/video-modality units, disabled or disable-windowed units,
// nested aggregator refs, known text-only families (deepseek) and units
// marked no-image-input are all skipped.
func TestSelectVisionUnit_FiltersNonVisionCandidates(t *testing.T) {
	a := newRecognizeActor(llmclient.Registry{},
		CallableUnit{ID: "gen", Model: "gpt-image-2", ProviderName: "imgp", Modality: "image"},
		CallableUnit{ID: "vid", Model: "veo-3", ProviderName: "vp", Modality: "video"},
		CallableUnit{ID: "off", Model: "gpt-4o", ProviderName: "offp", Disabled: true},
		CallableUnit{ID: "win", Model: "gpt-4o", ProviderName: "winp", DisableUntil: time.Now().Add(time.Minute).Unix()},
		CallableUnit{ID: "nest", AggregatorID: "other-agg", Model: "gpt-4o"},
		CallableUnit{ID: "ds", Model: "deepseek-chat", ProviderName: "dsp"},
		CallableUnit{ID: "marked", Model: "gpt-4o", ProviderName: "markedp"},
		CallableUnit{ID: "good", Model: "qwen-vl-max", ProviderName: "qwen"},
	)
	a.noImageInput["markedp::gpt-4o"] = true

	u, err := a.selectVisionUnit(domain.AIAggregatorImageRecognizeReq{}, nil)
	if err != nil {
		t.Fatalf("selectVisionUnit: %v", err)
	}
	if u.ID != "good" {
		t.Fatalf("selected %q, want %q", u.ID, "good")
	}
}

// TestSelectVisionUnit_PinNarrows locks that an explicit Provider/Model pin
// restricts selection to the matching unit.
func TestSelectVisionUnit_PinNarrows(t *testing.T) {
	a := newRecognizeActor(llmclient.Registry{},
		visionUnit("first", "openai", "ok"),
		visionUnit("pinned", "moonshot", "ok"),
	)

	u, err := a.selectVisionUnit(domain.AIAggregatorImageRecognizeReq{Provider: "moonshot", Model: "pinned"}, nil)
	if err != nil {
		t.Fatalf("selectVisionUnit: %v", err)
	}
	if u.ID != "pinned" {
		t.Fatalf("selected %q, want pinned", u.ID)
	}
}

// TestSelectVisionUnit_NoVisionUnits locks the actionable error when the pool
// holds no vision-capable unit at all.
func TestSelectVisionUnit_NoVisionUnits(t *testing.T) {
	a := newRecognizeActor(llmclient.Registry{},
		CallableUnit{ID: "ds", Model: "deepseek-chat", ProviderName: "dsp"},
	)

	_, err := a.selectVisionUnit(domain.AIAggregatorImageRecognizeReq{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no vision-capable units") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ── recognition flow ──

// TestHandleImageRecognize_RotatesOnImageUnsupported locks the failover
// semantics: a vision-heuristic unit whose model rejects image input is marked
// no-image-input and the recognition rotates to the next vision-capable unit —
// never retried with the image stripped.
func TestHandleImageRecognize_RotatesOnImageUnsupported(t *testing.T) {
	restoreGate := swapGate(t, "faileyp", "okp")
	defer restoreGate()

	capture := &capturingClient{events: []llmclient.Event{
		{Kind: llmclient.EventTextDelta, Text: "a red cube on a table"},
		{Kind: llmclient.EventStop, StopReason: llmclient.StopReasonStop},
	}}
	a := newRecognizeActor(recognizeRegistry(capture),
		visionUnit("reject-me", "faileyp", "reject-image"),
		visionUnit("answerer", "okp", "ok"),
	)

	resp, err := a.handleImageRecognize(&testPureCtx{done: make(chan struct{})}, domain.AIAggregatorImageRecognizeReq{
		Image: "data:image/png;base64,AAAA",
	})
	if err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	if resp.Text != "a red cube on a table" {
		t.Fatalf("text = %q", resp.Text)
	}
	if resp.Model != "answerer" || resp.ProviderName != "okp" {
		t.Fatalf("unit = %s/%s, want answerer/okp", resp.ProviderName, resp.Model)
	}
	if !a.unitLacksImageInput(CallableUnit{ProviderName: "faileyp", Model: "reject-me"}) {
		t.Fatal("rejecting unit should be marked no-image-input")
	}
}

// TestHandleImageRecognize_RequestShape locks the wire shape: one user message
// with the image block first, the default description prompt as text, and no
// reasoning effort.
func TestHandleImageRecognize_RequestShape(t *testing.T) {
	restoreGate := swapGate(t, "okp")
	defer restoreGate()

	capture := &capturingClient{events: []llmclient.Event{
		{Kind: llmclient.EventTextDelta, Text: "desc"},
		{Kind: llmclient.EventStop},
	}}
	a := newRecognizeActor(recognizeRegistry(capture),
		visionUnit("answerer", "okp", "ok"),
	)

	_, err := a.handleImageRecognize(&testPureCtx{done: make(chan struct{})}, domain.AIAggregatorImageRecognizeReq{
		Image: "data:image/png;base64,AAAA",
	})
	if err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}

	req := capture.lastRequest()
	if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
		t.Fatalf("messages = %+v, want one user message", req.Messages)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("content blocks = %d, want 2 (image + prompt)", len(blocks))
	}
	if blocks[0].Type != llmclient.BlockTypeImage || blocks[0].ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("block[0] = %+v, want image block with data URL", blocks[0])
	}
	if blocks[1].Type != llmclient.BlockTypeText || !strings.Contains(blocks[1].Text, "Describe this image") {
		t.Fatalf("block[1] = %+v, want default description prompt", blocks[1])
	}
}

// TestHandleImageRecognize_ExplicitPromptOverridesDefault locks that a caller
// question replaces the default describe prompt.
func TestHandleImageRecognize_ExplicitPromptOverridesDefault(t *testing.T) {
	restoreGate := swapGate(t, "okp")
	defer restoreGate()

	capture := &capturingClient{events: []llmclient.Event{
		{Kind: llmclient.EventTextDelta, Text: "42"},
		{Kind: llmclient.EventStop},
	}}
	a := newRecognizeActor(recognizeRegistry(capture),
		visionUnit("answerer", "okp", "ok"),
	)

	_, err := a.handleImageRecognize(&testPureCtx{done: make(chan struct{})}, domain.AIAggregatorImageRecognizeReq{
		Image:  "data:image/png;base64,AAAA",
		Prompt: "How many cubes are in the picture?",
	})
	if err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}

	req := capture.lastRequest()
	blocks := req.Messages[0].Content
	if blocks[1].Text != "How many cubes are in the picture?" {
		t.Fatalf("prompt = %q", blocks[1].Text)
	}
}
