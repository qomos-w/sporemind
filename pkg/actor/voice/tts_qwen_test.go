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

func TestQwenTTSPath(t *testing.T) {
	cases := map[string]string{
		"qwen-audio-3.0-tts-flash": qwenTTSSpeechPath,
		"qwen-audio-3.0-tts-plus":  qwenTTSSpeechPath,
		"cosyvoice-v3-flash":       qwenTTSSpeechPath,
		"qwen3-tts-flash":          qwenTTSMultiPath,
		"qwen3-tts-instruct-flash": qwenTTSMultiPath,
		"qwen-tts":                 qwenTTSMultiPath,
		"":                         qwenTTSMultiPath,
	}
	for model, want := range cases {
		if got := qwenTTSPath(model); got != want {
			t.Errorf("qwenTTSPath(%q) = %q, want %q", model, got, want)
		}
	}
}

func TestQwenTTSEndpoint(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"", qwenTTSDefaultBase + qwenTTSMultiPath},
		{"https://dashscope.aliyuncs.com", "https://dashscope.aliyuncs.com" + qwenTTSMultiPath},
		{"https://dashscope.aliyuncs.com/", "https://dashscope.aliyuncs.com" + qwenTTSMultiPath},
		{"https://dashscope.aliyuncs.com/api/v1", "https://dashscope.aliyuncs.com" + qwenTTSMultiPath},
		{"https://ws1.cn-beijing.maas.aliyuncs.com", "https://ws1.cn-beijing.maas.aliyuncs.com" + qwenTTSMultiPath},
	}
	for _, c := range cases {
		if got := qwenTTSEndpoint(c.base, qwenTTSMultiPath); got != c.want {
			t.Errorf("qwenTTSEndpoint(%q) = %q, want %q", c.base, got, c.want)
		}
	}
}

func TestQwenLanguageType(t *testing.T) {
	cases := map[string]string{
		"":      "",
		"zh-CN": "Chinese",
		"en-US": "English",
		"ja-JP": "Japanese",
		"ko":    "Korean",
		"xx-YY": "",
	}
	for in, want := range cases {
		if got := qwenLanguageType(in); got != want {
			t.Errorf("qwenLanguageType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQwenAudioFormatFromURL(t *testing.T) {
	cases := map[string]string{
		"https://oss.example.com/a/b/out.wav?Expires=1&Sig=x": "wav",
		"https://oss.example.com/out.MP3":                     "mp3",
		"https://oss.example.com/out":                         "",
		"":                                                    "",
	}
	for in, want := range cases {
		if got := qwenAudioFormatFromURL(in); got != want {
			t.Errorf("qwenAudioFormatFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSynthesizeQwen_AudioTTSUrl verifies the default model uses the
// SpeechSynthesizer route, defaults model/voice/format, and downloads the audio
// from the URL returned in non-streaming mode.
func TestSynthesizeQwen_AudioTTSUrl(t *testing.T) {
	var gotPath, gotAuth string
	var got qwenTTSPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/out.wav" {
			_, _ = w.Write([]byte("wav-bytes"))
			return
		}
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"audio": map[string]any{"data": "", "url": "http://" + r.Host + "/out.wav"},
			},
		})
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL}
	req := gen.VoiceSynthesizeReq{Input: "你好"}

	audio, format, err := synthesizeQwen(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "wav-bytes" {
		t.Errorf("audio = %q", string(audio))
	}
	if format != "wav" {
		t.Errorf("format = %q, want wav", format)
	}
	if gotPath != qwenTTSSpeechPath {
		t.Errorf("path = %q, want %q", gotPath, qwenTTSSpeechPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("auth = %q", gotAuth)
	}
	if got.Model != qwenTTSModel {
		t.Errorf("model = %q, want %q", got.Model, qwenTTSModel)
	}
	if got.Input.Voice != qwenTTSVoice {
		t.Errorf("voice = %q, want %q", got.Input.Voice, qwenTTSVoice)
	}
	if got.Input.Format != "wav" {
		t.Errorf("format = %q, want wav", got.Input.Format)
	}
	if got.Input.SampleRate != qwenTTSSampleRate {
		t.Errorf("sample_rate = %d, want %d", got.Input.SampleRate, qwenTTSSampleRate)
	}
}

// TestSynthesizeQwen_Qwen3InlineAudio verifies model-based route selection plus
// the language_type/instruction mapping and inline base64 decoding.
func TestSynthesizeQwen_Qwen3InlineAudio(t *testing.T) {
	var gotPath string
	var got qwenTTSPayload
	b64 := base64.StdEncoding.EncodeToString([]byte("inline-bytes"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": map[string]any{
				"audio": map[string]any{"data": b64, "url": ""},
			},
		})
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL, Language: "zh-CN"}
	req := gen.VoiceSynthesizeReq{
		Input:       "hi",
		Model:       "qwen3-tts-instruct-flash",
		Voice:       "Cherry",
		Instruction: "cheerful and upbeat",
		Format:      "wav",
	}

	audio, format, err := synthesizeQwen(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "inline-bytes" {
		t.Errorf("audio = %q", string(audio))
	}
	if format != "wav" {
		t.Errorf("format = %q, want wav", format)
	}
	if gotPath != qwenTTSMultiPath {
		t.Errorf("path = %q, want %q", gotPath, qwenTTSMultiPath)
	}
	if got.Model != "qwen3-tts-instruct-flash" {
		t.Errorf("model = %q", got.Model)
	}
	if got.Input.Voice != "Cherry" {
		t.Errorf("voice = %q", got.Input.Voice)
	}
	if got.Input.LanguageType != "Chinese" {
		t.Errorf("language_type = %q, want Chinese", got.Input.LanguageType)
	}
	if got.Input.Instructions != "cheerful and upbeat" {
		t.Errorf("instructions = %q", got.Input.Instructions)
	}
	if got.Input.Format != "" || got.Input.SampleRate != 0 {
		t.Errorf("SpeechSynthesizer-only fields leaked: %+v", got.Input)
	}
}

func TestSynthesizeQwen_MissingAPIKey(t *testing.T) {
	_, _, err := synthesizeQwen(gen.VoiceSynthesizeReq{Input: "x"}, gen.VoiceAccount{})
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestSynthesizeQwen_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"voice not found"}`))
	}))
	defer srv.Close()

	_, _, err := synthesizeQwen(gen.VoiceSynthesizeReq{Input: "x"}, gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "http 400") {
		t.Errorf("err = %v, want http 400", err)
	}
}

func TestSynthesizeQwen_MissingAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status_code": 400,
			"code":        "InvalidParameter",
			"message":     "engine error",
			"output":      map[string]any{"audio": map[string]any{"data": "", "url": ""}},
		})
	}))
	defer srv.Close()

	_, _, err := synthesizeQwen(gen.VoiceSynthesizeReq{Input: "x"}, gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "engine error") {
		t.Errorf("err = %v, want engine error", err)
	}
}
