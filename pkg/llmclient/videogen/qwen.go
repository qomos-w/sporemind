// The Qwen backend drives Alibaba Cloud Model Studio (DashScope / Bailian)
// Wan (通义万相) video generation through the video-synthesis async-task API:
// submit a task with a text prompt (plus an optional first-frame image and an
// optional audio track), poll the task resource until it succeeds or fails,
// then download the generated mp4 from output.video_url. It is the Qwen-family
// counterpart of the Ark (Seedance) and Gemini (Veo) backends.
package videogen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// qwenDefaultEndpoint is the DashScope API base used when Params.Endpoint is
	// empty (mainland China). The Singapore region uses qwenIntlEndpoint;
	// callers in that region set Params.Endpoint explicitly.
	qwenDefaultEndpoint = "https://dashscope.aliyuncs.com/api/v1"
	qwenIntlEndpoint    = "https://dashscope-intl.aliyuncs.com/api/v1"

	// qwenAPIBasePath is the native DashScope API prefix. Unlike the
	// OpenAI-compatible surface (…/compatible-mode/v1), the async task API lives
	// under /api/v1.
	qwenAPIBasePath = "/api/v1"

	// qwenCompatibleModeSuffix is the OpenAI-compatible base suffix. A media
	// account frequently stores that base URL because it also serves chat, so
	// the native prefix is substituted before the task paths are appended.
	qwenCompatibleModeSuffix = "/compatible-mode/v1"

	// qwenVideoSynthesisPath is the async task-creation endpoint
	// (POST {base}/services/aigc/video-generation/video-synthesis).
	qwenVideoSynthesisPath = "/services/aigc/video-generation/video-synthesis"

	// qwenTasksPath is the task-resource prefix; the task id returned by the
	// submit call is appended to build the poll URL
	// (GET {base}/tasks/{task_id}).
	qwenTasksPath = "/tasks"

	// qwenDefaultModel is the model used when Params.Model is empty: Wan 2.5
	// text-to-video at 720p.
	qwenDefaultModel = "wan-2.5-t2v-720p"

	// qwenStatusPending / qwenStatusRunning are the non-terminal task states;
	// qwenStatusSucceeded, qwenStatusFailed, and qwenStatusCanceled are terminal.
	// DashScope reports them upper-cased.
	qwenStatusPending   = "PENDING"
	qwenStatusRunning   = "RUNNING"
	qwenStatusSucceeded = "SUCCEEDED"
	qwenStatusFailed    = "FAILED"
	qwenStatusCanceled  = "CANCELED"
	// qwenStatusCancelled is the British spelling some DashScope regions
	// return; both are treated as terminal failures.
	qwenStatusCancelled = "CANCELLED"
)

// qwenSubmitRequest is the task-creation body accepted by
// POST {base}/services/aigc/video-generation/video-synthesis. The Wan 2.5
// family auto-dubs background audio when input.audio_url is omitted, so no
// audio toggle is sent; watermark is always sent so the provider sees the
// caller's explicit choice (its own default is false).
type qwenSubmitRequest struct {
	Model      string         `json:"model"`
	Input      qwenTaskInput  `json:"input"`
	Parameters qwenParameters `json:"parameters"`
}

// qwenTaskInput is the input object. Only the fields the shared Params can
// express are sent: prompt (text-to-video) plus the optional first-frame image
// (img_url, image-to-video models) and audio track (audio_url, Wan 2.5 audio
// driving).
type qwenTaskInput struct {
	Prompt   string `json:"prompt,omitempty"`
	ImageURL string `json:"img_url,omitempty"`
	AudioURL string `json:"audio_url,omitempty"`
}

// qwenParameters carries the Wan video parameters. Size is the "width*height"
// token from qwenSize; an unsupported ratio yields an empty size and the
// provider default applies.
type qwenParameters struct {
	Size      string `json:"size,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	Watermark bool   `json:"watermark"`
}

// qwenTaskResource models the task resource returned by both the submit call
// (task_id plus an initial task_status) and the poll call (task_status, and
// either video_url on success or code/message on failure). Task-level failures
// carry code/message inside output; request-level errors carry them at the top
// level, so both envelopes are decoded.
type qwenTaskResource struct {
	RequestID string         `json:"request_id,omitempty"`
	Output    qwenTaskOutput `json:"output,omitempty"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
}

type qwenTaskOutput struct {
	TaskID     string `json:"task_id,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
	VideoURL   string `json:"video_url,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
}

// GenerateQwen submits a Wan video-generation task through the DashScope
// video-synthesis API and resolves the generated mp4 bytes: POST the task with
// the X-DashScope-Async header, poll {base}/tasks/{task_id} at pollInterval
// until it reaches a terminal state, and download output.video_url on success.
// See Params for the supported content mix (text prompt + optional first-frame
// image + optional audio track).
func GenerateQwen(ctx context.Context, p Params) (*Result, error) {
	endpoint := normalizeQwenEndpoint(p.Endpoint)
	if p.Model == "" {
		p.Model = qwenDefaultModel
	}
	if p.Prompt == "" && len(p.ReferenceImages) == 0 {
		return nil, fmt.Errorf("videogen/qwen: at least a prompt or one reference image is required")
	}
	if len(p.ReferenceVideos) > 0 {
		// The Wan video-synthesis API has no reference-video input; failing
		// loudly keeps the caller from believing the clip influenced the result.
		return nil, fmt.Errorf("videogen/qwen: reference videos are not supported by the Wan video API; supply a prompt, a first-frame image, or an audio track")
	}
	taskID, err := submitQwenTask(ctx, p, endpoint)
	if err != nil {
		return nil, err
	}
	return pollQwenTask(ctx, p, endpoint, taskID)
}

// normalizeQwenEndpoint resolves a configured endpoint into the native
// DashScope API base. An empty endpoint yields qwenDefaultEndpoint; a bare host
// gains qwenAPIBasePath; an OpenAI-compatible base (…/compatible-mode/v1) has
// that suffix replaced by qwenAPIBasePath; every other base (native /api/v1 or
// a relay's own path) is kept as-is.
func normalizeQwenEndpoint(endpoint string) string {
	e := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if e == "" {
		return qwenDefaultEndpoint
	}
	if strings.HasSuffix(strings.ToLower(e), qwenCompatibleModeSuffix) {
		e = strings.TrimRight(e[:len(e)-len(qwenCompatibleModeSuffix)], "/")
	}
	if strings.HasSuffix(strings.ToLower(e), qwenAPIBasePath) {
		return e
	}
	if !hasURLPath(e) {
		return e + qwenAPIBasePath
	}
	return e
}

// hasURLPath reports whether raw carries a path beyond its host (and optional
// port). Unparsable input counts as "has a path" so it is passed through
// untouched rather than appended to.
func hasURLPath(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	return strings.Trim(u.Path, "/") != ""
}

// qwenSize maps an aspect ratio to the Wan "width*height" size token. The table
// follows the official Wan ratio/size matrix; a ratio outside it returns "" so
// the provider default applies instead of guessing a size the model may refuse.
func qwenSize(aspectRatio string) string {
	switch strings.TrimSpace(aspectRatio) {
	case "16:9":
		return "1280*720"
	case "9:16":
		return "720*1280"
	case "1:1":
		return "960*960"
	case "4:3":
		return "1104*832"
	case "3:4":
		return "832*1104"
	}
	return ""
}

func submitQwenTask(ctx context.Context, p Params, endpoint string) (string, error) {
	input := qwenTaskInput{Prompt: p.Prompt}
	if len(p.ReferenceImages) > 0 {
		// The first reference image is the first frame: the Wan
		// image-to-video models consume input.img_url. Text-to-video models
		// reject it, so it is only sent when the caller supplied one.
		input.ImageURL = p.ReferenceImages[0]
	}
	if len(p.ReferenceAudios) > 0 {
		input.AudioURL = p.ReferenceAudios[0]
	}
	body := qwenSubmitRequest{
		Model: p.Model,
		Input: input,
		Parameters: qwenParameters{
			Size:      qwenSize(p.AspectRatio),
			Duration:  p.Duration,
			Watermark: p.Watermark,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("videogen/qwen: marshal submit: %w", err)
	}
	respBody, err := qwenPost(ctx, p, endpoint+qwenVideoSynthesisPath, payload)
	if err != nil {
		return "", err
	}
	var resp qwenTaskResource
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return "", fmt.Errorf("videogen/qwen: decode submit: %w (body: %s)", err, truncate(string(respBody)))
	}
	if strings.EqualFold(resp.Output.TaskStatus, qwenStatusFailed) {
		return "", fmt.Errorf("videogen/qwen: submit failed: %s", qwenErrMessage(&resp, respBody))
	}
	if resp.Output.TaskID == "" {
		return "", fmt.Errorf("videogen/qwen: submit returned no task id: %s", qwenErrMessage(&resp, respBody))
	}
	return resp.Output.TaskID, nil
}

// pollQwenTask repeatedly GETs the task resource until it succeeds, fails, or
// the context expires. Generation can take minutes, so the total budget is
// governed entirely by the caller's context.
func pollQwenTask(ctx context.Context, p Params, endpoint, taskID string) (*Result, error) {
	pollURL := endpoint + qwenTasksPath + "/" + taskID
	for {
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("videogen/qwen: task %s timed out: %w", taskID, ctx.Err())
		case <-timer.C:
		}
		respBody, err := qwenGet(ctx, p, pollURL)
		if err != nil {
			return nil, err
		}
		var st qwenTaskResource
		if err := json.Unmarshal(respBody, &st); err != nil {
			return nil, fmt.Errorf("videogen/qwen: decode poll: %w (body: %s)", err, truncate(string(respBody)))
		}
		switch strings.ToUpper(strings.TrimSpace(st.Output.TaskStatus)) {
		case "":
			// Provider did not report a status; keep polling until the context
			// expires rather than failing on an undocumented transient state.
			continue
		case qwenStatusPending, qwenStatusRunning:
			// Still in flight: loop and poll again.
		case qwenStatusSucceeded:
			if st.Output.VideoURL == "" {
				return nil, fmt.Errorf("videogen/qwen: task %s succeeded without video_url: %s", taskID, truncate(string(respBody)))
			}
			return downloadQwenVideo(ctx, p, endpoint, st.Output.VideoURL)
		case qwenStatusFailed, qwenStatusCanceled, qwenStatusCancelled:
			return nil, fmt.Errorf("videogen/qwen: task %s failed: %s", taskID, qwenErrMessage(&st, respBody))
		default:
			return nil, fmt.Errorf("videogen/qwen: task %s unexpected status %q", taskID, st.Output.TaskStatus)
		}
	}
}

// qwenErrMessage renders a task failure cause, preferring the task-level
// output.code/output.message and falling back to the request-level envelope
// (and finally to the raw body, which callers have already truncated). The
// provider message is truncated so an oversized error never floods logs.
// raw must be the (already truncated) response body.
func qwenErrMessage(st *qwenTaskResource, raw []byte) string {
	code, message := st.Code, st.Message
	if st.Output.Code != "" || st.Output.Message != "" {
		code, message = st.Output.Code, st.Output.Message
	}
	if message == "" {
		if code != "" {
			return "[" + truncate(code) + "]"
		}
		return truncate(string(raw))
	}
	if code != "" {
		return fmt.Sprintf("[%s] %s", truncate(code), truncate(message))
	}
	return truncate(message)
}

// downloadQwenVideo fetches the generated mp4 from the task's video_url. The
// Bearer token is sent only when the URL is served by the same host as the API
// endpoint: DashScope hands out presigned OSS URLs on a different host, and an
// unexpected Authorization header makes those URLs fail.
func downloadQwenVideo(ctx context.Context, p Params, endpoint, videoURL string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/qwen: build download: %w", err)
	}
	req.Header.Set("User-Agent", userAgentForParams(p))
	if sameHost(videoURL, endpoint) {
		req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	}
	body, err := qwenRoundtrip(req, "download", p)
	if err != nil {
		return nil, err
	}
	return &Result{Data: body, MimeType: "video/mp4", URL: videoURL}, nil
}

// ---- HTTP -----------------------------------------------------------------

func qwenPost(ctx context.Context, p Params, target string, payload []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("videogen/qwen: build request: %w", err)
	}
	setQwenHeaders(req, p)
	return qwenRoundtrip(req, "submit", p)
}

func qwenGet(ctx context.Context, p Params, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("videogen/qwen: build request: %w", err)
	}
	setQwenHeaders(req, p)
	return qwenRoundtrip(req, "poll", p)
}

// setQwenHeaders applies the DashScope credentials plus the async marker. The
// X-DashScope-Async header is required on task creation (the HTTP API is
// asynchronous only) and is set on POST alone, so the poll GET carries just the
// credentials.
func setQwenHeaders(req *http.Request, p Params) {
	req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	req.Header.Set("User-Agent", userAgentForParams(p))
	if req.Method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DashScope-Async", "enable")
	}
}

// userAgentForParams resolves the User-Agent for a request, falling back to the
// package default.
func userAgentForParams(p Params) string {
	if p.UserAgent != "" {
		return p.UserAgent
	}
	return defaultUserAgent
}

func qwenRoundtrip(req *http.Request, label string, p Params) ([]byte, error) {
	client, err := httpClientForParams(p)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("videogen/qwen: %s: %w", label, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("videogen/qwen: %s read: %w", label, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("videogen/qwen: %s http %d: %s", label, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}
