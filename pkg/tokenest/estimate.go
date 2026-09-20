package tokenest

import (
	"sync"

	"github.com/qomos-w/sporemind/pkg/domain"
	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Token estimation is backed by OpenAI's tiktoken BPE (cl100k_base). The BPE
// ranks are embedded so tokenization is fully offline. cl100k_base is the
// single encoding used for all models — it is a good cross-family approximation
// (Claude, Gemini, DeepSeek, GPT-4/3.5, and the now-deprioritized OpenAI
// o/GPT-4o families). These estimates are FALLBACKS used only when a provider
// returns no usage and the token probe fails; they must never overwrite real
// provider/probe token counts (budget bar, compaction threshold, requestStats).

// DefaultEncoding is the single encoding used for every model. cl100k_base is a
// good cross-family approximation and is the historical baseline.
const DefaultEncoding = tiktoken.MODEL_CL100K_BASE

var (
	defaultEncOnce sync.Once
	defaultEnc     *tiktoken.Tiktoken
	defaultEncErr  error
)

func getDefaultEncoding() (*tiktoken.Tiktoken, error) {
	defaultEncOnce.Do(func() {
		defaultEnc, defaultEncErr = tiktoken.GetEncoding(DefaultEncoding)
	})
	return defaultEnc, defaultEncErr
}

// EncodingForModel maps a model name to its tiktoken encoding. sporemind now
// uses cl100k_base for every model (including the deprioritized OpenAI
// o/GPT-4o families); it is a good cross-family approximation for the token
// fallback path. The model argument is accepted for API stability but does not
// change the result.
func EncodingForModel(model string) string {
	_ = model
	return DefaultEncoding
}

// EstimateTokens returns a tiktoken-based token count for text using the
// default encoding (cl100k_base). It is a fallback estimator only.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	enc, err := getDefaultEncoding()
	if err != nil || enc == nil {
		return heuristicEstimate(text)
	}
	return len(enc.EncodeOrdinary(text))
}

// EstimateTokensForModel returns a tiktoken token count. All models share the
// same cl100k_base encoding, so this is equivalent to EstimateTokens; the model
// argument is retained for API stability. Falls back to a cheap heuristic so it
// never returns an error-driven zero.
func EstimateTokensForModel(text, model string) int {
	_ = model
	return EstimateTokens(text)
}

// EstimateContentTokens sums tiktoken estimates across the provided text fields.
func EstimateContentTokens(texts ...string) int {
	total := 0
	for _, t := range texts {
		total += EstimateTokens(t)
	}
	return total
}

// CountMessageChars sums CharCounts across all text fields in a slice of
// ChatMessages. Retained for calibration feedback characterisation; it does not
// drive the token estimate itself (tiktoken does).
func CountMessageChars(msgs []domain.ChatMessage) CharCounts {
	var cc CharCounts
	for _, msg := range msgs {
		cc.add(msg.ReasoningContent)
		for _, cb := range msg.Content {
			cc.add(cb.Text)
			cc.add(cb.Input)
		}
	}
	return cc
}

// heuristicEstimate is the last-resort estimator used only if the embedded
// tiktoken BPE fails to initialize (which should never happen with shipped
// assets). It is intentionally cheap and conservative.
func heuristicEstimate(text string) int {
	var cjk int
	for _, r := range text {
		if isCJK(r) {
			cjk++
		}
	}
	runes := 0
	for range text {
		runes++
	}
	nonCJK := runes - cjk
	return nonCJK/4 + int(float64(cjk)*1.5)
}

func (cc *CharCounts) add(text string) {
	for _, r := range text {
		switch Classify(r) {
		case ClassLatin:
			cc.Latin++
		case ClassCJK:
			cc.CJK++
		case ClassOther:
			cc.Other++
		}
	}
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x3040 && r <= 0x309F) || // Hiragana
		(r >= 0x30A0 && r <= 0x30FF) || // Katakana
		(r >= 0xAC00 && r <= 0xD7AF) // Hangul Syllables
}
