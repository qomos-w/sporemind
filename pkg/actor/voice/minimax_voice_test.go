package voice

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// newMinimaxLabServer serves the /files/upload, /voice_clone and /voice_design
// endpoints and records what the client sent.
func newMinimaxLabServer(t *testing.T, cloneReq *minimaxVoiceCloneRequest, designReq *minimaxVoiceDesignRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/files/upload":
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			purpose := r.FormValue("purpose")
			fileID := int64(0)
			switch purpose {
			case "voice_clone":
				fileID = 111
				file, _, err := r.FormFile("file")
				if err != nil {
					t.Errorf("form file: %v", err)
				} else {
					data, _ := io.ReadAll(file)
					if len(data) == 0 {
						t.Error("clone upload body is empty")
					}
				}
			case "prompt_audio":
				fileID = 222
			default:
				t.Errorf("unexpected purpose %q", purpose)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"file":       map[string]any{"file_id": fileID, "filename": "a.mp3", "bytes": 10},
				"base_resp":  map[string]any{"status_code": 0, "status_msg": ""},
			})
		case "/v1/voice_clone":
			if err := json.NewDecoder(r.Body).Decode(cloneReq); err != nil {
				t.Errorf("decode clone body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"demo_audio": "https://cdn.example.com/demo.mp3",
				"base_resp":  map[string]any{"status_code": 0, "status_msg": ""},
			})
		case "/v1/voice_design":
			if err := json.NewDecoder(r.Body).Decode(designReq); err != nil {
				t.Errorf("decode design body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"voice_id":    "designed-1",
				"trial_audio": hex.EncodeToString([]byte{1, 2, 3}),
				"base_resp":   map[string]any{"status_code": 0, "status_msg": ""},
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestHandleVoiceClone(t *testing.T) {
	var cloneReq minimaxVoiceCloneRequest
	srv := newMinimaxLabServer(t, &cloneReq, nil)
	defer srv.Close()

	a := newTestActor(t)

	cr, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "mm-tts", Kind: "tts", Provider: "minimax", APIKey: "k1", BaseURL: srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	resp, err := a.handleVoiceClone(nil, gen.VoiceCloneReq{
		AccountID:               cr.Account.ID,
		AudioData:               []byte("clone-bytes"),
		AudioName:               "sample.mp3",
		PreviewText:             "你好世界",
		NeedNoiseReduction:      true,
		NeedVolumeNormalization: true,
	})
	if err != nil {
		t.Fatalf("handleVoiceClone: %v", err)
	}
	if resp.VoiceID == "" || !strings.HasPrefix(resp.VoiceID, "voice") {
		t.Fatalf("voice id = %q", resp.VoiceID)
	}
	if resp.DemoAudioURL != "https://cdn.example.com/demo.mp3" {
		t.Fatalf("demo audio = %q", resp.DemoAudioURL)
	}
	if cloneReq.FileID != 111 {
		t.Fatalf("file_id = %d", cloneReq.FileID)
	}
	if cloneReq.VoiceID != resp.VoiceID {
		t.Fatalf("clone voice_id %q != resp %q", cloneReq.VoiceID, resp.VoiceID)
	}
	if cloneReq.Text != "你好世界" || cloneReq.Model != minimaxTTSModel {
		t.Fatalf("preview text/model = %q/%q", cloneReq.Text, cloneReq.Model)
	}
	if !cloneReq.NeedNoiseReduction || !cloneReq.NeedVolumeNormalization {
		t.Fatal("noise/volume flags lost")
	}
}

func TestHandleVoiceCloneWithPromptAudio(t *testing.T) {
	var cloneReq minimaxVoiceCloneRequest
	srv := newMinimaxLabServer(t, &cloneReq, nil)
	defer srv.Close()

	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "mm-tts", Kind: "tts", Provider: "minimax", APIKey: "k1", BaseURL: srv.URL,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	resp, err := a.handleVoiceClone(nil, gen.VoiceCloneReq{
		AudioData:        []byte("clone"),
		AudioName:        "s.mp3",
		VoiceID:          "my-custom-id",
		PromptAudioData:  []byte("prompt"),
		PromptAudioName:  "p.mp3",
		PromptText:       "示例文本",
	})
	if err != nil {
		t.Fatalf("handleVoiceClone: %v", err)
	}
	if resp.VoiceID != "my-custom-id" {
		t.Fatalf("voice id = %q", resp.VoiceID)
	}
	if cloneReq.ClonePrompt == nil || cloneReq.ClonePrompt.PromptAudio != 222 {
		t.Fatalf("clone_prompt = %+v", cloneReq.ClonePrompt)
	}
	if cloneReq.ClonePrompt.PromptText != "示例文本" {
		t.Fatalf("prompt text = %q", cloneReq.ClonePrompt.PromptText)
	}
}

func TestHandleVoiceCloneWrongProvider(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "o", Kind: "tts", Provider: "openai", APIKey: "k",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	_, err := a.handleVoiceClone(nil, gen.VoiceCloneReq{AudioData: []byte("x"), AudioName: "a.mp3"})
	if err == nil || !strings.Contains(err.Error(), "minimax tts account") {
		t.Fatalf("err = %v", err)
	}
}

func TestHandleVoiceCloneEmptyAudio(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "mm", Kind: "tts", Provider: "minimax", APIKey: "k",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := a.handleVoiceClone(nil, gen.VoiceCloneReq{AudioName: "a.mp3"}); err == nil {
		t.Fatal("want error for empty audio data")
	}
}

func TestHandleVoiceDesign(t *testing.T) {
	var designReq minimaxVoiceDesignRequest
	srv := newMinimaxLabServer(t, nil, &designReq)
	defer srv.Close()

	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "mm-tts", Kind: "tts", Provider: "minimax", APIKey: "k1", BaseURL: srv.URL,
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}

	resp, err := a.handleVoiceDesign(nil, gen.VoiceDesignReq{
		Prompt:      "沉稳的青年男声，语速适中",
		PreviewText: "今天天气不错",
	})
	if err != nil {
		t.Fatalf("handleVoiceDesign: %v", err)
	}
	if resp.VoiceID != "designed-1" {
		t.Fatalf("voice id = %q", resp.VoiceID)
	}
	if string(resp.TrialAudio) != string([]byte{1, 2, 3}) {
		t.Fatalf("trial audio = %v", resp.TrialAudio)
	}
	if designReq.Prompt != "沉稳的青年男声，语速适中" || designReq.PreviewText != "今天天气不错" {
		t.Fatalf("design req = %+v", designReq)
	}
}

func TestHandleVoiceDesignValidation(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleAccountCreate(nil, gen.VoiceAccountCreateReq{
		Name: "mm", Kind: "tts", Provider: "minimax", APIKey: "k",
	}); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if _, err := a.handleVoiceDesign(nil, gen.VoiceDesignReq{Prompt: "x"}); err == nil {
		t.Fatal("want error for missing preview text")
	}
}
