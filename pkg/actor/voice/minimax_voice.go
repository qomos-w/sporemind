package voice

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

// MiniMax voice cloning & design: /v1/files/upload + /v1/voice_clone and
// /v1/voice_design, authorized by a "tts"-kind minimax account. The upstream
// clone response does not echo a voice id, so the caller owns it.

type minimaxFileUploadResponse struct {
	File struct {
		FileID int64 `json:"file_id"`
	} `json:"file"`
	BaseResp *minimaxBaseResp `json:"base_resp"`
}

type minimaxClonePrompt struct {
	PromptAudio int64  `json:"prompt_audio,omitempty"`
	PromptText  string `json:"prompt_text,omitempty"`
}

type minimaxVoiceCloneRequest struct {
	FileID                  int64               `json:"file_id"`
	VoiceID                 string              `json:"voice_id"`
	ClonePrompt             *minimaxClonePrompt `json:"clone_prompt,omitempty"`
	Text                    string              `json:"text,omitempty"`
	Model                   string              `json:"model,omitempty"`
	NeedNoiseReduction      bool                `json:"need_noise_reduction"`
	NeedVolumeNormalization bool                `json:"need_volume_normalization"`
}

type minimaxVoiceCloneResponse struct {
	DemoAudio string           `json:"demo_audio"`
	BaseResp  *minimaxBaseResp `json:"base_resp"`
}

type minimaxVoiceDesignRequest struct {
	Prompt      string `json:"prompt"`
	PreviewText string `json:"preview_text"`
	VoiceID     string `json:"voice_id,omitempty"`
}

type minimaxVoiceDesignResponse struct {
	VoiceID    string           `json:"voice_id"`
	TrialAudio string           `json:"trial_audio"`
	BaseResp   *minimaxBaseResp `json:"base_resp"`
}

// minimaxUploadFile uploads one audio file (purpose=voice_clone|prompt_audio)
// and returns its file_id.
func minimaxUploadFile(cfg gen.VoiceAccount, purpose string, data []byte, filename string) (int64, error) {
	if filename == "" {
		filename = purpose + ".mp3"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("purpose", purpose); err != nil {
		return 0, fmt.Errorf("minimax upload: %w", err)
	}
	fw, err := mw.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return 0, fmt.Errorf("minimax upload: %w", err)
	}
	if _, err := fw.Write(data); err != nil {
		return 0, fmt.Errorf("minimax upload: %w", err)
	}
	if err := mw.Close(); err != nil {
		return 0, fmt.Errorf("minimax upload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, minimaxBaseURL(cfg)+"/files/upload", &buf)
	if err != nil {
		return 0, fmt.Errorf("minimax upload: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)

	respBody, err := minimaxDo("minimax upload", req, cfg.Proxy)
	if err != nil {
		return 0, err
	}
	var result minimaxFileUploadResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("minimax upload: decode: %w", err)
	}
	if err := minimaxCheckBase("minimax upload", result.BaseResp); err != nil {
		return 0, err
	}
	if result.File.FileID == 0 {
		return 0, fmt.Errorf("minimax upload: no file_id in response")
	}
	return result.File.FileID, nil
}

// minimaxGenerateVoiceID mints a caller-supplied voice id for voice_clone
// (alnum, so it satisfies every documented id constraint).
func minimaxGenerateVoiceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "voice" + hex.EncodeToString(b[:])
}

// handleVoiceClone uploads the clone sample (plus optional prompt audio) and
// runs the MiniMax voice_clone call on the referenced minimax tts account.
func (a *Actor) handleVoiceClone(_ actor.PureContext, req gen.VoiceCloneReq) (gen.VoiceCloneResp, error) {
	acc, err := a.accountFor("tts", req.AccountID)
	if err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: %w", err)
	}
	if !strings.EqualFold(acc.Provider, "minimax") {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: clone requires a minimax tts account, got %q", acc.Provider)
	}
	if len(req.AudioData) == 0 {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: clone: audio data is empty")
	}

	voiceID := req.VoiceID
	if voiceID == "" {
		voiceID = minimaxGenerateVoiceID()
	}
	fileID, err := minimaxUploadFile(acc, "voice_clone", req.AudioData, req.AudioName)
	if err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: %w", err)
	}

	cloneReq := minimaxVoiceCloneRequest{
		FileID:                  fileID,
		VoiceID:                 voiceID,
		NeedNoiseReduction:      req.NeedNoiseReduction,
		NeedVolumeNormalization: req.NeedVolumeNormalization,
	}
	if len(req.PromptAudioData) > 0 {
		promptID, err := minimaxUploadFile(acc, "prompt_audio", req.PromptAudioData, req.PromptAudioName)
		if err != nil {
			return gen.VoiceCloneResp{}, fmt.Errorf("voice: %w", err)
		}
		cloneReq.ClonePrompt = &minimaxClonePrompt{PromptAudio: promptID, PromptText: req.PromptText}
	}
	if req.PreviewText != "" {
		cloneReq.Text = req.PreviewText
		cloneReq.Model = minimaxTTSModel
	}

	payload, err := json.Marshal(cloneReq)
	if err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: clone: marshal body: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, minimaxBaseURL(acc)+"/voice_clone", bytes.NewReader(payload))
	if err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: clone: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "SporeMind/"+version.Version)

	respBody, err := minimaxDo("minimax clone", httpReq, acc.Proxy)
	if err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: %w", err)
	}
	var result minimaxVoiceCloneResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: clone: decode: %w", err)
	}
	if err := minimaxCheckBase("minimax clone", result.BaseResp); err != nil {
		return gen.VoiceCloneResp{}, fmt.Errorf("voice: %w", err)
	}
	return gen.VoiceCloneResp{VoiceID: voiceID, DemoAudioURL: result.DemoAudio}, nil
}

// handleVoiceDesign designs a voice from a text description via MiniMax
// /v1/voice_design; the trial audio is hex-decoded to raw mp3 bytes.
func (a *Actor) handleVoiceDesign(_ actor.PureContext, req gen.VoiceDesignReq) (gen.VoiceDesignResp, error) {
	acc, err := a.accountFor("tts", req.AccountID)
	if err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: %w", err)
	}
	if !strings.EqualFold(acc.Provider, "minimax") {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design requires a minimax tts account, got %q", acc.Provider)
	}
	if strings.TrimSpace(req.Prompt) == "" || strings.TrimSpace(req.PreviewText) == "" {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: prompt and preview text are required")
	}

	payload, err := json.Marshal(minimaxVoiceDesignRequest{
		Prompt:      req.Prompt,
		PreviewText: req.PreviewText,
		VoiceID:     req.VoiceID,
	})
	if err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: marshal body: %w", err)
	}
	httpReq, err := http.NewRequest(http.MethodPost, minimaxBaseURL(acc)+"/voice_design", bytes.NewReader(payload))
	if err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "SporeMind/"+version.Version)

	respBody, err := minimaxDo("minimax design", httpReq, acc.Proxy)
	if err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: %w", err)
	}
	var result minimaxVoiceDesignResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: decode: %w", err)
	}
	if err := minimaxCheckBase("minimax design", result.BaseResp); err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: %w", err)
	}
	if result.VoiceID == "" {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: no voice id in response")
	}
	trial, err := hex.DecodeString(result.TrialAudio)
	if err != nil {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: decode hex trial audio: %w", err)
	}
	if len(trial) == 0 {
		return gen.VoiceDesignResp{}, fmt.Errorf("voice: design: no trial audio in response")
	}
	return gen.VoiceDesignResp{VoiceID: result.VoiceID, TrialAudio: trial}, nil
}
