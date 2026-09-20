package aiaggregator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// visionPinTTL bounds how long a resolved media vision binding is trusted
// before the next recognition consults the media actor again.
const visionPinTTL = 60 * time.Second

// visionPinLookupTimeout bounds the one-shot media.active_account lookup.
const visionPinLookupTimeout = 10 * time.Second

// visionPinValue is the media-configured vision binding: either a
// (Provider, Model) pin or an aggregator config id routing recognition
// through that aggregator's pool.
type visionPinValue struct {
	Provider   string
	Model      string
	Aggregator string
}

// resolveVisionPin returns the media-configured vision model binding, or nil
// when none is configured or the media actor is unreachable (callers fall back
// to pool scanning). Cached for visionPinTTL.
func (a *Actor) resolveVisionPin() *visionPinValue {
	a.visionPinMu.Lock()
	defer a.visionPinMu.Unlock()
	if a.visionPin != nil && time.Since(a.visionPinAt) < visionPinTTL {
		return a.visionPin
	}

	pin := a.fetchVisionPin()
	a.visionPin = pin
	a.visionPinAt = time.Now()
	return pin
}

// fetchVisionPin performs the uncached media lookup. Errors degrade to nil:
// an unreachable media actor must not break recognition, which still scans
// the pool.
func (a *Actor) fetchVisionPin() *visionPinValue {
	if a.actorCtx == nil {
		return nil
	}
	mediaRef, ok := a.actorCtx.LookupService("media")
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(a.actorCtx.Lifecycle(), visionPinLookupTimeout)
	defer cancel()
	call := mediaRef.Invoke(ctx, "media.active_account", gen.MediaActiveAccountReq{Kind: "vision"})
	result, err := call.Final(ctx)
	if err != nil {
		return nil
	}
	switch r := result.(type) {
	case gen.MediaActiveAccountResp:
		return visionPinFromResp(r.BoundProvider, r.BoundModel, r.BoundAggregator)
	case *gen.MediaActiveAccountResp:
		if r != nil {
			return visionPinFromResp(r.BoundProvider, r.BoundModel, r.BoundAggregator)
		}
	}
	return nil
}

func visionPinFromResp(provider, model, aggregator string) *visionPinValue {
	if aggregator != "" {
		return &visionPinValue{Aggregator: aggregator}
	}
	if provider != "" && model != "" {
		return &visionPinValue{Provider: provider, Model: model}
	}
	return nil
}

// imageRecognitionCacheLimit bounds the recognition cache; entries beyond it
// evict oldest-first.
const imageRecognitionCacheLimit = 64

// imageRecognitionCacheKey hashes the (prompt, image) pair that determines the
// answer.
func imageRecognitionCacheKey(prompt, image string) string {
	h := sha256.New()
	h.Write([]byte(prompt))
	h.Write([]byte{0})
	h.Write([]byte(image))
	return hex.EncodeToString(h.Sum(nil))
}

// cachedImageRecognition returns a cached recognition answer, if present.
func (a *Actor) cachedImageRecognition(key string) (domain.AIAggregatorImageRecognizeResp, bool) {
	a.imageRecogMu.Lock()
	defer a.imageRecogMu.Unlock()
	resp, ok := a.imageRecogCache[key]
	return resp, ok
}

// storeImageRecognition caches a recognition answer with oldest-first eviction.
func (a *Actor) storeImageRecognition(key string, resp domain.AIAggregatorImageRecognizeResp) {
	a.imageRecogMu.Lock()
	defer a.imageRecogMu.Unlock()
	if a.imageRecogCache == nil {
		a.imageRecogCache = make(map[string]domain.AIAggregatorImageRecognizeResp)
	}
	if _, exists := a.imageRecogCache[key]; !exists {
		a.imageRecogOrder = append(a.imageRecogOrder, key)
		if len(a.imageRecogOrder) > imageRecognitionCacheLimit {
			oldest := a.imageRecogOrder[0]
			a.imageRecogOrder = a.imageRecogOrder[1:]
			delete(a.imageRecogCache, oldest)
		}
	}
	a.imageRecogCache[key] = resp
}

// recognizeImageCached wraps handleImageRecognize with the recognition cache:
// identical (prompt, image) pairs are answered once.
func (a *Actor) recognizeImageCached(ctx actor.PureContext, req domain.AIAggregatorImageRecognizeReq) (domain.AIAggregatorImageRecognizeResp, error) {
	prompt := req.Prompt
	if prompt == "" {
		prompt = imageRecognizeDefaultPrompt
	}
	key := imageRecognitionCacheKey(prompt, req.Image)
	if resp, ok := a.cachedImageRecognition(key); ok {
		return resp, nil
	}
	resp, err := a.handleImageRecognize(ctx, req)
	if err != nil {
		return domain.AIAggregatorImageRecognizeResp{}, err
	}
	a.storeImageRecognition(key, resp)
	return resp, nil
}

// substituteImagesFromCache is the proactive-path counterpart of
// substituteImagesForTextOnlyRetry: image blocks with a cached recognition
// answer become text blocks; everything else degrades to the
// omitted-placeholder. No LLM calls happen here — recognition is only
// triggered by the 400 interception, so a marked unit costs one vision call
// per image, ever. Copy-on-write, same discipline as stripImageBlocks.
func (a *Actor) substituteImagesFromCache(req domain.SendSessionMessageReq) domain.SendSessionMessageReq {
	if !requestHasImageBlocks(req) {
		return req
	}
	msgs := make([]domain.ChatMessage, len(req.Messages))
	copy(msgs, req.Messages)
	for i := range msgs {
		hasImage := false
		for _, b := range msgs[i].Content {
			if b.Type == domain.ContentBlockImage {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		blocks := make([]domain.ContentBlock, len(msgs[i].Content))
		copy(blocks, msgs[i].Content)
		for j := range blocks {
			if blocks[j].Type != domain.ContentBlockImage {
				continue
			}
			key := imageRecognitionCacheKey(imageRecognizeDefaultPrompt, blocks[j].ImageURL)
			if resp, ok := a.cachedImageRecognition(key); ok && strings.TrimSpace(resp.Text) != "" {
				blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: imageRefSuffix(imageRecognizedPrefix+"\n"+resp.Text, msgs[i].ID, j)}
				continue
			}
			blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: imageRefSuffix(imageOmittedPlaceholder, msgs[i].ID, j)}
		}
		msgs[i].Content = blocks
	}
	req.Messages = msgs
	return req
}

// imageSubstitutionLimit caps how many image blocks one interception
// recognizes before the rest degrade to omitted-placeholders.
const imageSubstitutionLimit = 3

// imageSubstitutionBudget bounds the total recognition time inside one
// interception so a slow vision model cannot stall the dispatch retry.
const imageSubstitutionBudget = 90 * time.Second

// imageRecognizedPrefix marks substituted blocks in the wire request so the
// text-only primary can tell recognition text from ordinary user text.
const imageRecognizedPrefix = "[image recognized]"

// imageRefSuffix appends the msg:<id>:<blockIdx> handle so the text-only
// primary can re-recognize the image with a task-specific prompt via the
// recognize_image tool (its Image param resolves the handle against the
// persisted steps; the index disambiguates multi-image messages).
func imageRefSuffix(text, msgID string, blockIdx int) string {
	if msgID == "" {
		return text
	}
	return text + "\n[image ref: msg:" + msgID + ":" + strconv.Itoa(blockIdx) + "]"
}

// substituteImagesForTextOnlyRetry rewrites req's image blocks into text
// blocks carrying recognition text from the vision pipeline (media-configured
// binding first, pool scan fallback). Blocks beyond imageSubstitutionLimit,
// after the budget is spent, or whose recognition fails degrade to the
// omitted-placeholder — the same shape as stripImageBlocks. Copy-on-write:
// the caller's message and block slices are never mutated. The primary unit
// is not replaced; only the image blocks change on the retry.
func (a *Actor) substituteImagesForTextOnlyRetry(ctx actor.PureContext, req domain.SendSessionMessageReq) (domain.SendSessionMessageReq, bool) {
	if !requestHasImageBlocks(req) {
		return req, false
	}
	recognized := 0
	anySubstituted := false
	deadline := time.Now().Add(imageSubstitutionBudget)

	msgs := make([]domain.ChatMessage, len(req.Messages))
	copy(msgs, req.Messages)
	for i := range msgs {
		hasImage := false
		for _, b := range msgs[i].Content {
			if b.Type == domain.ContentBlockImage {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		blocks := make([]domain.ContentBlock, len(msgs[i].Content))
		copy(blocks, msgs[i].Content)
		for j := range blocks {
			if blocks[j].Type != domain.ContentBlockImage {
				continue
			}
			image := blocks[j].ImageURL
			if recognized < imageSubstitutionLimit && time.Now().Before(deadline) {
				resp, err := a.recognizeImageCached(ctx, domain.AIAggregatorImageRecognizeReq{Image: image})
				if err == nil && strings.TrimSpace(resp.Text) != "" {
					blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: imageRefSuffix(imageRecognizedPrefix+"\n"+resp.Text, msgs[i].ID, j)}
					recognized++
					anySubstituted = true
					continue
				}
				ctx.Logger().Info("aiaggregator: image substitution fell back to placeholder",
					"errorLogged", err != nil, "recognized", recognized)
			}
			blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: imageRefSuffix(imageOmittedPlaceholder, msgs[i].ID, j)}
		}
		msgs[i].Content = blocks
	}
	req.Messages = msgs
	return req, anySubstituted
}
