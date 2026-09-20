package voice

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestRecognizeMinimax(t *testing.T) {
	var gotAuth, gotContentType, gotModel string
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		// r.FormValue parses the multipart body; do it before any raw body read.
		gotModel = r.FormValue("model")
		if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) == 0 {
			t.Errorf("multipart body missing file part")
		} else {
			gotBody = "name=\"file\""
		}
		_, _ = w.Write([]byte(`{"text":"hello minimax","trace_id":"t1"}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "k1", BaseURL: srv.URL + "/v1", Model: "asr-1.0"}
	text, err := recognizeMinimax([]byte("fake-mp3"), "mp3", acc)
	if err != nil {
		t.Fatalf("recognizeMinimax: %v", err)
	}
	if text != "hello minimax" {
		t.Fatalf("text = %q", text)
	}
	if gotAuth != "Bearer k1" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotModel != "asr-1.0" {
		t.Fatalf("model = %q", gotModel)
	}
	if !strings.Contains(gotBody, "name=\"file\"") {
		t.Fatalf("multipart body missing file part: %q", gotBody)
	}
}

func TestRecognizeMinimaxDefaultModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m := r.FormValue("model"); m != minimaxSTTModel {
			t.Errorf("model = %q, want %q", m, minimaxSTTModel)
		}
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "k1", BaseURL: srv.URL}
	if _, err := recognizeMinimax([]byte("x"), "mp3", acc); err != nil {
		t.Fatalf("recognizeMinimax: %v", err)
	}
}

func TestRecognizeMinimaxBaseRespError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"base_resp":{"status_code":1004,"status_msg":"invalid api key"}}`))
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "bad", BaseURL: srv.URL}
	_, err := recognizeMinimax([]byte("x"), "mp3", acc)
	if err == nil || !strings.Contains(err.Error(), "1004") {
		t.Fatalf("err = %v, want base_resp 1004", err)
	}
}

func TestRecognizeMinimaxMissingKey(t *testing.T) {
	if _, err := recognizeMinimax([]byte("x"), "mp3", gen.VoiceAccount{Provider: "minimax"}); err == nil {
		t.Fatal("want error for missing api key")
	}
}

func TestSynthesizeMinimax(t *testing.T) {
	audio := []byte{0x01, 0x02, 0xff, 0x00}
	var gotBody minimaxT2ARequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/t2a_v2" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":      map[string]any{"audio": hex.EncodeToString(audio)},
			"base_resp": map[string]any{"status_code": 0, "status_msg": ""},
		})
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "k1", BaseURL: srv.URL + "/v1", Model: "speech-02-hd", Voice: "female-shaonv"}
	got, format, err := synthesizeMinimax(gen.VoiceSynthesizeReq{Input: "你好", Model: "speech-02-hd", Voice: "female-shaonv", Format: "mp3"}, acc)
	if err != nil {
		t.Fatalf("synthesizeMinimax: %v", err)
	}
	if string(got) != string(audio) {
		t.Fatalf("audio = %v", got)
	}
	if format != "mp3" {
		t.Fatalf("format = %q", format)
	}
	if gotBody.VoiceSetting.VoiceID != "female-shaonv" {
		t.Fatalf("voice_id = %q", gotBody.VoiceSetting.VoiceID)
	}
	if gotBody.Stream {
		t.Fatal("stream must be false for hex output")
	}
	if gotBody.OutputFormat != "hex" {
		t.Fatalf("output_format = %q", gotBody.OutputFormat)
	}
	if gotBody.AudioSetting.Format != "mp3" {
		t.Fatalf("audio format = %q", gotBody.AudioSetting.Format)
	}
}

func TestSynthesizeMinimaxDefaultsAndWav(t *testing.T) {
	var gotBody minimaxT2ARequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"audio": hex.EncodeToString([]byte{9})},
		})
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "k1", BaseURL: srv.URL}
	got, format, err := synthesizeMinimax(gen.VoiceSynthesizeReq{Input: "hi", Format: "wav"}, acc)
	if err != nil {
		t.Fatalf("synthesizeMinimax: %v", err)
	}
	if len(got) != 1 || got[0] != 9 {
		t.Fatalf("audio = %v", got)
	}
	if format != "wav" {
		t.Fatalf("format = %q", format)
	}
	if gotBody.Model != minimaxTTSModel {
		t.Fatalf("model = %q", gotBody.Model)
	}
	if gotBody.VoiceSetting.VoiceID != minimaxTTSVoice {
		t.Fatalf("voice = %q", gotBody.VoiceSetting.VoiceID)
	}
	if gotBody.AudioSetting.Format != "wav" {
		t.Fatalf("audio format = %q", gotBody.AudioSetting.Format)
	}
}

func TestSynthesizeMinimaxError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"base_resp": map[string]any{"status_code": 2049, "status_msg": "insufficient balance"},
		})
	}))
	defer srv.Close()

	acc := gen.VoiceAccount{Provider: "minimax", APIKey: "k1", BaseURL: srv.URL}
	if _, _, err := synthesizeMinimax(gen.VoiceSynthesizeReq{Input: "hi"}, acc); err == nil || !strings.Contains(err.Error(), "2049") {
		t.Fatalf("err = %v, want 2049", err)
	}
}
