package aimanager

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// modelContextDefaults is the built-in baseline for model default parameters.
// Keys are model name prefixes; values are context window sizes in tokens.
// This mirrors the TS const const/modelContextDefaults.ts.
var modelContextDefaults = map[string]int32{
	// Anthropic
	"claude-opus-5":     1000000,
	"claude-sonnet-5":   1000000,
	"claude-opus-4-8":   1000000,
	"claude-opus-4-7":   1000000,
	"claude-opus-4-6":   1000000,
	"claude-sonnet-4-6": 1000000,
	"claude-sonnet-4-5": 1000000,
	"claude-haiku-4-5":  200000,
	// OpenAI (source: openrouter.ai/api/v1/models)
	"gpt-6-astra":   1050000,
	"gpt-5.6-sol":   1050000,
	"gpt-5.6-luna":  1050000,
	"gpt-5.6-terra": 1050000,
	"gpt-5.5":       1050000,
	"gpt-5.5-pro":   1050000,
	"gpt-5.4":       1050000,
	"gpt-5.4-pro":   1050000,
	"gpt-5.4-mini":  400000,
	"gpt-5.4-nano":  400000,
	"gpt-5":         400000,
	"gpt-5-mini":    400000,
	"gpt-5-nano":    400000,
	"gpt-5-pro":     400000,
	"gpt-5-codex":   400000,
	"gpt-5.1":       400000,
	"gpt-5.2":       400000,
	"gpt-5.3-codex": 400000,
	"gpt-4.1":       1047576,
	"gpt-4o":        128000,
	"gpt-4o-mini":   128000,
	"gpt-4-turbo":   128000,
	"o3":            200000,
	"o3-pro":        200000,
	"o3-mini":       200000,
	"o4-mini":       200000,
	"o1":            200000,
	"o1-pro":        200000,
	// Google (source: openrouter.ai/api/v1/models)
	"gemini-3.8-flash":      1048576,
	"gemini-3.7-flash":      1048576,
	"gemini-3.6-flash":      1048576,
	"gemini-3.5-flash":      1048576,
	"gemini-3.5-flash-lite": 1048576,
	"gemini-3.1-flash-lite": 1048576,
	"gemini-3.1-pro-preview": 1048576,
	"gemini-3-flash-preview": 1048576,
	"gemini-2.5-pro":        1048576,
	"gemini-2.5-flash":      1048576,
	"gemini-2.5-flash-lite": 1048576,
	"gemini-2.0-flash":      1048576,
	// DeepSeek (source: deepseek-harness + openrouter.ai/api/v1/models)
	"deepseek-v4.1-flash": 1048576,
	"deepseek-v4-flash":   1310720,
	"deepseek-v4-pro":     1048576,
	"deepseek-v3.2":       163840,
	"deepseek-chat":       163840,
	"deepseek-reasoner":   163840,
	"deepseek-r1":         163840,
	// Kimi (Moonshot) (source: openrouter.ai/api/v1/models)
	"kimi-k3":                 1048576,
	"kimi-k2-thinking":        262144,
	"kimi-k2.7-code":          262144,
	"kimi-for-coding":         262144,
	"kimi-k2.7":               262144,
	"kimi-k2.6":               262144,
	"kimi-k2.5":               262144,
	"kimi-k2":                 131072,
	"kimi-for-coding-highspeed": 262144,
	"k3":                      1048576,
	"moonshot-v1-128k":        131072,
	// xAI (source: openrouter.ai/api/v1/models)
	"grok-4.20":    2000000,
	"grok-4.6":     500000,
	"grok-4.5":     500000,
	"grok-4.3":     1000000,
	"grok-4":       131072,
	"grok-4-fast":  2 * 1048576,
	"grok-3":       131072,
	"grok-3-mini":  131072,
	// GLM (智谱) (source: openrouter.ai/api/v1/models)
	"glm-5.3":       1310720,
	"glm-5.3-flash": 1310720,
	"glm-5.2":       1048576,
	"glm-5.1":       204800,
	"glm-5":         204800,
	"glm-5-turbo":   202752,
	"glm-4.7":       204800,
	"glm-4.7-flash": 200000,
	"glm-4.6":       204800,
	"glm-4-plus":    131072,
	"glm-4-long":    1048576,
	// Qwen (source: openrouter.ai/api/v1/models)
	"qwen3.8-max":        1000000,
	"qwen3.8":            1000000,
	"qwen3.7-max":        1000000,
	"qwen3.7-flash":      1000000,
	"qwen3.7-plus":       1000000,
	"qwen3.6-flash":      1000000,
	"qwen3.6-plus":       1000000,
	"qwen3.5-plus":       1000000,
	"qwen3.5-flash":      1000000,
	"qwen3-max":          262144,
	"qwen3-coder-plus":   1000000,
	"qwen3-coder-flash":  1000000,
	"qwen-plus":          1000000,
	"qwen-max":           131072,
	"qwen-turbo":         131072,
	// Mistral (source: openrouter.ai/api/v1/models)
	"mistral-large-2512":   262144,
	"mistral-large-latest": 262144,
	"mistral-medium-3-5":   262144,
	"mistral-small-2603":   262144,
	"mistral-small-latest": 262144,
	"mistral-small-3.2":    256000,
	"codestral":            256000,
	// ERNIE
	"ernie-4.5": 128 * 1024,
	"ernie-x1":  128 * 1024,
	// MiniMax (source: openrouter.ai/api/v1/models)
	"minimax-m3":   1048576,
	"minimax-m2.7": 204800,
	"minimax-m2.5": 204800,
	"minimax-m2.1": 204800,
	"minimax-m2":   204800,
	"minimax-m1":   1000000,
	// ByteDance Seed (source: openrouter.ai/api/v1/models)
	"seed-2.1-turbo":  262144,
	"seed-2.0-code":   262144,
	"seed-2.0-lite":   262144,
	"seed-2.0-mini":   262144,
	"seed-1.6":        262144,
	"seed-1.6-flash":  262144,
	// NVIDIA (source: openrouter.ai/api/v1/models)
	"nemotron-3.5-lightning": 1000000,
	"nemotron-3-ultra":       1000000,
	"nemotron-3-super":       262144,
	// Amazon Nova (source: openrouter.ai/api/v1/models)
	"nova-premier": 1000000,
	"nova-2-lite":  1000000,
	"nova-pro":     300000,
	"nova-lite":    300000,
	// Meta Llama (source: openrouter.ai/api/v1/models)
	"llama-4-scout":    1310720,
	"llama-4-maverick": 1048576,
	// Cohere (source: openrouter.ai/api/v1/models)
	"command-a": 256000,
	// Xiaomi MiMo (source: openrouter.ai/api/v1/models)
	"mimo-v2.5-pro": 1050000,
	"mimo-v2.5":     1050000,
}

// builtinModelDefaults returns the baseline as a sorted slice of ModelDefault.
// Only Prefix and MaxContextLength are populated; optional fields are zero.
func builtinModelDefaults() []domain.ModelDefault {
	out := make([]domain.ModelDefault, 0, len(modelContextDefaults))
	for prefix, ctx := range modelContextDefaults {
		out = append(out, domain.ModelDefault{
			Prefix:           prefix,
			MaxContextLength: ctx,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Prefix < out[j].Prefix
	})
	return out
}

// mergeModelDefaults merges the built-in baseline with user-edited entries.
// User entries with the same prefix override the baseline; new prefixes
// augment it. The result is sorted by prefix.
func mergeModelDefaults(baseline, user []domain.ModelDefault) []domain.ModelDefault {
	byPrefix := make(map[string]domain.ModelDefault, len(baseline)+len(user))
	for _, d := range baseline {
		byPrefix[d.Prefix] = d
	}
	for _, d := range user {
		if d.Prefix != "" {
			byPrefix[d.Prefix] = d
		}
	}
	keys := make([]string, 0, len(byPrefix))
	for k := range byPrefix {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]domain.ModelDefault, len(keys))
	for i, k := range keys {
		out[i] = byPrefix[k]
	}
	return out
}

// effectiveModelDefaults returns the merged defaults table (baseline + user
// overrides) sorted by prefix. Caller must hold a.mu (read or write).
func (a *Actor) effectiveModelDefaults() []domain.ModelDefault {
	return mergeModelDefaults(builtinModelDefaults(), a.modelDefaults)
}

// handleModelDefaultsGet returns the effective defaults table.
func (a *Actor) handleModelDefaultsGet(_ actor.PureContext) (domain.AIManagerModelDefaultsGetResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return domain.AIManagerModelDefaultsGetResp{
		Items: a.effectiveModelDefaults(),
	}, nil
}

// handleModelDefaultsSet replaces the user-edited defaults entries and
// persists the state. Only human users may call this.
//
// Stateless (PureContext): the mutation and the Save both happen inside one
// a.mu write critical section — Save's contract requires the caller to hold
// the write lock, so persisting outside it would race with peer stateless
// handlers mutating Providers/aggregators under the same lock.
func (a *Actor) handleModelDefaultsSet(ctx actor.PureContext, req domain.AIManagerModelDefaultsSetReq) (domain.AIManagerModelDefaultsSetResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerModelDefaultsSetResp{Ok: false, Error: err.Error()}, nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// Strip entries with empty prefix before storing.
	cleaned := make([]domain.ModelDefault, 0, len(req.Items))
	for _, d := range req.Items {
		if d.Prefix != "" {
			cleaned = append(cleaned, d)
		}
	}
	sort.Slice(cleaned, func(i, j int) bool {
		return cleaned[i].Prefix < cleaned[j].Prefix
	})
	a.modelDefaults = cleaned
	if err := a.Save(); err != nil {
		return domain.AIManagerModelDefaultsSetResp{Ok: false, Error: err.Error()}, nil
	}
	return domain.AIManagerModelDefaultsSetResp{Ok: true}, nil
}

// openRouterModelsURL is the public OpenRouter models endpoint. It requires no
// authentication and returns every model OpenRouter proxies, each with an id
// like "openai/gpt-5.4" and a context_length field.
const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// openRouterModelPayload mirrors the subset of the OpenRouter /v1/models
// response that we care about: each model carries an id (provider/model) and a
// context_length (in tokens).
type openRouterModelPayload struct {
	Data []struct {
		ID            string `json:"id"`
		ContextLength int32  `json:"context_length"`
	} `json:"data"`
}

// handleFetchOpenRouterModels fetches the OpenRouter models list server-side,
// strips the provider prefix from each id (e.g. "openai/gpt-5.4" → "gpt-5.4"),
// deduplicates by prefix keeping the largest context_length, sorts by prefix,
// and returns ModelDefault entries with only Prefix + MaxContextLength set.
// It is a public read-only callable; no authentication or admin role required.
func (a *Actor) handleFetchOpenRouterModels(_ actor.PureContext) (domain.AIManagerFetchOpenRouterModelsResp, error) {
	items, err := fetchOpenRouterModels(openRouterModelsURL)
	if err != nil {
		return domain.AIManagerFetchOpenRouterModelsResp{}, fmt.Errorf("fetch openrouter models: %w", err)
	}
	return domain.AIManagerFetchOpenRouterModelsResp{Items: items}, nil
}

// fetchOpenRouterModels fetches the OpenRouter models endpoint, parses the JSON
// response, strips the provider prefix (everything before the first "/"),
// deduplicates by prefix keeping the max context_length, sorts by prefix, and
// returns the result as ModelDefault entries (only Prefix + MaxContextLength).
func fetchOpenRouterModels(url string) ([]domain.ModelDefault, error) {
	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter returned %d: %s", resp.StatusCode, string(body))
	}

	var payload openRouterModelPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Deduplicate by prefix, keeping the max context_length for each prefix.
	best := make(map[string]int32, len(payload.Data))
	for _, m := range payload.Data {
		prefix := stripProviderPrefix(m.ID)
		if prefix == "" {
			continue
		}
		if m.ContextLength > best[prefix] {
			best[prefix] = m.ContextLength
		}
	}

	keys := make([]string, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]domain.ModelDefault, 0, len(keys))
	for _, k := range keys {
		out = append(out, domain.ModelDefault{
			Prefix:           k,
			MaxContextLength: best[k],
		})
	}
	return out, nil
}

// stripProviderPrefix removes the provider namespace from an OpenRouter model
// id. OpenRouter ids are of the form "provider/model-name"; we keep only the
// part after the first "/". If the id contains no "/", it is returned as-is.
func stripProviderPrefix(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if idx := strings.Index(id, "/"); idx >= 0 {
		return strings.TrimSpace(id[idx+1:])
	}
	return id
}
