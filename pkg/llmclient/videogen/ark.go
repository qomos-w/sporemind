// The Ark backend drives Volcengine (mainland China) and BytePlus (overseas)
// Seedance video generation through the contents/generations async-task API:
// submit a task carrying a text prompt plus any number of reference images,
// videos, and audios, poll the task until it is succeeded or failed, then
// download the generated mp4 from the task's video_url.
package videogen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// arkDefaultEndpoint is the Ark API base used when Params.Endpoint is empty
	// (mainland China). The oversea BytePlus region uses arkGlobalEndpoint;
	// callers in that region set Params.Endpoint explicitly.
	arkDefaultEndpoint = "https://ark.cn-beijing.volces.com/api/v3"
	arkGlobalEndpoint  = "https://ark.ap-southeast.bytepluses.com/api/v3"

	arkTasksPath = "/contents/generations/tasks"
)

// arkSubmitRequest is the task submission body accepted by
// POST {base}/contents/generations/tasks. Ratio and duration are optional;
// generate_audio and watermark are always sent so the provider sees the
// caller's explicit choice. In the Seedance 2.0 prompt, entries are referenced
// as [Image 1], [Video 1], [Audio 1] in the order they appear in Content.
type arkSubmitRequest struct {
	Model         string           `json:"model"`
	GenerateAudio bool             `json:"generate_audio"`
	Ratio         string           `json:"ratio,omitempty"`
	Duration      int              `json:"duration,omitempty"`
	Watermark     bool             `json:"watermark"`
	Content       []arkContentPart `json:"content"`
}

// arkContentPart is one content entry of the task body. Exactly one payload
// field (Text, ImageURL, VideoURL, or AudioURL) is set for a given type.
type arkContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *arkMediaURL `json:"image_url,omitempty"`
	VideoURL *arkMediaURL `json:"video_url,omitempty"`
	AudioURL *arkMediaURL `json:"audio_url,omitempty"`
	Role     string       `json:"role,omitempty"`
}

// arkMediaURL wraps the url of one image/video/audio reference. image_url also
// accepts a base64 data URL in addition to http(s) URLs.
type arkMediaURL struct {
	URL string `json:"url"`
}

// arkTaskID is the submit response: {"id": "<task_id>"}.
type arkTaskID struct {
	ID string `json:"id"`
}

// arkTaskStatus models the task resource returned by
// GET {base}/contents/generations/tasks/{id}.
type arkTaskStatus struct {
	ID      string          `json:"id,omitempty"`
	Model   string          `json:"model,omitempty"`
	Status  string          `json:"status,omitempty"`
	Content *arkTaskContent `json:"content,omitempty"`
	Error   *arkTaskError   `json:"error,omitempty"`
	Usage   *arkTaskUsage   `json:"usage,omitempty"`
}

type arkTaskContent struct {
	VideoURL string `json:"video_url,omitempty"`
}

type arkTaskError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type arkTaskUsage struct {
	CompletionTokens int `json:"completion_tokens,omitempty"`
}

// GenerateArk submits a Seedance video-generation task through the Ark API and
// resolves the generated mp4 bytes: POST the task, poll it at pollInterval
// (production cadence 2s; the provider guideline is 10s) until it reaches a
// terminal state, and download content.video_url on success. See Params for
// the supported content mix (text prompt + reference images/videos/audios).
func GenerateArk(ctx context.Context, p Params) (*Result, error) {
	endpoint := p.Endpoint
	if endpoint == "" {
		endpoint = arkDefaultEndpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if p.Model == "" {
		return nil, fmt.Errorf("videogen/ark: model is required")
	}
	if p.Prompt == "" && len(p.ReferenceImages) == 0 && len(p.ReferenceVideos) == 0 && len(p.ReferenceAudios) == 0 {
		return nil, fmt.Errorf("videogen/ark: at least a prompt or one reference is required")
	}
	taskID, err := submitArkTask(ctx, p, endpoint)
	if err != nil {
		return nil, err
	}
	return pollArkTask(ctx, p, endpoint, taskID)
}

func submitArkTask(ctx context.Context, p Params, endpoint string) (string, error) {
	content := make([]arkContentPart, 0, 1+len(p.ReferenceImages)+len(p.ReferenceVideos)+len(p.ReferenceAudios))
	if p.Prompt != "" {
		content = append(content, arkContentPart{Type: "text", Text: p.Prompt})
	}
	for _, ref := range p.ReferenceImages {
		content = append(content, arkContentPart{Type: "image_url", ImageURL: &arkMediaURL{URL: ref}, Role: "reference_image"})
	}
	for _, ref := range p.ReferenceVideos {
		content = append(content, arkContentPart{Type: "video_url", VideoURL: &arkMediaURL{URL: ref}, Role: "reference_video"})
	}
	for _, ref := range p.ReferenceAudios {
		content = append(content, arkContentPart{Type: "audio_url", AudioURL: &arkMediaURL{URL: ref}, Role: "reference_audio"})
	}
	body := arkSubmitRequest{
		Model:         p.Model,
		GenerateAudio: p.GenerateAudio,
		Ratio:         p.AspectRatio,
		Duration:      p.Duration,
		Watermark:     p.Watermark,
		Content:       content,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("videogen/ark: marshal submit: %w", err)
	}
	respBody, err := arkPost(ctx, p, endpoint+arkTasksPath, payload)
	if err != nil {
		return "", err
	}
	var resp arkTaskID
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("videogen/ark: decode submit: %w (body: %s)", err, truncate(string(respBody)))
	}
	if resp.ID == "" {
		return "", fmt.Errorf("videogen/ark: submit returned no task id: %s", truncate(string(respBody)))
	}
	return resp.ID, nil
}

// pollArkTask repeatedly GETs the task resource until it succeeds, fails, or
// the context expires. Generation can take minutes, so the total budget is
// governed entirely by the caller's context.
func pollArkTask(ctx context.Context, p Params, endpoint, taskID string) (*Result, error) {
	pollURL := fmt.Sprintf("%s%s/%s", endpoint, arkTasksPath, taskID)
	for {
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("videogen/ark: task %s timed out: %w", taskID, ctx.Err())
		case <-timer.C:
		}
		respBody, err := arkGet(ctx, p, pollURL)
		if err != nil {
			return nil, err
		}
		var st arkTaskStatus
		if err := json.Unmarshal(respBody, &st); err != nil {
			return nil, fmt.Errorf("videogen/ark: decode poll: %w (body: %s)", err, truncate(string(respBody)))
		}
		switch st.Status {
		case "":
			// Provider did not report a status; keep polling until the context
			// expires rather than failing on an undocumented transient state.
			continue
		case "queued", "running":
			// Still in flight: loop and poll again.
		case "succeeded":
			if st.Content == nil || st.Content.VideoURL == "" {
				return nil, fmt.Errorf("videogen/ark: task %s succeeded without video_url: %s", taskID, truncate(string(respBody)))
			}
			return downloadArkVideo(ctx, p, endpoint, st.Content.VideoURL)
		case "failed":
			return nil, fmt.Errorf("videogen/ark: task %s failed: %s", taskID, arkErrMessage(st.Error))
		default:
			return nil, fmt.Errorf("videogen/ark: task %s unexpected status %q", taskID, st.Status)
		}
	}
}

// arkErrMessage renders a task failure cause, truncating the provider message
// so an oversized error body never floods logs or callers.
func arkErrMessage(e *arkTaskError) string {
	if e == nil || e.Message == "" {
		return "unknown error"
	}
	if e.Code != "" {
		return fmt.Sprintf("[%s] %s", e.Code, truncate(e.Message))
	}
	return truncate(e.Message)
}

// downloadArkVideo fetches the generated mp4 from the task's video_url. The
// Bearer token is sent only when the URL is served by the same host as the API
// endpoint, so that presigned/public CDN URLs are not broken by an unexpected
// Authorization header (e.g. S3 presigned URLs reject Authorization).
func downloadArkVideo(ctx context.Context, p Params, endpoint, videoURL string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/ark: build download: %w", err)
	}
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	if sameHost(videoURL, endpoint) {
		req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	}
	body, err := arkRoundtrip(req, "download", p)
	if err != nil {
		return nil, err
	}
	return &Result{Data: body, MimeType: "video/mp4", URL: videoURL}, nil
}

// ---- HTTP -----------------------------------------------------------------

func arkPost(ctx context.Context, p Params, target string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("videogen/ark: build request: %w", err)
	}
	setArkHeaders(req, p)
	return arkRoundtrip(req, "submit", p)
}

func arkGet(ctx context.Context, p Params, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/ark: build request: %w", err)
	}
	setArkHeaders(req, p)
	return arkRoundtrip(req, "poll", p)
}

func setArkHeaders(req *http.Request, p Params) {
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

func arkRoundtrip(req *http.Request, label string, p Params) ([]byte, error) {
	client, err := httpClientForParams(p)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("videogen/ark: %s: %w", label, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("videogen/ark: %s read: %w", label, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("videogen/ark: %s http %d: %s", label, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}