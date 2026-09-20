package aiaggregator

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// cachedImageRecognitionCount exposes the cache size for assertions.
func (a *Actor) cachedImageRecognitionCount() int {
	a.imageRecogMu.Lock()
	defer a.imageRecogMu.Unlock()
	return len(a.imageRecogCache)
}

// visionClient serves a one-shot recognition answer and records wire requests.
type visionClient struct {
	mu    sync.Mutex
	calls []llmclient.Request
}

func (c *visionClient) Stream(_ context.Context, req llmclient.Request) (llmclient.Stream, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req)
	c.mu.Unlock()
	ch := make(chan llmclient.Event, 2)
	ch <- llmclient.Event{Kind: llmclient.EventTextDelta, Text: "a red circle on a white background"}
	ch <- llmclient.Event{Kind: llmclient.EventStop}
	close(ch)
	return &okStream{ch: ch}, nil
}

func (c *visionClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *visionClient) requests() []llmclient.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]llmclient.Request, len(c.calls))
	copy(out, c.calls)
	return out
}

// newSubstitutionDispatchActor builds an actor whose pool has a text-only
// primary ("imgreject" protocol, rejects the first image request) and a vision
// unit ("vision" protocol).
func newSubstitutionDispatchActor(primary, vision llmclient.Client) (*Actor, CallableUnit) {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "imgreject",
		Factory:  func(_, _ string) llmclient.Client { return primary },
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "vision",
		Factory:  func(_, _ string) llmclient.Client { return vision },
	})
	unit := CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "imgreject"}
	vis := CallableUnit{ID: "v::gpt-4o", Model: "gpt-4o", ProviderName: "v", Protocol: "vision"}
	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{unit, vis},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	return a, unit
}

func substitutionDispatchFixture() (*testPureCtx, *collectingEmitter) {
	return &testPureCtx{done: make(chan struct{})}, &collectingEmitter{done: make(chan struct{})}
}

func requestBlocksContain(req llmclient.Request, prefix string) bool {
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == llmclient.BlockTypeText && strings.HasPrefix(b.Text, prefix) {
				return true
			}
		}
	}
	return false
}

func requestImageBlockCount(req llmclient.Request) int {
	n := 0
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == llmclient.BlockTypeImage {
				n++
			}
		}
	}
	return n
}

// TestDispatch_ImageUnsupported_SubstitutesWithRecognizedText locks the
// interception contract: a 400 image-unsupported on the primary marks the unit,
// recognizes the image via a vision unit, and retries the SAME primary with the
// image block replaced by recognition text. The session history is untouched.
func TestDispatch_ImageUnsupported_SubstitutesWithRecognizedText(t *testing.T) {
	restoreGate := swapGate(t, "a", "v")
	defer restoreGate()

	primary := &imageRejectClient{}
	vision := &visionClient{}
	a, unit := newSubstitutionDispatchActor(primary, vision)
	ctx, emit := substitutionDispatchFixture()

	req := imageReqFixture()
	if err := a.handleDispatch(ctx, req, emit); err != nil {
		t.Fatalf("dispatch should succeed via substituted retry, got: %v", err)
	}

	if !a.unitLacksImageInput(unit) {
		t.Error("primary unit should be marked no-image-input")
	}
	if got := vision.callCount(); got != 1 {
		t.Fatalf("vision recognition calls = %d, want 1", got)
	}
	if got := primary.callCount(); got != 2 {
		t.Fatalf("primary calls = %d, want 2 (image attempt + substituted retry)", got)
	}
	counts := primary.requestImageCounts()
	if len(counts) != 2 || counts[0] != 1 || counts[1] != 0 {
		t.Fatalf("primary per-request image counts = %v, want [1 0]", counts)
	}
	calls := primary.requests()
	if requestImageBlockCount(calls[1]) != 0 {
		t.Error("substituted retry still carries image blocks")
	}
	if !requestBlocksContain(calls[1], imageRecognizedPrefix) {
		t.Error("substituted retry misses recognized text block")
	}
	if requestBlocksContain(calls[1], imageOmittedPlaceholder) {
		t.Error("substituted retry degraded to placeholder despite a healthy vision unit")
	}
	if !strings.Contains(calls[1].Messages[1].Content[1].Text, "a red circle") {
		t.Errorf("recognized text not carried: %q", calls[1].Messages[1].Content[1].Text)
	}
	if !reflect.DeepEqual(req, imageReqFixture()) {
		t.Error("substitution mutated the caller's session history")
	}
	if emit.resolvedUnit() == nil || emit.resolvedUnit().Model != "m" {
		t.Errorf("resolved unit should stay the primary, got %+v", emit.resolvedUnit())
	}
}

// TestDispatch_ImageUnsupported_ProactivePathRecognizesNewImage: a marked
// unit dispatching an UNCACHED image (a fresh screenshot) is recognized live
// before the wire request is built — no 400, no omitted-placeholder. This is
// the 2026-09-12 live-trace regression: only the first image was ever
// recognized and every later screenshot silently degraded.
func TestDispatch_ImageUnsupported_ProactivePathRecognizesNewImage(t *testing.T) {
	restoreGate := swapGate(t, "a", "v")
	defer restoreGate()

	primary := &imageRejectClient{}
	vision := &visionClient{}
	a, _ := newSubstitutionDispatchActor(primary, vision)
	ctx, emit := substitutionDispatchFixture()

	// Bootstrap: first dispatch marks the unit and caches image a.png.
	if err := a.handleDispatch(ctx, imageReqFixture(), emit); err != nil {
		t.Fatalf("first dispatch failed: %v", err)
	}

	// Fresh screenshot: same shape, different image URL — not in the cache.
	fresh := imageReqFixture()
	fresh.Messages[1].Content[1].ImageURL = "https://example.com/b.png"
	if err := a.handleDispatch(ctx, fresh, emit); err != nil {
		t.Fatalf("dispatch with new image failed: %v", err)
	}

	if got := vision.callCount(); got != 2 {
		t.Fatalf("vision recognition calls = %d, want 2 (a.png + live b.png)", got)
	}
	counts := primary.requestImageCounts()
	// First dispatch: [1 0] (400 attempt + substituted retry). Second dispatch
	// must be [0] — the marked unit's first attempt already carries text.
	if len(counts) != 3 || counts[2] != 0 {
		t.Fatalf("primary per-request image counts = %v, want [1 0 0] (proactive substitution)", counts)
	}
	calls := primary.requests()
	if requestBlocksContain(calls[2], imageRecognizedPrefix) {
		if requestBlocksContain(calls[2], imageOmittedPlaceholder) {
			t.Error("new image degraded to placeholder despite live recognition")
		}
	} else {
		t.Error("proactively substituted request misses recognized text block")
	}
}

// TestDispatch_ImageUnsupported_ProactivePathReusesCache: the next dispatch to
// the marked unit substitutes from cache — no new vision call, no 400, and the
// recognition text (not a placeholder) still reaches the primary.
func TestDispatch_ImageUnsupported_ProactivePathReusesCache(t *testing.T) {
	restoreGate := swapGate(t, "a", "v")
	defer restoreGate()

	primary := &imageRejectClient{}
	vision := &visionClient{}
	a, unit := newSubstitutionDispatchActor(primary, vision)
	ctx, emit := substitutionDispatchFixture()

	if err := a.handleDispatch(ctx, imageReqFixture(), emit); err != nil {
		t.Fatalf("first dispatch failed: %v", err)
	}
	if err := a.handleDispatch(ctx, imageReqFixture(), emit); err != nil {
		t.Fatalf("second dispatch failed: %v", err)
	}

	if got := vision.callCount(); got != 1 {
		t.Fatalf("vision recognition calls after two dispatches = %d, want 1 (cache)", got)
	}
	if got := primary.callCount(); got != 3 {
		t.Fatalf("primary calls = %d, want 3 (400 + retry + cached pre-substitute)", got)
	}
	counts := primary.requestImageCounts()
	if len(counts) != 3 || counts[2] != 0 {
		t.Fatalf("third primary request should carry no image blocks, counts = %v", counts)
	}
	last := primary.requests()[2]
	if !requestBlocksContain(last, imageRecognizedPrefix) {
		t.Error("cached recognition text missing from the proactive path")
	}
	if requestBlocksContain(last, imageOmittedPlaceholder) {
		t.Error("proactive path degraded a cached image to placeholder")
	}
	if !a.unitLacksImageInput(unit) {
		t.Error("marker lost between dispatches")
	}
}

// TestDispatch_Other400NotIntercepted locks the user constraint: a 400 whose
// message carries no image-unsupported marker is NOT intercepted — one call,
// no rewrite retry, no marking, error surfaces with its status.
func TestDispatch_Other400NotIntercepted(t *testing.T) {
	restoreGate := swapGate(t, "a")
	defer restoreGate()

	other := &errorClient{streamErr: &llmclient.UpstreamError{
		StatusCode: 400,
		Type:       "invalid_request_error",
		Message:    "invalid model id",
	}}
	a, unit := newImageStripDispatchActor(other)
	ctx, emit := substitutionDispatchFixture()

	err := a.handleDispatch(ctx, imageReqFixture(), emit)
	if err == nil {
		t.Fatal("non-image 400 must surface as an error")
	}
	if status := upstreamStatus(t, err); status != 400 {
		t.Fatalf("surfaced status = %d, want 400", status)
	}
	if a.unitLacksImageInput(unit) {
		t.Error("unrelated 400 must not mark the unit text-only")
	}
	if a.cachedImageRecognitionCount() != 0 {
		t.Error("unrelated 400 must not populate the recognition cache")
	}
}

// TestDispatch_KnownTextModel_ProactivelyRecognizesWithout400 locks the
// 2026-09-14 regression: an aggregator-backed primary slot resolves to a
// family-known text-only model (deepseek), which never emits an
// image-unsupported 400 — llmclient's family sanitizer would otherwise replace
// the screenshot with a bare "[image]" marker and the model answers "I can't
// see the image content directly in text". The unit must recognize the image
// through the vision pipeline BEFORE the wire request is built, with no doomed
// image round-trip and without setting the learned marker.
func TestDispatch_KnownTextModel_ProactivelyRecognizesWithout400(t *testing.T) {
	restoreGate := swapGate(t, "a", "v")
	defer restoreGate()

	primary := &visionClient{}
	vision := &visionClient{}
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "knownText",
		Factory:  func(_, _ string) llmclient.Client { return primary },
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "vision",
		Factory:  func(_, _ string) llmclient.Client { return vision },
	})
	unit := CallableUnit{ID: "a::deepseek-chat", Model: "deepseek-chat", ProviderName: "a", Protocol: "knownText"}
	vis := CallableUnit{ID: "v::gpt-4o", Model: "gpt-4o", ProviderName: "v", Protocol: "vision"}
	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{unit, vis},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	ctx, emit := substitutionDispatchFixture()

	if err := a.handleDispatch(ctx, imageReqFixture(), emit); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	if got := vision.callCount(); got != 1 {
		t.Fatalf("vision recognition calls = %d, want 1 (family-known text model must recognize proactively)", got)
	}
	if got := primary.callCount(); got != 1 {
		t.Fatalf("primary calls = %d, want 1 (no image round-trip)", got)
	}
	req := primary.requests()[0]
	if requestImageBlockCount(req) != 0 {
		t.Error("known text-only model received image blocks")
	}
	if !requestBlocksContain(req, imageRecognizedPrefix) {
		t.Error("recognized text block missing from the wire request")
	}
	if requestBlocksContain(req, imageOmittedPlaceholder) {
		t.Error("image degraded to placeholder despite a healthy vision unit")
	}
	if a.unitLacksImageInput(unit) {
		t.Error("no 400 occurred; the learned marker must stay unset")
	}
}

// TestSubstituteImagesFromCache_MixedCacheAndPlaceholder: cached images become
// recognition text, uncached ones placeholders, and the caller's request is
// never mutated.
func TestSubstituteImagesFromCache_MixedCacheAndPlaceholder(t *testing.T) {
	a := &Actor{}
	a.storeImageRecognition(imageRecognitionCacheKey(imageRecognizeDefaultPrompt, "https://x/1.png"), domain.AIAggregatorImageRecognizeResp{Text: "cached answer"})

	orig := domain.SendSessionMessageReq{Messages: []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{
			{Type: domain.ContentBlockImage, ImageURL: "https://x/1.png"},
			{Type: domain.ContentBlockImage, ImageURL: "https://x/2.png"},
		}},
	}}
	snapshot := domain.SendSessionMessageReq{Messages: append([]domain.ChatMessage(nil), orig.Messages...)}
	snapshot.Messages[0].Content = append([]domain.ContentBlock(nil), orig.Messages[0].Content...)

	got := a.substituteImagesFromCache(orig)
	blocks := got.Messages[0].Content
	if len(blocks) != 2 {
		t.Fatalf("block count = %d, want 2", len(blocks))
	}
	if blocks[0].Type != domain.ContentBlockText || !strings.HasPrefix(blocks[0].Text, imageRecognizedPrefix) || !strings.Contains(blocks[0].Text, "cached answer") {
		t.Errorf("cached image not substituted: %+v", blocks[0])
	}
	if blocks[1].Type != domain.ContentBlockText || blocks[1].Text != imageOmittedPlaceholder {
		t.Errorf("uncached image should be placeholder: %+v", blocks[1])
	}
	if !reflect.DeepEqual(orig, snapshot) {
		t.Error("substituteImagesFromCache mutated the caller's request")
	}
}

// TestSubstituteImagesForTextOnlyRetry_RespectsLimit: only the first
// imageSubstitutionLimit blocks are recognized; the rest degrade to
// placeholders.
func TestSubstituteImagesForTextOnlyRetry_RespectsLimit(t *testing.T) {
	restoreGate := swapGate(t, "v")
	defer restoreGate()

	vision := &visionClient{}
	a, _ := newSubstitutionDispatchActor(&imageRejectClient{alwaysFail: true}, vision)
	a.units = []CallableUnit{{ID: "v::gpt-4o", Model: "gpt-4o", ProviderName: "v", Protocol: "vision"}}
	ctx := &testPureCtx{done: make(chan struct{})}

	req := domain.SendSessionMessageReq{Messages: []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{
			{Type: domain.ContentBlockImage, ImageURL: "https://x/1.png"},
			{Type: domain.ContentBlockImage, ImageURL: "https://x/2.png"},
			{Type: domain.ContentBlockImage, ImageURL: "https://x/3.png"},
			{Type: domain.ContentBlockImage, ImageURL: "https://x/4.png"},
			{Type: domain.ContentBlockImage, ImageURL: "https://x/5.png"},
		}},
	}}
	got, anySubstituted := a.substituteImagesForTextOnlyRetry(ctx, req)
	if !anySubstituted {
		t.Fatal("expected substitutions")
	}
	if n := vision.callCount(); n != imageSubstitutionLimit {
		t.Fatalf("vision calls = %d, want %d", n, imageSubstitutionLimit)
	}
	for i, b := range got.Messages[0].Content {
		if i < imageSubstitutionLimit {
			if b.Text == imageOmittedPlaceholder {
				t.Errorf("block %d should carry recognition text", i)
			}
		} else if b.Text != imageOmittedPlaceholder {
			t.Errorf("block %d beyond the limit should be placeholder, got %q", i, b.Text)
		}
	}
	if requestHasImageBlocks(domain.SendSessionMessageReq{Messages: got.Messages}) {
		t.Error("substituted request still carries image blocks")
	}
}

// TestHandleImageRecognize_AdoptsMediaVisionPin: an unpinned recognition call
// routes to the media-configured vision unit; an explicit Provider/Model pin
// overrides the media binding.
func TestHandleImageRecognize_AdoptsMediaVisionPin(t *testing.T) {
	restoreGate := swapGate(t, "v", "o")
	defer restoreGate()

	pinned := &visionClient{}
	other := &visionClient{}
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "visionA",
		Factory:  func(_, _ string) llmclient.Client { return pinned },
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "visionB",
		Factory:  func(_, _ string) llmclient.Client { return other },
	})
	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{{ID: "v::gpt-4o", Model: "gpt-4o", ProviderName: "v", Protocol: "visionA"}, {ID: "o::gpt-4o-mini", Model: "gpt-4o-mini", ProviderName: "o", Protocol: "visionB"}},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	a.visionPin = &visionPinValue{Provider: "v", Model: "gpt-4o"}
	a.visionPinAt = time.Now()
	ctx := &testPureCtx{done: make(chan struct{})}

	resp, err := a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/1.png"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "gpt-4o" || resp.ProviderName != "v" {
		t.Fatalf("unpinned recognition should follow the media pin, got %+v", resp)
	}
	if pinned.callCount() != 1 || other.callCount() != 0 {
		t.Fatalf("media pin routing wrong: pinned=%d other=%d", pinned.callCount(), other.callCount())
	}

	if _, err = a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/2.png", Provider: "o", Model: "gpt-4o-mini"}); err != nil {
		t.Fatal(err)
	}
	if pinned.callCount() != 1 || other.callCount() != 1 {
		t.Fatalf("explicit pin should override media binding: pinned=%d other=%d", pinned.callCount(), other.callCount())
	}
}

// TestVisionPinCacheExpiry: a stale pin is refetched; a nil actorCtx degrades
// to no pin (pool scanning).
func TestVisionPinCacheExpiry(t *testing.T) {
	a := &Actor{}
	a.visionPin = &visionPinValue{Provider: "v", Model: "gpt-4o"}
	a.visionPinAt = time.Now().Add(-2 * visionPinTTL)
	if pin := a.resolveVisionPin(); pin != nil {
		t.Fatalf("nil actorCtx should degrade to no pin after expiry, got %+v", pin)
	}
	a.visionPin = &visionPinValue{Provider: "v", Model: "gpt-4o"}
	a.visionPinAt = time.Now()
	if pin := a.resolveVisionPin(); pin == nil || pin.Model != "gpt-4o" {
		t.Fatalf("fresh pin should be honored, got %+v", pin)
	}
}

// TestHandleImageRecognize_RoutesViaAggregator: req.Aggregator and an
// aggregator-shaped media pin both route recognition to the named child
// aggregator; the self-reference (binding pointing at this very aggregator)
// falls back to local pool scan instead of recursing. Without an actor rig the
// child routing surfaces as the documented resolve error, which distinguishes
// "routing attempted" from a local dispatch.
func TestHandleImageRecognize_RoutesViaAggregator(t *testing.T) {
	restoreGate := swapGate(t, "v")
	defer restoreGate()

	local := &visionClient{}
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "vision",
		Factory:  func(_, _ string) llmclient.Client { return local },
	})
	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{{ID: "v::gpt-4o", Model: "gpt-4o", ProviderName: "v", Protocol: "vision"}},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	ctx := &testPureCtx{done: make(chan struct{})}

	// Explicit aggregator routing wins over local dispatch.
	_, err := a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/1.png", Aggregator: "child-agg"})
	if err == nil || !strings.Contains(err.Error(), "aggregator ref") {
		t.Fatalf("expected child routing error, got %v", err)
	}
	if local.callCount() != 0 {
		t.Fatalf("aggregator routing must not dispatch locally, calls=%d", local.callCount())
	}

	// Aggregator-shaped media pin routes the same way.
	a.visionPin = &visionPinValue{Aggregator: "child-agg"}
	a.visionPinAt = time.Now()
	if _, err = a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/2.png"}); err == nil || !strings.Contains(err.Error(), "aggregator ref") {
		t.Fatalf("expected pin routing error, got %v", err)
	}
	if local.callCount() != 0 {
		t.Fatalf("pin routing must not dispatch locally, calls=%d", local.callCount())
	}

	// A pin pointing at this aggregator itself recognizes locally (recursion
	// break: aggHealthKey == "test-agg" for a bare test actor).
	a.visionPin = &visionPinValue{Aggregator: "test-agg"}
	resp, err := a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/3.png"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProviderName != "v" || local.callCount() != 1 {
		t.Fatalf("self pin should fall back to local pool: resp=%+v calls=%d", resp, local.callCount())
	}

	// Explicit req.Aggregator naming this aggregator is likewise absorbed.
	if _, err = a.handleImageRecognize(ctx, domain.AIAggregatorImageRecognizeReq{Image: "https://x/4.png", Aggregator: "test-agg"}); err != nil {
		t.Fatal(err)
	}
	if local.callCount() != 2 {
		t.Fatalf("self aggregator routing should recognize locally, calls=%d", local.callCount())
	}
}
