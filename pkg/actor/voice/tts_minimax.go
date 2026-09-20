package voice

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

const (
	minimaxTTSModel = "speech-02-hd"
	minimaxTTSVoice = "male-qn-qingse"
)

type minimaxVoiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed"`
	Vol     float64 `json:"vol"`
	Pitch   float64 `json:"pitch"`
}

type minimaxAudioSetting struct {
	SampleRate int    `json:"sample_rate"`
	Bitrate    int    `json:"bitrate"`
	Format     string `json:"format"`
	Channel    int    `json:"channel"`
}

type minimaxT2ARequest struct {
	Model        string              `json:"model"`
	Text         string              `json:"text"`
	Stream       bool                `json:"stream"`
	VoiceSetting minimaxVoiceSetting `json:"voice_setting"`
	AudioSetting minimaxAudioSetting `json:"audio_setting"`
	OutputFormat string              `json:"output_format"` // "hex" (non-streaming only)
}

type minimaxT2AResponse struct {
	Data struct {
		Audio string `json:"audio"`
	} `json:"data"`
	BaseResp *minimaxBaseResp `json:"base_resp"`
}

// synthesizeMinimax synthesizes speech via the MiniMax /v1/t2a_v2 endpoint
// (non-streaming; data.audio is hex-encoded). Returns the decoded bytes and
// the container format.
func synthesizeMinimax(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("minimax tts: api_key not configured")
	}
	model := req.Model
	if model == "" {
		model = minimaxTTSModel
	}
	voice := req.Voice
	if voice == "" {
		voice = minimaxTTSVoice
	}
	format := req.Format
	if format != "wav" {
		format = "mp3"
	}

	payload, err := json.Marshal(minimaxT2ARequest{
		Model:        model,
		Text:         req.Input,
		Stream:       false,
		VoiceSetting: minimaxVoiceSetting{VoiceID: voice, Speed: 1, Vol: 1, Pitch: 0},
		AudioSetting: minimaxAudioSetting{SampleRate: 32000, Bitrate: 128000, Format: format, Channel: 1},
		OutputFormat: "hex",
	})
	if err != nil {
		return nil, "", fmt.Errorf("minimax tts: marshal body: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, minimaxBaseURL(acc)+"/t2a_v2", bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("minimax tts: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "SporeMind/"+version.Version)

	respBody, err := minimaxDo("minimax tts", httpReq, acc.Proxy)
	if err != nil {
		return nil, "", err
	}
	var result minimaxT2AResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, "", fmt.Errorf("minimax tts: decode: %w", err)
	}
	if err := minimaxCheckBase("minimax tts", result.BaseResp); err != nil {
		return nil, "", err
	}
	if result.Data.Audio == "" {
		return nil, "", fmt.Errorf("minimax tts: no audio in response")
	}
	audio, err := hex.DecodeString(result.Data.Audio)
	if err != nil {
		return nil, "", fmt.Errorf("minimax tts: decode hex audio: %w", err)
	}
	return audio, format, nil
}
