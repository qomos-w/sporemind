package websearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// DeepSeek provider adapter tests
// ---------------------------------------------------------------------------

func TestDeepseekSearch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			t.Errorf("expected path ending in /messages, got %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Errorf("unexpected x-api-key: %q", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected Authorization: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("unexpected anthropic-version: %q", r.Header.Get("anthropic-version"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content type: %q", r.Header.Get("Content-Type"))
		}

		var req deepseekRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Model != "deepseek-v4-flash" {
			t.Errorf("unexpected model: %q", req.Model)
		}
		if req.MaxTokens != 4096 {
			t.Errorf("unexpected max_tokens: %d", req.MaxTokens)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("unexpected messages: %+v", req.Messages)
		}
		if text := req.Messages[0].Content; len(text) != 1 || text[0].Type != "text" ||
			text[0].Text != "Perform a web search for the query: golang testing" {
			t.Errorf("unexpected user content: %+v", text)
		}
		if len(req.Tools) != 1 {
			t.Fatalf("unexpected tools: %+v", req.Tools)
		}
		tool := req.Tools[0]
		if tool.Type != "web_search_20250305" || tool.Name != "web_search" || tool.MaxUses != 5 {
			t.Errorf("unexpected tool: %+v", tool)
		}

		resp := deepseekResponse{}
		resp.Content = append(resp.Content,
			deepseekContentBlock{
				Type: "text",
				Text: "summary text",
				Citations: []deepseekCitation{
					{URL: "https://go.dev/doc/testing", CitedText: "first cited snippet"},
					{URL: "https://go.dev/doc/testing", CitedText: "second snippet must be ignored"},
					{URL: "https://example.com/only-cited", CitedText: "orphan citation"},
				},
			},
			deepseekContentBlock{
				Type: "web_search_tool_result",
				WebSearchResults: []deepseekWebSearchResult{
					{URL: "https://go.dev/doc/testing", Title: "Go Testing", PageAge: "2024-01-01"},
					{URL: "https://go.dev/doc/testing", Title: "Go Testing (dup)"},
					{URL: "https://example.com/only-cited", Title: "Cited Only", PageAge: "2023-05-05"},
					{URL: "https://blog.golang.org/cover", Title: "The Cover Story"},
					{URL: "https://truncated.example/", Title: "Truncated Entry"},
				},
			},
		)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &deepseekProvider{http: server.Client()}
	results, err := p.Search(context.Background(), "golang testing", SearchOpts{
		MaxResults: 3,
		ApiKey:     "test-key",
		Endpoint:   server.URL,
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 results (MaxResults truncation), got %d: %+v", len(results), results)
	}
	want := []gen.WebSearchResult{
		{Title: "Go Testing", URL: "https://go.dev/doc/testing", Snippet: "first cited snippet", PublishedDate: "2024-01-01"},
		{Title: "Cited Only", URL: "https://example.com/only-cited", Snippet: "orphan citation", PublishedDate: "2023-05-05"},
		{Title: "The Cover Story", URL: "https://blog.golang.org/cover", Snippet: ""},
	}
	for i, w := range want {
		if results[i] != w {
			t.Errorf("result[%d] = %+v, want %+v", i, results[i], w)
		}
	}
}

func TestDeepseekSearch_NoToolResultBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := deepseekResponse{Content: []deepseekContentBlock{
			{Type: "text", Text: "model refused to search"},
		}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := &deepseekProvider{http: server.Client()}
	_, err := p.Search(context.Background(), "anything", SearchOpts{ApiKey: "k", Endpoint: server.URL})
	if err == nil {
		t.Fatal("expected error when response has no web_search_tool_result blocks")
	}
	if !strings.Contains(err.Error(), "no web_search_tool_result blocks") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeepseekSearch_HTTPError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantMsg string
	}{
		{"nested error message", http.StatusUnauthorized, `{"error":{"type":"authentication_error","message":"invalid api key"}}`, "invalid api key"},
		{"top-level message", http.StatusBadRequest, `{"message":"bad request shape"}`, "bad request shape"},
		{"unparseable fallback", http.StatusInternalServerError, `not-json`, "HTTP 500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer server.Close()

			p := &deepseekProvider{http: server.Client()}
			_, err := p.Search(context.Background(), "q", SearchOpts{ApiKey: "k", Endpoint: server.URL})
			if err == nil {
				t.Fatal("expected error for non-2xx status")
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantMsg)
			}
		})
	}
}
