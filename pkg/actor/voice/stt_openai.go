package voice

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const openaiDefaultBase = "https://api.openai.com/v1"

// openaiBaseURL returns the OpenAI-compatible API base, falling back to the
// official endpoint when the account has no custom BaseURL configured.
func openaiBaseURL(cfg gen.VoiceAccount) string {
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return b
	}
	return openaiDefaultBase
}

func recognizeOpenAI(audioData []byte, format string, cfg gen.VoiceAccount, hotwords []string) (string, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("openai: api_key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = "whisper-1"
	}

	fields := map[string]string{
		"model": model,
	}
	if cfg.Language != "" {
		fields["language"] = cfg.Language
	}
	if len(hotwords) > 0 {
		fields["prompt"] = strings.Join(hotwords, " ")
	}

	body, contentType, err := buildMultipartBody(audioData, format, fields)
	if err != nil {
		return "", fmt.Errorf("openai: build body: %w", err)
	}

	url := openaiBaseURL(cfg) + "/audio/transcriptions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	return doSTTRequest(req, cfg.Proxy)
}