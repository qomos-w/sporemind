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

func TestRecognizeQwen_Success(t *testing.T) {
	var got qwenASRPayload
	var gotPath, gotSSE string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSSE = r.Header.Get("X-DashScope-SSE")
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		resp := qwenASRResp{}
		resp.Output.Text = " 你好，世界 "
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL, Language: "zh-CN"}
	text, err := recognizeQwen([]byte("fake-audio"), "wav", acc, nil)
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if text != "你好，世界" {
		t.Errorf("text = %q, want %q", text, "你好，世界")
	}

	if gotPath != qwenASRPath {
		t.Errorf("path = %q, want %q", gotPath, qwenASRPath)
	}
	if gotSSE != "disable" {
		t.Errorf("X-DashScope-SSE = %q, want disable", gotSSE)
	}
	if got.Model != qwenASRModel {
		t.Errorf("model = %q, want %q", got.Model, qwenASRModel)
	}
	if got.Parameters.Format != "wav" {
		t.Errorf("format = %q, want wav", got.Parameters.Format)
	}
	if len(got.Parameters.LanguageHints) != 1 || got.Parameters.LanguageHints[0] != "zh" {
		t.Errorf("language_hints = %v, want [zh]", got.Parameters.LanguageHints)
	}
	if len(got.Input.Messages) != 1 || len(got.Input.Messages[0].Content) != 1 {
		t.Fatalf("unexpected messages structure: %+v", got.Input.Messages)
	}
	part := got.Input.Messages[0].Content[0]
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

func TestRecognizeQwen_DefaultsAndHotwords(t *testing.T) {
	var got qwenASRPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		resp := qwenASRResp{}
		resp.Output.Text = "ok"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL}
	_, err := recognizeQwen([]byte("mp3-bytes"), "mp3", acc, []string{"张三", " 李四 "})
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if got.Model != qwenASRModel {
		t.Errorf("default model = %q, want %q", got.Model, qwenASRModel)
	}
	if len(got.Parameters.LanguageHints) != 0 {
		t.Errorf("language_hints should be empty when unset, got %v", got.Parameters.LanguageHints)
	}
	if got.Parameters.Vocabulary["张三"] != 5 || got.Parameters.Vocabulary["李四"] != 5 {
		t.Errorf("vocabulary = %v, want 张三/李四 at weight 5", got.Parameters.Vocabulary)
	}
	part := got.Input.Messages[0].Content[0]
	if !strings.HasPrefix(part.InputAudio.Data, "data:audio/mpeg;base64,") {
		t.Errorf("mp3 data uri = %q", part.InputAudio.Data)
	}
}

func TestQwenBaseURL_Default(t *testing.T) {
	if got := qwenBaseURL(gen.VoiceAccount{}); got != qwenDefaultBase {
		t.Errorf("default base = %q, want %q", got, qwenDefaultBase)
	}
	if got := qwenBaseURL(gen.VoiceAccount{BaseURL: "https://example.com/"}); got != "https://example.com" {
		t.Errorf("trimmed base = %q, want https://example.com", got)
	}
}

func TestRecognizeQwen_MissingAPIKey(t *testing.T) {
	_, err := recognizeQwen([]byte("x"), "wav", gen.VoiceAccount{}, nil)
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestRecognizeQwen_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"invalid key"}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "bad-key", BaseURL: srv.URL}
	_, err := recognizeQwen([]byte("x"), "wav", acc, nil)
	if err == nil || !strings.Contains(err.Error(), "http 401") {
		t.Errorf("err = %v, want http 401", err)
	}
}

func TestRecognizeQwen_OKStatusErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"DataInspectionFailed","message":"blocked"}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL}
	_, err := recognizeQwen([]byte("x"), "wav", acc, nil)
	if err == nil || !strings.Contains(err.Error(), "DataInspectionFailed") {
		t.Errorf("err = %v, want DataInspectionFailed", err)
	}
}
