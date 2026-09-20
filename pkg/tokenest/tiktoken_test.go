package tokenest

import (
	"strings"
	"testing"
)

// TestEstimateTokens_KnownTexts verifies tiktoken counts against known
// reference values. These anchor the estimator to OpenAI's published
// cl100k_base behavior and guard against regression to the old char-ratio
// heuristic.
func TestEstimateTokens_KnownTexts(t *testing.T) {
	cases := []struct {
		name string
		text string
		// cl100k_base reference token count.
		want int
	}{
		{"empty", "", 0},
		{"hello world", "hello world", 2},
		{"the quick brown fox", "the quick brown fox jumps over the lazy dog", 9},
		{"repeat a", strings.Repeat("a", 100), 13},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EstimateTokens(c.text)
			if got != c.want {
				t.Errorf("EstimateTokens(%q) = %d, want %d", c.name, got, c.want)
			}
		})
	}
}

func TestEstimateTokens_PositiveForUnicode(t *testing.T) {
	// CJK / mixed content must produce a positive count and be larger than the
	// naive char-ratio would give for the CJK portion (tiktoken tokenizes CJK
	// more coarsely). Just assert positivity and determinism.
	texts := []string{
		"你好世界",
		"こんにちは",
		"한국어 텍스트",
		"Mix of English and 中文 with emoji 🚀.",
	}
	for _, text := range texts {
		n := EstimateTokens(text)
		if n <= 0 {
			t.Errorf("EstimateTokens(%q) = %d, want > 0", text, n)
		}
		if n != EstimateTokens(text) {
			t.Errorf("EstimateTokens(%q) is non-deterministic", text)
		}
	}
}

func TestEstimateTokensForModel_EncodingSelection(t *testing.T) {
	text := "hello world, this is a model-aware token estimate"

	// All models share cl100k_base; estimates must match the default and stay
	// positive regardless of model name.
	cl100k := EstimateTokensForModel(text, "")
	if cl100k <= 0 {
		t.Fatalf("default estimate = %d, want > 0", cl100k)
	}
	for _, model := range []string{"gpt-4o", "gpt-4o-2024-08-06", "o1-preview", "o3-mini", "gpt-4.1", "gpt-4.5-preview"} {
		n := EstimateTokensForModel(text, model)
		if n != cl100k {
			t.Errorf("EstimateTokensForModel(%q) = %d, want %d (same as cl100k baseline)", model, n, cl100k)
		}
	}
}

func TestEncodingForModel_Families(t *testing.T) {
	cases := []struct {
		model string
		want  string
	}{
		{"", DefaultEncoding},
		{"gpt-4", "cl100k_base"},
		{"gpt-4-0314", "cl100k_base"},
		{"gpt-3.5-turbo", "cl100k_base"},
		// OpenAI o/GPT-4o families are deprioritized and now share cl100k_base.
		{"gpt-4o", "cl100k_base"},
		{"gpt-4o-2024-05-13", "cl100k_base"},
		{"o1-preview", "cl100k_base"},
		{"o3-mini", "cl100k_base"},
		{"o4-mini", "cl100k_base"},
		{"gpt-4.5-preview", "cl100k_base"},
		{"gpt-4.1-2025-04-14", "cl100k_base"},
		// Unknown / non-OpenAI families fall back to the stable default so
		// estimation never fails on a model name.
		{"claude-sonnet-4-6", DefaultEncoding},
		{"deepseek-chat", DefaultEncoding},
		{"totally-unknown-model", DefaultEncoding},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			if got := EncodingForModel(c.model); got != c.want {
				t.Errorf("EncodingForModel(%q) = %q, want %q", c.model, got, c.want)
			}
		})
	}
}

// TestEstimateTokens_MonotonicWithLength guards against a degenerate tokenizer
// where longer text does not yield at least as many tokens.
func TestEstimateTokens_MonotonicWithLength(t *testing.T) {
	short := "hello"
	long := strings.Repeat("hello ", 100)
	if EstimateTokens(short) >= EstimateTokens(long) {
		t.Error("EstimateTokens must increase with longer text")
	}
}
