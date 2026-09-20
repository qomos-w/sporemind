package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// Qwen image generation (Alibaba Cloud Model Studio / DashScope).
//
// DashScope splits image generation across three task endpoints, and which one
// serves a model is a documented property of the model family:
//
//   - /api/v1/services/aigc/image-generation/generation — asynchronous
//     (X-DashScope-Async: enable), "messages" payload. Serves the qwen-image-3.0
//     series (the default family here) and the wan2.6/wan2.7 image families;
//     text-to-image and image-to-image/editing share one request shape.
//   - /api/v1/services/aigc/text2image/image-synthesis — asynchronous,
//     "input.prompt" payload. Serves the legacy asynchronous text-to-image
//     models (qwen-image, qwen-image-plus, wanx*); prompt-only, no references.
//   - /api/v1/services/aigc/multimodal-generation/generation — synchronous,
//     "messages" payload. Serves every remaining legacy model (qwen-image-max,
//     the qwen-image-2.0 series) and is their documented image-to-image /
//     editing route.
//
// All three report outcomes the same way, so one submitter covers them: a
// response that already carries an image URL resolves immediately, one that
// carries a task id is polled through /api/v1/tasks/{task_id} until it reaches
// a terminal state (SUCCEEDED | FAILED | CANCELED | UNKNOWN), and the image
// bytes are then downloaded from the task's presigned (24h) URL.
//
// DashScope's OpenAI-compatible /images/generations endpoint is deliberately
// not used here: it is synchronous-only and cannot edit with reference images;
// providers whose Kind is "openai" keep riding the openai path.
const (
	qwenDefaultEndpoint   = "https://dashscope.aliyuncs.com"
	qwenDefaultImageModel = "qwen-image-3.0-pro"

	qwenGenerationPath = "/api/v1/services/aigc/image-generation/generation"
	qwenSynthesisPath  = "/api/v1/services/aigc/text2image/image-synthesis"
	qwenMultimodalPath = "/api/v1/services/aigc/multimodal-generation/generation"
	qwenTasksPath      = "/api/v1/tasks/"
)

// qwenPollInterval is the delay between DashScope task-status polls: 2s sits in
// the provider's 2-5s guidance and is short enough that a 10-60s generation
// resolves promptly. Declared as a var so tests can shorten it.
var qwenPollInterval = 2 * time.Second

// qwenContent is one entry of a message content array: exactly one of text or
// image is set per entry.
type qwenContent struct {
	Text  string `json:"text,omitempty"`
	Image string `json:"image,omitempty"`
}

type qwenMessage struct {
	Role    string        `json:"role"`
	Content []qwenContent `json:"content"`
}

// qwenInput carries the prompt either as messages (unified/multimodal
// endpoints) or as input.prompt (legacy text2image endpoint).
type qwenInput struct {
	Prompt   string        `json:"prompt,omitempty"`
	Messages []qwenMessage `json:"messages,omitempty"`
}

type qwenParameters struct {
	Size string `json:"size,omitempty"`
	N    int    `json:"n,omitempty"`
	// PromptExtend and Watermark are sent explicitly (the wire defaults are
	// not identical across the legacy and 3.0 families).
	PromptExtend bool `json:"prompt_extend"`
	Watermark    bool `json:"watermark"`
}

type qwenSubmitRequest struct {
	Model      string         `json:"model"`
	Input      qwenInput      `json:"input"`
	Parameters qwenParameters `json:"parameters"`
}

// qwenTaskOutput is the shared "output" object of submit, poll, and synchronous
// responses: task bookkeeping, the error fields of a failed task, and the two
// result shapes DashScope uses (choices[].message.content[].image for the
// unified/multimodal endpoints, results[].url for the legacy one).
type qwenTaskOutput struct {
	TaskID     string `json:"task_id,omitempty"`
	TaskStatus string `json:"task_status,omitempty"`
	Code       string `json:"code,omitempty"`
	Message    string `json:"message,omitempty"`
	Choices    []struct {
		FinishReason string `json:"finish_reason,omitempty"`
		Message      struct {
			Role    string              `json:"role,omitempty"`
			Content []qwenResultContent `json:"content,omitempty"`
		} `json:"message"`
	} `json:"choices,omitempty"`
	Results []struct {
		URL string `json:"url,omitempty"`
	} `json:"results,omitempty"`
}

// qwenResultContent is the response-side content entry ({"image": "...",
// "type": "image"}).
type qwenResultContent struct {
	Image string `json:"image,omitempty"`
	Type  string `json:"type,omitempty"`
}

type qwenTaskResponse struct {
	Output    qwenTaskOutput `json:"output"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// qwenSubmitShape identifies which DashScope endpoint (and payload shape) a
// model is served by.
type qwenSubmitShape int

const (
	// qwenShapeGeneration: async image-generation/generation, messages payload.
	qwenShapeGeneration qwenSubmitShape = iota
	// qwenShapeSynthesis: async text2image/image-synthesis, input.prompt.
	qwenShapeSynthesis
	// qwenShapeMultimodal: sync multimodal-generation/generation, messages.
	qwenShapeMultimodal
)

func generateQwen(ctx context.Context, p Params) (*Result, error) {
	model := p.Model
	if model == "" {
		model = qwenDefaultImageModel
	}
	endpoint := normalizeQwenEndpoint(p.Endpoint)
	tag := fmt.Sprintf("imagegen/qwen: model %q via %s", model, endpoint)

	client := httpClient
	if p.Proxy != "" {
		hc, err := llmclient.HTTPClientForProxy(p.Proxy)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tag, err)
		}
		client = hc
	}

	submitURL, body, async := qwenSubmit(model, p, endpoint)
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal body: %w", tag, err)
	}

	respBody, err := qwenDo(ctx, client, http.MethodPost, submitURL, payload, p, async)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tag, err)
	}
	var submitted qwenTaskResponse
	if err := json.Unmarshal(respBody, &submitted); err != nil {
		return nil, fmt.Errorf("%s: decode submit: %w", tag, err)
	}
	if u := qwenImageURL(submitted.Output); u != "" {
		res, err := downloadImage(ctx, client, u)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tag, err)
		}
		return res, nil
	}
	if msg := qwenAPIError(submitted); msg != "" {
		return nil, fmt.Errorf("%s: submit failed: %s", tag, msg)
	}
	if submitted.Output.TaskID == "" {
		return nil, fmt.Errorf("%s: submit returned no task id: %s", tag, truncate(string(respBody)))
	}
	return qwenPoll(ctx, client, endpoint, submitted.Output.TaskID, p, tag)
}

// qwenSubmit builds the submit request (endpoint URL, body, and whether the
// asynchronous header applies) for a model, its prompt, and any references.
func qwenSubmit(model string, p Params, endpoint string) (string, qwenSubmitRequest, bool) {
	params := qwenParameters{N: 1, PromptExtend: true, Watermark: false}
	refs := p.referenceImages()

	switch qwenSubmitShapeFor(model, len(refs) > 0) {
	case qwenShapeGeneration:
		params.Size = qwenCustomSize(p.Size, p.AspectRatio)
		return endpoint + qwenGenerationPath, qwenSubmitRequest{
			Model:      model,
			Input:      qwenInput{Messages: qwenMessages(refs, p.Prompt)},
			Parameters: params,
		}, true

	case qwenShapeMultimodal:
		params.Size = qwenLegacySize(p.AspectRatio)
		return endpoint + qwenMultimodalPath, qwenSubmitRequest{
			Model:      model,
			Input:      qwenInput{Messages: qwenMessages(refs, p.Prompt)},
			Parameters: params,
		}, false

	default:
		params.Size = qwenLegacySize(p.AspectRatio)
		return endpoint + qwenSynthesisPath, qwenSubmitRequest{
			Model:      model,
			Input:      qwenInput{Prompt: p.Prompt},
			Parameters: params,
		}, true
	}
}

// qwenMessages builds the single-message payload shared by the messages-shaped
// endpoints: any reference images first, then the prompt — DashScope documents
// image-to-image as 1-3 image entries followed by exactly one text entry.
func qwenMessages(refs []string, prompt string) []qwenMessage {
	content := make([]qwenContent, 0, len(refs)+1)
	for _, ref := range refs {
		content = append(content, qwenContent{Image: qwenImageRef(ref)})
	}
	content = append(content, qwenContent{Text: prompt})
	return []qwenMessage{{Role: "user", Content: content}}
}

// qwenSubmitShapeFor maps a model onto its DashScope task endpoint. The
// dispatch is by model family because DashScope documents the endpoint as a
// property of the family, not of the request:
//
//	qwen-image-3.0*, wan2.6*, wan2.7*  → generation (async, messages)
//	qwen-image-2*, *-max, *-edit       → multimodal (sync, messages)
//	anything else with references      → multimodal (image-to-image needs it)
//	anything else                      → synthesis (async, input.prompt)
func qwenSubmitShapeFor(model string, hasRefs bool) qwenSubmitShape {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "qwen-image-3"),
		strings.HasPrefix(m, "wan2.6"),
		strings.HasPrefix(m, "wan2.7"):
		return qwenShapeGeneration
	case strings.HasPrefix(m, "qwen-image-2"),
		strings.Contains(m, "qwen-image-max"),
		strings.HasSuffix(m, "-edit"):
		// Sync-only families; the multimodal endpoint is also their documented
		// image-to-image/editing route.
		return qwenShapeMultimodal
	case hasRefs:
		// text2image/image-synthesis takes a prompt only, so a reference-image
		// request must ride the multimodal endpoint even for async models.
		return qwenShapeMultimodal
	default:
		return qwenShapeSynthesis
	}
}

// qwenPoll polls /api/v1/tasks/{task_id} until the task reaches a terminal
// state, then downloads the resulting image. The overall budget is governed by
// the caller-supplied context.
func qwenPoll(ctx context.Context, client *http.Client, endpoint, taskID string, p Params, tag string) (*Result, error) {
	pollURL := endpoint + qwenTasksPath + taskID
	for {
		timer := time.NewTimer(qwenPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("%s: task %s timed out: %w", tag, taskID, ctx.Err())
		case <-timer.C:
		}
		respBody, err := qwenDo(ctx, client, http.MethodGet, pollURL, nil, p, false)
		if err != nil {
			return nil, fmt.Errorf("%s: poll task %s: %w", tag, taskID, err)
		}
		var st qwenTaskResponse
		if err := json.Unmarshal(respBody, &st); err != nil {
			return nil, fmt.Errorf("%s: decode poll: %w", tag, err)
		}
		switch strings.ToUpper(strings.TrimSpace(st.Output.TaskStatus)) {
		case "SUCCEEDED":
			u := qwenImageURL(st.Output)
			if u == "" {
				return nil, fmt.Errorf("%s: task %s succeeded without an image url: %s", tag, taskID, truncate(string(respBody)))
			}
			res, err := downloadImage(ctx, client, u)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", tag, err)
			}
			return res, nil
		case "FAILED", "CANCELED", "UNKNOWN":
			return nil, fmt.Errorf("%s: task %s %s: %s", tag, taskID, strings.ToLower(st.Output.TaskStatus), qwenAPIError(st))
		case "", "PENDING", "RUNNING":
			// Still in flight: poll again.
		default:
			return nil, fmt.Errorf("%s: task %s unexpected status %q", tag, taskID, st.Output.TaskStatus)
		}
	}
}

// qwenDo performs one DashScope round trip and returns the (truncated-safe)
// response body, mapping HTTP failures to an error carrying the provider body.
// The async header is set only on submits that must create a task.
func qwenDo(ctx context.Context, client *http.Client, method, target string, payload []byte, p Params, async bool) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	if async {
		req.Header.Set("X-DashScope-Async", "enable")
	}
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(respBody)))
	}
	return respBody, nil
}

// qwenImageURL extracts the first generated image URL from a task output,
// covering both DashScope result shapes: choices[].message.content[].image
// (unified and multimodal endpoints) and results[].url (legacy synthesis).
func qwenImageURL(o qwenTaskOutput) string {
	for _, choice := range o.Choices {
		for _, c := range choice.Message.Content {
			if c.Image != "" {
				return c.Image
			}
		}
	}
	for _, r := range o.Results {
		if r.URL != "" {
			return r.URL
		}
	}
	return ""
}

// qwenAPIError renders the error a DashScope response carries, if any. The
// task-level fields win over the envelope-level ones (a failed poll reports the
// cause inside output; a rejected submit reports it at the top level).
func qwenAPIError(st qwenTaskResponse) string {
	if s := joinQwenCodeMessage(st.Output.Code, st.Output.Message); s != "" {
		return s
	}
	return joinQwenCodeMessage(st.Code, st.Message)
}

func joinQwenCodeMessage(code, message string) string {
	switch {
	case code != "" && message != "":
		return fmt.Sprintf("[%s] %s", code, truncate(message))
	case code != "":
		return "[" + code + "]"
	case message != "":
		return truncate(message)
	default:
		return ""
	}
}

// normalizeQwenEndpoint resolves the DashScope host root from a provider base
// URL: the native task API hangs off the host root, while relayed/LLM provider
// BaseURLs are routinely stored with an OpenAI-style suffix
// (https://dashscope.aliyuncs.com/compatible-mode/v1), which is trimmed here.
func normalizeQwenEndpoint(endpoint string) string {
	e := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if e == "" {
		return qwenDefaultEndpoint
	}
	lower := strings.ToLower(e)
	for _, suffix := range []string{"/compatible-mode/v1", "/api/v1", "/v1"} {
		if strings.HasSuffix(lower, suffix) {
			return strings.TrimRight(e[:len(e)-len(suffix)], "/")
		}
	}
	return e
}

// qwenImageRef normalizes one reference image into a form DashScope accepts: a
// public http(s) URL or a data URL. Raw base64 (no data: prefix) is wrapped
// with an image/png mime so the provider can decode it.
func qwenImageRef(ref string) string {
	if strings.HasPrefix(ref, "data:") ||
		strings.HasPrefix(ref, "http://") ||
		strings.HasPrefix(ref, "https://") {
		return ref
	}
	return "data:image/png;base64," + ref
}

// qwenCustomSize maps the relative size budget ("1K"|"2K"|"4K", empty = 1K) and
// aspect ratio onto a DashScope "width*height" string for the families that
// accept arbitrary dimensions (qwen-image-3.0, wan2.6/2.7 image): the model
// allows any resolution whose total pixel area is within 512*512..2048*2048, so
// 1K picks a 1024 edge and 2K/4K the 2048 maximum. Dimensions are multiples of
// 16.
func qwenCustomSize(size, aspect string) string {
	edge := 1024
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "2K", "4K":
		edge = 2048
	}
	switch strings.TrimSpace(aspect) {
	case "16:9":
		return fmt.Sprintf("%d*%d", edge, edge*9/16)
	case "9:16":
		return fmt.Sprintf("%d*%d", edge*9/16, edge)
	case "4:3":
		return fmt.Sprintf("%d*%d", edge, edge*3/4)
	case "3:4":
		return fmt.Sprintf("%d*%d", edge*3/4, edge)
	default:
		return fmt.Sprintf("%d*%d", edge, edge)
	}
}

// qwenLegacySize maps the aspect ratio onto the historical fixed resolution set
// of the legacy qwen-image family. Those models validate `size` against a closed
// list (qwen-image-max/plus reject anything else), and the largest common
// denominator accepted by qwen-image, qwen-image-plus, qwen-image-max and the
// qwen-image-2.0 series is this set — so a 2K/4K budget cannot lift them and
// only the aspect ratio picks a resolution.
func qwenLegacySize(aspect string) string {
	switch strings.TrimSpace(aspect) {
	case "4:3":
		return "1472*1104"
	case "9:16":
		return "928*1664"
	case "3:4":
		return "1104*1472"
	case "1:1":
		return "1328*1328"
	default:
		// 16:9 is the family default.
		return "1664*928"
	}
}
