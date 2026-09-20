package aiaggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// imageRecognizeTimeout bounds one recognition attempt loop (selection +
// non-streaming completion). Vision answers are short; three minutes covers a
// slow reasoning model without letting a stalled stream hang the tool call.
// The caller-side tool budget (domain.ToolCallTimeout) is wider, so the inner
// deadline is what fires.
const imageRecognizeTimeout = 3 * time.Minute

// imageRecognizeDefaultPrompt is used when the caller sends no instruction:
// the answer returns to a text-only model as its only view of the image, so
// the default leans exhaustive (objects, layout, OCR).
const imageRecognizeDefaultPrompt = "Describe this image in detail: objects, people, layout, colors, visible text (transcribe it), and anything noteworthy."

// handleImageRecognize sends one image plus an optional instruction to a
// vision-capable chat unit and returns the model's text answer. Selection
// prefers an explicit Provider/Model pin; otherwise it scans the pool for a
// vision-capable unit (chat modality, not marked no-image-input, family not
// known text-only). Failover rotates only among vision-capable units — unlike
// dispatch, an image-unsupported failure never retries with images stripped,
// because a stripped recognition is worthless.
//
// req.Aggregator routes the whole recognition to a named aggregator config's
// pool (the child runs its own vision selection); the media-configured vision
// binding may likewise be an aggregator. Routing stops at the target: the
// child receives the request without Aggregator, and a binding pointing at
// the aggregator itself falls through to local pool scan, so the hop chain
// can never loop.
func (a *Actor) handleImageRecognize(ctx actor.PureContext, req domain.AIAggregatorImageRecognizeReq) (domain.AIAggregatorImageRecognizeResp, error) {
	a.ensureConfigLoaded()

	// Explicit aggregator routing wins over everything except that the
	// target being this aggregator itself is a config-time self reference.
	if req.Aggregator != "" && req.Aggregator != a.aggHealthKey() {
		return a.recognizeViaChildAggregator(req)
	}
	req.Aggregator = ""

	// Unpinned requests adopt the media-configured vision binding (settings →
	// media → vision). An explicit Provider/Model pin from the caller wins.
	if req.Provider == "" && req.Model == "" {
		if pin := a.resolveVisionPin(); pin != nil {
			switch {
			case pin.Aggregator != "" && pin.Aggregator != a.aggHealthKey():
				req.Aggregator = pin.Aggregator
				return a.recognizeViaChildAggregator(req)
			case pin.Aggregator == "":
				req.Provider = pin.Provider
				req.Model = pin.Model
			}
			// pin.Aggregator == a.aggHealthKey(): the binding points at this
			// very aggregator — recognize from the local pool instead of
			// recursing.
		}
	}

	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		prompt = imageRecognizeDefaultPrompt
	}
	llmReq := domain.SendSessionMessageReq{
		SessionID: "image-recognize",
		Messages: []domain.ChatMessage{
			{
				Role: "user",
				Content: []domain.ContentBlock{
					{Type: domain.ContentBlockImage, ImageURL: req.Image},
					{Type: domain.ContentBlockText, Text: prompt},
				},
			},
		},
	}
	// Recognition wants a fast, grounded answer; reasoning effort adds latency
	// without improving fidelity (mirrors handleSummarize).
	llmReq.ThinkingBudget = 0
	llmReq.ReasoningEffort = ""

	recognizeCtx, cancel := context.WithTimeout(a.lifecycleCtx, imageRecognizeTimeout)
	defer cancel()

	triedIDs := make(map[string]bool)
	stopRetry := &stopRetryBudget{}
	emptyTextRotations := 1
	for {
		unit, selErr := a.selectVisionUnit(req, triedIDs)
		if selErr != nil {
			return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: %w", selErr)
		}
		triedIDs[unit.ID] = true

		tryUnit := *unit
		tryUnit.ReasoningEffort = ""
		stream, release, openErr := a.tryDispatchUnit(ctx, tryUnit, llmReq, recognizeCtx)
		if openErr != nil {
			classification := llmclient.ClassifyStreamOpenError(openErr)
			a.applyStreamOpenFailure(unit, openErr, classification)
			if llmclient.IsImageInputUnsupportedErr(openErr) {
				// Per-unit capability, not a health failure: record it and
				// rotate. The next selectVisionUnit scan excludes the unit
				// via both triedIDs and the no-image-input marker.
				a.markNoImageInput(*unit)
				ctx.Logger().Info("aiaggregator: recognition unit rejected image input, rotating",
					"unit", unit.ID, "provider", unit.ProviderName, "model", unit.Model)
				continue
			}
			if !classification.Rotatable && !stopRetry.consume(classification, openErr) {
				return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: stream open: %w", openErr)
			}
			continue
		}

		text, _, consumeErr := a.consumeSummarizeStream(stream, unit)
		release()
		if consumeErr != nil {
			return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: %w", consumeErr)
		}
		a.reportProviderSuccess(unit.ProviderName)
		llmclient.RecordSuccess(unit.ProviderName, unit.Model)
		if strings.TrimSpace(text) != "" {
			return domain.AIAggregatorImageRecognizeResp{
				Text:         text,
				Model:        unit.Model,
				ProviderName: unit.ProviderName,
			}, nil
		}

		// Completed with zero text. One rotation re-rolls on another
		// vision-capable unit; after that report instead of a silent empty
		// success (mirrors handleSummarize).
		noTextErr := fmt.Errorf("aiaggregator.image_recognize: model returned no text (unit=%s/%s)", unit.ProviderName, unit.Model)
		if emptyTextRotations == 0 {
			return domain.AIAggregatorImageRecognizeResp{}, noTextErr
		}
		emptyTextRotations--
	}
}

// selectVisionUnit scans the configured pool for a vision-capable chat unit,
// excluding units already tried in this recognition call. An explicit
// Provider/Model pin narrows the scan to the matching (provider, model) pair;
// vision capability still applies — a pinned model known or learned to reject
// image input yields "no vision-capable unit" rather than a doomed dispatch.
func (a *Actor) selectVisionUnit(req domain.AIAggregatorImageRecognizeReq, excludeIDs map[string]bool) (*CallableUnit, error) {
	a.mu.RLock()
	now := time.Now().Unix()
	var candidates []CallableUnit
	for i := range a.units {
		u := a.units[i]
		if u.Modality != "" && u.Modality != "chat" {
			continue // image/video generation units
		}
		if u.Disabled || unitInDisableWindow(u, now) {
			continue
		}
		if u.AggregatorID != "" || a.isSelfAggregatorRef(u) {
			continue // nested pools: vision capability unknown here
		}
		if excludeIDs[u.ID] {
			continue
		}
		if req.Provider != "" && u.ProviderName != req.Provider {
			continue
		}
		if req.Model != "" && u.Model != req.Model {
			continue
		}
		// a.mu is already held here; index the marker map directly instead of
		// unitLacksImageInput (whose nested RLock risks writer-intervened
		// deadlock).
		if a.noImageInput[noImageInputKey(u)] || !llmclient.ModelSupportsImageInput(u.Model) {
			continue
		}
		candidates = append(candidates, u)
	}
	a.mu.RUnlock()
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no vision-capable units available%s — add a vision model (e.g. gpt-4o / gemini flash / qwen-vl) to the aggregator pool", pinSuffix(req))
	}
	for _, u := range filterAvailableUnits(candidates, time.Now()) {
		cu := u
		return &cu, nil
	}
	return nil, fmt.Errorf("all vision-capable units are cooling down or disabled%s", pinSuffix(req))
}

func pinSuffix(req domain.AIAggregatorImageRecognizeReq) string {
	switch {
	case req.Aggregator != "":
		return fmt.Sprintf(" (aggregator %s)", req.Aggregator)
	case req.Provider != "" && req.Model != "":
		return fmt.Sprintf(" matching pin %s/%s", req.Provider, req.Model)
	case req.Provider != "":
		return fmt.Sprintf(" matching pin provider %s", req.Provider)
	case req.Model != "":
		return fmt.Sprintf(" matching pin model %s", req.Model)
	default:
		return ""
	}
}

// recognizeViaChildAggregator routes a recognition request to a named
// aggregator config's actor, which runs its own vision selection over its own
// pool. The child's self-reference check (req.Aggregator == its aggHealthKey)
// absorbs the hop — the target always executes locally rather than
// re-routing, so the chain terminates after one child.
func (a *Actor) recognizeViaChildAggregator(req domain.AIAggregatorImageRecognizeReq) (domain.AIAggregatorImageRecognizeResp, error) {
	childRef, err := a.resolveChildAggregatorRef(req.Aggregator)
	if err != nil {
		return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: %w", err)
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, imageRecognizeTimeout)
	defer cancel()
	call := childRef.Invoke(callCtx, "aiaggregator.image_recognize", req)
	result, err := call.Final(callCtx)
	_ = call.Close()
	if err != nil {
		return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: aggregator %q: %w", req.Aggregator, err)
	}
	switch r := result.(type) {
	case domain.AIAggregatorImageRecognizeResp:
		return r, nil
	case *domain.AIAggregatorImageRecognizeResp:
		if r != nil {
			return *r, nil
		}
		return domain.AIAggregatorImageRecognizeResp{}, nil
	default:
		body, merr := json.Marshal(result)
		if merr != nil {
			return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: aggregator %q: marshal: %w", req.Aggregator, merr)
		}
		var resp domain.AIAggregatorImageRecognizeResp
		if jerr := json.Unmarshal(body, &resp); jerr != nil {
			return domain.AIAggregatorImageRecognizeResp{}, fmt.Errorf("aiaggregator.image_recognize: aggregator %q: decode: %w", req.Aggregator, jerr)
		}
		return resp, nil
	}
}
