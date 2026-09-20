package voice

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const (
	mimoDefaultBase = "https://api.xiaomimimo.com/v1"
	mimoASRModel    = "mimo-v2.5-asr"
)

// mimoBaseURL returns the MiMo-compatible API base, falling back to the
// official endpoint when the account has no custom BaseURL configured.
func mimoBaseURL(cfg gen.VoiceAccount) string {
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return b
	}
	return mimoDefaultBase
}

// mimoAudioDataURI builds a data URI for the audio bytes suitable for MiMo's
// chat-completions input_audio content part.
func mimoAudioDataURI(audioData []byte, format string) string {
	mime := "audio/wav"
	switch format {
	case "mp3":
		mime = "audio/mp3"
	case "m4a":
		mime = "audio/mp4"
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(audioData))
}

// mimoASRContentPart is a single content part carrying base64-encoded audio.
type mimoASRContentPart struct {
	Type       string              `json:"type"`
	InputAudio mimoASRInputAudio   `json:"input_audio"`
}

type mimoASRInputAudio struct {
	Data string `json:"data"`
}

// mimoASRMessage is the user message wrapping the audio content array.
type mimoASRMessage struct {
	Role    string                `json:"role"`
	Content []mimoASRContentPart  `json:"content"`
}

// mimoASRPayload mirrors the MiMo ASR chat-completions request body.
type mimoASRPayload struct {
	Model      string           `json:"model"`
	Messages   []mimoASRMessage `json:"messages"`
	ASROptions mimoASROptions   `json:"asr_options,omitempty"`
}

type mimoASROptions struct {
	Language string `json:"language,omitempty"`
}

// mimoASRResp captures the text result from the chat-completions response.
type mimoASRResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// recognizeMiMo transcribes audio via the MiMo-V2.5-ASR chat-completions
// endpoint. The audio must be wav or mp3; pcm is converted to wav upstream.
func recognizeMiMo(audioData []byte, format string, cfg gen.VoiceAccount) (string, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("mimo asr: api_key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = mimoASRModel
	}

	payload := mimoASRPayload{
		Model: mimoASRModel,
		Messages: []mimoASRMessage{
			{
				Role: "user",
				Content: []mimoASRContentPart{
					{
						Type:       "input_audio",
						InputAudio: mimoASRInputAudio{Data: mimoAudioDataURI(audioData, format)},
					},
				},
			},
		},
	}
	if cfg.Language != "" {
		payload.ASROptions.Language = cfg.Language
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("mimo asr: marshal body: %w", err)
	}

	url := mimoBaseURL(cfg) + "/chat/completions"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("mimo asr: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	client, err := sttHTTPClient(cfg.Proxy)
	if err != nil {
		return "", fmt.Errorf("mimo asr: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mimo asr: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mimo asr: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mimo asr: http %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	var parsed mimoASRResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("mimo asr: decode body: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("mimo asr: response missing choices")
	}
	return strings.TrimSpace(parsed.Choices[0].Message.Content), nil
}
