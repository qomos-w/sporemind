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
	// qwenDefaultBase is the DashScope (Alibaba Cloud Model Studio) API root
	// for the China (Beijing) region.
	//
	// Qwen-Audio-3.0-ASR-Flash is NOT served from the OpenAI-compatible base
	// (…/compatible-mode/v1); per the official docs it is called through the
	// native multimodal-generation endpoint below. See
	// https://help.aliyun.com/zh/model-studio/fun-asr-flash-recorded-speech-recognition-http-api
	qwenDefaultBase = "https://dashscope.aliyuncs.com"

	// qwenASRModel is the default Qwen-Audio-3.0 ASR model.
	qwenASRModel = "qwen-audio-3.0-asr-flash"

	// qwenASRPath is the DashScope synchronous speech-recognition endpoint.
	qwenASRPath = "/api/v1/services/aigc/multimodal-generation/generation"
)

// qwenBaseURL returns the DashScope API root, falling back to the official
// endpoint when the account has no custom BaseURL configured. A custom
// BaseURL may point at a workspace-dedicated domain
// (https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com) or the shared root.
func qwenBaseURL(cfg gen.VoiceAccount) string {
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return b
	}
	return qwenDefaultBase
}

// qwenAudioDataURI builds the RFC 2397 data URI carrying base64-encoded audio
// that DashScope expects inside the input_audio content part. pcm is converted
// to wav upstream.
func qwenAudioDataURI(audioData []byte, format string) string {
	mime := "audio/wav"
	switch format {
	case "mp3":
		mime = "audio/mpeg"
	case "m4a":
		mime = "audio/mp4"
	case "ogg", "opus":
		mime = "audio/ogg"
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(audioData))
}

// qwenLanguageCode normalizes an account language tag ("zh-CN") to the short
// code DashScope language_hints accepts ("zh"). Returns "" for empty input.
func qwenLanguageCode(language string) string {
	language = strings.TrimSpace(language)
	if language == "" {
		return ""
	}
	if i := strings.IndexAny(language, "-_"); i >= 0 {
		language = language[:i]
	}
	return strings.ToLower(language)
}

// qwenVocabulary converts the free-form hotword list into the weighted
// vocabulary map DashScope's inline hotwords expect. Weight 5 is the maximum
// normal weight (a value of 50 is reserved for super hotwords).
func qwenVocabulary(hotwords []string) map[string]int {
	vocab := make(map[string]int, len(hotwords))
	for _, w := range hotwords {
		if w = strings.TrimSpace(w); w != "" {
			vocab[w] = 5
		}
	}
	return vocab
}

// qwenContentPart is a single content entry carrying base64 audio.
type qwenContentPart struct {
	Type       string         `json:"type"`
	InputAudio qwenInputAudio `json:"input_audio"`
}

type qwenInputAudio struct {
	Data string `json:"data"`
}

// qwenMessage is the user message wrapping the audio content array.
type qwenMessage struct {
	Role    string            `json:"role"`
	Content []qwenContentPart `json:"content"`
}

type qwenInput struct {
	Messages []qwenMessage `json:"messages"`
}

// qwenParameters mirrors the DashScope model parameters block.
type qwenParameters struct {
	Format        string         `json:"format"`
	LanguageHints []string       `json:"language_hints,omitempty"`
	Vocabulary    map[string]int `json:"vocabulary,omitempty"`
}

// qwenASRPayload mirrors the DashScope synchronous ASR request body.
type qwenASRPayload struct {
	Model      string         `json:"model"`
	Input      qwenInput      `json:"input"`
	Parameters qwenParameters `json:"parameters"`
}

// qwenASRResp captures the recognized text (and any error envelope) from the
// DashScope response.
type qwenASRResp struct {
	Output struct {
		Text string `json:"text"`
	} `json:"output"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// recognizeQwen transcribes audio via the DashScope synchronous
// Qwen-Audio-3.0-ASR-Flash endpoint. The audio is passed as a base64 data URI.
func recognizeQwen(audioData []byte, format string, cfg gen.VoiceAccount, hotwords []string) (string, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("qwen asr: api_key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = qwenASRModel
	}

	payload := qwenASRPayload{
		Model: model,
		Input: qwenInput{
			Messages: []qwenMessage{
				{
					Role: "user",
					Content: []qwenContentPart{
						{
							Type:       "input_audio",
							InputAudio: qwenInputAudio{Data: qwenAudioDataURI(audioData, format)},
						},
					},
				},
			},
		},
		Parameters: qwenParameters{Format: format},
	}
	if code := qwenLanguageCode(cfg.Language); code != "" {
		payload.Parameters.LanguageHints = []string{code}
	}
	if len(hotwords) > 0 {
		payload.Parameters.Vocabulary = qwenVocabulary(hotwords)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("qwen asr: marshal body: %w", err)
	}

	url := qwenBaseURL(cfg) + qwenASRPath
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("qwen asr: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	// Force the non-streaming variant; SSE is the server default for long audio.
	req.Header.Set("X-DashScope-SSE", "disable")
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	client, err := sttHTTPClient(cfg.Proxy)
	if err != nil {
		return "", fmt.Errorf("qwen asr: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("qwen asr: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("qwen asr: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qwen asr: http %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	var parsed qwenASRResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("qwen asr: decode body: %w", err)
	}
	text := strings.TrimSpace(parsed.Output.Text)
	if text == "" {
		// DashScope reports some failures with HTTP 200 plus an error envelope.
		if parsed.Code != "" || parsed.Message != "" {
			return "", fmt.Errorf("qwen asr: %s: %s", parsed.Code, truncate(parsed.Message))
		}
		return "", fmt.Errorf("qwen asr: response missing output text")
	}
	return text, nil
}
