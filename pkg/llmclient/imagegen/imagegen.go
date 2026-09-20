package imagegen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Params carries everything needed to make one image-generation API call.
type Params struct {
	Prompt          string
	Model           string
	Endpoint        string // provider endpoint base URL
	Protocol        string // provider Kind: "openai" | "gemini" | etc.
	AuthToken       string
	UserAgent       string   // per-provider override; empty = use default
	Proxy           string   // per-provider HTTP(S)/SOCKS5 proxy URL; empty = direct connection
	Quality         string   // "fast" | "balanced" | "quality"
	Size            string   // "1K" | "2K" | "4K"
	AspectRatio     string   // "1:1" | "16:9" | "9:16" | etc.
	InputImage      string   // base64-encoded source image for editing (optional; single-image compatibility, merged into ReferenceImages)
	ReferenceImages []string // base64-encoded and/or URL reference images for multi-reference generation (optional)
}

// referenceImages returns the effective reference list: the single-image
// InputImage is merged in front of ReferenceImages when present, so callers
// can use either form (or both) without duplication.
func (p Params) referenceImages() []string {
	if p.InputImage == "" {
		return p.ReferenceImages
	}
	return append([]string{p.InputImage}, p.ReferenceImages...)
}

// splitReferenceImage extracts the base64 payload and mime type from one
// reference-image entry. Raw base64 passes through with image/png; a data URL
// contributes its declared prefix (e.g. "data:image/jpeg;base64,..." →
// "image/jpeg"). URL references cannot be embedded in gemini inlineData or
// openai edits multipart, which accept base64 only, so they yield ok=false.
func splitReferenceImage(ref string) (data, mime string, ok bool) {
	if strings.HasPrefix(ref, "data:") {
		comma := strings.Index(ref, ",")
		if comma <= 0 || comma+1 >= len(ref) {
			return "", "", false
		}
		return ref[comma+1:], mimeFromDataURLPrefix(ref[len("data:"):comma]), true
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return "", "", false
	}
	return ref, "image/png", true
}

// mimeFromDataURLPrefix parses the "image/png;base64" header of a data URL
// and returns the declared mime type, defaulting to image/png when absent.
func mimeFromDataURLPrefix(header string) string {
	if semi := strings.Index(header, ";"); semi >= 0 {
		header = header[:semi]
	}
	if header == "" {
		return "image/png"
	}
	return header
}

// Result is the generated image.
type Result struct {
	Data     []byte
	MimeType string
}

// Generate routes to the appropriate image-generation backend based on the
// provider protocol (Kind).
func Generate(ctx context.Context, p Params) (*Result, error) {
	switch p.Protocol {
	case "gemini":
		return generateGemini(ctx, p)
	case "openai", "endpoint":
		return generateOpenAI(ctx, p)
	case "minimax":
		return generateMiniMax(ctx, p)
	case "doubao", "imagex":
		return generateDoubao(ctx, p)
	case "qwen", "dashscope":
		// Qwen (Alibaba Cloud Model Studio / DashScope) native task API;
		// "dashscope" is accepted as an alias for the same provider.
		return generateQwen(ctx, p)
	default:
		return nil, fmt.Errorf("imagegen: unsupported protocol %q", p.Protocol)
	}
}

// truncate caps a string at 512 bytes for safe inclusion in error messages,
// mirroring pkg/llmclient/mediagen. Upstream error bodies must be pre-truncated
// before they are embedded in errors or propagated across actors.
func truncate(s string) string {
	const max = 512
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// downloadImage fetches image bytes from a returned (typically presigned/public
// CDN) URL — no auth — and sniffs the mime type from the response headers.
func downloadImage(ctx context.Context, client *http.Client, url string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download image: build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download image: http %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download image: read body: %w", err)
	}
	mimeType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(mimeType, "image/") {
		mimeType = "image/jpeg"
	}
	return &Result{Data: data, MimeType: mimeType}, nil
}
