package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient/imagegen"
	"github.com/qomos-w/sporemind/pkg/llmclient/mediagen"
)

// handleImageGenerate generates an image via the active "image" media account
// when one exists, otherwise falls back to resolving an image unit from the
// system aggregator and calling the provider's image API. The result is saved
// to <projectRoot>/assets/generated/ (domain.GeneratedAssetsDir) and the file
// path is returned.
func (a *Actor) handleImageGenerate(ctx actor.PureContext, req gen.ImageGenerateReq) (string, error) {
	selection, mediaOk := a.activeMediaAccount(ctx, "image")
	if mediaOk && selection.Account.Kind != "" {
		account := selection.Account
		model := firstNonEmpty(req.Model, account.Model)
		var data []byte
		var mimeType string
		if isGeminiProvider(account.Provider) {
			imgCtx, imgCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
			defer imgCancel()
			result, err := imagegen.Generate(imgCtx, imagegen.Params{
				Prompt:          req.Prompt,
				Model:           model,
				Endpoint:        account.BaseURL,
				Protocol:        "gemini",
				AuthToken:       account.APIKey,
				Size:            req.Size,
				AspectRatio:     req.AspectRatio,
				ReferenceImages: referenceImagesForImage(req),
				Proxy:           account.Proxy,
			})
			if err != nil {
				return "", fmt.Errorf("generate_image: account provider %q: %w", account.Provider, err)
			}
			data, mimeType = result.Data, result.MimeType
		} else if isMiniMaxProvider(account.Provider) {
			imgCtx, imgCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
			defer imgCancel()
			result, err := imagegen.Generate(imgCtx, imagegen.Params{
				Prompt:          req.Prompt,
				Model:           model,
				Endpoint:        account.BaseURL,
				Protocol:        "minimax",
				AuthToken:       account.APIKey,
				Size:            req.Size,
				AspectRatio:     req.AspectRatio,
				ReferenceImages: referenceImagesForImage(req),
				Proxy:           account.Proxy,
			})
			if err != nil {
				return "", fmt.Errorf("generate_image: account provider %q: %w", account.Provider, err)
			}
			data, mimeType = result.Data, result.MimeType
		} else if isDoubaoImageProvider(account.Provider) {
			imgCtx, imgCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
			defer imgCancel()
			result, err := imagegen.Generate(imgCtx, imagegen.Params{
				Prompt:          req.Prompt,
				Model:           model,
				Endpoint:        account.BaseURL,
				Protocol:        "doubao",
				AuthToken:       account.APIKey,
				Size:            req.Size,
				AspectRatio:     req.AspectRatio,
				ReferenceImages: referenceImagesForImage(req),
				Proxy:           account.Proxy,
			})
			if err != nil {
				return "", fmt.Errorf("generate_image: account provider %q: %w", account.Provider, err)
			}
			data, mimeType = result.Data, result.MimeType
		} else if isQwenProvider(account.Provider) {
			imgCtx, imgCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
			defer imgCancel()
			result, err := imagegen.Generate(imgCtx, imagegen.Params{
				Prompt:          req.Prompt,
				Model:           model,
				Endpoint:        account.BaseURL,
				Protocol:        "qwen",
				AuthToken:       account.APIKey,
				Size:            req.Size,
				AspectRatio:     req.AspectRatio,
				ReferenceImages: referenceImagesForImage(req),
				Proxy:           account.Proxy,
			})
			if err != nil {
				return "", fmt.Errorf("generate_image: account provider %q: %w", account.Provider, err)
			}
			data, mimeType = result.Data, result.MimeType
		} else {
			result, err := generateMedia(ctx, mediagen.Params{
				Kind:            mediagen.KindImage,
				Prompt:          req.Prompt,
				Model:           model,
				Endpoint:        account.BaseURL,
				AuthToken:       account.APIKey,
				Size:            mediaImageSize(req.Size, req.AspectRatio),
				AspectRatio:     req.AspectRatio,
				ReferenceImages: referenceImagesForImage(req),
				Proxy:           account.Proxy,
			})
			if err != nil {
				return "", fmt.Errorf("generate_image: account provider %q: %w", account.Provider, err)
			}
			data, mimeType = result.Data, result.MimeType
		}
		return a.saveGeneratedMedia(ctx, "img", data, mimeType)
	}

	// No active media account is available, so resolve an image unit through
	// the system aggregator as the backwards-compatible fallback.
	aggRef := a.cachedAggRef(ctx, systemAggID)
	if aggRef == nil {
		return "", fmt.Errorf("generate_image: aggregator not available")
	}

	// 1. Resolve image unit + auth token via the aggregator.
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("generate_image: planner not available")
	}
	resolveReq := gen.AIAggregatorImageResolveReq{
		// The user's media-page provider-model binding (when set) narrows the
		// resolve to the selected (Provider, Model) pair; an explicit
		// Provider/Model in the tool request still wins.
		Provider: firstNonEmpty(req.Provider, selection.BoundProvider),
		Model:    firstNonEmpty(req.Model, selection.BoundModel),
	}
	resolveCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(resolveCtx, aggRef, "aiaggregator.image_resolve", resolveReq).Await()
	cancel()
	if err != nil {
		return "", fmt.Errorf("generate_image: resolve image unit: %w", err)
	}

	resolved, err := decodeImageResolve(result)
	if err != nil {
		return "", fmt.Errorf("generate_image: %w", err)
	}
	if resolved.Model == "" {
		return "", fmt.Errorf("generate_image: no image-generation model configured — add a provider with an image model (e.g. gpt-image-2 or gemini-3.1-flash-image-preview)")
	}

	// 2. Call the image generation API. The budget matches the media-account
	// path (mediaGenerationTimeout) so a long-running 4K request is not cut
	// short by a narrower fallback budget.
	imgCtx, imgCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer imgCancel()
	imgResult, err := imagegen.Generate(imgCtx, imagegen.Params{
		Prompt:          req.Prompt,
		Model:           resolved.Model,
		Endpoint:        resolved.Endpoint,
		Protocol:        resolved.Protocol,
		AuthToken:       resolved.AuthToken,
		UserAgent:       resolved.UserAgent,
		Proxy:           resolved.Proxy,
		Quality:         req.Quality,
		Size:            req.Size,
		AspectRatio:     req.AspectRatio,
		ReferenceImages: referenceImagesForImage(req),
	})
	if err != nil {
		return "", fmt.Errorf("generate_image: provider %q: %w", resolved.ProviderName, err)
	}

	return a.saveGeneratedMedia(ctx, "img", imgResult.Data, imgResult.MimeType)
}

// handleImageRecognize sends an image to a vision-capable model (resolved via
// an aggregator) and returns the model's textual description, so a text-only
// primary model can act on image content. The optional Aggregator names the
// aggregator pool to use (empty = system); Provider/Model pin a specific
// vision unit inside that pool.
func (a *Actor) handleImageRecognize(ctx actor.PureContext, req gen.ImageRecognizeReq) (string, error) {
	imageRef := recognizeImageRef(req.Image)
	if imageRef == "" {
		return "", fmt.Errorf("recognize_image: Image is required")
	}
	if strings.HasPrefix(req.Image, "msg:") {
		resolved, ok := a.imageFromMessage(strings.TrimPrefix(req.Image, "msg:"))
		if !ok {
			return "", fmt.Errorf("recognize_image: no image found for reference %q", req.Image)
		}
		imageRef = resolved
	}

	aggID := req.Aggregator
	if aggID == "" {
		aggID = systemAggID
	}
	aggRef := a.cachedAggRef(ctx, aggID)
	if aggRef == nil {
		return "", fmt.Errorf("recognize_image: aggregator %q not available", aggID)
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("recognize_image: planner not available")
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.ToolCallTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, aggRef, "aiaggregator.image_recognize", gen.AIAggregatorImageRecognizeReq{
		Image:    imageRef,
		Prompt:   req.Prompt,
		Provider: req.Provider,
		Model:    req.Model,
	}).Await()
	if err != nil {
		return "", fmt.Errorf("recognize_image: %w", err)
	}

	resp, err := decodeImageRecognize(result)
	if err != nil {
		return "", fmt.Errorf("recognize_image: %w", err)
	}
	return resp.Text, nil
}

// decodeImageRecognize decodes the planner result of
// aiaggregator.image_recognize into the typed response. Decode errors are
// surfaced so a malformed upstream result does not masquerade as an empty
// recognition.
func decodeImageRecognize(result any) (gen.AIAggregatorImageRecognizeResp, error) {
	if r, ok := result.(gen.AIAggregatorImageRecognizeResp); ok {
		return r, nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return gen.AIAggregatorImageRecognizeResp{}, fmt.Errorf("decode image recognize result: %w", err)
	}
	var resp gen.AIAggregatorImageRecognizeResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return gen.AIAggregatorImageRecognizeResp{}, fmt.Errorf("decode image recognize result: %w", err)
	}
	return resp, nil
}

// recognizeImageRef normalizes the tool's Image field (file path, URL, or raw
// base64) into a vision content reference: http(s) and data URLs pass through;
// file bytes and raw base64 become a data URL with a sniffed mime type, as
// required by the image content block wire format.
func recognizeImageRef(input string) string {
	if strings.HasPrefix(input, "msg:") {
		return input // resolved against persisted steps by the actor, not here
	}
	if input == "" || isURLRef(input) || strings.HasPrefix(input, "data:") {
		return input
	}
	if data, err := os.ReadFile(input); err == nil {
		return dataImageURL(data)
	}
	// Not a readable file: treat as raw base64. Sniff the mime from the
	// decoded prefix without materializing the whole payload.
	prefix, err := io.ReadAll(io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(input)), 512))
	if err != nil || len(prefix) == 0 {
		return input
	}
	return "data:" + http.DetectContentType(prefix) + ";base64," + input
}

// dataImageURL builds a data URL from raw image bytes.
func dataImageURL(data []byte) string {
	return "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// imageFromMessage resolves a "msg:<id>" / "msg:<id>:<blockIdx>" reference —
// the handle the engine attaches to recognized-image text so a text-only
// primary can re-recognize a conversation image (paste or screenshot) with a
// task-specific prompt — to the image block's data URL in the persisted step
// with that ID. Without an index the first image block resolves (single-image
// messages); the index disambiguates multi-image messages.
func (a *Actor) imageFromMessage(ref string) (string, bool) {
	stepID, idx := ref, -1
	if i := strings.LastIndexByte(ref, ':'); i >= 0 {
		if n, err := strconv.Atoi(ref[i+1:]); err == nil && n >= 0 {
			stepID, idx = ref[:i], n
		}
	}
	for i := range a.steps {
		if a.steps[i].ID != stepID {
			continue
		}
		if idx < 0 {
			for _, b := range a.steps[i].Content {
				if b.Type == domain.ContentBlockImage && b.ImageURL != "" {
					return b.ImageURL, true
				}
			}
			return "", false
		}
		if idx < len(a.steps[i].Content) {
			b := a.steps[i].Content[idx]
			if b.Type == domain.ContentBlockImage && b.ImageURL != "" {
				return b.ImageURL, true
			}
		}
		return "", false
	}
	return "", false
}

// referenceImagesForImage merges the single-image InputImage (backwards
// compatible, first position) with the multi-reference ReferenceImages list
// into one normalized list. Every entry goes through resolveInputImage: file
// paths are read and base64-encoded, URLs and raw base64 pass through. All
// three generation paths (media gemini, media non-gemini, aggregator) feed
// this list to their Params.ReferenceImages.
func referenceImagesForImage(req gen.ImageGenerateReq) []string {
	refs := make([]string, 0, len(req.ReferenceImages)+1)
	refs = append(refs, inputImageReference(req.InputImage)...)
	for _, ref := range req.ReferenceImages {
		refs = append(refs, resolveInputImage(ref))
	}
	return refs
}

// decodeImageResolve decodes the planner result of aiaggregator.image_resolve
// into the typed response. Decode errors are surfaced so a malformed upstream
// result does not masquerade as "no image-generation model configured".
func decodeImageResolve(result any) (gen.AIAggregatorImageResolveResp, error) {
	if r, ok := result.(gen.AIAggregatorImageResolveResp); ok {
		return r, nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return gen.AIAggregatorImageResolveResp{}, fmt.Errorf("decode image resolve result: %w", err)
	}
	var resp gen.AIAggregatorImageResolveResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return gen.AIAggregatorImageResolveResp{}, fmt.Errorf("decode image resolve result: %w", err)
	}
	return resp, nil
}

// mediaImageSize maps the relative Size budget ("1K"|"2K"|"4K") to a concrete
// "WxH" dimension for GPT-compatible media endpoints. It mirrors imagegen's
// mapOpenAISize: gpt-image-class endpoints only support 1024x1024, 1536x1024
// and 1024x1536 (plus "auto"), so 2K and 4K both map to the largest legal size
// for the requested orientation. Aspect ratio applies at every size budget —
// GLM-Image accepts any 512–2048 multiple-of-32 dimensions, so 16:9/9:16 map
// to landscape/portrait regardless of the budget. This keeps
// openai_custom/glm media accounts from receiving an illegal "2K"/"4K" size
// and failing with a 400. Aspect ratios snap to the nearest discrete
// orientation (landscape group → 1536x1024, portrait group → 1024x1536).
func mediaImageSize(size, aspect string) string {
	landscape := aspect == "16:9" || aspect == "3:2" || aspect == "4:3" || aspect == "5:4" || aspect == "21:9"
	portrait := aspect == "9:16" || aspect == "2:3" || aspect == "3:4" || aspect == "4:5"
	if landscape {
		return "1536x1024"
	}
	if portrait {
		return "1024x1536"
	}
	return "1024x1024"
}

// screenshotMime maps a computeruse screenshot EncodeFormat to a MIME type.
// Unknown formats default to JPEG, matching the handler's default encoder.
func screenshotMime(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// screenshotObservation converts a computeruse.screenshot response into the
// LLM-facing pair: a compact metadata tool_result text (no base64 payload)
// plus a user-role observation message carrying the image as a data-URL
// ContentBlockImage — the exact same embedding shape as user-submitted chat
// images, so vision primaries see it via the image_url path while text-only
// primaries get it through the one-shot recognition pass. Returns a nil
// observation when the response carries no image bytes.
func screenshotObservation(resp domain.ComputerUseScreenshotResp) (string, *domain.ChatMessage) {
	if len(resp.ImageBytes) == 0 || resp.Unchanged {
		return "", nil
	}
	mime := screenshotMime(resp.Format)
	meta := struct {
		Format      string `json:"Format"`
		Width       int32  `json:"Width"`
		Height      int32  `json:"Height"`
		CursorX     int32  `json:"CursorX,omitempty"`
		CursorY     int32  `json:"CursorY,omitempty"`
		DisplayName string `json:"DisplayName,omitempty"`
		Message     string `json:"Message,omitempty"`
		Note        string `json:"Note"`
	}{
		Format:      resp.Format,
		Width:       resp.Width,
		Height:      resp.Height,
		CursorX:     resp.CursorX,
		CursorY:     resp.CursorY,
		DisplayName: resp.DisplayName,
		Message:     resp.Message,
		Note:        "screenshot image is attached in the following message",
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		metaJSON = []byte(`{"Note":"screenshot image is attached in the following message"}`)
	}
	obs := &domain.ChatMessage{
		Role: domain.ChatRoleUser,
		Content: []domain.ContentBlock{
			{Type: domain.ContentBlockText, Text: fmt.Sprintf("[computeruse.screenshot %dx%d %s]", resp.Width, resp.Height, resp.Format)},
			{
				Type:     domain.ContentBlockImage,
				ImageURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(resp.ImageBytes),
				MimeType: mime,
			},
		},
	}
	return string(metaJSON), obs
}
