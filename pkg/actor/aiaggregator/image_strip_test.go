package aiaggregator

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

func imageReqFixture() domain.SendSessionMessageReq {
	return domain.SendSessionMessageReq{
		Messages: []domain.ChatMessage{
			{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "look at this"},
			}},
			{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "before"},
				{Type: domain.ContentBlockImage, ImageURL: "https://example.com/a.png", MimeType: "image/png"},
				{Type: domain.ContentBlockText, Text: "after"},
			}},
			{Role: domain.ChatRoleAssistant, Content: []domain.ContentBlock{
				{Type: domain.ContentBlockText, Text: "noted"},
			}},
		},
	}
}

func TestRequestHasImageBlocks(t *testing.T) {
	if !requestHasImageBlocks(imageReqFixture()) {
		t.Error("fixture with image block reported no images")
	}
	noImage := imageReqFixture()
	noImage.Messages[1].Content[1] = domain.ContentBlock{Type: domain.ContentBlockText, Text: "x"}
	if requestHasImageBlocks(noImage) {
		t.Error("request without image blocks reported images")
	}
	if requestHasImageBlocks(domain.SendSessionMessageReq{}) {
		t.Error("empty request reported images")
	}
}

func TestStripImageBlocks_ReplacesWithPlaceholderAndKeepsOrder(t *testing.T) {
	orig := imageReqFixture()
	stripped := stripImageBlocks(orig)

	msg := stripped.Messages[1]
	if len(msg.Content) != 3 {
		t.Fatalf("block count changed: %d, want 3", len(msg.Content))
	}
	if msg.Content[0].Text != "before" || msg.Content[2].Text != "after" {
		t.Errorf("surrounding blocks reordered: %+v", msg.Content)
	}
	if msg.Content[1].Type != domain.ContentBlockText || msg.Content[1].Text != imageOmittedPlaceholder {
		t.Errorf("image block not replaced by placeholder: %+v", msg.Content[1])
	}
	if msg.Content[1].ImageURL != "" || msg.Content[1].MimeType != "" {
		t.Errorf("placeholder block leaks image fields: %+v", msg.Content[1])
	}
	// Untouched messages keep their identity.
	if stripped.Messages[0].Content[0].Text != "look at this" || stripped.Messages[2].Content[0].Text != "noted" {
		t.Errorf("unrelated messages modified: %+v", stripped.Messages)
	}
}

func TestStripImageBlocks_DoesNotMutateHistory(t *testing.T) {
	orig := imageReqFixture()
	stripped := stripImageBlocks(orig)
	if stripped.Messages[1].Content[1].Type != domain.ContentBlockText {
		t.Fatalf("strip did not return a stripped copy")
	}
	// The caller's request (session history shared by reference) must be
	// byte-for-byte what it was before the strip.
	if !reflect.DeepEqual(orig, imageReqFixture()) {
		t.Errorf("stripImageBlocks mutated the caller's request:\n got %+v\nwant %+v", orig, imageReqFixture())
	}
}

func TestStripImageBlocks_NoImagesNoOp(t *testing.T) {
	req := domain.SendSessionMessageReq{Messages: []domain.ChatMessage{
		{Role: domain.ChatRoleUser, Content: []domain.ContentBlock{{Type: domain.ContentBlockText, Text: "plain"}}},
	}}
	got := stripImageBlocks(req)
	if !reflect.DeepEqual(got, req) {
		t.Errorf("no-image request changed:\n got %+v\nwant %+v", got, req)
	}
}

// ════════════════════════════════════════════════════════════════════════════
// Dispatch flow — a 400 "model does not support image input" marks the unit
// text-only and retries the SAME unit with image blocks stripped into text
// placeholders; the session history is never modified; later dispatches to the
// marked unit are stripped up front with no failed round-trip.
// ════════════════════════════════════════════════════════════════════════════

// imageRejectClient fails the first Stream call with the observed upstream
// image-unsupported 400 (unless alwaysFail), then serves a clean one-shot
// stream. Every received wire request is recorded for assertions.
type imageRejectClient struct {
	mu         sync.Mutex
	alwaysFail bool
	calls      []llmclient.Request
}

func (c *imageRejectClient) Stream(_ context.Context, req llmclient.Request) (llmclient.Stream, error) {
	c.mu.Lock()
	c.calls = append(c.calls, req)
	isFirst := len(c.calls) == 1
	c.mu.Unlock()
	if c.alwaysFail || isFirst {
		return nil, &llmclient.UpstreamError{
			StatusCode: 400,
			Type:       "invalid_request_error",
			Message:    "this model does not support image input",
		}
	}
	ch := make(chan llmclient.Event, 2)
	ch <- llmclient.Event{Kind: llmclient.EventTextDelta, Text: "ok"}
	ch <- llmclient.Event{Kind: llmclient.EventStop}
	close(ch)
	return &okStream{ch: ch}, nil
}

func (c *imageRejectClient) requestImageCounts() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, len(c.calls))
	for i, r := range c.calls {
		n := 0
		for _, m := range r.Messages {
			for _, b := range m.Content {
				if b.Type == llmclient.BlockTypeImage {
					n++
				}
			}
		}
		out[i] = n
	}
	return out
}

func (c *imageRejectClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *imageRejectClient) requests() []llmclient.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]llmclient.Request, len(c.calls))
	copy(out, c.calls)
	return out
}

func (c *imageRejectClient) lastRequestHasPlaceholder() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return false
	}
	for _, m := range c.calls[len(c.calls)-1].Messages {
		for _, b := range m.Content {
			if b.Type == llmclient.BlockTypeText && b.Text == imageOmittedPlaceholder {
				return true
			}
		}
	}
	return false
}

func newImageStripDispatchActor(client llmclient.Client) (*Actor, CallableUnit) {
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "imgreject",
		Factory:  func(_, _ string) llmclient.Client { return client },
	})
	unit := CallableUnit{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "imgreject"}
	a := &Actor{
		id:           "test-agg",
		units:        []CallableUnit{unit},
		strategy:     NewFallbackStrategy(),
		registry:     *reg,
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	return a, unit
}

func TestDispatch_ImageUnsupported_MarksAndRetriesStripped(t *testing.T) {
	restoreGate := swapGate(t, "a")
	defer restoreGate()

	client := &imageRejectClient{}
	a, unit := newImageStripDispatchActor(client)
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	req := imageReqFixture()
	if err := a.handleDispatch(ctx, req, emit); err != nil {
		t.Fatalf("dispatch should succeed via stripped retry, got: %v", err)
	}
	if got := client.requestImageCounts(); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Errorf("wire request image counts = %v, want [1 0] (first with image, retry stripped)", got)
	}
	if !client.lastRequestHasPlaceholder() {
		t.Error("retry request should carry the text placeholder block")
	}
	if !a.unitLacksImageInput(unit) {
		t.Error("unit should be marked as lacking image input after the 400")
	}
	if !reflect.DeepEqual(req, imageReqFixture()) {
		t.Errorf("session history mutated by dispatch:\n got %+v\nwant %+v", req, imageReqFixture())
	}
	if ru := emit.resolvedUnit(); ru == nil {
		t.Error("expected resolved_unit chunk for the stripped retry")
	}
}

func TestDispatch_ImageUnsupported_ProactiveStripOnNextDispatch(t *testing.T) {
	restoreGate := swapGate(t, "a")
	defer restoreGate()

	client := &imageRejectClient{}
	a, _ := newImageStripDispatchActor(client)
	ctx := &testPureCtx{done: make(chan struct{})}

	if err := a.handleDispatch(ctx, imageReqFixture(), &collectingEmitter{done: make(chan struct{})}); err != nil {
		t.Fatalf("first dispatch should succeed via stripped retry, got: %v", err)
	}
	if client.callCount() != 2 {
		t.Fatalf("first dispatch should make 2 attempts, got %d", client.callCount())
	}

	// Second dispatch to the now-marked unit must be stripped up front: a
	// single attempt, already without image blocks.
	emit2 := &collectingEmitter{done: make(chan struct{})}
	if err := a.handleDispatch(ctx, imageReqFixture(), emit2); err != nil {
		t.Fatalf("second dispatch should succeed on first attempt, got: %v", err)
	}
	if got := client.requestImageCounts(); !reflect.DeepEqual(got, []int{1, 0, 0}) {
		t.Errorf("wire request image counts = %v, want [1 0 0] (third attempt pre-stripped)", got)
	}
}

func TestDispatch_ImageUnsupported_PersistentFailureTerminates(t *testing.T) {
	restoreGate := swapGate(t, "a")
	defer restoreGate()

	client := &imageRejectClient{alwaysFail: true}
	a, _ := newImageStripDispatchActor(client)
	ctx := &testPureCtx{done: make(chan struct{})}

	err := a.handleDispatch(ctx, imageReqFixture(), &collectingEmitter{done: make(chan struct{})})
	if err == nil {
		t.Fatal("persistent image-unsupported failure must surface, got success")
	}
	if got := upstreamStatus(t, err); got != 400 {
		t.Errorf("error should chain the 400 upstream error, got status %d: %v", got, err)
	}
	// Exactly two attempts: original + one stripped retry; no rotation (the
	// pool has only this unit) and no loop.
	if n := client.callCount(); n != 2 {
		t.Errorf("attempt count = %d, want 2 (original + stripped retry)", n)
	}
}

// TestDispatch_ImageUnsupported_FailoverGetsItsOwnStripRetry verifies the
// per-unit guard: unit A burns its stripped retry (still failing), the stop
// budget rotates to unit B, and B receives its own mark + stripped retry
// instead of being blocked by A's earlier retry.
func TestDispatch_ImageUnsupported_FailoverGetsItsOwnStripRetry(t *testing.T) {
	restoreGate := swapGate(t, "a", "b")
	defer restoreGate()

	clientA := &imageRejectClient{alwaysFail: true}
	clientB := &imageRejectClient{}
	// Two protocols so each provider's unit gets its own client: A always
	// fails, B recovers after its strip retry.
	reg := llmclient.NewRegistry()
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "imgfail",
		Factory:  func(_, _ string) llmclient.Client { return clientA },
	})
	reg.MustRegister(llmclient.Descriptor{
		Protocol: "imgreject",
		Factory:  func(_, _ string) llmclient.Client { return clientB },
	})
	a := &Actor{
		id:       "test-agg",
		strategy: NewFallbackStrategy(),
		registry: *reg,
		// Two-unit pool: A always fails, B recovers after its strip retry.
		aimanagerRef: tokenFakeRef(),
		lifecycleCtx: context.Background(),
	}
	a.units = []CallableUnit{
		{ID: "a::m", Model: "m", ProviderName: "a", Protocol: "imgfail"},
		{ID: "b::m", Model: "m", ProviderName: "b", Protocol: "imgreject"},
	}
	ctx := &testPureCtx{done: make(chan struct{})}
	emit := &collectingEmitter{done: make(chan struct{})}

	if err := a.handleDispatch(ctx, imageReqFixture(), emit); err != nil {
		t.Fatalf("dispatch should succeed on B via its own stripped retry, got: %v", err)
	}
	if got := clientA.requestImageCounts(); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Errorf("unit A wire image counts = %v, want [1 0]", got)
	}
	if got := clientB.requestImageCounts(); !reflect.DeepEqual(got, []int{1, 0}) {
		t.Errorf("unit B wire image counts = %v, want [1 0] (B gets its own strip retry)", got)
	}
	if ru := emit.resolvedUnit(); ru == nil || ru.Provider != "b" {
		t.Errorf("resolved unit should be provider b, got %+v", ru)
	}
}
