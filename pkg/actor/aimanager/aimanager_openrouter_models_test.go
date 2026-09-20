package aimanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchOpenRouterModels_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("expected Accept: application/json, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "openai/gpt-5.4",        "context_length": 1050000},
				{"id": "openai/gpt-5.4-mini",   "context_length": 400000},
				{"id": "openai/o3-mini",        "context_length": 200000},
				{"id": "anthropic/claude-sonnet-4-5", "context_length": 200000},
				{"id": "openai/o3-mini",        "context_length": 250000}, // duplicate, higher context
				{"id": "google/gemini-2.5-flash", "context_length": 1048576},
				{"id": "openai/gpt-4.1",        "context_length": 1047576},
				// No slash — should be passed through as-is
				{"id": "moonshot-v1-128k",      "context_length": 131072},
				// Empty id, should be skipped
				{"id": "",                      "context_length": 0},
			},
		})
	}))
	defer srv.Close()

	items, err := fetchOpenRouterModels(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Expected: 8 unique prefixes (deduped, sorted)
	// o3-mini appears twice — keep max context_length (250000)
	expected := []struct {
		prefix string
		ctx    int32
	}{
		{"claude-sonnet-4-5", 200000},
		{"gemini-2.5-flash", 1048576},
		{"gpt-4.1", 1047576},
		{"gpt-5.4", 1050000},
		{"gpt-5.4-mini", 400000},
		{"moonshot-v1-128k", 131072},
		{"o3-mini", 250000}, // dup kept max
	}

	if len(items) != len(expected) {
		t.Fatalf("got %d items, want %d\nitems: %+v", len(items), len(expected), items)
	}
	for i, exp := range expected {
		if items[i].Prefix != exp.prefix {
			t.Errorf("items[%d].Prefix = %q, want %q", i, items[i].Prefix, exp.prefix)
		}
		if items[i].MaxContextLength != exp.ctx {
			t.Errorf("items[%d].MaxContextLength = %d, want %d (prefix=%q)", i, items[i].MaxContextLength, exp.ctx, exp.prefix)
		}
		// Only Prefix and MaxContextLength should be populated
		if items[i].MaxTokens != 0 {
			t.Errorf("items[%d].MaxTokens = %d, want 0", i, items[i].MaxTokens)
		}
		if items[i].Modality != "" {
			t.Errorf("items[%d].Modality = %q, want empty", i, items[i].Modality)
		}
	}
}

func TestFetchOpenRouterModels_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchOpenRouterModels(srv.URL)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
}

func TestFetchOpenRouterModels_Unreachable(t *testing.T) {
	_, err := fetchOpenRouterModels("http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error on unreachable, got nil")
	}
}

func TestFetchOpenRouterModels_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{},
		})
	}))
	defer srv.Close()

	items, err := fetchOpenRouterModels(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("got %d items, want 0", len(items))
	}
}

func TestStripProviderPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"openai/gpt-5.4", "gpt-5.4"},
		{"anthropic/claude-sonnet-4-5", "claude-sonnet-4-5"},
		{"google/gemini-2.5-flash", "gemini-2.5-flash"},
		{"moonshot-v1-128k", "moonshot-v1-128k"},
		{"", ""},
		{"  openai/gpt-5.4  ", "gpt-5.4"},
		{"/gpt-5.4", "gpt-5.4"},
		{"onlymodel", "onlymodel"},
	}
	for _, tc := range tests {
		got := stripProviderPrefix(tc.input)
		if got != tc.want {
			t.Errorf("stripProviderPrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}