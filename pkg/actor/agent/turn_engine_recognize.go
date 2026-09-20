package agent

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// imageRecognitionTimeout bounds a single automatic recognition call. It is
// deliberately shorter than domain.ToolCallTimeout so a stuck vision unit
// cannot stall the dispatch loop for minutes.
const imageRecognitionTimeout = 90 * time.Second

// defaultImageRecognitionPrompt is used when the pass recognizes an image
// automatically (no LLM-chosen prompt available): it asks for the facts a
// text-only primary model needs to act on the image.
const defaultImageRecognitionPrompt = "Describe this image factually for a text-only model: the overall scene, notable objects and layout, any UI elements, and transcribe any visible text verbatim."

// imageRecognitionFailedPrefix marks RecognitionText written by a failed
// recognition attempt (one-shot semantics: the failure is stored, never
// retried). The wire projection keeps the marker instead of pretending the
// image was successfully described.
const imageRecognitionFailedPrefix = "[image recognition failed"

// recognizedImageWireText projects a RecognitionText into the wire text a
// text-only primary sees: successful descriptions carry the [image
// recognized] marker, failures keep their explicit failure text, and every
// projection ends with the msg:<id>:<blockIdx> handle so the model can
// re-recognize the same image with a task-specific prompt via
// recognize_image (the index disambiguates multi-image messages).
func recognizedImageWireText(recognitionText, msgID string, blockIdx int) string {
	text := recognitionText
	if !strings.HasPrefix(text, imageRecognitionFailedPrefix) {
		text = "[image recognized]\n" + text
	}
	if msgID != "" {
		text += "\n[image ref: msg:" + msgID + ":" + strconv.Itoa(blockIdx) + "]"
	}
	return text
}

// recognizeHistoryImages is the pre-dispatch one-shot recognition pass. When
// the dispatch target's unit is known and does not support image input, every
// unrecognized image block in engine history is sent to
// aiaggregator.image_recognize exactly once; success or failure both mark the
// block Recognized (the user-visible contract: recognition happens once per
// message, and vision primaries never read these fields because the pass — and
// the dispatchImagesAsText wire replacement — only engage for text-only units).
func (e *turnEngine) recognizeHistoryImages(ctx actor.Context) {
	e.dispatchImagesAsText = false
	if e.resolveTargets == nil || !historyHasUserImage(e.history) {
		return
	}
	targets := e.resolveTargets(ctx, e.primarySlot)
	if len(targets) == 0 || targets[0].aggRef == nil {
		return
	}
	// Gate on a known concrete unit. An aggregator-backed slot with no pinned
	// unit may still route to a vision model, so leave its images alone (the
	// aggregator's strip path handles a text-only pick).
	unit := targets[0].unit
	if unit.Model == "" || llmclient.ModelSupportsImageInput(unit.Model) {
		return
	}
	// Text-only unit confirmed: wire replacement must engage even when every
	// image is already recognized (e.g. rebuilt from persisted steps).
	e.dispatchImagesAsText = true
	if !historyHasUnrecognizedImage(e.history) {
		return
	}
	target := targets[0]
	planner := ctx.Planner()
	if planner == nil {
		return
	}
	for i := range e.history {
		msg := &e.history[i]
		if msg.Role != domain.ChatRoleUser {
			continue
		}
		for j := range msg.Content {
			block := &msg.Content[j]
			if block.Type != domain.ContentBlockImage || block.Recognized || block.ImageURL == "" {
				continue
			}
			text, err := e.recognizeImageBlock(ctx, planner, target.aggRef, block.ImageURL)
			block.Recognized = true
			if err != nil {
				e.logger.Warn("turnEngine: image recognition failed", "turnID", e.turnID, "error", err.Error())
				block.RecognitionText = fmt.Sprintf("[image recognition failed: %s]", truncate(err.Error(), 200))
			} else {
				block.RecognitionText = text
			}
			if e.onImageRecognized != nil {
				e.onImageRecognized(msg.ID, j, block.Recognized, block.RecognitionText)
			}
		}
	}
}

// historyHasUserImage reports whether any user-role message carries an image
// block at all, so the pass can skip target resolution and planner work
// entirely on image-less turns.
func historyHasUserImage(history []domain.ChatMessage) bool {
	for i := range history {
		if history[i].Role != domain.ChatRoleUser {
			continue
		}
		for j := range history[i].Content {
			b := &history[i].Content[j]
			if b.Type == domain.ContentBlockImage && b.ImageURL != "" {
				return true
			}
		}
	}
	return false
}

// historyHasUnrecognizedImage reports whether any user-role message still
// carries an unrecognized image block.
func historyHasUnrecognizedImage(history []domain.ChatMessage) bool {
	for i := range history {
		if history[i].Role != domain.ChatRoleUser {
			continue
		}
		for j := range history[i].Content {
			b := &history[i].Content[j]
			if b.Type == domain.ContentBlockImage && !b.Recognized && b.ImageURL != "" {
				return true
			}
		}
	}
	return false
}

// recognizeImageBlock performs one aiaggregator.image_recognize call and
// returns the description text.
func (e *turnEngine) recognizeImageBlock(ctx actor.Context, planner actor.Planner, aggRef ref.Ref, imageRef string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), imageRecognitionTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, aggRef, "aiaggregator.image_recognize", gen.AIAggregatorImageRecognizeReq{
		Image:  imageRef,
		Prompt: defaultImageRecognitionPrompt,
	}).Await()
	if err != nil {
		return "", err
	}
	resp, err := decodeImageRecognize(result)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}
