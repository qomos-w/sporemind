package nativetools

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestRegistry_AppendWebSearch_Anthropic(t *testing.T) {
	r := DefaultRegistry()
	base := []domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}}

	got := r.AppendWebSearch(base, "claude-sonnet-4-6")
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[1].Name != "web_search" {
		t.Errorf("tool[1].Name = %q, want web_search", got[1].Name)
	}
	if got[1].Type != "web_search_20250305" {
		t.Errorf("tool[1].Type = %q, want web_search_20250305", got[1].Type)
	}
	if got[1].NativeConfig != nil {
		t.Errorf("tool[1].NativeConfig = %+v, want nil for anthropic", got[1].NativeConfig)
	}
}

func TestRegistry_AppendWebSearch_BigModel(t *testing.T) {
	r := DefaultRegistry()
	base := []domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}}

	got := r.AppendWebSearch(base, "glm-5.1")
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[1].Name != "web_search" {
		t.Errorf("tool[1].Name = %q, want web_search", got[1].Name)
	}
	if got[1].Type != "web_search" {
		t.Errorf("tool[1].Type = %q, want web_search", got[1].Type)
	}
	cfg, ok := got[1].NativeConfig["web_search"].(map[string]any)
	if !ok {
		t.Fatalf("tool[1].NativeConfig[web_search] = %+v, want map[string]any", got[1].NativeConfig["web_search"])
	}
	if cfg["enable"] != true {
		t.Errorf("web_search.enable = %v, want true", cfg["enable"])
	}
}

func TestRegistry_AppendWebSearch_OpenAI(t *testing.T) {
	r := DefaultRegistry()
	base := []domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}}

	models := []string{"gpt-4", "gpt-4o", "o1-mini", "o3-mini"}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			got := r.AppendWebSearch(base, model)
			// OpenAI Chat Completions does not support native "web_search"
			// tools, so the registry should not add one for GPT/O1/O3 models.
			if len(got) != 1 {
				t.Fatalf("expected 1 tool for OpenAI model %q, got %d", model, len(got))
			}
			if got[0].Name != "filesystem_read" {
				t.Errorf("tool[0].Name = %q, want filesystem_read", got[0].Name)
			}
		})
	}
}

func TestRegistry_AppendWebSearch_NoMatch(t *testing.T) {
	r := DefaultRegistry()
	base := []domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}}

	unknownModels := []string{"", "  ", "mistral-7b", "gemini-pro", "llama-3"}
	for _, model := range unknownModels {
		t.Run(model, func(t *testing.T) {
			got := r.AppendWebSearch(base, model)
			if len(got) != 1 {
				t.Fatalf("expected 1 tool for model %q, got %d", model, len(got))
			}
			if got[0].Name != "filesystem_read" {
				t.Errorf("tool[0].Name = %q, want filesystem_read", got[0].Name)
			}
		})
	}
}

func TestRegistry_RegisterCustomProvider(t *testing.T) {
	r := NewRegistry()

	// Custom provider that matches any model containing "custom"
	r.Register(mockProvider{
		match: func(model string) bool { return model == "custom-model" },
		spec:  domain.ToolSpec{Name: "web_search", Type: "custom_web_search"},
	})

	base := []domain.ToolSpec{{Name: "tool1"}}
	got := r.AppendWebSearch(base, "custom-model")
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[1].Type != "custom_web_search" {
		t.Errorf("tool[1].Type = %q, want custom_web_search", got[1].Type)
	}

	// Unknown model should not match custom provider
	got2 := r.AppendWebSearch(base, "other-model")
	if len(got2) != 1 {
		t.Fatalf("expected 1 tool for unknown model, got %d", len(got2))
	}
}

func TestRegistry_MatchPriority(t *testing.T) {
	r := NewRegistry()

	// Register a provider that matches everything first
	r.Register(mockProvider{
		match: func(model string) bool { return true },
		spec:  domain.ToolSpec{Name: "web_search", Type: "first_match"},
	})
	// Then register the anthropic provider
	r.Register(anthropicProvider{})

	base := []domain.ToolSpec{{Name: "tool1"}}
	got := r.AppendWebSearch(base, "claude-sonnet-4-6")
	// First registered provider wins, not anthropic
	if len(got) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(got))
	}
	if got[1].Type != "first_match" {
		t.Errorf("tool[1].Type = %q, want first_match (registration order matters)", got[1].Type)
	}
}

func TestRegistry_Reconcile(t *testing.T) {
	r := DefaultRegistry()
	// Simulate the turn-engine best-effort tool list for a glm model: a
	// standard function tool plus the native web_search (added by model name).
	glmTools := r.AppendWebSearch(
		[]domain.ToolSpec{{Name: "filesystem_read", Description: "read file"}},
		"glm-5.1",
	)
	if len(glmTools) != 2 {
		t.Fatalf("setup: expected 2 tools for glm, got %d", len(glmTools))
	}

	t.Run("glm web_search dropped over openai protocol", func(t *testing.T) {
		// GLM is reached via the openai/endpoint protocol in this runtime; the
		// OpenAI Chat Completions schema only accepts function/plugin tools, so
		// the native web_search must be stripped (this is the 400 root cause).
		got := r.Reconcile(glmTools, "openai")
		if len(got) != 1 || got[0].Name != "filesystem_read" {
			t.Fatalf("expected only the function tool over openai, got %+v", got)
		}
	})

	t.Run("glm web_search dropped over endpoint protocol", func(t *testing.T) {
		got := r.Reconcile(glmTools, "endpoint")
		if len(got) != 1 || got[0].Name != "filesystem_read" {
			t.Fatalf("expected only the function tool over endpoint, got %+v", got)
		}
	})

	t.Run("glm web_search kept over bigmodel protocol", func(t *testing.T) {
		// A future native bigmodel protocol/client would legitimately carry it.
		got := r.Reconcile(glmTools, "bigmodel")
		if len(got) != 2 {
			t.Fatalf("expected web_search kept over bigmodel protocol, got %+v", got)
		}
	})
}

func TestRegistry_Reconcile_AnthropicKept(t *testing.T) {
	r := DefaultRegistry()
	tools := r.AppendWebSearch(
		[]domain.ToolSpec{{Name: "filesystem_read"}},
		"claude-sonnet-4-6",
	)
	if len(tools) != 2 {
		t.Fatalf("setup: expected 2 tools for claude, got %d", len(tools))
	}

	// web_search_20250305 is a first-class anthropic server tool: keep it.
	got := r.Reconcile(tools, "anthropic")
	if len(got) != 2 || got[1].Type != "web_search_20250305" {
		t.Fatalf("expected web_search kept over anthropic protocol, got %+v", got)
	}

	// Over the openai protocol it must be dropped.
	got = r.Reconcile(tools, "openai")
	if len(got) != 1 {
		t.Fatalf("expected web_search dropped over openai protocol, got %+v", got)
	}
}

func TestRegistry_Reconcile_PreservesStandardToolsAndHistory(t *testing.T) {
	r := DefaultRegistry()
	tools := []domain.ToolSpec{
		{Name: "filesystem_read", Description: "read"},
		{Name: "shell_exec", Description: "run"},
		{Name: "web_search", Type: "web_search", NativeConfig: map[string]any{"web_search": map[string]any{"enable": true}}},
	}
	// Standard function tools (empty Type) are always retained regardless of
	// protocol; only the native declaration is filtered.
	for _, proto := range []string{"openai", "endpoint", "anthropic", "bigmodel", ""} {
		got := r.Reconcile(tools, proto)
		var std int
		for _, t := range got {
			if t.Type == "" {
				std++
			}
		}
		if std != 2 {
			t.Errorf("protocol %q: expected 2 standard tools kept, got %d (%+v)", proto, std, got)
		}
	}
}

type mockProvider struct {
	match func(string) bool
	spec  domain.ToolSpec
}

func (m mockProvider) Name() string                     { return "mock" }
func (m mockProvider) Match(model string) bool          { return m.match(model) }
func (m mockProvider) WebSearch() (domain.ToolSpec, bool) { return m.spec, true }
func (m mockProvider) Protocol() string                 { return "mock" }
