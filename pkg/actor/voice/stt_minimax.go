package voice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const minimaxSTTModel = "asr-1.0"

// recognizeMinimax transcribes audio via the MiniMax /v1/speech_to_text
// endpoint (multipart upload, non-streaming JSON response). The provider has
// no hotword parameter, so hotwords are ignored.
func recognizeMinimax(audioData []byte, format string, cfg gen.VoiceAccount) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("minimax stt: api_key not configured")
	}
	model := cfg.Model
	if model == "" {
		model = minimaxSTTModel
	}

	body, contentType, err := buildMultipartBody(audioData, format, map[string]string{
		"model":           model,
		"response_format": "json",
		"stream":          "false",
	})
	if err != nil {
		return "", fmt.Errorf("minimax stt: build body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, minimaxBaseURL(cfg)+"/speech_to_text", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("minimax stt: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	respBody, err := minimaxDo("minimax stt", req, cfg.Proxy)
	if err != nil {
		return "", err
	}
	var result struct {
		Text     string           `json:"text"`
		BaseResp *minimaxBaseResp `json:"base_resp"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("minimax stt: decode: %w", err)
	}
	if err := minimaxCheckBase("minimax stt", result.BaseResp); err != nil {
		return "", err
	}
	if result.Text == "" {
		return "", fmt.Errorf("minimax stt: no text in response")
	}
	return result.Text, nil
}
