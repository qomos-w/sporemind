package voice

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	openaiChatAudioModel = "gpt-4o-audio-preview"
	openaiChatAudioVoice = "alloy"
)

// openaiChatAudioMsg is a single chat message sent to the audio-modality
// completions endpoint.
type openaiChatAudioMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openaiChatAudioPayload mirrors the OpenAI Chat Completions request body for
// the audio output modality (model "gpt-4o-audio-preview" and compatible
// providers such as Xiaomi MiMo). The text to be spoken and an optional
// prosody/style instruction are carried as chat messages; the audio bytes come
// back base64-encoded in the response.
type openaiChatAudioPayload struct {
	Model       string                `json:"model"`
	Modalities  []string              `json:"modalities"`
	Audio       openaiChatAudioOut    `json:"audio"`
	Messages    []openaiChatAudioMsg  `json:"messages"`
}

type openaiChatAudioOut struct {
	Format string `json:"format"`
	Voice  string `json:"voice"`
}

// openaiChatAudioResp captures only the fields needed to extract audio bytes
// from the completions response.
type openaiChatAudioResp struct {
	Choices []struct {
		Message struct {
			Audio struct {
				Data string `json:"data"`
			} `json:"audio"`
		} `json:"message"`
	} `json:"choices"`
}

// synthesizeOpenAIChat synthesizes speech via the OpenAI Chat Completions
// audio-modality standard (POST {base}/chat/completions with
// modalities ["text","audio"]). When req.Instruction is set it is sent as a
// user message that precedes the assistant message holding the text to read,
// matching the style-prompt convention used by audio-preview-compatible
// providers. Returns the decoded audio bytes and the container format.
func synthesizeOpenAIChat(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("openai chat tts: api_key not configured")
	}

	model := req.Model
	if model == "" {
		model = openaiChatAudioModel
	}
	voice := req.Voice
	if voice == "" {
		voice = openaiChatAudioVoice
	}
	format := req.Format
	if format == "" {
		format = "mp3"
	}

	var messages []openaiChatAudioMsg
	if inst := req.Instruction; inst != "" {
		messages = []openaiChatAudioMsg{
			{Role: "user", Content: inst},
			{Role: "assistant", Content: req.Input},
		}
	} else {
		messages = []openaiChatAudioMsg{
			{Role: "user", Content: "Read aloud exactly:\n\n" + req.Input},
		}
	}

	body, err := json.Marshal(openaiChatAudioPayload{
		Model:      model,
		Modalities: []string{"text", "audio"},
		Audio:      openaiChatAudioOut{Format: format, Voice: voice},
		Messages:   messages,
	})
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: marshal body: %w", err)
	}

	url := openaiBaseURL(acc) + "/chat/completions"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("openai chat tts: http %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	var parsed openaiChatAudioResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", fmt.Errorf("openai chat tts: decode body: %w", err)
	}
	if len(parsed.Choices) == 0 || parsed.Choices[0].Message.Audio.Data == "" {
		return nil, "", fmt.Errorf("openai chat tts: response missing audio data")
	}

	audio, err := base64.StdEncoding.DecodeString(parsed.Choices[0].Message.Audio.Data)
	if err != nil {
		return nil, "", fmt.Errorf("openai chat tts: decode base64 audio: %w", err)
	}

	return audio, format, nil
}
