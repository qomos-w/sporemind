package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"gopkg.in/yaml.v2"
)

// LLM injection for tests.
//
// summarizeViaPlan is a package-level variable (see compaction.go) that tests
// can swap to inject mock or real LLM behavior. The default is the production
// implementation (summarizeViaPlanImpl) which calls aiaggregator.summarize via
// Plan node. Tests can replace it with a mock:
//
//	original := summarizeViaPlan
//	defer func() { summarizeViaPlan = original }()
//	summarizeViaPlan = func(ctx actor.Context, aggRef ref.Ref, session, model, providerName, inputText, systemPrompt string, timeout time.Duration) (string, error) {
//	    return "mock summary", nil
//	}
//
// Real LLM via YAML configuration or environment variables
//
// Configuration is loaded from a YAML file named test-llm.yaml, searched in
// this order:
//
//   1. {projectRoot}/build/test-llm.yaml  — git-ignored by default
//   2. {packageDir}/test-llm.yaml         — same dir as this source file
//
// The file format:
//
//	llm:
//	  provider: openai        # "anthropic" or "openai"
//	  api_key: sk-...         # API key for the chosen provider
//	  model: "glm-5.1"        # optional model override
//	  endpoint: "..."         # optional base URL override
//
// Environment variables override YAML values:
//
//	SPOREMIND_TEST_LLM_PROVIDER  — overrides yaml.llm.provider
//	SPOREMIND_TEST_LLM_API_KEY   — overrides yaml.llm.api_key
//	SPOREMIND_TEST_LLM_MODEL     — overrides yaml.llm.model
//	SPOREMIND_TEST_LLM_ENDPOINT  — overrides yaml.llm.endpoint
//
// When a provider is configured (via YAML or env), InitTestLLMInjector() swaps
// summarizeViaPlan to a real LLM caller. Tests call this in TestMain or init()
// to enable integration-style runs:
//
//	func TestMain(m *testing.M) {
//	    agent.InitTestLLMInjector()
//	    os.Exit(m.Run())
//	}
//
// When no configuration is present InitTestLLMInjector() is a no-op — tests
// continue to use the production Plan-based path or whatever mock the
// individual test has set.

// testLLMConfig mirrors the test-llm.yaml structure.
type testLLMConfig struct {
	LLM struct {
		Provider string `yaml:"provider"`
		APIKey   string `yaml:"api_key"`
		Model    string `yaml:"model"`
		Endpoint string `yaml:"endpoint"`
	} `yaml:"llm"`
}

// findProjectRoot walks upward from dir until it finds a directory containing
// go.mod. Returns "" if not found.
func findProjectRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadTestLLMConfig searches for test-llm.yaml in the following order:
//  1. {projectRoot}/build/test-llm.yaml  — e.g. checked-in build/ dir, ignored by git
//  2. {packageDir}/test-llm.yaml         — same dir as this source file
//
// Returns empty config if none are found or cannot be parsed.
func loadTestLLMConfig() testLLMConfig {
	var cfg testLLMConfig

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return cfg
	}
	pkgDir := filepath.Dir(thisFile)

	candidates := []string{filepath.Join(pkgDir, "test-llm.yaml")}
	if root := findProjectRoot(pkgDir); root != "" {
		candidates = append([]string{filepath.Join(root, "build", "test-llm.yaml")}, candidates...)
	}

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			_ = yaml.Unmarshal(data, &cfg)
			return cfg
		}
	}
	return cfg
}

// resolveTestLLMConfig merges YAML file config with environment variable
// overrides. Precedence: env vars > YAML > empty.
func resolveTestLLMConfig() (provider, apiKey, model, endpoint string) {
	cfg := loadTestLLMConfig()

	provider = cfg.LLM.Provider
	if v := os.Getenv("SPOREMIND_TEST_LLM_PROVIDER"); v != "" {
		provider = v
	}

	apiKey = cfg.LLM.APIKey
	if v := os.Getenv("SPOREMIND_TEST_LLM_API_KEY"); v != "" {
		apiKey = v
	}

	model = cfg.LLM.Model
	if v := os.Getenv("SPOREMIND_TEST_LLM_MODEL"); v != "" {
		model = v
	}

	endpoint = cfg.LLM.Endpoint
	if v := os.Getenv("SPOREMIND_TEST_LLM_ENDPOINT"); v != "" {
		endpoint = v
	}

	return provider, apiKey, model, endpoint
}

// InitTestLLMInjector inspects test-llm.yaml and environment variables, and if
// a provider is configured, replaces summarizeViaPlan with a real LLM caller.
// When no configuration is present it does nothing.
func InitTestLLMInjector() {
	provider, apiKey, model, endpoint := resolveTestLLMConfig()

	if provider == "" {
		return
	}
	if apiKey == "" {
		panic(fmt.Sprintf(
			"SPOREMIND_TEST_LLM_PROVIDER (or test-llm.yaml llm.provider) is set to %q but SPOREMIND_TEST_LLM_API_KEY (or test-llm.yaml llm.api_key) is missing",
			provider))
	}
	if model == "" {
		model = defaultTestModel(provider)
	}

	summarizeViaPlan = newRealSummarizeViaPlan(provider, apiKey, model, endpoint)
}

// IsTestLLMConfigured reports whether InitTestLLMInjector would (or did)
// successfully swap in a real LLM caller. Useful for tests to decide whether
// to skip integration checks.
func IsTestLLMConfigured() bool {
	provider, apiKey, _, _ := resolveTestLLMConfig()
	return provider != "" && apiKey != ""
}

func defaultTestModel(provider string) string {
	switch strings.ToLower(provider) {
	case "anthropic":
		return "claude-3-5-haiku-20241022"
	case "openai":
		return "gpt-4o-mini"
	default:
		return ""
	}
}

// newRealSummarizeViaPlan returns a summarizeViaPlan implementation that
// calls the provider's native API directly, bypassing the aiaggregator Plan
// node. This gives tests a fast integration path without spawning child actors.
func newRealSummarizeViaPlan(provider, apiKey, model, endpoint string) func(ctx actor.Context, aggRef ref.Ref, session string, unit domain.ModelUnit, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
	return func(ctx actor.Context, aggRef ref.Ref, session string, unit domain.ModelUnit, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
		m := model
		if unit.Model != "" {
			m = unit.Model
		}
		return callRealLLM(ctx.Lifecycle(), provider, apiKey, m, endpoint, inputText, systemPrompt, timeout)
	}
}

// callRealLLM dispatches to the provider's SDK. Extend the switch as new
// providers are added.
func callRealLLM(ctx context.Context, provider, apiKey, model, endpoint, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
	switch strings.ToLower(provider) {
	case "anthropic":
		return callAnthropic(ctx, apiKey, model, inputText, systemPrompt, timeout)
	case "openai":
		return callOpenAI(ctx, apiKey, model, endpoint, inputText, systemPrompt, timeout)
	default:
		return "", fmt.Errorf("unsupported test LLM provider: %s", provider)
	}
}

// callAnthropic calls Anthropic Messages API using the SDK imported in this
// module. Returns the first text content block from the response.
func callAnthropic(ctx context.Context, apiKey, model, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
	// Stub: import and use github.com/anthropics/anthropic-sdk-go or equivalent.
	// Keeping this as a compile-time placeholder so the injection framework
	// is structurally complete. Uncomment and fill in when the SDK import is
	// added to go.mod.
	_ = systemPrompt
	return "", fmt.Errorf("anthropic real LLM call not yet implemented (provider=%s model=%s)", model, inputText[:min(len(inputText), 20)])
}

// ── OpenAI-compatible HTTP client (no external SDK) ──

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type openAIChatMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIChatCompletionReq struct {
	Model    string              `json:"model"`
	Messages []openAIChatMessage `json:"messages"`
	Tools    []openAITool        `json:"tools,omitempty"`
}

type openAIChatCompletionResp struct {
	Choices []struct {
		Message      openAIChatMessage `json:"message"`
		FinishReason string            `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// callOpenAI calls an OpenAI-compatible Chat Completions endpoint using the
// standard library. The endpoint argument overrides the default OpenAI URL,
// enabling third-party gateways such as bigmodel.cn.
func callOpenAI(ctx context.Context, apiKey, model, endpoint, inputText string, systemPrompt string, timeout time.Duration) (string, error) {
	baseURL := endpoint
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"

	messages := []openAIChatMessage{
		{Role: "user", Content: inputText},
	}
	if systemPrompt != "" {
		messages = append([]openAIChatMessage{{Role: "system", Content: systemPrompt}}, messages...)
	}

	reqBody := openAIChatCompletionReq{
		Model:    model,
		Messages: messages,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		url,
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed openAIChatCompletionResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("api error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("empty choices in response")
	}
	return parsed.Choices[0].Message.Content, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
