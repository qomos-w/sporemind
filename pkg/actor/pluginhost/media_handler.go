package pluginhost

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

	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient/imagegen"
	"github.com/qomos-w/sporemind/pkg/llmclient/mediagen"
	"github.com/qomos-w/sporemind/pkg/llmclient/videogen"

	"github.com/qomos-w/gospore/ref"
)

// Media generation local host services (image.generate / video.generate).
//
// The host mediates the whole flow so credentials never cross the bridge:
// it resolves the serving unit (pinned Provider+Model via the aggregator, or
// the kind's active media account, or aggregator auto-pick), runs the
// provider HTTP call itself, and writes the artifact into the calling app's
// media/ runtime directory — served same-origin by the plugin's own listener
// (the app dir is also the SDK static root pushed via OnLoad config). The
// plugin only ever sees AppMediaGenResp (path + mime + size + provenance).
//
// Unit semantics mirror llm.complete: Provider+Model both set pins the unit
// strictly (resolve failure errors out, no soft fallback); both empty uses
// the kind's active media account then aggregator auto-pick; model-only is
// rejected (Unit is the only selection primitive — no naked models).
//
// Reference inputs are URLs or base64 ONLY: the agent tool also accepts local
// file paths (it runs with the user's project FS), but a plugin path must not
// become an undeclared fs.read — bare local paths are rejected explicitly.

// mediaKindImage / mediaKindVideo name the generation kinds and the media
// account Kind values they consult.
const (
	mediaKindImage = "image"
	mediaKindVideo = "video"
)

// handleMediaGenerate serves one image.generate / video.generate local host
// call. pluginID comes from the bridge-injected Plugin field (the handler
// never trusts a plugin-supplied ID); payload is the raw request JSON.
func (a *Actor) handleMediaGenerate(callID string, pluginID string, payload []byte) ([]byte, error) {
	budget := appbinding.HostCallBudget(callID)
	if budget <= 0 {
		budget = hostBridgeInvokeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()

	switch callID {
	case "image.generate":
		return a.generateMediaFor(ctx, callID, mediaKindImage, pluginID, payload)
	case "video.generate":
		return a.generateMediaFor(ctx, callID, mediaKindVideo, pluginID, payload)
	default:
		return nil, fmt.Errorf("pluginhost: unknown media callID %q", callID)
	}
}

// mediaGenRequest is the wire shape both generate callIDs share: the typed
// request fields plus the bridge-injected Plugin identity.
type mediaGenRequest struct {
	Plugin   string `json:"Plugin"`
	Prompt   string `json:"Prompt"`
	Provider string `json:"Provider,omitempty"`
	Model    string `json:"Model,omitempty"`

	// image fields
	Quality         string   `json:"Quality,omitempty"`
	Size            string   `json:"Size,omitempty"`
	AspectRatio     string   `json:"AspectRatio,omitempty"`
	InputImage      string   `json:"InputImage,omitempty"`
	ReferenceImages []string `json:"ReferenceImages,omitempty"`

	// video fields
	Duration        string   `json:"Duration,omitempty"`
	ReferenceVideos []string `json:"ReferenceVideos,omitempty"`
	ReferenceAudios []string `json:"ReferenceAudios,omitempty"`
}

// generateMediaFor runs resolve → generate → persist for one kind.
func (a *Actor) generateMediaFor(ctx context.Context, callID, kind, pluginID string, payload []byte) ([]byte, error) {
	var req mediaGenRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, fmt.Errorf("pluginhost: %s decode: %w", callID, err)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("pluginhost: %s: Prompt is required", callID)
	}
	if pluginID == "" {
		return nil, fmt.Errorf("pluginhost: %s: calling plugin unknown", callID)
	}

	pinned := req.Provider != "" && req.Model != ""
	if !pinned && req.Model != "" && req.Provider == "" {
		return nil, fmt.Errorf("pluginhost: %s: model-only selection is not a unit — specify both Provider and Model, or neither (active account / auto-pick)", callID)
	}

	refs, err := normalizeMediaRefs(kind, req)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", callID, err)
	}

	var data []byte
	var mimeType, provider, model string
	if pinned {
		resolved, rerr := a.resolveMediaUnit(ctx, kind, req.Provider, req.Model)
		if rerr != nil {
			return nil, fmt.Errorf("pluginhost: %s: resolve pinned unit %s/%s: %w", callID, req.Provider, req.Model, rerr)
		}
		data, mimeType, provider, model, err = a.dispatchMediaGeneration(ctx, kind, mediaUnit{
			Endpoint: resolved.Endpoint, Protocol: resolved.Protocol,
			AuthToken: resolved.AuthToken, UserAgent: resolved.UserAgent, Proxy: resolved.Proxy,
			Model: resolved.Model, Provider: resolved.ProviderName,
		}, req, refs)
		if err != nil {
			return nil, fmt.Errorf("pluginhost: %s: %w", callID, err)
		}
	} else {
		account, ok := a.activeMediaAccountFor(ctx, kind)
		if ok && account.Account.Kind != "" {
			data, mimeType, provider, model, err = a.dispatchMediaGeneration(ctx, kind, mediaUnit{
				Endpoint: account.Account.BaseURL, AuthToken: account.Account.APIKey,
				Proxy: account.Account.Proxy, Model: firstNonEmptyMedia(req.Model, account.Account.Model),
				Provider: account.Account.Provider, // protocol inferred from provider name below
			}, req, refs)
			if err != nil {
				return nil, fmt.Errorf("pluginhost: %s: %w", callID, err)
			}
		} else {
			// Mirror the agent tool (agent_image.go): with no active media
			// account the user's media-page provider-model binding narrows
			// the pool auto-pick — otherwise pool order decides silently.
			resolved, rerr := a.resolveMediaUnit(ctx, kind, account.BoundProvider, account.BoundModel)
			if rerr != nil {
				return nil, fmt.Errorf("pluginhost: %s: no active %s media account and unit resolve failed: %w", callID, kind, rerr)
			}
			if resolved.Model == "" {
				return nil, fmt.Errorf("pluginhost: %s: no active %s media account configured and no %s-generation unit available — add a media account or a provider unit", callID, kind, kind)
			}
			data, mimeType, provider, model, err = a.dispatchMediaGeneration(ctx, kind, mediaUnit{
				Endpoint: resolved.Endpoint, Protocol: resolved.Protocol,
				AuthToken: resolved.AuthToken, UserAgent: resolved.UserAgent, Proxy: resolved.Proxy,
				Model: resolved.Model, Provider: resolved.ProviderName,
			}, req, refs)
			if err != nil {
				return nil, fmt.Errorf("pluginhost: %s: %w", callID, err)
			}
		}
	}

	rel, err := a.saveAppMedia(pluginID, kind, data, mimeType)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: %w", callID, err)
	}
	resp := gen.AppMediaGenResp{
		Path:      rel,
		MimeType:  mimeType,
		SizeBytes: int64(len(data)),
		Provider:  provider,
		Model:     model,
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("pluginhost: %s: encode response: %w", callID, err)
	}
	return raw, nil
}

// mediaUnit is the resolved serving unit: routing + credentials + provenance.
type mediaUnit struct {
	Endpoint  string
	Protocol  string // resolved units carry the provider Kind; account paths infer it
	AuthToken string
	UserAgent string
	Proxy     string
	Model     string
	Provider  string
}

// dispatchMediaGeneration runs the provider HTTP call for the resolved unit.
func (a *Actor) dispatchMediaGeneration(ctx context.Context, kind string, u mediaUnit, req mediaGenRequest, refs mediaRefs) (data []byte, mimeType, provider, model string, err error) {
	if kind == mediaKindImage {
		if u.Protocol == "gemini" || (u.Protocol == "" && isGeminiMediaProvider(u.Provider)) {
			result, gerr := imagegen.Generate(ctx, imagegen.Params{
				Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, Protocol: "gemini",
				AuthToken: u.AuthToken, UserAgent: u.UserAgent, Proxy: u.Proxy,
				Quality: req.Quality, Size: req.Size, AspectRatio: req.AspectRatio,
				ReferenceImages: refs.images,
			})
			if gerr != nil {
				return nil, "", "", "", gerr
			}
			return result.Data, result.MimeType, u.Provider, u.Model, nil
		}
		if u.Protocol == "minimax" || (u.Protocol == "" && isMiniMaxMediaProvider(u.Provider)) {
			result, gerr := imagegen.Generate(ctx, imagegen.Params{
				Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, Protocol: "minimax",
				AuthToken: u.AuthToken, UserAgent: u.UserAgent, Proxy: u.Proxy,
				Size: req.Size, AspectRatio: req.AspectRatio,
				ReferenceImages: refs.images,
			})
			if gerr != nil {
				return nil, "", "", "", gerr
			}
			return result.Data, result.MimeType, u.Provider, u.Model, nil
		}
		if u.Protocol == "qwen" || u.Protocol == "dashscope" || (u.Protocol == "" && isQwenMediaProvider(u.Provider)) {
			result, gerr := imagegen.Generate(ctx, imagegen.Params{
				Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, Protocol: "qwen",
				AuthToken: u.AuthToken, UserAgent: u.UserAgent, Proxy: u.Proxy,
				Size: req.Size, AspectRatio: req.AspectRatio,
				ReferenceImages: refs.images,
			})
			if gerr != nil {
				return nil, "", "", "", gerr
			}
			return result.Data, result.MimeType, u.Provider, u.Model, nil
		}
		result, gerr := mediagen.Generate(ctx, mediagen.Params{
			Kind: mediagen.KindImage, Prompt: req.Prompt, Model: u.Model,
			Endpoint: u.Endpoint, AuthToken: u.AuthToken, UserAgent: u.UserAgent,
			Size: mediaImageSize(req.Size, req.AspectRatio), AspectRatio: req.AspectRatio,
			ReferenceImages: refs.images, Proxy: u.Proxy,
		})
		if gerr != nil {
			return nil, "", "", "", gerr
		}
		return result.Data, result.MimeType, u.Provider, u.Model, nil
	}

	// video
	switch {
	case u.Protocol == "gemini" || (u.Protocol == "" && isGeminiMediaProvider(u.Provider)):
		result, gerr := videogen.GenerateGemini(ctx, videogen.Params{
			Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, AuthToken: u.AuthToken,
			Duration: parseMediaDuration(req.Duration), AspectRatio: req.AspectRatio,
			UserAgent: u.UserAgent, Proxy: u.Proxy,
		})
		if gerr != nil {
			return nil, "", "", "", gerr
		}
		return result.Data, result.MimeType, u.Provider, u.Model, nil
	case u.Protocol == "ark" || (u.Protocol == "" && isArkMediaProvider(u.Provider)):
		result, gerr := videogen.GenerateArk(ctx, videogen.Params{
			Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, AuthToken: u.AuthToken,
			Duration: parseMediaDuration(req.Duration), AspectRatio: req.AspectRatio,
			ReferenceImages: refs.images, ReferenceVideos: req.ReferenceVideos,
			ReferenceAudios: req.ReferenceAudios,
			GenerateAudio:   true, Watermark: false,
			UserAgent: u.UserAgent, Proxy: u.Proxy,
		})
		if gerr != nil {
			return nil, "", "", "", gerr
		}
		return result.Data, result.MimeType, u.Provider, u.Model, nil
	case u.Protocol == "qwen" || (u.Protocol == "" && isQwenMediaProvider(u.Provider)):
		// The Wan API takes no reference video, so req.ReferenceVideos is
		// deliberately not forwarded; Wan 2.5 auto-dubs the audio track.
		result, gerr := videogen.GenerateQwen(ctx, videogen.Params{
			Prompt: req.Prompt, Model: u.Model, Endpoint: u.Endpoint, AuthToken: u.AuthToken,
			Duration: parseMediaDuration(req.Duration), AspectRatio: req.AspectRatio,
			ReferenceImages: refs.images, ReferenceAudios: req.ReferenceAudios,
			Watermark: false,
			UserAgent: u.UserAgent, Proxy: u.Proxy,
		})
		if gerr != nil {
			return nil, "", "", "", gerr
		}
		return result.Data, result.MimeType, u.Provider, u.Model, nil
	default:
		result, gerr := mediagen.Generate(ctx, mediagen.Params{
			Kind: mediagen.KindVideo, Prompt: req.Prompt, Model: u.Model,
			Endpoint: u.Endpoint, AuthToken: u.AuthToken, UserAgent: u.UserAgent,
			Duration: parseMediaDuration(req.Duration), AspectRatio: req.AspectRatio,
			ReferenceImages: refs.images, ReferenceAudios: req.ReferenceAudios,
			Proxy: u.Proxy,
		})
		if gerr != nil {
			return nil, "", "", "", gerr
		}
		return result.Data, result.MimeType, u.Provider, u.Model, nil
	}
}

// mediaRefs carries the validated reference inputs.
type mediaRefs struct {
	images []string
}

// normalizeMediaRefs validates reference inputs. Local file paths are
// rejected (an undeclared fs.read); URLs and base64 pass through. The single
// InputImage (image kind) is merged in front, mirroring the agent contract.
func normalizeMediaRefs(kind string, req mediaGenRequest) (mediaRefs, error) {
	images := make([]string, 0, len(req.ReferenceImages)+1)
	if kind == mediaKindImage && req.InputImage != "" {
		normalized, err := normalizeMediaRef(req.InputImage)
		if err != nil {
			return mediaRefs{}, fmt.Errorf("InputImage: %w", err)
		}
		images = append(images, normalized)
	}
	for _, ref := range req.ReferenceImages {
		normalized, err := normalizeMediaRef(ref)
		if err != nil {
			return mediaRefs{}, fmt.Errorf("ReferenceImages: %w", err)
		}
		images = append(images, normalized)
	}
	for _, ref := range req.ReferenceVideos {
		if !isHTTPRef(ref) {
			return mediaRefs{}, fmt.Errorf("ReferenceVideos: %q must be an http(s) URL", ref)
		}
	}
	for _, ref := range req.ReferenceAudios {
		if !isHTTPRef(ref) {
			return mediaRefs{}, fmt.Errorf("ReferenceAudios: %q must be an http(s) URL", ref)
		}
	}
	return mediaRefs{images: images}, nil
}

// normalizeMediaRef passes URLs through and requires base64 for everything
// else. Bare local paths are rejected: the agent tool reads them from the
// project FS, but a plugin-supplied path must not become an undeclared
// fs.read on the host.
func normalizeMediaRef(ref string) (string, error) {
	if ref == "" || isHTTPRef(ref) || strings.HasPrefix(ref, "data:") {
		return ref, nil
	}
	if _, err := base64.StdEncoding.DecodeString(ref); err != nil {
		return "", fmt.Errorf("%q is neither an http(s) URL, data URI, nor base64 — local file paths are not accepted (send base64 or a URL)", truncateMediaRef(ref))
	}
	return ref, nil
}

func truncateMediaRef(ref string) string {
	if len(ref) > 64 {
		return ref[:64] + "…"
	}
	return ref
}

// activeMediaAccountFor queries the media service for the kind's active
// account. Missing service / errors degrade to (zero, false) — the caller
// falls back to the aggregator pool, mirroring the agent tool.
func (a *Actor) activeMediaAccountFor(ctx context.Context, kind string) (gen.MediaActiveAccountResp, bool) {
	r := a.streamServiceRef("media")
	if r == nil {
		return gen.MediaActiveAccountResp{}, false
	}
	raw, err := invokeServiceRaw(ctx, r, "media.active_account", gen.MediaActiveAccountReq{Kind: kind})
	if err != nil {
		return gen.MediaActiveAccountResp{}, false
	}
	var resp gen.MediaActiveAccountResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return gen.MediaActiveAccountResp{}, false
	}
	return resp, true
}

// resolveMediaUnit resolves a generation unit through the system aggregator.
// provider/model both empty = pool auto-pick; both set = strict pin (the
// caller guarantees the pin case and surfaces resolve failures directly).
func (a *Actor) resolveMediaUnit(ctx context.Context, kind, provider, model string) (resolved gen.AIAggregatorImageResolveResp, err error) {
	r := a.streamServiceRef("aiaggregator")
	if r == nil {
		return resolved, fmt.Errorf("aggregator service unavailable")
	}
	callID := "aiaggregator.image_resolve"
	if kind == mediaKindVideo {
		callID = "aiaggregator.video_resolve"
	}
	req := gen.AIAggregatorImageResolveReq{Provider: provider, Model: model}
	raw, ierr := invokeServiceRaw(ctx, r, callID, req)
	if ierr != nil {
		return resolved, ierr
	}
	if kind == mediaKindVideo {
		var videoResp gen.AIAggregatorVideoResolveResp
		if uerr := json.Unmarshal(raw, &videoResp); uerr != nil {
			return resolved, fmt.Errorf("decode resolve response: %w", uerr)
		}
		return gen.AIAggregatorImageResolveResp{
			Model: videoResp.Model, Endpoint: videoResp.Endpoint, ProviderName: videoResp.ProviderName,
			Protocol: videoResp.Protocol, AuthToken: videoResp.AuthToken, UserAgent: videoResp.UserAgent,
			Proxy: videoResp.Proxy,
		}, nil
	}
	if uerr := json.Unmarshal(raw, &resolved); uerr != nil {
		return resolved, fmt.Errorf("decode resolve response: %w", uerr)
	}
	return resolved, nil
}

// saveAppMedia writes the artifact into the calling app's media/ runtime
// directory and returns the app-relative path (slash-separated, loadable as
// a same-origin URL by the panel). The primary anchor is the static root the
// appmanager pushed in the artifact's OnLoad config (the same root the SDK
// serves "/" from) — the only anchor an inventory-installed (zip) app has,
// because its artifact lives in the content-addressed store instead of
// <appDir>/.sporecode/build/. Load records predating the static root (or a
// missing entry) fall back to deriving the app dir from the loaded artifact
// path; unknown layouts refuse to guess a root.
func (a *Actor) saveAppMedia(pluginID, kind string, data []byte, mimeType string) (string, error) {
	if dir := a.staticRootOf(pluginID); dir != "" {
		return writeAppMedia(dir, kind, data, mimeType)
	}
	if a.loader == nil {
		return "", fmt.Errorf("artifact loader unavailable")
	}
	return saveAppMediaAt(a.loader.ArtifactPath(pluginID), kind, data, mimeType)
}

// staticRootOf resolves the static root the appmanager anchored for this
// plugin at artifact load (OnLoad config staticDir) from the persisted load
// record. The config is host-authored, so it carries the same trust level as
// the recorded ArtifactPath; empty when there is no record or no field.
func (a *Actor) staticRootOf(pluginID string) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	load, ok := a.ArtifactLoads[pluginID]
	if !ok {
		return ""
	}
	return parseOnLoadStaticDir(load.OnLoadConfig)
}

// parseOnLoadStaticDir extracts the staticDir field from the plugin's OnLoad
// config JSON. Anything unparseable means no anchor — the caller falls back.
func parseOnLoadStaticDir(onLoadConfig []byte) string {
	if len(onLoadConfig) == 0 {
		return ""
	}
	var cfg struct {
		StaticDir string `json:"staticDir"`
	}
	if err := json.Unmarshal(onLoadConfig, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.StaticDir)
}

// saveAppMediaAt writes the artifact into <appDir>/media/ (appDir derived
// from the artifact path — the same layout anchor the SDK static root uses;
// unknown layouts refuse to guess a root) and returns the app-relative path.
func saveAppMediaAt(artifactPath, kind string, data []byte, mimeType string) (string, error) {
	appDir := appbinding.AppDirFromArtifactPath(artifactPath)
	if appDir == "" {
		return "", fmt.Errorf("cannot place generated media: artifact path %q is not anchored to an app dir", artifactPath)
	}
	return writeAppMedia(appDir, kind, data, mimeType)
}

// writeAppMedia persists the artifact under <appDir>/media/ and returns the
// app-relative slash path.
func writeAppMedia(appDir, kind string, data []byte, mimeType string) (string, error) {
	dir := filepath.Join(appDir, "media")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	ext := mediaExtFor(mimeType, kind)
	name := fmt.Sprintf("%s-%d.%s", kind, time.Now().UnixMilli(), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return "media/" + name, nil
}

func mediaExtFor(mimeType, kind string) string {
	if extensions, _ := mime.ExtensionsByType(mimeType); len(extensions) > 0 {
		if ext := strings.TrimPrefix(extensions[0], "."); ext != "" {
			return ext
		}
	}
	if kind == mediaKindVideo {
		return "mp4"
	}
	return "png"
}

// invokeServiceRaw performs a one-shot service invoke and returns the raw
// response payload (mirrors the sshmanager host-call branch pattern).
func invokeServiceRaw(ctx context.Context, r ref.Ref, callID string, req any) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", callID, err)
	}
	call := r.Invoke(ctx, callID, payload)
	if call == nil {
		return nil, fmt.Errorf("invoke %q returned nil", callID)
	}
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		return nil, fmt.Errorf("invoke %q: %w", callID, err)
	}
	return raw, nil
}

// --- small pure helpers (agent-tool parity; kept local because the agent
// package is per-agent and these are trivial, stable predicates) ---

func isHTTPRef(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func isGeminiMediaProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "gemini")
}

func isMiniMaxMediaProvider(provider string) bool {
	return strings.EqualFold(strings.TrimSpace(provider), "minimax")
}

// isQwenMediaProvider reports whether a media account name routes to the native
// Qwen (DashScope) image backend. The name is free-form user text, so the
// comparison is case/space tolerant; "dashscope" and "bailian" are accepted
// aliases for the same platform.
func isQwenMediaProvider(provider string) bool {
	provider = strings.TrimSpace(provider)
	switch {
	case strings.EqualFold(provider, "qwen"),
		strings.EqualFold(provider, "dashscope"),
		strings.EqualFold(provider, "bailian"):
		return true
	}
	return false
}

func isArkMediaProvider(provider string) bool {
	provider = strings.TrimSpace(provider)
	switch {
	case strings.EqualFold(provider, "ark"),
		strings.EqualFold(provider, "volcengine"),
		strings.EqualFold(provider, "byteplus"):
		return true
	}
	return false
}

func firstNonEmptyMedia(first, fallback string) string {
	if first != "" {
		return first
	}
	return fallback
}

// parseMediaDuration maps "5s" → 5; invalid or <1s yields 0 (provider
// default), mirroring the agent tool.
func parseMediaDuration(value string) int {
	value = strings.TrimSpace(strings.TrimSuffix(value, "s"))
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 1 {
		return 0
	}
	return seconds
}

// mediaImageSize maps the relative size budget + aspect ratio to a concrete
// WxH for GPT-compatible media endpoints (gpt-image-class endpoints reject
// "1K"/"2K"/"4K" outright). Mirrors the agent tool's mediaImageSize.
func mediaImageSize(size, aspect string) string {
	switch aspect {
	case "16:9", "3:2", "4:3", "5:4", "21:9":
		return "1536x1024"
	case "9:16", "2:3", "3:4", "4:5":
		return "1024x1536"
	}
	return "1024x1024"
}
