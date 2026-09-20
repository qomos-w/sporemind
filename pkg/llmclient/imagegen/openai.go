package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/qomos-w/sporemind/pkg/llmclient"
)

type openaiImageRequest struct {
	Model        string `json:"model"`
	Prompt       string `json:"prompt"`
	N            int    `json:"n"`
	Size         string `json:"size,omitempty"`
	Quality      string `json:"quality,omitempty"`
	OutputFormat string `json:"output_format"`
}

type openaiImageResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		URL           string `json:"url"`
		RevisedPrompt string `json:"revised_prompt,omitempty"`
	} `json:"data"`
}

func generateOpenAI(ctx context.Context, p Params) (*Result, error) {
	quality := mapOpenAIQuality(p.Quality)
	size := mapOpenAISize(p.Model, p.Size, p.AspectRatio)
	endpoint := strings.TrimRight(p.Endpoint, "/")
	// tag rides every error so a failed call is attributable (which model on
	// which endpoint) without logs.
	tag := fmt.Sprintf("imagegen/openai: model %q via %s", p.Model, endpoint)

	// OpenAI's /images/edits multipart accepts base64 only: base64 entries
	// (raw or data-URL) ride the body as form files, while URL references are
	// dropped and a URL-only request degrades to /images/generations without
	// reference images (documented limitation: openai edits accepts base64).
	refs := p.referenceImages()
	images := base64ReferenceImages(refs)
	path := openAIEndpointPath(refs)
	contentType := "application/json"
	var payload []byte
	var err error
	if path == openAIEditsPath {
		contentType, payload, err = buildOpenAIEditsBody(p, images, size, quality)
	} else {
		payload, err = json.Marshal(openaiImageRequest{
			Model:        p.Model,
			Prompt:       p.Prompt,
			N:            1,
			Size:         size,
			Quality:      quality,
			OutputFormat: "png",
		})
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tag, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: new request: %w", tag, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)

	client := httpClient
	if p.Proxy != "" {
		hc, err := llmclient.HTTPClientForProxy(p.Proxy)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tag, err)
		}
		client = hc
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tag, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", tag, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// Relays whose image models ride /chat/completions (OpenRouter-style)
		// expose no images API at all; retry there before failing.
		return generateOpenAIChat(ctx, client, p, endpoint, tag)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", tag, resp.StatusCode, truncate(string(respBody)))
	}

	var result openaiImageResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", tag, err)
	}
	for _, d := range result.Data {
		if d.B64JSON != "" {
			data, err := base64.StdEncoding.DecodeString(d.B64JSON)
			if err != nil {
				return nil, fmt.Errorf("%s: decode base64: %w", tag, err)
			}
			return &Result{Data: data, MimeType: "image/png"}, nil
		}
		if d.URL != "" {
			// Many providers (GLM cogview, dall-e, most relays) return a URL
			// instead of b64_json.
			res, err := downloadImage(ctx, client, d.URL)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", tag, err)
			}
			return res, nil
		}
	}
	return nil, fmt.Errorf("%s: no image data in response: %s", tag, truncate(string(respBody)))
}

// ---- chat/completions image fallback --------------------------------------
//
// OpenRouter-style relays serve image models (nano banana etc.) through
// /chat/completions: the images endpoints 404 and the image comes back as
// multimodal message content (message.images or image_url content parts).

type chatImageURL struct {
	URL string `json:"url"`
}

type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageMessage struct {
	Role    string            `json:"role"`
	Content []chatContentPart `json:"content"`
}

type chatImageRequest struct {
	Model    string             `json:"model"`
	Messages []chatImageMessage `json:"messages"`
}

type chatImageResponse struct {
	Choices []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
			Images  []struct {
				Type     string        `json:"type"`
				ImageURL *chatImageURL `json:"image_url"`
			} `json:"images"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func generateOpenAIChat(ctx context.Context, client *http.Client, p Params, endpoint, tag string) (*Result, error) {
	content := []chatContentPart{{Type: "text", Text: p.Prompt}}
	for _, ref := range p.referenceImages() {
		if !strings.HasPrefix(ref, "data:") && !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
			ref = "data:image/png;base64," + ref
		}
		content = append(content, chatContentPart{Type: "image_url", ImageURL: &chatImageURL{URL: ref}})
	}
	payload, err := json.Marshal(chatImageRequest{
		Model:    p.Model,
		Messages: []chatImageMessage{{Role: "user", Content: content}},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: chat fallback marshal: %w", tag, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: chat fallback new request: %w", tag, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.AuthToken)
	ua := p.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	req.Header.Set("User-Agent", ua)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: chat fallback: %w", tag, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: chat fallback read body: %w", tag, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d (images endpoint 404, chat/completions fallback): %s", tag, resp.StatusCode, truncate(string(respBody)))
	}
	var parsed chatImageResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("%s: chat fallback decode: %w", tag, err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, fmt.Errorf("%s: chat fallback: %s", tag, truncate(parsed.Error.Message))
	}
	imageURL := firstChatImageURL(parsed)
	if imageURL == "" {
		return nil, fmt.Errorf("%s: chat fallback returned no image: %s", tag, truncate(string(respBody)))
	}
	if data, mime, ok := splitReferenceImage(imageURL); ok {
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, fmt.Errorf("%s: chat fallback decode base64: %w", tag, err)
		}
		return &Result{Data: raw, MimeType: mime}, nil
	}
	res, err := downloadImage(ctx, client, imageURL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tag, err)
	}
	return res, nil
}

// firstChatImageURL extracts the first image reference from a chat response:
// message.images (one-api/new-api convention) first, then multimodal content
// parts.
func firstChatImageURL(r chatImageResponse) string {
	if len(r.Choices) == 0 {
		return ""
	}
	msg := r.Choices[0].Message
	for _, img := range msg.Images {
		if img.ImageURL != nil && img.ImageURL.URL != "" {
			return img.ImageURL.URL
		}
	}
	var parts []chatContentPart
	if err := json.Unmarshal(msg.Content, &parts); err != nil {
		return ""
	}
	for _, part := range parts {
		if part.Type == "image_url" && part.ImageURL != nil && part.ImageURL.URL != "" {
			return part.ImageURL.URL
		}
	}
	return ""
}

// OpenAI images endpoint paths.
const (
	openAIGenerationsPath = "/images/generations"
	openAIEditsPath       = "/images/edits"
)

// openAIEndpointPath selects the images endpoint: /images/edits when at least
// one base64 reference image can ride the multipart body, /images/generations
// otherwise. URL-only references cannot enter edits multipart, so they route
// to generations without reference images (documented limitation: openai edits
// accepts base64 only).
func openAIEndpointPath(refs []string) string {
	if len(base64ReferenceImages(refs)) > 0 {
		return openAIEditsPath
	}
	return openAIGenerationsPath
}

// base64ReferenceImages keeps the base64 subset of the effective reference
// list (raw base64 or data URLs), stripping data-URL prefixes.
func base64ReferenceImages(refs []string) []string {
	images := make([]string, 0, len(refs))
	for _, ref := range refs {
		if data, _, ok := splitReferenceImage(ref); ok {
			images = append(images, data)
		}
	}
	return images
}

// buildOpenAIEditsBody builds the multipart/form-data body for the OpenAI
// /images/edits endpoint. Every base64 reference image is sent as a form file:
// a single image keeps the "image" field name (backwards compatible), while
// multiple images use gpt-image's "image[]" array convention. Prompt, model,
// size, quality, n and response_format are sent as form fields.
func buildOpenAIEditsBody(p Params, images []string, size, quality string) (string, []byte, error) {
	field := "image"
	if len(images) > 1 {
		field = "image[]"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for _, img := range images {
		decoded, err := base64.StdEncoding.DecodeString(img)
		if err != nil {
			return "", nil, fmt.Errorf("edits: decode input image: %w", err)
		}
		part, err := mw.CreateFormFile(field, "input.png")
		if err != nil {
			return "", nil, fmt.Errorf("edits: create form file: %w", err)
		}
		if _, err := part.Write(decoded); err != nil {
			return "", nil, fmt.Errorf("edits: write image part: %w", err)
		}
	}
	writeField := func(k, v string) {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	writeField("model", p.Model)
	writeField("prompt", p.Prompt)
	writeField("n", "1")
	writeField("size", size)
	writeField("quality", quality)
	writeField("response_format", "png")
	if err := mw.Close(); err != nil {
		return "", nil, fmt.Errorf("edits: close multipart: %w", err)
	}
	return mw.FormDataContentType(), buf.Bytes(), nil
}

func mapOpenAIQuality(q string) string {
	switch q {
	case "balanced":
		return "medium"
	case "quality":
		return "high"
	default:
		return "low"
	}
}

// mapOpenAISize maps the relative Size budget ("1K"|"2K"|"4K") and AspectRatio
// to a legal gpt-image size.  gpt-image only supports 1024x1024, 1536x1024 and
// 1024x1536 (plus "auto"); there is no 2K/4K resolution, so the aspect ratio
// alone decides orientation at every budget.  gpt-image-2 models reject every
// explicit size on relay deployments, so the field is omitted and the server
// default ("auto") applies.
func mapOpenAISize(model, size, aspect string) string {
	if strings.Contains(model, "gpt-image-2") {
		return ""
	}
	landscape := aspect == "16:9"
	portrait := aspect == "9:16"
	if landscape {
		return "1536x1024"
	}
	if portrait {
		return "1024x1536"
	}
	return "1024x1024"
}
