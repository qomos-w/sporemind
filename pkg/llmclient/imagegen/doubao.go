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

// Doubao (ByteDance/Volcengine) image generation rides the Ark platform's
// *image* surface: a single POST {base}/images/generations authenticated with a
// Bearer Ark API key, returning either a presigned image URL (response_format
// "url") or inline base64 (response_format "b64_json"). This is deliberately
// distinct from the Seedance *video* backend (pkg/llmclient/videogen/ark.go),
// which uses the asynchronous contents/generations/tasks submit-and-poll API —
// the two Ark surfaces must not be conflated.
const (
	// doubaoDefaultEndpoint is the Ark API base used when Params.Endpoint is
	// empty (mainland China). The oversea region uses
	// https://ark.ap-southeast.bytepluses.com/api/v3; callers there set
	// Params.Endpoint explicitly.
	doubaoDefaultEndpoint = "https://ark.cn-beijing.volces.com/api/v3"

	// doubaoDefaultImageModel is the Seedream text-to-image model used when the
	// caller leaves Params.Model empty.
	doubaoDefaultImageModel = "doubao-seedream-3.0-t2i"

	doubaoImagesPath = "/images/generations"
)

// doubaoImageRequest is the Ark images/generations body. Only the fields the
// caller can influence are sent; response_format is pinned to "url" and the
// watermark is disabled so generated assets come back clean. Image carries an
// optional reference image (Seedream image-to-image); it is omitted entirely
// for the pure text-to-image path.
type doubaoImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Size           string `json:"size,omitempty"`
	ResponseFormat string `json:"response_format"`
	Watermark      bool   `json:"watermark"`
	Image          string `json:"image,omitempty"`
}

// doubaoImageResponse models the Ark image reply. Exactly one of URL / B64JSON
// is populated per data entry, matching response_format.
type doubaoImageResponse struct {
	Data []struct {
		URL     string `json:"url"`
		B64JSON string `json:"b64_json"`
	} `json:"data"`
	Error *doubaoError `json:"error,omitempty"`
}

// doubaoError is the structured error object Ark returns on failure.
type doubaoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func generateDoubao(ctx context.Context, p Params) (*Result, error) {
	model := p.Model
	if model == "" {
		model = doubaoDefaultImageModel
	}
	if strings.TrimSpace(p.Prompt) == "" && len(p.referenceImages()) == 0 {
		return nil, fmt.Errorf("imagegen/doubao: model %q: prompt is required", model)
	}
	endpoint := strings.TrimRight(p.Endpoint, "/")
	if endpoint == "" {
		endpoint = doubaoDefaultEndpoint
	}
	tag := fmt.Sprintf("imagegen/doubao: model %q via %s", model, endpoint)

	reqBody := doubaoImageRequest{
		Model:          model,
		Prompt:         p.Prompt,
		Size:           doubaoImageSize(p.Size, p.AspectRatio),
		ResponseFormat: "url",
		Watermark:      false,
	}
	// Seedream accepts a single reference image for image-to-image; URLs and
	// data URLs pass through, while a raw base64 payload is wrapped as a data
	// URL (the form Ark expects).
	if refs := p.referenceImages(); len(refs) > 0 {
		reqBody.Image = doubaoReferenceImage(refs[0])
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("%s: marshal body: %w", tag, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+doubaoImagesPath, bytes.NewReader(payload))
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

	var result doubaoImageResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: http %d: %s", tag, resp.StatusCode, truncate(string(respBody)))
		}
		return nil, fmt.Errorf("%s: decode: %w", tag, err)
	}
	if result.Error != nil && (result.Error.Code != "" || result.Error.Message != "") {
		return nil, fmt.Errorf("%s: api error %s: %s", tag, result.Error.Code, truncate(result.Error.Message))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", tag, resp.StatusCode, truncate(string(respBody)))
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("%s: no image data in response: %s", tag, truncate(string(respBody)))
	}

	first := result.Data[0]
	if first.B64JSON != "" {
		data, err := base64.StdEncoding.DecodeString(first.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("%s: decode base64: %w", tag, err)
		}
		return &Result{Data: data, MimeType: doubaoMimeType(data)}, nil
	}
	if first.URL != "" {
		res, err := downloadImage(ctx, client, first.URL)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", tag, err)
		}
		return res, nil
	}
	return nil, fmt.Errorf("%s: image entry carries neither url nor b64_json: %s", tag, truncate(string(respBody)))
}

// doubaoReferenceImage normalizes one reference image into the form Ark's
// `image` field accepts: an http(s) URL or a data URL. A raw base64 payload is
// wrapped as a data:image/png URL.
func doubaoReferenceImage(ref string) string {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") || strings.HasPrefix(ref, "data:") {
		return ref
	}
	return "data:image/png;base64," + ref
}

// doubaoMimeType sniffs the decoded image bytes so a b64_json reply is labelled
// correctly regardless of the encoding the provider chose; non-image payloads
// fall back to the JPEG default Ark normally returns.
func doubaoMimeType(data []byte) string {
	if mt := http.DetectContentType(data); strings.HasPrefix(mt, "image/") {
		return mt
	}
	return "image/jpeg"
}

// doubaoImageSize renders the Ark "WxH" size string from the relative size
// budget and the aspect ratio. Ark exposes no aspect_ratio parameter: the
// orientation is encoded in the dimensions. An unset budget with no aspect
// ratio returns "" so the provider default (1024x1024) applies.
func doubaoImageSize(size, aspect string) string {
	side := doubaoBaseSide(size)
	if side == 0 && aspect == "" {
		return ""
	}
	if side == 0 {
		side = 1024
	}
	w, h := side, side
	switch aspect {
	case "16:9":
		h = side * 9 / 16
	case "9:16":
		w = side * 9 / 16
	case "4:3":
		h = side * 3 / 4
	case "3:4":
		w = side * 3 / 4
	}
	return fmt.Sprintf("%dx%d", w, h)
}

// doubaoBaseSide maps the relative size budget ("1K"|"2K"|"4K") onto the
// longest image side in pixels; 0 means "unset".
func doubaoBaseSide(size string) int {
	switch strings.ToUpper(strings.TrimSpace(size)) {
	case "1K":
		return 1024
	case "2K":
		return 2048
	case "4K":
		return 4096
	}
	return 0
}
