package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const (
	// deepseekAnthropicBaseURL is the default DeepSeek Anthropic-compatible
	// Messages API base (opts.Endpoint overrides it).
	deepseekAnthropicBaseURL = "https://api.deepseek.com/anthropic/v1"

	deepseekModel          = "deepseek-v4-flash"
	deepseekMaxTokens      = 4096
	deepseekToolMaxUses    = 5
	deepseekSearchToolType = "web_search_20250305"
	deepseekAnthropicVer   = "2023-06-01"
)

// deepseekProvider implements web search via the DeepSeek Anthropic-compatible
// Messages API with the server-side web_search_20250305 tool.
type deepseekProvider struct {
	http *http.Client
}

func (p *deepseekProvider) ID() string        { return "deepseek" }
func (p *deepseekProvider) Name() string      { return "DeepSeek Search" }
func (p *deepseekProvider) RequiresKey() bool { return true }

// deepseekTextBlock is a text content block in the Messages request/response.
type deepseekTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// deepseekMessage is one Anthropic Messages chat turn.
type deepseekMessage struct {
	Role    string              `json:"role"`
	Content []deepseekTextBlock `json:"content"`
}

// deepseekTool enables the server-side web search tool.
type deepseekTool struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	MaxUses int    `json:"max_uses,omitempty"`
}

// deepseekRequest is the Anthropic Messages request body.
type deepseekRequest struct {
	Model     string            `json:"model"`
	MaxTokens int               `json:"max_tokens"`
	Messages  []deepseekMessage `json:"messages"`
	Tools     []deepseekTool    `json:"tools"`
}

// deepseekCitation is a citation on a response text block; its cited_text is
// used as the result snippet for the cited url.
type deepseekCitation struct {
	URL       string `json:"url"`
	CitedText string `json:"cited_text"`
}

// deepseekWebSearchResult is one entry inside a web_search_tool_result block.
type deepseekWebSearchResult struct {
	URL     string `json:"url"`
	Title   string `json:"title"`
	PageAge string `json:"page_age,omitempty"`
}

// deepseekContentBlock is a heterogeneous response content block. Only the
// fields relevant to search mapping are modeled; unknown block types (e.g.
// server_tool_use) decode with zero-valued search fields.
type deepseekContentBlock struct {
	Type      string                     `json:"type"`
	Text      string                     `json:"text,omitempty"`
	Citations []deepseekCitation         `json:"citations,omitempty"`
	// WebSearchResults holds the web_search_result items of
	// web_search_tool_result blocks.
	WebSearchResults []deepseekWebSearchResult `json:"content,omitempty"`
}

// deepseekResponse is the Anthropic Messages response body.
type deepseekResponse struct {
	Content []deepseekContentBlock `json:"content"`
}

// deepseekErrorMessage extracts an error message from a non-2xx body shaped as
// {"error":{"message"}} or {"message"}. Returns "" when nothing usable.
func deepseekErrorMessage(body []byte) string {
	var e struct {
		Error   *struct{ Message string `json:"message"` } `json:"error"`
		Message string                                  `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return ""
	}
	if e.Error != nil && e.Error.Message != "" {
		return e.Error.Message
	}
	return e.Message
}

func (p *deepseekProvider) Search(ctx context.Context, query string, opts SearchOpts) ([]gen.WebSearchResult, error) {
	reqBody := deepseekRequest{
		Model:     deepseekModel,
		MaxTokens: deepseekMaxTokens,
		Messages: []deepseekMessage{{
			Role: "user",
			Content: []deepseekTextBlock{{
				Type: "text",
				Text: "Perform a web search for the query: " + query,
			}},
		}},
		Tools: []deepseekTool{{
			Type:    deepseekSearchToolType,
			Name:    "web_search",
			MaxUses: deepseekToolMaxUses,
		}},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("deepseek: marshal request: %w", err)
	}

	base := deepseekAnthropicBaseURL
	if opts.Endpoint != "" {
		base = opts.Endpoint
	}
	url := strings.TrimSuffix(base, "/") + "/messages"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("deepseek: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", opts.ApiKey)
	req.Header.Set("Authorization", "Bearer "+opts.ApiKey)
	req.Header.Set("anthropic-version", deepseekAnthropicVer)
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("deepseek: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("deepseek: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if msg := deepseekErrorMessage(respBody); msg != "" {
			return nil, fmt.Errorf("deepseek: HTTP %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("deepseek: HTTP %d", resp.StatusCode)
	}

	var dsResp deepseekResponse
	if err := json.Unmarshal(respBody, &dsResp); err != nil {
		return nil, fmt.Errorf("deepseek: parse response: %w", err)
	}

	// Snippets: text-block citations, url → cited_text, first occurrence wins.
	snippets := make(map[string]string)
	for _, block := range dsResp.Content {
		if block.Type != "text" {
			continue
		}
		for _, c := range block.Citations {
			if _, dup := snippets[c.URL]; !dup {
				snippets[c.URL] = c.CitedText
			}
		}
	}

	// Collect web_search_result items, deduped by url.
	var results []gen.WebSearchResult
	seen := make(map[string]bool)
	hadToolResult := false
	for _, block := range dsResp.Content {
		if block.Type != "web_search_tool_result" {
			continue
		}
		hadToolResult = true
		for _, r := range block.WebSearchResults {
			if r.URL == "" || seen[r.URL] {
				continue
			}
			seen[r.URL] = true
			results = append(results, gen.WebSearchResult{
				Title:         r.Title,
				URL:           r.URL,
				Snippet:       snippets[r.URL],
				PublishedDate: r.PageAge,
			})
		}
	}

	if !hadToolResult {
		return nil, fmt.Errorf("deepseek: no web_search_tool_result blocks")
	}

	if opts.MaxResults > 0 && len(results) > opts.MaxResults {
		results = results[:opts.MaxResults]
	}
	return results, nil
}
