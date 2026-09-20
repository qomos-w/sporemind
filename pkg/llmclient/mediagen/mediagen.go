// Package mediagen implements a GPT-compatible asynchronous media generation
// client. It submits image and video generation requests to an OpenAI-style
// endpoint and transparently resolves the artifact whether the provider
// responds synchronously (inline base64/URL) or asynchronously (a task id that
// must be polled to completion).
//
// The overall time budget for a single Generate call is bounded by the caller
// supplied context; cancellation aborts in-flight HTTP requests and polling.
package mediagen

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

	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// Media kinds.
const (
	KindImage = "image"
	KindVideo = "video"
)

// Params carries everything needed to make one media-generation API call.
type Params struct {
	Kind            string   // KindImage or KindVideo
	Prompt          string   // text prompt
	Model           string   // model identifier
	Endpoint        string   // provider base URL, e.g. https://api.example.com/v1
	AuthToken       string   // Bearer token
	Duration        int      // video duration in seconds
	AspectRatio     string   // e.g. "16:9"
	Size            string   // e.g. "1024x1024"
	Resolution      string   // e.g. "1080p"
	ReferenceImages []string // reference image URLs
	ReferenceAudios []string // reference audio URLs
	UserAgent       string   // optional User-Agent override; empty = default
	Proxy           string   // optional HTTP(S)/SOCKS5 proxy URL; empty = direct connection
}

// Result is a resolved generated media artifact.
type Result struct {
	Data     []byte // raw media bytes
	MimeType string // e.g. "image/png", "video/mp4"
	URL      string // source URL when the artifact was downloaded
}

// Generate submits a media generation request and resolves the artifact. It
// transparently handles both the synchronous (inline result) and asynchronous
// (task polling) response forms supported by GPT-compatible endpoints.
func Generate(ctx context.Context, p Params) (*Result, error) {
	endpoint := strings.TrimRight(p.Endpoint, "/")
	if endpoint == "" {
		return nil, fmt.Errorf("mediagen: empty endpoint")
	}
	switch p.Kind {
	case KindImage:
		return generateImage(ctx, p, endpoint)
	case KindVideo:
		return generateVideo(ctx, p, endpoint)
	default:
		return nil, fmt.Errorf("mediagen: unsupported kind %q", p.Kind)
	}
}

func generateImage(ctx context.Context, p Params, endpoint string) (*Result, error) {
	body := imageRequest{
		Model:       p.Model,
		Prompt:      p.Prompt,
		N:           1,
		Size:        p.Size,
		Resolution:  p.Resolution,
		AspectRatio: p.AspectRatio,
		ImageURLs:   p.ReferenceImages,
	}
	return submit(ctx, p, endpoint+"/images/generations", body, KindImage)
}

func generateVideo(ctx context.Context, p Params, endpoint string) (*Result, error) {
	body := videoRequest{
		Model:           p.Model,
		Prompt:          p.Prompt,
		Duration:        p.Duration,
		Size:            p.Size,
		AspectRatio:     p.AspectRatio,
		ReferenceImages: p.ReferenceImages,
		ReferenceAudios: p.ReferenceAudios,
	}
	return submit(ctx, p, endpoint+"/video/generations", body, KindVideo)
}

// imageRequest is the visioncoder-style image generation request body.
type imageRequest struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	N           int      `json:"n"`
	Size        string   `json:"size,omitempty"`
	Resolution  string   `json:"resolution,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
	ImageURLs   []string `json:"image_urls,omitempty"` // visioncoder convention
}

// videoRequest is the visioncoder-style video generation request body.
type videoRequest struct {
	Model           string   `json:"model"`
	Prompt          string   `json:"prompt"`
	Duration        int      `json:"duration,omitempty"`
	Size            string   `json:"size,omitempty"`
	AspectRatio     string   `json:"aspect_ratio,omitempty"`
	ReferenceImages []string `json:"referenceImages,omitempty"`
	ReferenceAudios []string `json:"referenceAudios,omitempty"`
}

// submit POSTs the generation request and resolves the result, adapting to
// either a direct-result or an async-task response.
func submit(ctx context.Context, p Params, target string, reqBody any, kind string) (*Result, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("mediagen: marshal request: %w", err)
	}
	respBody, err := doPost(ctx, p, target, payload)
	if err != nil {
		return nil, err
	}
	candidate := unwrapData(respBody)

	// Direct result: the response already carries the artifact.
	if b64, mediaURL, mt, ok := extractMedia(candidate, kind); ok {
		return resolveMedia(ctx, p, b64, mediaURL, mt, kind)
	}

	// Async task: the response carries a task id to poll.
	taskID, status, ok := extractTaskInfo(candidate)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("mediagen: unrecognized submit response: %s", truncate(string(respBody)))
	}
	return pollTask(ctx, p, taskID, status, kind)
}

// resolveMaterial decodes an inline base64 payload or downloads a URL into the
// final artifact bytes.
func resolveMedia(ctx context.Context, p Params, b64, mediaURL, mt, kind string) (*Result, error) {
	if mt == "" {
		mt = defaultMimeType(kind)
	}
	if b64 != "" {
		data, err := decodeBase64(b64)
		if err != nil {
			return nil, fmt.Errorf("mediagen: decode base64: %w", err)
		}
		return &Result{Data: data, MimeType: mt}, nil
	}
	if mediaURL != "" {
		data, err := download(ctx, p, mediaURL)
		if err != nil {
			return nil, err
		}
		return &Result{Data: data, MimeType: mt, URL: mediaURL}, nil
	}
	return nil, fmt.Errorf("mediagen: no media to resolve")
}

// pollTask repeatedly GETs the task status endpoint until the task succeeds,
// fails, or the context expires. On success it resolves and returns the
// artifact bytes.
func pollTask(ctx context.Context, p Params, taskID, status, kind string) (*Result, error) {
	endpoint := strings.TrimRight(p.Endpoint, "/")
	pollURL := fmt.Sprintf("%s/tasks/%s", endpoint, taskID)

	for {
		// Wait for the next poll interval, honoring context cancellation.
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("mediagen: task %s timed out: %w", taskID, ctx.Err())
		case <-timer.C:
		}

		raw, err := doGet(ctx, p, pollURL)
		if err != nil {
			return nil, err
		}
		candidate := unwrapData(raw)
		_, status, _ = extractTaskInfo(candidate)

		if failedStatuses[status] {
			return nil, fmt.Errorf("mediagen: task %s failed: %s", taskID, errOrEmpty(candidate))
		}
		// Some providers omit an explicit success status and instead return the
		// artifact directly; extractMedia resolves it whenever present.
		if b64, mediaURL, mt, ok := extractMedia(candidate, kind); ok {
			return resolveMedia(ctx, p, b64, mediaURL, mt, kind)
		}
		// Otherwise still pending: loop and poll again.
	}
}

// ---- HTTP -----------------------------------------------------------------

// httpClientForParams selects the HTTP client for a request: the shared
// per-proxy client pool when Params.Proxy is set, or the package default for
// direct connections.
func httpClientForParams(p Params) (*http.Client, error) {
	if p.Proxy == "" {
		return httpClient, nil
	}
	c, err := llmclient.HTTPClientForProxy(p.Proxy)
	if err != nil {
		return nil, fmt.Errorf("mediagen: %w", err)
	}
	return c, nil
}

func doPost(ctx context.Context, p Params, target string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("mediagen: build request: %w", err)
	}
	setHeaders(req, p)
	return doRoundtrip(req, "post", p)
}

func doGet(ctx context.Context, p Params, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("mediagen: build request: %w", err)
	}
	setHeaders(req, p)
	return doRoundtrip(req, "poll", p)
}

// download fetches artifact bytes from a media URL. The Bearer token is sent
// only when the URL is served by the same host as the endpoint, so that
// presigned/public CDN URLs are not broken by an unexpected Authorization
// header (e.g. S3 presigned URLs reject Authorization).
func download(ctx context.Context, p Params, mediaURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mediaURL, nil)
	if err != nil {
		return nil, fmt.Errorf("mediagen: build download: %w", err)
	}
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if sameHost(mediaURL, p.Endpoint) {
		req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	}
	return doRoundtrip(req, "download", p)
}

func setHeaders(req *http.Request, p Params) {
	req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
}

func doRoundtrip(req *http.Request, label string, p Params) ([]byte, error) {
	client, err := httpClientForParams(p)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mediagen: %s %s model %q: %w", label, req.URL.Redacted(), p.Model, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("mediagen: %s %s model %q: read: %w", label, req.URL.Redacted(), p.Model, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("mediagen: %s %s model %q: http %d: %s", label, req.URL.Redacted(), p.Model, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}

// ---- response parsing -----------------------------------------------------

// envelope captures the common {code,data} wrapper used by GPT-compatible
// endpoints. Data is left raw because it is polymorphic: an array for inline
// results, an object for task descriptors.
type envelope struct {
	Code int             `json:"code"`
	Data json.RawMessage `json:"data"`
}

// unwrapData returns the "data" payload of a response body, or the whole body
// when no "data" key is present.
func unwrapData(body []byte) json.RawMessage {
	var env envelope
	if err := json.Unmarshal(body, &env); err == nil && len(env.Data) > 0 {
		return env.Data
	}
	return json.RawMessage(body)
}

// extractTaskInfo reads a task descriptor (id + status) from a JSON object.
func extractTaskInfo(raw json.RawMessage) (id, status string, ok bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", "", false
	}
	var t struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(trimmed, &t); err != nil {
		return "", "", false
	}
	id = t.ID
	if id == "" {
		id = t.TaskID
	}
	status = t.Status
	return id, status, id != ""
}

// extractMedia recursively searches a JSON blob for the first media artifact
// (a base64 string or a media URL). It tolerates the variety of field names and
// nesting shapes returned by different GPT-compatible providers.
func extractMedia(raw json.RawMessage, kind string) (b64, mediaURL, mimeType string, ok bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", "", "", false
	}
	return searchMedia(v, kind, 0)
}

func searchMedia(v any, kind string, depth int) (b64, mediaURL, mimeType string, ok bool) {
	if depth > 5 {
		return "", "", "", false
	}
	switch t := v.(type) {
	case string:
		if isURL(t) {
			return "", t, "", true
		}
	case map[string]any:
		if b64, mediaURL, mimeType, ok = directMediaKeys(t, kind); ok {
			return
		}
		for _, key := range mediaContainerKeys {
			if nested, found := t[key]; found {
				if b64, mediaURL, mimeType, ok = searchMedia(nested, kind, depth+1); ok {
					return
				}
			}
		}
	case []any:
		for _, el := range t {
			if b64, mediaURL, mimeType, ok = searchMedia(el, kind, depth+1); ok {
				return
			}
		}
	}
	return "", "", "", false
}

// directMediaKeys checks an object for immediate base64/URL media fields.
func directMediaKeys(m map[string]any, kind string) (b64, mediaURL, mimeType string, ok bool) {
	for _, k := range []string{"b64_json", "b64", "base64"} {
		if s, _ := m[k].(string); s != "" {
			b64 = s
			break
		}
	}
	for _, k := range []string{"url", "image_url", "video_url", "download_url", "file"} {
		if s, _ := m[k].(string); s != "" && isURL(s) {
			mediaURL = s
			break
		}
	}
	if b64 != "" || mediaURL != "" {
		ok = true
		mimeType = inferMimeType(m, kind)
	}
	return b64, mediaURL, mimeType, ok
}

// ---- helpers --------------------------------------------------------------

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

func inferMimeType(m map[string]any, kind string) string {
	for _, k := range []string{"mime_type", "mimeType", "content_type", "contentType"} {
		if s, _ := m[k].(string); s != "" {
			return s
		}
	}
	return defaultMimeType(kind)
}

func defaultMimeType(kind string) string {
	if kind == KindVideo {
		return "video/mp4"
	}
	return "image/png"
}

func extractError(raw json.RawMessage) string {
	var e struct {
		Error        string `json:"error"`
		ErrorMessage string `json:"error_message"`
		Message      string `json:"message"`
		Detail       string `json:"detail"`
	}
	_ = json.Unmarshal(raw, &e)
	for _, s := range []string{e.Error, e.ErrorMessage, e.Message, e.Detail} {
		if s != "" {
			return s
		}
	}
	return ""
}

func errOrEmpty(raw json.RawMessage) string {
	if msg := extractError(raw); msg != "" {
		return msg
	}
	return truncate(string(raw))
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
