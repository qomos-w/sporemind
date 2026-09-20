package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient/mediagen"
	"github.com/qomos-w/sporemind/pkg/llmclient/videogen"
)

const (
	mediaGenerationTimeout = 10 * time.Minute
	nativeToolTimeout      = 15 * time.Minute
)

// handleVideoGenerate generates a video with the active video media account,
// saves it to <projectRoot>/assets/generated/ (domain.GeneratedAssetsDir), and
// returns the file path. When no media account is active it falls back to
// resolving a video unit through the system aggregator (mirroring the image
// path), so LLM providers with video models stay reachable.
func (a *Actor) handleVideoGenerate(ctx actor.PureContext, req gen.VideoGenerateReq) (string, error) {
	selection, mediaOk := a.activeMediaAccount(ctx, "video")
	if mediaOk && selection.Account.Kind != "" {
		account := selection.Account
		result, err := a.generateVideo(ctx, account, req)
		if err != nil {
			return "", fmt.Errorf("generate_video: %w", err)
		}
		return a.saveGeneratedMedia(ctx, "video", result.data, result.mimeType)
	}

	// No active media account: resolve a video unit via the system aggregator.
	// The user's media-page provider-model binding (when set) narrows the
	// resolve to the selected (Provider, Model) pair; an explicit Provider/Model
	// in the tool request still wins.
	aggRef := a.cachedAggRef(ctx, systemAggID)
	if aggRef == nil {
		return "", fmt.Errorf("generate_video: no active video media account configured and aggregator not available")
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", fmt.Errorf("generate_video: planner not available")
	}
	resolveReq := gen.AIAggregatorVideoResolveReq{
		Provider: firstNonEmpty(req.Provider, selection.BoundProvider),
		Model:    firstNonEmpty(req.Model, selection.BoundModel),
	}
	resolveCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	result, err := planner.Call(resolveCtx, aggRef, "aiaggregator.video_resolve", resolveReq).Await()
	cancel()
	if err != nil {
		return "", fmt.Errorf("generate_video: resolve video unit: %w", err)
	}
	resolved, err := decodeVideoResolve(result)
	if err != nil {
		return "", fmt.Errorf("generate_video: %w", err)
	}
	if resolved.Model == "" {
		return "", fmt.Errorf("generate_video: no active video media account configured and no video-generation model available — add a media account or a provider with a video model (e.g. veo or seedance)")
	}

	// Route by the resolved provider protocol: ark → Seedance backend, gemini →
	// Veo backend, everything else is rejected by the videogen dispatcher
	// with a clear error instead of a silent wrong-backend call.
	videoCtx, videoCancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer videoCancel()
	vidResult, err := videogen.Generate(videoCtx, videogen.Params{
		Prompt:          req.Prompt,
		Model:           firstNonEmpty(req.Model, resolved.Model),
		Endpoint:        resolved.Endpoint,
		Protocol:        resolved.Protocol,
		AuthToken:       resolved.AuthToken,
		UserAgent:       resolved.UserAgent,
		Proxy:           resolved.Proxy,
		Duration:        parseDurationSeconds(req.Duration),
		AspectRatio:     req.AspectRatio,
		ReferenceImages: videoReferenceImages(req),
		ReferenceVideos: req.ReferenceVideos,
		ReferenceAudios: req.ReferenceAudios,
	})
	if err != nil {
		return "", fmt.Errorf("generate_video: %w", err)
	}
	return a.saveGeneratedMedia(ctx, "video", vidResult.Data, vidResult.MimeType)
}

// generateVideoResult is the provider-agnostic artifact pair produced by either
// the GPT-compatible or the native Gemini video backend.
type generateVideoResult struct {
	data     []byte
	mimeType string
}

// generateVideo routes the generation call by the account provider: gemini
// accounts use the native Veo long-running operation flow, ark accounts the
// Seedance async-task flow, qwen accounts the DashScope Wan async-task flow;
// every other provider uses the GPT-compatible mediagen client.
func (a *Actor) generateVideo(ctx actor.PureContext, account gen.MediaAccount, req gen.VideoGenerateReq) (*generateVideoResult, error) {
	if isGeminiProvider(account.Provider) {
		result, err := generateGeminiVideo(ctx, videogen.Params{
			Prompt:      req.Prompt,
			Model:       firstNonEmpty(req.Model, account.Model),
			Endpoint:    account.BaseURL,
			AuthToken:   account.APIKey,
			Duration:    parseDurationSeconds(req.Duration),
			AspectRatio: req.AspectRatio,
			Proxy:       account.Proxy,
		})
		if err != nil {
			return nil, err
		}
		return &generateVideoResult{data: result.Data, mimeType: result.MimeType}, nil
	}
	if isArkProvider(account.Provider) {
		result, err := generateArkVideo(ctx, videogen.Params{
			Prompt:          req.Prompt,
			Model:           firstNonEmpty(req.Model, account.Model),
			Endpoint:        account.BaseURL,
			AuthToken:       account.APIKey,
			Duration:        parseDurationSeconds(req.Duration),
			AspectRatio:     req.AspectRatio,
			ReferenceImages: videoReferenceImages(req),
			ReferenceVideos: req.ReferenceVideos,
			ReferenceAudios: req.ReferenceAudios,
			// Seedance 2.0 generates audio natively; the agent default enables
			// it and keeps the watermark off.
			GenerateAudio: true,
			Watermark:     false,
			Proxy:         account.Proxy,
		})
		if err != nil {
			return nil, err
		}
		return &generateVideoResult{data: result.Data, mimeType: result.MimeType}, nil
	}
	if isQwenProvider(account.Provider) {
		result, err := generateQwenVideo(ctx, videogen.Params{
			Prompt:          req.Prompt,
			Model:           firstNonEmpty(req.Model, account.Model),
			Endpoint:        account.BaseURL,
			AuthToken:       account.APIKey,
			Duration:        parseDurationSeconds(req.Duration),
			AspectRatio:     req.AspectRatio,
			ReferenceImages: videoReferenceImages(req),
			ReferenceAudios: req.ReferenceAudios,
			// The Wan video API has no reference-video input, so
			// req.ReferenceVideos is deliberately not forwarded; Wan 2.5
			// auto-dubs the audio track, so no audio toggle is sent either.
			Watermark: false,
			Proxy:     account.Proxy,
		})
		if err != nil {
			return nil, err
		}
		return &generateVideoResult{data: result.Data, mimeType: result.MimeType}, nil
	}
	result, err := generateMedia(ctx, mediagen.Params{
		Kind:            mediagen.KindVideo,
		Prompt:          req.Prompt,
		Model:           firstNonEmpty(req.Model, account.Model),
		Endpoint:        account.BaseURL,
		AuthToken:       account.APIKey,
		Duration:        parseDurationSeconds(req.Duration),
		AspectRatio:     req.AspectRatio,
		ReferenceImages: req.ReferenceImages,
		ReferenceAudios: req.ReferenceAudios,
		Proxy:           account.Proxy,
	})
	if err != nil {
		return nil, err
	}
	return &generateVideoResult{data: result.Data, mimeType: result.MimeType}, nil
}

// activeMediaAccount returns the media service's active selection for a kind:
// either an active account (resp.Account non-zero) or the user-selected
// provider-model binding (resp.BoundProvider/BoundModel). ok is false when the
// media service is unreachable; a zero resp with ok=true means "nothing
// configured", letting the caller fall back to the aggregator first-match.
func (a *Actor) activeMediaAccount(ctx actor.PureContext, kind string) (gen.MediaActiveAccountResp, bool) {
	planner := ctx.Planner()
	if planner == nil {
		return gen.MediaActiveAccountResp{}, false
	}
	mediaRef, ok := ctx.LookupService("media")
	if !ok {
		return gen.MediaActiveAccountResp{}, false
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, mediaRef, "media.active_account", gen.MediaActiveAccountReq{Kind: kind}).Await()
	if err != nil {
		return gen.MediaActiveAccountResp{}, false
	}
	return decodeMediaActiveAccount(result), true
}

func decodeMediaActiveAccount(result any) gen.MediaActiveAccountResp {
	if response, ok := result.(gen.MediaActiveAccountResp); ok {
		return response
	}
	body, _ := json.Marshal(result)
	var response gen.MediaActiveAccountResp
	_ = json.Unmarshal(body, &response)
	return response
}

func generateMedia(ctx actor.PureContext, params mediagen.Params) (*mediagen.Result, error) {
	mediaCtx, cancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer cancel()
	return mediagen.Generate(mediaCtx, params)
}

// generateGeminiVideo bounds one native Veo generation call by the media
// generation timeout.
func generateGeminiVideo(ctx actor.PureContext, params videogen.Params) (*videogen.Result, error) {
	videoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer cancel()
	return videogen.GenerateGemini(videoCtx, params)
}

// generateArkVideo bounds one Ark (Seedance) generation call by the media
// generation timeout.
func generateArkVideo(ctx actor.PureContext, params videogen.Params) (*videogen.Result, error) {
	videoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer cancel()
	return videogen.GenerateArk(videoCtx, params)
}

// generateQwenVideo bounds one DashScope (Wan) generation call by the media
// generation timeout.
func generateQwenVideo(ctx actor.PureContext, params videogen.Params) (*videogen.Result, error) {
	videoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), mediaGenerationTimeout)
	defer cancel()
	return videogen.GenerateQwen(videoCtx, params)
}

func (a *Actor) saveGeneratedMedia(ctx actor.PureContext, prefix string, data []byte, mimeType string) (string, error) {
	projectRoot := a.resolveProjectRootPure(ctx)
	if projectRoot == "" {
		return "", fmt.Errorf("generate_%s: project root not available", prefix)
	}
	dir := domain.GeneratedAssetsPath(projectRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("generate_%s: mkdir: %w", prefix, err)
	}
	ext := mediaExtension(mimeType, prefix)
	path := filepath.Join(dir, fmt.Sprintf("%s_%d.%s", prefix, time.Now().UnixMilli(), ext))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("generate_%s: write file: %w", prefix, err)
	}
	return path, nil
}

// resolveProjectRootPure is the stateless-handler variant of resolveProjectRoot
// (agent_config.go, owned by the oracle/glassinteract non-blocking card). It
// reads the same atomic project-root cache and falls back to a parent
// project.info query under the PureContext lifecycle. It exists because the
// agent_config.go variant takes actor.Context, which a PureContext handler
// cannot fabricate; keep the two in sync when resolveProjectRoot evolves.
func (a *Actor) resolveProjectRootPure(ctx actor.PureContext) string {
	if cached := a.cachedProjectRoot.Load(); cached != nil {
		return *cached
	}
	parent := ctx.Parent()
	if parent == nil {
		return ""
	}
	planner := ctx.Planner()
	if planner == nil {
		return ""
	}
	infoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(infoCtx, parent, "project.info", nil).Await()
	if err != nil || result == nil {
		return ""
	}
	info := decodeProjectInfo(result)
	root := ""
	if len(info.Roots) > 0 {
		root = info.Roots[0].Path
	}
	a.cachedProjectRoot.Store(&root)
	return root
}

func mediaExtension(mimeType, prefix string) string {
	if extensions, _ := mime.ExtensionsByType(mimeType); len(extensions) > 0 {
		if ext := strings.TrimPrefix(extensions[0], "."); ext != "" {
			return ext
		}
	}
	if prefix == "video" {
		return "mp4"
	}
	return "png"
}

func parseDurationSeconds(value string) int {
	value = strings.TrimSpace(strings.TrimSuffix(value, "s"))
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 {
		return 0
	}
	return seconds
}

func firstNonEmpty(first, fallback string) string {
	if first != "" {
		return first
	}
	return fallback
}

// isGeminiProvider reports whether a media account routes to the native Gemini
// backend (imagegen/videogen). The provider name is free-form user text, so the
// comparison is case-insensitive and space-trimmed; every other provider goes
// through the GPT-compatible mediagen client.
func isGeminiProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "gemini")
}

// isMiniMaxProvider reports whether a media account routes to the native
// MiniMax image backend (imagegen protocol "minimax"): its /v1/image_generation
// API is not GPT-compatible, so it must not ride the mediagen client.
func isMiniMaxProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "minimax")
}

// isQwenProvider reports whether a media account routes to the native Qwen
// image backend (imagegen protocol "qwen"): its DashScope task API is not the
// GPT-compatible images wire, so it must not ride the mediagen client. The
// provider name is free-form user text, so the comparison is case-insensitive
// and space-trimmed; "dashscope" and "bailian" are accepted aliases for the
// same platform.
func isQwenProvider(provider string) bool {
	provider = strings.TrimSpace(provider)
	switch {
	case strings.EqualFold(provider, "qwen"),
		strings.EqualFold(provider, "dashscope"),
		strings.EqualFold(provider, "bailian"):
		return true
	}
	return false
}

// isArkProvider reports whether a media account routes to the Ark (Seedance)
// backend (Volcengine mainland / BytePlus overseas). The provider name is
// free-form user text, so the comparison is case-insensitive and
// space-trimmed against the accepted aliases ("ark", "volcengine", "byteplus",
// "doubao" — the last is the brand-facing id the media panel exposes for
// Doubao/Seedance 2.0, which is the same Ark backend).
func isArkProvider(provider string) bool {
	provider = strings.TrimSpace(provider)
	switch {
	case strings.EqualFold(provider, "ark"),
		strings.EqualFold(provider, "volcengine"),
		strings.EqualFold(provider, "doubao"),
		strings.EqualFold(provider, "byteplus"):
		return true
	}
	return false
}

// isDoubaoImageProvider reports whether a media account routes to the native
// Doubao (Volcengine Ark) image backend (imagegen protocol "doubao"): Its
// images/generations surface is not GPT-compatible in the parameters that
// matter (size, watermark, error shape), so it must not ride the mediagen
// client. The aliases overlap with isArkProvider because the provider is the
// same Ark platform; only the *modality* differs — images ride the synchronous
// images/generations API, video rides the asynchronous Seedance task API.
func isDoubaoImageProvider(provider string) bool {
	provider = strings.TrimSpace(provider)
	switch {
	case strings.EqualFold(provider, "doubao"),
		strings.EqualFold(provider, "imagex"),
		strings.EqualFold(provider, "volcengine"),
		strings.EqualFold(provider, "byteplus"):
		return true
	}
	return false
}

// isURLRef reports whether a reference is an http(s) URL that should pass
// through to the downstream endpoint untouched instead of being read as a file.
func isURLRef(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// resolveInputImage converts a path/URL/base64 InputImage into the base64 form
// required by the image-generation backends. A readable file path is read and
// base64-encoded so a compact path can travel in the tool_use history without
// embedding the payload; URLs and raw base64 pass through unchanged.
func resolveInputImage(input string) string {
	if input == "" || isURLRef(input) {
		return input
	}
	if data, err := os.ReadFile(input); err == nil {
		return base64.StdEncoding.EncodeToString(data)
	}
	return input
}

// inputImageReference converts a single reference image into the list expected
// by mediagen. It accepts a file path, a URL, or raw base64 (matching the
// generate_video ReferenceImages contract "asset paths, URLs, or base64");
// paths are read and base64-injected so they can travel in the JSON image_urls
// field, while URLs and base64 pass through unchanged.
func inputImageReference(input string) []string {
	if input == "" {
		return nil
	}
	return []string{resolveInputImage(input)}
}

// videoReferenceImages resolves every video ReferenceImages entry through
// resolveInputImage (file paths become base64 so a compact path can travel in
// the tool_use history, URLs and raw base64 pass through) for the Ark and
// mediagen backends.
func videoReferenceImages(req gen.VideoGenerateReq) []string {
	refs := make([]string, 0, len(req.ReferenceImages))
	for _, ref := range req.ReferenceImages {
		refs = append(refs, resolveInputImage(ref))
	}
	return refs
}

// decodeVideoResolve decodes the planner result of aiaggregator.video_resolve
// into the typed response. Decode errors are surfaced so a malformed upstream
// result does not masquerade as "no video-generation model available".
func decodeVideoResolve(result any) (gen.AIAggregatorVideoResolveResp, error) {
	if r, ok := result.(gen.AIAggregatorVideoResolveResp); ok {
		return r, nil
	}
	body, err := json.Marshal(result)
	if err != nil {
		return gen.AIAggregatorVideoResolveResp{}, fmt.Errorf("decode video resolve result: %w", err)
	}
	var resp gen.AIAggregatorVideoResolveResp
	if err := json.Unmarshal(body, &resp); err != nil {
		return gen.AIAggregatorVideoResolveResp{}, fmt.Errorf("decode video resolve result: %w", err)
	}
	return resp, nil
}
