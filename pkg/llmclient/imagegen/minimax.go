package imagegen

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// MiniMax image generation: POST {base}/v1/image_generation. Text-to-image and
// image-to-image share one endpoint; reference images ride subject_reference.
const (
	minimaxDefaultEndpoint   = "https://api.minimaxi.com/v1"
	minimaxDefaultImageModel = "image-01"
)

type minimaxBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

type minimaxSubjectReference struct {
	Type      string `json:"type"` // "character"
	ImageFile string `json:"image_file"`
}

type minimaxImageRequest struct {
	Model            string                    `json:"model"`
	Prompt           string                    `json:"prompt"`
	AspectRatio      string                    `json:"aspect_ratio,omitempty"`
	Width            int                       `json:"width,omitempty"`
	Height           int                       `json:"height,omitempty"`
	ResponseFormat   string                    `json:"response_format"` // "url"
	SubjectReference []minimaxSubjectReference `json:"subject_reference,omitempty"`
}

type minimaxImageResponse struct {
	Data struct {
		ImageURLs []string `json:"image_urls"`
	} `json:"data"`
	BaseResp *minimaxBaseResp `json:"base_resp,omitempty"`
}

func generateMiniMax(ctx context.Context, p Params) (*Result, error) {
	model := p.Model
	if model == "" {
		model = minimaxDefaultImageModel
	}
	reqBody := minimaxImageRequest{
		Model:          model,
		Prompt:         p.Prompt,
		ResponseFormat: "url",
	}
	if p.AspectRatio != "" {
		reqBody.AspectRatio = p.AspectRatio
	} else if w, h, ok := minimaxSize(p.Size); ok {
		reqBody.Width, reqBody.Height = w, h
	}
	if refs := p.referenceImages(); len(refs) > 0 {
		reqBody.SubjectReference = make([]minimaxSubjectReference, 0, len(refs))
		for _, ref := range refs {
			imageFile := ref
			if data, _, ok := splitReferenceImage(ref); ok {
				// base64 (raw or data URL) passes as-is
				imageFile = data
			} else if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
				continue
			}
			reqBody.SubjectReference = append(reqBody.SubjectReference, minimaxSubjectReference{
				Type:      "character",
				ImageFile: imageFile,
			})
		}
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("imagegen/minimax: model %q: marshal body: %w", model, err)
	}

	endpoint := strings.TrimRight(p.Endpoint, "/")
	if endpoint == "" {
		endpoint = minimaxDefaultEndpoint
	}
	if !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}
	tag := fmt.Sprintf("imagegen/minimax: model %q via %s", model, endpoint)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/image_generation", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s: new request: %w", tag, err)
	}
	req.Header.Set("Content-Type", "application/json")
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
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", tag, resp.StatusCode, truncate(string(respBody)))
	}

	var result minimaxImageResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("%s: decode: %w", tag, err)
	}
	if result.BaseResp != nil && result.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("%s: api error %d: %s", tag, result.BaseResp.StatusCode, result.BaseResp.StatusMsg)
	}
	if len(result.Data.ImageURLs) == 0 {
		return nil, fmt.Errorf("%s: no image urls in response", tag)
	}
	res, err := downloadImage(ctx, client, result.Data.ImageURLs[0])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", tag, err)
	}
	return res, nil
}

// minimaxSize maps the relative size budget ("1K"|"2K"|"4K") onto MiniMax
// width/height (512–2048, multiples of 8). When an aspect ratio is set it wins
// upstream, so no explicit dimensions are sent.
func minimaxSize(size string) (int, int, bool) {
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "1K":
		return 1024, 1024, true
	case "2K", "4K":
		return 2048, 2048, true
	}
	return 0, 0, false
}
