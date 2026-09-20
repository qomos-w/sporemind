package voice

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// startDoubaoMock launches an httptest.Server that mimics the Seed-TTS
// unidirectional endpoint. It captures the request headers/body and replies
// with the given raw response body.
func startDoubaoMock(t *testing.T, gotBody *doubaoTTSPayload, gotResourceID, gotAPIKey, gotReqID *string, respBody string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != doubaoTTSUnidirectionalPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		*gotResourceID = r.Header.Get("X-Api-Resource-Id")
		*gotAPIKey = r.Header.Get("X-Api-Key")
		*gotReqID = r.Header.Get("X-Api-Request-Id")
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q, want application/json", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
}

// doubaoStream builds the concatenated-JSON frame stream the real API returns.
func doubaoStream(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(fmt.Sprintf(`{"code":0,"data":%q}`, base64.StdEncoding.EncodeToString([]byte(c))))
	}
	b.WriteString(fmt.Sprintf(`{"code":%d,"message":"ok"}`, doubaoStreamEndCode))
	return b.String()
}

func TestSynthesizeDoubao_DefaultsAndStreaming(t *testing.T) {
	var gotBody doubaoTTSPayload
	var resourceID, apiKey, reqID string
	srv := startDoubaoMock(t, &gotBody, &resourceID, &apiKey, &reqID, doubaoStream("hello-", "world"), 0)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "test-key", BaseURL: srv.URL}
	audio, format, err := synthesizeDoubao(gen.VoiceSynthesizeReq{Input: "你好"}, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "hello-world" {
		t.Errorf("audio = %q, want %q", string(audio), "hello-world")
	}
	if format != doubaoDefaultFormat {
		t.Errorf("format = %q, want %q", format, doubaoDefaultFormat)
	}

	if apiKey != "test-key" {
		t.Errorf("X-Api-Key = %q", apiKey)
	}
	if resourceID != doubaoTTSDefaultModel {
		t.Errorf("X-Api-Resource-Id = %q, want default %q", resourceID, doubaoTTSDefaultModel)
	}
	if reqID == "" {
		t.Error("X-Api-Request-Id is empty")
	}
	if gotBody.User.UID == "" {
		t.Error("user.uid is empty")
	}
	if gotBody.ReqParams.Text != "你好" {
		t.Errorf("req_params.text = %q", gotBody.ReqParams.Text)
	}
	if gotBody.ReqParams.Speaker != doubaoDefaultVoice {
		t.Errorf("req_params.speaker = %q, want %q", gotBody.ReqParams.Speaker, doubaoDefaultVoice)
	}
	if gotBody.ReqParams.AudioParams.Format != doubaoDefaultFormat {
		t.Errorf("audio_params.format = %q, want %q", gotBody.ReqParams.AudioParams.Format, doubaoDefaultFormat)
	}
	if gotBody.ReqParams.AudioParams.SampleRate != doubaoSampleRate {
		t.Errorf("audio_params.sample_rate = %d, want %d", gotBody.ReqParams.AudioParams.SampleRate, doubaoSampleRate)
	}
}

func TestSynthesizeDoubao_ModelVoiceFormatOverrides(t *testing.T) {
	var gotBody doubaoTTSPayload
	var resourceID, _, _ string
	var apiKey, reqID string
	srv := startDoubaoMock(t, &gotBody, &resourceID, &apiKey, &reqID, doubaoStream("abc"), 0)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL}
	req := gen.VoiceSynthesizeReq{
		Input:  "hi",
		Model:  "seed-tts-2.0",
		Voice:  "zh_female_xiaohe_uranus_bigtts",
		Format: "wav",
	}
	audio, format, err := synthesizeDoubao(req, acc)
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if string(audio) != "abc" || format != "wav" {
		t.Errorf("audio=%q format=%q", audio, format)
	}
	if resourceID != "seed-tts-2.0" {
		t.Errorf("X-Api-Resource-Id = %q, want seed-tts-2.0", resourceID)
	}
	if gotBody.ReqParams.Speaker != "zh_female_xiaohe_uranus_bigtts" {
		t.Errorf("speaker = %q", gotBody.ReqParams.Speaker)
	}
	if gotBody.ReqParams.AudioParams.Format != "wav" {
		t.Errorf("format = %q, want wav", gotBody.ReqParams.AudioParams.Format)
	}
}

func TestSynthesizeDoubao_DefaultBaseURL(t *testing.T) {
	if got := doubaoTTSBaseURL(gen.VoiceAccount{}); got != doubaoTTSDefaultBase {
		t.Errorf("doubaoTTSBaseURL = %q, want %q", got, doubaoTTSDefaultBase)
	}
	if got := doubaoTTSBaseURL(gen.VoiceAccount{BaseURL: "https://voice.ap-southeast-1.bytepluses.com/"}); got != "https://voice.ap-southeast-1.bytepluses.com" {
		t.Errorf("doubaoTTSBaseURL override = %q", got)
	}
}

func TestSynthesizeDoubao_MissingAPIKey(t *testing.T) {
	_, _, err := synthesizeDoubao(gen.VoiceSynthesizeReq{Input: "x"}, gen.VoiceAccount{})
	if err == nil || !strings.Contains(err.Error(), "api_key not configured") {
		t.Errorf("err = %v, want api_key not configured", err)
	}
}

func TestSynthesizeDoubao_StreamErrorCode(t *testing.T) {
	var gotBody doubaoTTSPayload
	var resourceID, apiKey, reqID string
	srv := startDoubaoMock(t, &gotBody, &resourceID, &apiKey, &reqID, `{"code":55000000,"message":"resource id is mismatched"}`, 0)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL}
	_, _, err := synthesizeDoubao(gen.VoiceSynthesizeReq{Input: "x"}, acc)
	if err == nil || !strings.Contains(err.Error(), "55000000") {
		t.Errorf("err = %v, want stream error 55000000", err)
	}
}

func TestSynthesizeDoubao_NoAudio(t *testing.T) {
	var gotBody doubaoTTSPayload
	var resourceID, apiKey, reqID string
	srv := startDoubaoMock(t, &gotBody, &resourceID, &apiKey, &reqID, `{"code":0,"message":"","data":""}`, 0)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL}
	_, _, err := synthesizeDoubao(gen.VoiceSynthesizeReq{Input: "x"}, acc)
	if err == nil || !strings.Contains(err.Error(), "no audio") {
		t.Errorf("err = %v, want no audio", err)
	}
}

func TestSynthesizeDoubao_HTTPError(t *testing.T) {
	var gotBody doubaoTTSPayload
	var resourceID, apiKey, reqID string
	srv := startDoubaoMock(t, &gotBody, &resourceID, &apiKey, &reqID, `unauthorized`, http.StatusUnauthorized)
	defer srv.Close()

	acc := gen.VoiceAccount{APIKey: "k", BaseURL: srv.URL}
	_, _, err := synthesizeDoubao(gen.VoiceSynthesizeReq{Input: "x"}, acc)
	if err == nil || !strings.Contains(err.Error(), "http 401") {
		t.Errorf("err = %v, want http 401", err)
	}
}
