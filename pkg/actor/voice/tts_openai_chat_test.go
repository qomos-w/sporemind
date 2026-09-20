package voice

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// startChatAudioMock launches an httptest.Server that mimics a Chat Completions
// audio-modality endpoint. It echoes the request body back to got, and replies
// with a fixed base64 payload.
func startChatAudioMock(t *testing.T, got *openaiChatAudioPayload) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		audioB64 := base64.StdEncoding.EncodeToString([]byte("audio-bytes"))
		resp := openaiChatAudioResp{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Audio struct {
					Data string `json:"data"`
				} `json:"audio"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Audio.Data = audioB64
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestSynthesizeOpenAIChat_WithInstruction(t *testing.T) {
	var got openaiChatAudioPayload
	srv := startChatAudioMock(t, &got)
	defer srv.Close()

	acc := gen.VoiceAccount{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}
	req := gen.VoiceSynthesizeReq{
		Input:       "Hey boss, I passed!",
		Instruction: "Bright, bouncy, slightly sing-song tone.",
		Model:       "mimo-v2.5-tts",
		Voice:       "Chloe",
		Format:      "wav",
	}

	audio, format, err := synthesizeOpenAIChat(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "audio-bytes" {
		t.Errorf("audio = %q, want %q", string(audio), "audio-bytes")
	}
	if format != "wav" {
		t.Errorf("format = %q, want wav", format)
	}

	if got.Model != "mimo-v2.5-tts" {
		t.Errorf("model = %q", got.Model)
	}
	if len(got.Modalities) != 2 || got.Modalities[0] != "text" || got.Modalities[1] != "audio" {
		t.Errorf("modalities = %v, want [text audio]", got.Modalities)
	}
	if got.Audio.Voice != "Chloe" || got.Audio.Format != "wav" {
		t.Errorf("audio block = %+v", got.Audio)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Content != req.Instruction {
		t.Errorf("messages[0] = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || got.Messages[1].Content != req.Input {
		t.Errorf("messages[1] = %+v", got.Messages[1])
	}
}

func TestSynthesizeOpenAIChat_WithoutInstruction(t *testing.T) {
	var got openaiChatAudioPayload
	srv := startChatAudioMock(t, &got)
	defer srv.Close()

	acc := gen.VoiceAccount{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}
	req := gen.VoiceSynthesizeReq{Input: "Just read this."}

	audio, _, err := synthesizeOpenAIChat(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "audio-bytes" {
		t.Errorf("audio = %q", string(audio))
	}

	// No instruction => single user message wrapping the input verbatim.
	if len(got.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("messages[0].role = %q", got.Messages[0].Role)
	}
	if !strings.Contains(got.Messages[0].Content, req.Input) {
		t.Errorf("messages[0].content = %q, want to contain input", got.Messages[0].Content)
	}
	// Defaults applied when empty.
	if got.Model != openaiChatAudioModel {
		t.Errorf("model = %q, want default %q", got.Model, openaiChatAudioModel)
	}
	if got.Audio.Voice != openaiChatAudioVoice {
		t.Errorf("voice = %q, want default %q", got.Audio.Voice, openaiChatAudioVoice)
	}
	if got.Audio.Format != "mp3" {
		t.Errorf("format = %q, want default mp3", got.Audio.Format)
	}
}

func TestSynthesizeOpenAIChat_MissingAPIKey(t *testing.T) {
	acc := gen.VoiceAccount{}
	_, _, err := synthesizeOpenAIChat(gen.VoiceSynthesizeReq{Input: "x"}, acc)
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestSynthesizeOpenAIChat_MissingAudioData(t *testing.T) {
	// Server returns a valid JSON shape but with no audio data.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{}}]}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL}
	_, _, err := synthesizeOpenAIChat(gen.VoiceSynthesizeReq{Input: "x"}, acc)
	if err == nil || !strings.Contains(err.Error(), "missing audio data") {
		t.Errorf("err = %v, want missing audio data", err)
	}
}
