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

func TestSynthesizeMiMo_Defaults(t *testing.T) {
	var got openaiChatAudioPayload
	srv := startChatAudioMock(t, &got)
	defer srv.Close()

	acc := gen.VoiceAccount{
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}
	req := gen.VoiceSynthesizeReq{Input: "你好"}

	audio, format, err := synthesizeMiMo(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "audio-bytes" {
		t.Errorf("audio = %q", string(audio))
	}
	if format != "mp3" {
		t.Errorf("format = %q, want mp3", format)
	}

	if got.Model != mimoTTSModel {
		t.Errorf("model = %q, want %q", got.Model, mimoTTSModel)
	}
	if got.Audio.Voice != mimoTTSVoice {
		t.Errorf("voice = %q, want %q", got.Audio.Voice, mimoTTSVoice)
	}
	if got.Audio.Format != "mp3" {
		t.Errorf("audio format = %q, want mp3", got.Audio.Format)
	}
}

func TestSynthesizeMiMo_AccountOverrides(t *testing.T) {
	var got openaiChatAudioPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
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
	defer srv.Close()

	acc := gen.VoiceAccount{
		APIKey:  "test-key",
		BaseURL: srv.URL,
		Model:   "mimo-v2-tts",
		Voice:   "default_zh",
	}
	req := gen.VoiceSynthesizeReq{
		Input:  "你好",
		Model:  acc.Model,
		Voice:  acc.Voice,
		Format: "wav",
	}

	_, _, err := synthesizeMiMo(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}

	if got.Model != "mimo-v2-tts" {
		t.Errorf("model = %q, want account override", got.Model)
	}
	if got.Audio.Voice != "default_zh" {
		t.Errorf("voice = %q, want account override", got.Audio.Voice)
	}
	if got.Audio.Format != "wav" {
		t.Errorf("format = %q, want request override", got.Audio.Format)
	}
}

func TestSynthesizeMiMo_MissingAPIKey(t *testing.T) {
	_, _, err := synthesizeMiMo(gen.VoiceSynthesizeReq{Input: "x"}, gen.VoiceAccount{})
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}
