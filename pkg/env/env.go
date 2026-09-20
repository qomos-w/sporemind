package env

import (
	"os"
)

func init() {
	// Load .env from project root if present.
	// Falls back silently when the file does not exist.
	_ = LoadDotenv(".env")
}

// LLMConfig holds provider endpoint configuration.
type LLMConfig struct {
	Name      string
	BaseURL   string
	AuthToken string
	Model     string
}

// ClaudeConfig returns Anthropic-compatible provider configuration.
// Environment variables take precedence; defaults point to the
// bigmodel.cn Anthropic-compatible endpoint for development.
func ClaudeConfig() LLMConfig {
	return LLMConfig{
		Name:      envOr("SPOREMIND_ANTHROPIC_NAME", "Anthropic"),
		BaseURL:   envOr("SPOREMIND_ANTHROPIC_BASE_URL", "https://open.bigmodel.cn/api/anthropic"),
		AuthToken: envOr("SPOREMIND_ANTHROPIC_API_KEY", ""),
		Model:     envOr("SPOREMIND_ANTHROPIC_MODEL", "glm-5.1"),
	}
}

// OpenAIConfig returns OpenAI-compatible provider configuration.
// Environment variables take precedence; defaults point to the
// bigmodel.cn OpenAI-compatible endpoint for development.
func OpenAIConfig() LLMConfig {
	return LLMConfig{
		Name:      envOr("SPOREMIND_OPENAI_NAME", "OpenAI"),
		BaseURL:   envOr("SPOREMIND_OPENAI_BASE_URL", "https://open.bigmodel.cn/api/coding/paas/v4"),
		AuthToken: envOr("SPOREMIND_OPENAI_API_KEY", ""),
		Model:     envOr("SPOREMIND_OPENAI_MODEL", "glm-5.1"),
	}
}

// DefaultAuthToken returns the API key from SPOREMIND_API_KEY environment variable.
// There is no compiled-in fallback — the key must be provided explicitly.
func DefaultAuthToken() string {
	return os.Getenv("SPOREMIND_API_KEY")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
