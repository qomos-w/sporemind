package agent

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// recognizeTestContext wires an agent Actor with a cached system-aggregator ref
// and a planner that answers aiaggregator.image_recognize with fixed text,
// capturing the request it received.
func recognizeTestContext(t *testing.T, capture *gen.AIAggregatorImageRecognizeReq, answer string) (actor.PureContext, *Actor) {
	t.Helper()
	a := &Actor{}
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
			if callID != "aiaggregator.image_recognize" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			if req, ok := payload.(gen.AIAggregatorImageRecognizeReq); ok {
				*capture = req
			}
			return gen.AIAggregatorImageRecognizeResp{Text: answer, Model: "qwen-vl-max", ProviderName: "qwen"}, nil
		}}
	}
	return ctx, a
}

// TestHandleImageRecognize_ForwardsPromptAndPins locks the request contract:
// the tool's Prompt/Provider/Model fields ride through to
// aiaggregator.image_recognize and the description text returns to the caller.
// TestHandleImageRecognize_MsgRefResolvesPersistedImage: the msg:<id> handle
// (appended to recognized-image wire text) resolves to the image block of the
// persisted step, so a text-only primary can re-recognize a conversation
// image with a task-specific prompt.
func TestHandleImageRecognize_MsgRefResolvesPersistedImage(t *testing.T) {
	var capture gen.AIAggregatorImageRecognizeReq
	ctx, a := recognizeTestContext(t, &capture, "the submit button is bottom-right")
	a.steps = append(a.steps, domain.Step{
		ID:      "obs-1",
		Role:    "assistant",
		Type:    "user_inject",
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "[computeruse.screenshot 320x500 jpeg]"},
			{Type: domain.ContentBlockImage, ImageURL: "data:image/jpeg;base64,aGVsbG8=", MimeType: "image/jpeg"},
		},
	})

	text, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{
		Image:  "msg:obs-1",
		Prompt: "where is the submit button?",
	})
	if err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	if text != "the submit button is bottom-right" {
		t.Fatalf("text = %q", text)
	}
	if capture.Image != "data:image/jpeg;base64,aGVsbG8=" {
		t.Fatalf("msg ref must resolve to the persisted data URL, got %q", capture.Image)
	}

	// Unknown message id: explicit error, not a silent mis-dispatch.
	if _, err = a.handleImageRecognize(ctx, gen.ImageRecognizeReq{Image: "msg:nope"}); err == nil {
		t.Fatal("unknown msg ref must error")
	}
}

// TestImageFromMessage_BlockIndex: multi-image messages carry per-block refs
// (msg:<id>:<idx>); each resolves to ITS image, not the message's first.
func TestImageFromMessage_BlockIndex(t *testing.T) {
	a := &Actor{steps: []domain.Step{{
		ID:   "multi",
		Role: "assistant",
		Type: "user_inject",
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: "two shots"},
			{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,FIRST"},
			{Type: domain.ContentBlockImage, ImageURL: "data:image/png;base64,SECOND"},
		},
	}}}
	if url, ok := a.imageFromMessage("multi"); !ok || url != "data:image/png;base64,FIRST" {
		t.Fatalf("no-index ref should resolve the first image, got %q %v", url, ok)
	}
	if url, ok := a.imageFromMessage("multi:2"); !ok || url != "data:image/png;base64,SECOND" {
		t.Fatalf("indexed ref should resolve the second image, got %q %v", url, ok)
	}
	if _, ok := a.imageFromMessage("multi:9"); ok {
		t.Fatal("out-of-range index must not resolve")
	}
}

// TestHandleImageRecognize_ForwardsPromptAndPins locks prompt/pin forwarding.
func TestHandleImageRecognize_ForwardsPromptAndPins(t *testing.T) {
	var capture gen.AIAggregatorImageRecognizeReq
	ctx, a := recognizeTestContext(t, &capture, "three red cubes")

	text, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{
		Image:    "https://example.com/pic.png",
		Prompt:   "count the cubes",
		Provider: "qwen",
		Model:    "qwen-vl-max",
	})
	if err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	if text != "three red cubes" {
		t.Fatalf("text = %q", text)
	}
	if capture.Image != "https://example.com/pic.png" || capture.Prompt != "count the cubes" ||
		capture.Provider != "qwen" || capture.Model != "qwen-vl-max" {
		t.Fatalf("captured request = %+v", capture)
	}
}

// TestHandleImageRecognize_FileBecomesDataURL locks the image normalization:
// a readable file path is read, mime-sniffed, and sent as a data URL.
func TestHandleImageRecognize_FileBecomesDataURL(t *testing.T) {
	var capture gen.AIAggregatorImageRecognizeReq
	ctx, a := recognizeTestContext(t, &capture, "a png")

	// Minimal valid PNG header (magic bytes only) — enough for sniffing.
	pngHeader := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, pngHeader, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{Image: path}); err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngHeader)
	if capture.Image != want {
		t.Fatalf("image ref = %q, want %q", capture.Image, want)
	}
}

// TestHandleImageRecognize_RawBase64BecomesDataURL locks that raw base64 input
// is wrapped into a sniffed data URL.
func TestHandleImageRecognize_RawBase64BecomesDataURL(t *testing.T) {
	var capture gen.AIAggregatorImageRecognizeReq
	ctx, a := recognizeTestContext(t, &capture, "a jpeg")

	// JPEG magic bytes followed by a tail, encoded as ONE contiguous base64
	// payload (concatenating separate encodings would inject mid-string
	// padding and stop the streaming sniff decoder).
	payload := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("rest-of-payload")...)
	encoded := base64.StdEncoding.EncodeToString(payload)

	if _, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{Image: encoded}); err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	if !strings.HasPrefix(capture.Image, "data:image/jpeg;base64,") {
		t.Fatalf("image ref = %q, want sniffed jpeg data URL", capture.Image)
	}
	if capture.Image != "data:image/jpeg;base64,"+encoded {
		t.Fatalf("image ref = %q", capture.Image)
	}
}

// TestHandleImageRecognize_NamedAggregatorRoutesToItsRef locks that a non-empty
// Aggregator field resolves that aggregator's cached ref instead of system.
func TestHandleImageRecognize_NamedAggregatorRoutesToItsRef(t *testing.T) {
	a := &Actor{}
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{"vision-pool": aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	var gotRefID string
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, r ref.Ref, callID string, _ any) (any, error) {
			if callID != "aiaggregator.image_recognize" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			gotRefID = r.ID().String()
			return gen.AIAggregatorImageRecognizeResp{Text: "ok"}, nil
		}}
	}

	if _, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{
		Image:      "data:image/png;base64,AAAA",
		Aggregator: "vision-pool",
	}); err != nil {
		t.Fatalf("handleImageRecognize: %v", err)
	}
	if gotRefID != aggRef.ID().String() {
		t.Fatalf("routed to %q, want vision-pool ref %q", gotRefID, aggRef.ID().String())
	}
}

// TestHandleImageRecognize_MissingAggregatorErrors locks the explicit failure
// when the requested aggregator cannot be resolved (no silent fallback).
func TestHandleImageRecognize_MissingAggregatorErrors(t *testing.T) {
	a := &Actor{}
	a.aggRefCache = map[string]ref.Ref{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleImageRecognize(ctx, gen.ImageRecognizeReq{Image: "data:image/png;base64,AAAA"})
	if err == nil || !strings.Contains(err.Error(), "aggregator \"system\" not available") {
		t.Fatalf("unexpected error: %v", err)
	}
}
