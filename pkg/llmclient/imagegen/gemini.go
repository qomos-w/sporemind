package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/qomos-w/sporemind/pkg/llmclient"
)

const geminiDefaultEndpoint = "https://generativelanguage.googleapis.com"

type geminiPart struct {
	Text       string        `json:"text,omitempty"`
	InlineData *geminiInline `json:"inlineData,omitempty"`
}

type geminiInline struct {
	Data     string `json:"data"`
	MimeType string `json:"mimeType"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiImageConfig struct {
	AspectRatio string `json:"aspectRatio,omitempty"`
	ImageSize   string `json:"imageSize,omitempty"`
}

type geminiGenerationConfig struct {
	ResponseModalities []string          `json:"responseModalities"`
	ImageConfig        geminiImageConfig `json:"imageConfig,omitempty"`
}

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiResponsePart struct {
	InlineData *geminiInline `json:"inlineData,omitempty"`
	// Thought marks intermediate reasoning parts; Gemini 3.x may emit draft
	// images with thought=true before the final image.
	Thought bool `json:"thought,omitempty"`
}

type geminiResponseCandidate struct {
	Content struct {
		Parts []geminiResponsePart `json:"parts"`
	} `json:"content"`
}

type geminiResponse struct {
	Candidates []geminiResponseCandidate `json:"candidates"`
}

// normalizeGeminiEndpoint trims trailing slashes and a trailing OpenAI-style
// "/v1" (or native "/v1beta") segment from a provider base URL. Relay BaseURLs
// are routinely copied with the /v1 suffix; the native path appends
// /v1beta/... itself, so an untrimmed suffix would double up.
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

func generateGemini(ctx context.Context, p Params) (*Result, error) {
	if strings.TrimSpace(p.Model) == "" {
		return nil, fmt.Errorf("imagegen/gemini: no image model configured — set the account default model (e.g. gemini-2.5-flash-image)")
	}
	endpoint := normalizeGeminiEndpoint(p.Endpoint)
	tag := fmt.Sprintf("imagegen/gemini: model %q via %s", p.Model, endpoint)

	parts := []geminiPart{}
	for _, ref := range p.referenceImages() {
		data, mime, ok := splitReferenceImage(ref)
		if !ok {
			// URL references cannot ride inlineData (base64 only); the caller
			// must resolve them to base64 for gemini.
			continue
		}
		parts = append(parts, geminiPart{
			InlineData: &geminiInline{Data: data, MimeType: mime},
		})
	}
	parts = append(parts, geminiPart{Text: p.Prompt})

	imageConfig := geminiImageConfig{}
	if p.AspectRatio != "" {
		imageConfig.AspectRatio = p.AspectRatio
	}
	if p.Size != "" {
		imageConfig.ImageSize = mapGeminiSize(p.Size)
	}

	body := geminiRequest{
		Contents: []geminiContent{{Parts: parts}},
		GenerationConfig: geminiGenerationConfig{
			// TEXT+IMAGE matches the model default (docs: "The model
			// defaults to returning text and image responses") and is
			// required by gemini-3-pro-image; 3.1-flash also accepts
			// IMAGE-only, but interleaved is the safer common denominator.
			ResponseModalities: []string{"TEXT", "IMAGE"},
			ImageConfig:        imageConfig,
		},
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal: %w", tag, err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", endpoint, p.Model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: new request: %w", tag, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", p.AuthToken)
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", tag, resp.StatusCode, truncate(string(respBody)))
	}

	var result geminiResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", tag, err)
	}

	// Skip thought parts (draft images) and take the LAST non-thought
	// inlineData: the documented convention is that the final image part
	// follows any intermediate reasoning output.
	var finalData, finalMime string
	for _, cand := range result.Candidates {
		for _, part := range cand.Content.Parts {
			if part.Thought || part.InlineData == nil || part.InlineData.Data == "" {
				continue
			}
			finalData = part.InlineData.Data
			finalMime = part.InlineData.MimeType
		}
	}
	if finalData == "" {
		return nil, fmt.Errorf("%s: no image data in response: %s", tag, truncate(string(respBody)))
	}
	data, err := base64.StdEncoding.DecodeString(finalData)
	if err != nil {
		return nil, fmt.Errorf("%s: decode base64: %w", tag, err)
	}
	if finalMime == "" {
		finalMime = "image/png"
	}
	return &Result{Data: data, MimeType: finalMime}, nil
}

func mapGeminiSize(size string) string {
	switch size {
	case "2K":
		return "2K"
	case "4K":
		return "4K"
	default:
		return "1K"
	}
}
