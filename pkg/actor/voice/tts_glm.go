package voice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	glmTTSURL    = "https://open.bigmodel.cn/api/paas/v4/audio/speech"
	glmTTSModel  = "glm-tts"
	glmTTSVoice  = "tongtong"
)

// glmTTSPayload mirrors the Zhipu audio.speech request body.
type glmTTSPayload struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

// synthesizeGLM synthesizes speech from text via Zhipu GLM TTS.
// Returns the raw audio bytes and the container format ("mp3" or "wav").
func synthesizeGLM(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	apiKey := acc.APIKey
	if apiKey == "" {
		return nil, "", fmt.Errorf("glm tts: api_key not configured")
	}

	model := req.Model
	if model == "" {
		model = glmTTSModel
	}
	voice := req.Voice
	if voice == "" {
		voice = glmTTSVoice
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}

	body, err := json.Marshal(glmTTSPayload{
		Model:          model,
		Input:          req.Input,
		Voice:          voice,
		ResponseFormat: format,
	})
	if err != nil {
		return nil, "", fmt.Errorf("glm tts: marshal body: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, glmTTSURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("glm tts: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("glm tts: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("glm tts: http: %w", err)
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("glm tts: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("glm tts: http %d: %s", resp.StatusCode, truncate(string(audio)))
	}

	return audio, format, nil
}
