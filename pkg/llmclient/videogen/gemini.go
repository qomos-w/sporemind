// Package videogen implements native video-generation clients that do not fit
// the GPT-compatible mediagen shape. The Gemini backend drives Google Veo
// through the Gemini API long-running operation flow: submit
// v1beta/models/{model}:predictLongRunning, poll the returned operation until
// done, then resolve the generated video bytes (inline base64 or an HTTP(S)
// download URL). The Ark backend drives Volcengine/BytePlus Seedance through
// the contents/generations async-task flow: submit a text + multi-reference
// content list, poll the task until it succeeds or fails, then download the
// resulting mp4 (see ark.go). The Qwen backend drives Alibaba Cloud Model Studio
// (DashScope) Wan through the video-synthesis async-task flow: submit a text
// prompt plus an optional first-frame image and audio track, poll the task, then
// download the resulting mp4 (see qwen.go).
package videogen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const geminiDefaultEndpoint = "https://generativelanguage.googleapis.com"

// normalizeGeminiEndpoint mirrors imagegen: relay BaseURLs often carry a
// trailing /v1 (or /v1beta) that must not double with the native path.
func normalizeGeminiEndpoint(endpoint string) string {
	e := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if e == "" {
		return geminiDefaultEndpoint
	}
	lower := strings.ToLower(e)
	for _, suffix := range []string{"/v1", "/v1beta"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimRight(e[:len(e)-len(suffix)], "/")
		}
	}
	return e
}

// Params carries everything needed to make one video-generation call. It is
// shared by the Gemini (Veo), Ark (Seedance), and Qwen (Wan) backends: Gemini
// reads Prompt/Model/Endpoint/AuthToken/Duration/AspectRatio and ignores the
// reference/watermark fields, while Ark additionally consumes
// ReferenceImages/ReferenceVideos/ReferenceAudios/GenerateAudio/Watermark. Qwen
// consumes the prompt plus the first ReferenceImages entry (first frame) and the
// first ReferenceAudios entry, ignores ReferenceVideos (the Wan API takes no
// reference video), and lets the provider auto-dub the audio.
type Params struct {
	Prompt          string
	Model           string
	Endpoint        string // provider base URL; empty = the backend's default endpoint (gemini/ark/qwen)
	AuthToken       string // API key sent as x-goog-api-key to Gemini, as Bearer to Ark and Qwen
	Protocol        string // provider Kind: "ark" | "gemini" | "qwen" | "openai"; routed by Generate
	Duration        int    // video duration in seconds
	AspectRatio     string // e.g. "16:9"; sent as "ratio" to Ark, resolved to a Wan size token for Qwen
	ReferenceImages []string // reference image URLs or base64 data URLs (Ark; first entry = first frame for Qwen)
	ReferenceVideos []string // reference video URLs (Ark; rejected by the Qwen backend)
	ReferenceAudios []string // reference audio URLs (Ark; the first entry drives Wan 2.5 audio)
	GenerateAudio   bool     // generate audio alongside the video (Ark; the Qwen path auto-dubs)
	Watermark       bool     // watermark toggle (Ark, Qwen); ignored by the Gemini path
	UserAgent       string   // optional override; empty = default
	Proxy           string   // optional HTTP(S)/SOCKS5 proxy URL; empty = direct connection
}

// Result is the resolved generated video.
type Result struct {
	Data     []byte
	MimeType string
	URL      string // source URL when the artifact was downloaded
}

// geminiOperation models a long-running operation resource returned by the
// Gemini API. Response is left raw because its shape varies by operation type.
type geminiOperation struct {
	Name     string           `json:"name"`
	Done     bool             `json:"done,omitempty"`
	Error    *geminiOpError   `json:"error,omitempty"`
	Response json.RawMessage  `json:"response,omitempty"`
}

type geminiOpError struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Status  string `json:"status,omitempty"`
}

type geminiVideoParameters struct {
	AspectRatio      string `json:"aspectRatio,omitempty"`
	DurationSeconds  int    `json:"durationSeconds,omitempty"`
	PersonGeneration string `json:"personGeneration,omitempty"`
}

type geminiVideoRequest struct {
	Instances  []map[string]string   `json:"instances"`
	Parameters geminiVideoParameters  `json:"parameters,omitempty"`
}

// GenerateGemini submits a Veo video generation request through the Gemini API
// long-running operation flow and resolves the generated video bytes.
func GenerateGemini(ctx context.Context, p Params) (*Result, error) {
	// Same relay-BaseURL immunity as imagegen: strip a trailing /v1 or /v1beta
	// before the native /v1beta/... path is appended.
	endpoint := normalizeGeminiEndpoint(p.Endpoint)
	if p.Model == "" {
		return nil, fmt.Errorf("videogen/gemini: model is required")
	}

	op, err := submitGeminiVideo(ctx, p, endpoint)
	if err != nil {
		return nil, err
	}
	if op.Done {
		return resolveGeminiVideo(ctx, p, op)
	}
	if op.Name == "" {
		return nil, fmt.Errorf("videogen/gemini: submit returned no operation name")
	}
	return pollGeminiOperation(ctx, p, endpoint, op.Name)
}

func submitGeminiVideo(ctx context.Context, p Params, endpoint string) (*geminiOperation, error) {
	body := geminiVideoRequest{
		Instances: []map[string]string{{"prompt": p.Prompt}},
		Parameters: geminiVideoParameters{
			AspectRatio:      p.AspectRatio,
			DurationSeconds:  p.Duration,
			PersonGeneration: "dont_allow",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: marshal: %w", err)
	}
	target := fmt.Sprintf("%s/v1beta/models/%s:predictLongRunning", endpoint, p.Model)
	respBody, err := geminiPost(ctx, p, target, payload)
	if err != nil {
		return nil, err
	}
	var op geminiOperation
	if err := json.Unmarshal(respBody, &op); err != nil {
		return nil, fmt.Errorf("videogen/gemini: decode submit: %w", err)
	}
	return &op, nil
}

// pollGeminiOperation repeatedly GETs the operation resource until it is done,
// fails, or the context expires.
func pollGeminiOperation(ctx context.Context, p Params, endpoint, opName string) (*Result, error) {
	pollURL := fmt.Sprintf("%s/v1beta/%s", endpoint, opName)
	for {
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("videogen/gemini: operation %s timed out: %w", opName, ctx.Err())
		case <-timer.C:
		}
		respBody, err := geminiGet(ctx, p, pollURL)
		if err != nil {
			return nil, err
		}
		var op geminiOperation
		if err := json.Unmarshal(respBody, &op); err != nil {
			return nil, fmt.Errorf("videogen/gemini: decode poll: %w", err)
		}
		if op.Error != nil && op.Error.Message != "" {
			return nil, fmt.Errorf("videogen/gemini: operation %s failed: %s", opName, op.Error.Message)
		}
		if op.Done {
			return resolveGeminiVideo(ctx, p, &op)
		}
	}
}

// resolveGeminiVideo extracts the generated artifact from a completed operation.
func resolveGeminiVideo(ctx context.Context, p Params, op *geminiOperation) (*Result, error) {
	if op.Error != nil && op.Error.Message != "" {
		return nil, fmt.Errorf("videogen/gemini: operation failed: %s", op.Error.Message)
	}
	// Search the operation response first; fall back to the whole body for
	// providers that inline the result outside a "response" wrapper.
	raw := op.Response
	if len(raw) == 0 {
		if b, err := json.Marshal(op); err == nil {
			raw = b
		}
	}
	if b64, mediaURL, mt, ok := searchGeminiVideo(raw, 0); ok {
		return resolveGeminiArtifact(ctx, p, b64, mediaURL, mt)
	}
	return nil, fmt.Errorf("videogen/gemini: no video in operation response: %s", truncate(string(raw)))
}

// resolveGeminiArtifact decodes an inline base64 payload or downloads an HTTP(S)
// URL into the final video bytes.
func resolveGeminiArtifact(ctx context.Context, p Params, b64, mediaURL, mt string) (*Result, error) {
	if mt == "" {
		mt = "video/mp4"
	}
	if b64 != "" {
		data, err := decodeBase64(b64)
		if err != nil {
			return nil, fmt.Errorf("videogen/gemini: decode base64: %w", err)
		}
		return &Result{Data: data, MimeType: mt}, nil
	}
	if isURL(mediaURL) {
		data, err := downloadGemini(ctx, p, mediaURL)
		if err != nil {
			return nil, err
		}
		return &Result{Data: data, MimeType: mt, URL: mediaURL}, nil
	}
	if strings.HasPrefix(mediaURL, "gs://") {
		return nil, fmt.Errorf("videogen/gemini: operation returned a gs:// URI (%s) which requires GCS credentials; configure Veo to return base64 or an HTTP URL", mediaURL)
	}
	return nil, fmt.Errorf("videogen/gemini: no resolvable video artifact")
}

// ---- HTTP -----------------------------------------------------------------

func geminiPost(ctx context.Context, p Params, target string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: build request: %w", err)
	}
	setGeminiHeaders(req, p)
	return geminiRoundtrip(req, "submit", p)
}

func geminiGet(ctx context.Context, p Params, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: build request: %w", err)
	}
	setGeminiHeaders(req, p)
	return geminiRoundtrip(req, "poll", p)
}

// downloadGemini fetches artifact bytes from a media URL. The API key is sent
// only when the URL is served by the same host as the endpoint so that signed
// or public CDN URLs are not broken by an unexpected credential header.
func downloadGemini(ctx context.Context, p Params, mediaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: build download: %w", err)
	}
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if sameHost(mediaURL, p.Endpoint) {
		req.Header.Set("x-goog-api-key", p.AuthToken)
	}
	return geminiRoundtrip(req, "download", p)
}

func setGeminiHeaders(req *http.Request, p Params) {
	req.Header.Set("x-goog-api-key", p.AuthToken)
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
}

func geminiRoundtrip(req *http.Request, label string, p Params) ([]byte, error) {
	client, err := httpClientForParams(p)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: %s: %w", label, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("videogen/gemini: %s read: %w", label, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("videogen/gemini: %s http %d: %s", label, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}

// ---- response parsing -----------------------------------------------------

// geminiContainerKeys are nested object/array keys that may hold the artifact.
var geminiContainerKeys = []string{
	"generateVideoResponse", "generatedSamples", "video", "videos",
	"response", "data", "results", "output", "result",
}

// searchGeminiVideo recursively searches a JSON blob for the first video
// artifact (a base64 payload or a media URL).
func searchGeminiVideo(raw json.RawMessage, depth int) (b64, mediaURL, mimeType string, ok bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", "", "", false
	}
	return searchGeminiValue(v, depth)
}

func searchGeminiValue(v any, depth int) (b64, mediaURL, mimeType string, ok bool) {
	if depth > 6 {
		return "", "", "", false
	}
	switch t := v.(type) {
	case string:
		// Bare strings are only matched as URLs/GCS URIs; arbitrary base64
		// detection on free strings is too promiscuous (short strings decode).
		if isURL(t) || strings.HasPrefix(t, "gs://") {
			return "", t, "", true
		}
	case map[string]any:
		if b64, mediaURL, mimeType, ok = geminiDirectKeys(t); ok {
			return
		}
		for _, key := range geminiContainerKeys {
			if nested, found := t[key]; found {
				if b64, mediaURL, mimeType, ok = searchGeminiValue(nested, depth+1); ok {
					return
				}
			}
		}
	case []any:
		for _, el := range t {
			if b64, mediaURL, mimeType, ok = searchGeminiValue(el, depth+1); ok {
				return
			}
		}
	}
	return "", "", "", false
}

// geminiDirectKeys checks an object for immediate base64/URL media fields.
func geminiDirectKeys(m map[string]any) (b64, mediaURL, mimeType string, ok bool) {
	for _, k := range []string{"bytesBase64Encoded", "b64_json", "b64", "base64"} {
		if s, _ := m[k].(string); s != "" {
			b64 = s
			break
		}
	}
	for _, k := range []string{"uri", "url", "video_url", "download_url", "videoUri", "gcsUri"} {
		if s, _ := m[k].(string); s != "" && (isURL(s) || strings.HasPrefix(s, "gs://")) {
			mediaURL = s
			break
		}
	}
	if b64 != "" || mediaURL != "" {
		ok = true
		for _, k := range []string{"mimeType", "mime_type", "contentType", "content_type"} {
			if s, _ := m[k].(string); s != "" {
				mimeType = s
				break
			}
		}
	}
	return b64, mediaURL, mimeType, ok
}

// ---- helpers ---------------------------------------------------------------

func decodeBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.URLEncoding,
		base64.RawStdEncoding,
		base64.RawURLEncoding,
	} {
		if data, err := enc.DecodeString(s); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("invalid base64 payload")
}

func sameHost(a, b string) bool {
	ha := hostOf(a)
	hb := hostOf(b)
	return ha != "" && ha == hb
}

func hostOf(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

func truncate(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
