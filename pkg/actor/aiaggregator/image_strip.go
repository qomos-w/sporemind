package aiaggregator

import (
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// imageOmittedPlaceholder replaces an image block in the wire request when the
// target unit cannot accept image input. A text block (rather than dropping
// the block) preserves the structural fact that an image was part of the
// conversation at that position.
const imageOmittedPlaceholder = "[image omitted: this model does not support image input]"

// requestHasImageBlocks reports whether any message in the request carries an
// image content block. Pure; no actor state involved.
func requestHasImageBlocks(req domain.SendSessionMessageReq) bool {
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == domain.ContentBlockImage {
				return true
			}
		}
	}
	return false
}

// stripImageBlocks returns req with every image content block replaced by a
// text placeholder block. Only the wire request is rewritten: the caller's
// message and block slices (the session history, shared by reference) are
// never mutated — affected slices are copied before modification. Block order
// and message structure are preserved. A no-op request is returned as-is.
func stripImageBlocks(req domain.SendSessionMessageReq) domain.SendSessionMessageReq {
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
			if blocks[j].Type == domain.ContentBlockImage {
				blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: imageOmittedPlaceholder}
			}
		}
		msgs[i].Content = blocks
	}
	req.Messages = msgs
	return req
}

// noImageInputKey is the marker map key for a unit's (provider, model) pair.
func noImageInputKey(unit CallableUnit) string {
	return unit.ProviderName + "::" + unit.Model
}

// unitLacksImageInput reports whether this aggregator has learned that the
// unit's model rejects image input. Aggregator-ref entries (no provider/model
// of their own) never match.
func (a *Actor) unitLacksImageInput(unit CallableUnit) bool {
	if unit.ProviderName == "" || unit.Model == "" {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.noImageInput[noImageInputKey(unit)]
}

// unitNeedsImageSubstitution reports whether image blocks in a request to this
// unit must be rewritten before dispatch: either the unit has been learned to
// reject image input (a 400 marked it), or the model is known by family
// heuristic not to accept image content (e.g. deepseek). Both cases route
// through the recognition pipeline (or the omitted-placeholder when no vision
// unit is available) — never a bare "[image]" marker, which leaves a text-only
// model blind to the screenshot it was told about. Aggregator-ref entries have
// no model of their own and never need substitution.
func (a *Actor) unitNeedsImageSubstitution(unit CallableUnit) bool {
	if a.unitLacksImageInput(unit) {
		return true
	}
	if unit.Model == "" {
		return false
	}
	return !llmclient.ModelSupportsImageInput(unit.Model)
}

// markNoImageInput records that the unit's model rejects image input, so
// subsequent requests to it are stripped in buildLLMRequest without paying
// the doomed 400 round-trip.
func (a *Actor) markNoImageInput(unit CallableUnit) {
	if unit.ProviderName == "" || unit.Model == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.noImageInput == nil {
		a.noImageInput = make(map[string]bool)
	}
	a.noImageInput[noImageInputKey(unit)] = true
}
