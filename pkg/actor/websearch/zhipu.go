package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const (
	zhipuSearchURL     = "https://open.bigmodel.cn/api/paas/v4/web_search"
	zhipuDefaultEngine = "search_std"
)

// zhipuProvider implements the Zhipu (智谱) Web Search API.
type zhipuProvider struct {
	http *http.Client
}

func (p *zhipuProvider) ID() string          { return "zhipu" }
func (p *zhipuProvider) Name() string        { return "Zhipu Web Search" }
func (p *zhipuProvider) RequiresKey() bool   { return true }

// zhipuRequest is the JSON body for POST /paas/v4/web_search.
type zhipuRequest struct {
	SearchQuery         string `json:"search_query"`
	SearchEngine        string `json:"search_engine"`
	SearchIntent        bool   `json:"search_intent"`
	Count               int    `json:"count,omitempty"`
	SearchRecencyFilter string `json:"search_recency_filter,omitempty"`
	ContentSize         string `json:"content_size,omitempty"`
}

// zhipuResponse is the JSON response from POST /paas/v4/web_search.
type zhipuResponse struct {
	ID           string `json:"id"`
	Created      int64  `json:"created"`
	SearchResult []struct {
		Title       string `json:"title"`
		Link        string `json:"link"`
		Content     string `json:"content"`
		Media       string `json:"media"`
		Icon        string `json:"icon"`
		Refer       string `json:"refer"`
		PublishDate string `json:"publish_date"`
	} `json:"search_result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *zhipuProvider) Search(ctx context.Context, query string, opts SearchOpts) ([]gen.WebSearchResult, error) {
	if len(query) > 70 {
		return nil, fmt.Errorf("zhipu: query exceeds 70 characters")
	}

	engine := opts.Engine
	if engine == "" {
		engine = zhipuDefaultEngine
	}

	timeRange := opts.TimeRange
	if timeRange == "" {
		timeRange = "noLimit"
	}

	reqBody := zhipuRequest{
		SearchQuery:         query,
		SearchEngine:        engine,
		SearchIntent:        false,
		Count:               opts.MaxResults,
		SearchRecencyFilter: timeRange,
		ContentSize:         "medium",
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("zhipu: marshal request: %w", err)
	}

	url := zhipuSearchURL
	if opts.Endpoint != "" {
		url = opts.Endpoint
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("zhipu: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+opts.ApiKey)
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("zhipu: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("zhipu: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zhipu: HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}

	var zhipuResp zhipuResponse
	if err := json.Unmarshal(respBody, &zhipuResp); err != nil {
		return nil, fmt.Errorf("zhipu: parse response: %w", err)
	}

	if zhipuResp.Error != nil {
		return nil, fmt.Errorf("zhipu: error %s: %s", zhipuResp.Error.Code, zhipuResp.Error.Message)
	}

	results := make([]gen.WebSearchResult, 0, len(zhipuResp.SearchResult))
	for _, r := range zhipuResp.SearchResult {
		results = append(results, gen.WebSearchResult{
			Title:         r.Title,
			URL:           r.Link,
			Snippet:       r.Content,
			Source:        r.Media,
			Icon:          r.Icon,
			PublishedDate: r.PublishDate,
		})
	}

	return results, nil
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
