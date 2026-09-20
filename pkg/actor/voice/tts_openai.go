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
	openaiTTSModel = "tts-1"
	openaiTTSVoice = "alloy"
)

// openaiTTSPayload mirrors the OpenAI audio.speech request body.
type openaiTTSPayload struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	Voice          string `json:"voice"`
	ResponseFormat string `json:"response_format"`
}

// synthesizeOpenAI synthesizes speech from text via an OpenAI-compatible
// audio/speech endpoint (official OpenAI or a custom BaseURL). Returns the raw
// audio bytes and the container format ("mp3" or "wav").
func synthesizeOpenAI(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("openai tts: api_key not configured")
	}

	model := req.Model
	if model == "" {
		model = openaiTTSModel
	}
	voice := req.Voice
	if voice == "" {
		voice = openaiTTSVoice
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}

	body, err := json.Marshal(openaiTTSPayload{
		Model:          model,
		Input:          req.Input,
		Voice:          voice,
		ResponseFormat: format,
	})
	if err != nil {
		return nil, "", fmt.Errorf("openai tts: marshal body: %w", err)
	}

	url := openaiBaseURL(acc) + "/audio/speech"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("openai tts: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("openai tts: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("openai tts: http: %w", err)
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("openai tts: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("openai tts: http %d: %s", resp.StatusCode, truncate(string(audio)))
	}

	return audio, format, nil
}