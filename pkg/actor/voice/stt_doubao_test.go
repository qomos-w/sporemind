package voice

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// withFastDoubaoPoll shortens the poll interval for the duration of a test.
func withFastDoubaoPoll(t *testing.T) {
	t.Helper()
	old := doubaoPollInterval
	doubaoPollInterval = time.Millisecond
	t.Cleanup(func() { doubaoPollInterval = old })
}

func TestRecognizeDoubao_Success(t *testing.T) {
	withFastDoubaoPoll(t)

	var submit doubaoSubmitBody
	var submitHeaders http.Header
	queries := 0

	mux := http.NewServeMux()
	mux.HandleFunc(doubaoSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		submitHeaders = r.Header.Clone()
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &submit); err != nil {
			t.Fatalf("decode submit: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc(doubaoQueryPath, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Api-Request-Id"); got == "" {
			t.Errorf("query missing X-Api-Request-Id")
		}
		queries++
		if queries == 1 {
			w.Header().Set("X-Api-Status-Code", "20000001")
			_, _ = w.Write([]byte("{}"))
			return
		}
		w.Header().Set("X-Api-Status-Code", doubaoStatusSuccess)
		_, _ = w.Write([]byte(`{"audio_info":{"duration":1200},"result":{"text":"  你好，世界  "}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "single-ark-key", BaseURL: srv.URL, Language: "zh-CN"}
	text, err := recognizeDoubao([]byte("fake-audio"), "wav", acc)
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if text != "你好，世界" {
		t.Errorf("text = %q, want %q", text, "你好，世界")
	}
	if queries < 2 {
		t.Errorf("expected at least 2 query polls, got %d", queries)
	}

	// Header contract.
	if got := submitHeaders.Get("X-Api-Key"); got != "single-ark-key" {
		t.Errorf("X-Api-Key = %q", got)
	}
	if got := submitHeaders.Get("X-Api-Resource-Id"); got != doubaoResourceSeedASR2 {
		t.Errorf("X-Api-Resource-Id = %q, want %q", got, doubaoResourceSeedASR2)
	}
	if got := submitHeaders.Get("X-Api-Sequence"); got != "-1" {
		t.Errorf("X-Api-Sequence = %q, want -1", got)
	}
	if rid := submitHeaders.Get("X-Api-Request-Id"); len(rid) != 36 || strings.Count(rid, "-") != 4 {
		t.Errorf("X-Api-Request-Id = %q, want a UUID", rid)
	}

	// Body contract.
	if submit.User.UID == "" {
		t.Errorf("user.uid empty")
	}
	if submit.Audio.Format != "wav" {
		t.Errorf("audio.format = %q", submit.Audio.Format)
	}
	if submit.Audio.Language != "zh-CN" {
		t.Errorf("audio.language = %q", submit.Audio.Language)
	}
	if submit.Audio.Rate != 16000 || submit.Audio.Bits != 16 || submit.Audio.Channel != 1 {
		t.Errorf("audio rate/bits/channel = %d/%d/%d", submit.Audio.Rate, submit.Audio.Bits, submit.Audio.Channel)
	}
	decoded, err := base64.StdEncoding.DecodeString(submit.Audio.Data)
	if err != nil || string(decoded) != "fake-audio" {
		t.Errorf("audio.data decode = %q err=%v", string(decoded), err)
	}
	if submit.Request.ModelName != doubaoModelName {
		t.Errorf("request.model_name = %q, want %q", submit.Request.ModelName, doubaoModelName)
	}
}

func TestRecognizeDoubao_LegacyAuthAndDefaultModel(t *testing.T) {
	withFastDoubaoPoll(t)

	var headers http.Header
	mux := http.NewServeMux()
	mux.HandleFunc(doubaoSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc(doubaoQueryPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", doubaoStatusSuccess)
		_, _ = w.Write([]byte(`{"result":{"text":"ok"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Legacy "appKey:accessKey" credential form.
	acc := gen.VoiceAccount{APIKey: "my-app:my-access", BaseURL: srv.URL}
	text, err := recognizeDoubao([]byte("x"), "m4a", acc)
	if err != nil {
		t.Fatalf("recognize: %v", err)
	}
	if text != "ok" {
		t.Errorf("text = %q", text)
	}
	if got := headers.Get("X-Api-App-Key"); got != "my-app" {
		t.Errorf("X-Api-App-Key = %q", got)
	}
	if got := headers.Get("X-Api-Access-Key"); got != "my-access" {
		t.Errorf("X-Api-Access-Key = %q", got)
	}
	if got := headers.Get("X-Api-Key"); got != "" {
		t.Errorf("X-Api-Key should be unset for legacy auth, got %q", got)
	}
	if got := headers.Get("X-Api-Resource-Id"); got != doubaoResourceSeedASR2 {
		t.Errorf("default resource id = %q", got)
	}
}

func TestRecognizeDoubao_MissingAPIKey(t *testing.T) {
	_, err := recognizeDoubao([]byte("x"), "wav", gen.VoiceAccount{})
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestRecognizeDoubao_SubmitHTTPError(t *testing.T) {
	withFastDoubaoPoll(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"header":{"reqid":"x","code":45000010,"message":"load grant: requested grant not found"}}`))
	}))
	defer srv.Close()

	_, err := recognizeDoubao([]byte("x"), "wav", gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "submit http 401") {
		t.Errorf("err = %v, want submit http 401", err)
	}
	if err == nil || !strings.Contains(err.Error(), "45000010") {
		t.Errorf("err = %v, want to carry the upstream code", err)
	}
}

func TestRecognizeDoubao_QueryStatusError(t *testing.T) {
	withFastDoubaoPoll(t)

	mux := http.NewServeMux()
	mux.HandleFunc(doubaoSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc(doubaoQueryPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", "45000002")
		_, _ = w.Write([]byte(`{"header":{"code":45000002,"message":"empty audio"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := recognizeDoubao([]byte("x"), "wav", gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "45000002") {
		t.Errorf("err = %v, want status 45000002", err)
	}
	if err == nil || !strings.Contains(err.Error(), "empty audio") {
		t.Errorf("err = %v, want status text", err)
	}
}

func TestRecognizeDoubao_MuteAudioIsTerminal(t *testing.T) {
	withFastDoubaoPoll(t)

	mux := http.NewServeMux()
	mux.HandleFunc(doubaoSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc(doubaoQueryPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", "20000003")
		_, _ = w.Write([]byte("{}"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := recognizeDoubao([]byte("x"), "wav", gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "mute") {
		t.Errorf("err = %v, want mute-audio terminal error", err)
	}
}

func TestRecognizeDoubao_EmptyText(t *testing.T) {
	withFastDoubaoPoll(t)

	mux := http.NewServeMux()
	mux.HandleFunc(doubaoSubmitPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc(doubaoQueryPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Api-Status-Code", doubaoStatusSuccess)
		_, _ = w.Write([]byte(`{"result":{"text":"   "}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := recognizeDoubao([]byte("x"), "wav", gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "empty text") {
		t.Errorf("err = %v, want empty text error", err)
	}
}

func TestDoubaoResourceID(t *testing.T) {
	cases := map[string]string{
		"":                      doubaoResourceSeedASR2,
		"doubao-seed-asr-2.0":   doubaoResourceSeedASR2,
		"seedasr":               doubaoResourceSeedASR2,
		"doubao-seed-asr-1.0":   doubaoResourceSeedASR1,
		"bigasr":                doubaoResourceSeedASR1,
		"volc.bigasr.auc_turbo": "volc.bigasr.auc_turbo",
		"unknown-model":         doubaoResourceSeedASR2,
	}
	for in, want := range cases {
		if got := doubaoResourceID(in); got != want {
			t.Errorf("doubaoResourceID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoubaoFormat(t *testing.T) {
	cases := map[string]string{
		"wav":  "wav",
		"mp3":  "mp3",
		"m4a":  "m4a",
		"aac":  "aac",
		"webm": "ogg",
		"raw":  "pcm",
		"pcm":  "pcm",
		"xyz":  "wav",
	}
	for in, want := range cases {
		if got := doubaoFormat(in); got != want {
			t.Errorf("doubaoFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoubaoRequestID(t *testing.T) {
	a := doubaoRequestID()
	b := doubaoRequestID()
	if a == b {
		t.Errorf("request ids should be unique: %q", a)
	}
	if len(a) != 36 || strings.Count(a, "-") != 4 {
		t.Errorf("request id %q is not a UUID", a)
	}
	if a[14] != '4' {
		t.Errorf("request id %q is not version 4", a)
	}
}
