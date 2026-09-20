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

func TestRecognizeMiMo_Success(t *testing.T) {
	var got mimoASRPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		resp := mimoASRResp{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = " 你好，世界 "
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Language: "zh",
	}
	text, err := recognizeMiMo([]byte("fake-audio"), "wav", acc)
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if text != "你好，世界" {
		t.Errorf("text = %q, want %q", text, "你好，世界")
	}

	if got.Model != mimoASRModel {
		t.Errorf("model = %q, want %q", got.Model, mimoASRModel)
	}
	if got.ASROptions.Language != "zh" {
		t.Errorf("language = %q, want zh", got.ASROptions.Language)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].Content) != 1 {
		t.Fatalf("unexpected messages structure: %+v", got.Messages)
	}
	part := got.Messages[0].Content[0]
	if part.Type != "input_audio" {
		t.Errorf("content part type = %q", part.Type)
	}
	wantPrefix := "data:audio/wav;base64,"
	if !strings.HasPrefix(part.InputAudio.Data, wantPrefix) {
		t.Errorf("audio data uri prefix = %q", part.InputAudio.Data)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(part.InputAudio.Data, wantPrefix))
	if err != nil || string(decoded) != "fake-audio" {
		t.Errorf("audio data decode mismatch")
	}
}

func TestRecognizeMiMo_DefaultsAndMP3(t *testing.T) {
	var got mimoASRPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		resp := mimoASRResp{}
		resp.Choices = append(resp.Choices, struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}{})
		resp.Choices[0].Message.Content = "ok"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL}
	_, err := recognizeMiMo([]byte("mp3-bytes"), "mp3", acc)
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if got.ASROptions.Language != "" {
		t.Errorf("language should be empty when unset, got %q", got.ASROptions.Language)
	}
	part := got.Messages[0].Content[0]
	if !strings.HasPrefix(part.InputAudio.Data, "data:audio/mp3;base64,") {
		t.Errorf("mp3 data uri = %q", part.InputAudio.Data)
	}
}

func TestRecognizeMiMo_MissingAPIKey(t *testing.T) {
	_, err := recognizeMiMo([]byte("x"), "wav", gen.VoiceAccount{})
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestRecognizeMiMo_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key"}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "bad-key", BaseURL: srv.URL}
	_, err := recognizeMiMo([]byte("x"), "wav", acc)
	if err == nil || !strings.Contains(err.Error(), "http 401") {
		t.Errorf("err = %v, want http 401", err)
	}
}
