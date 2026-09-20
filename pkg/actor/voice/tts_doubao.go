package voice

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Doubao (Volcano Engine / BytePlus) Seed-TTS backend.
//
// Doubao voice synthesis is exposed by the Volcano Engine "Seed Speech"
// service, which is a separate product from the Ark OpenAI-compatible model
// API. Requests are addressed to the unidirectional HTTP endpoint
//
//	POST {base}/api/v3/tts/unidirectional
//
// authenticated with a single API key (X-Api-Key) plus the resource id that
// selects the TTS deployment (X-Api-Resource-Id). The response is a chunked
// stream of concatenated JSON frames (no newlines), each carrying one
// base64-encoded audio chunk in "data"; the stream terminates at
// code 20000000.
const (
	// doubaoTTSDefaultBase is the Volcano Engine (mainland China) Seed Speech
	// host. The BytePlus overseas host is
	// https://voice.ap-southeast-1.bytepluses.com — callers in that region
	// point acc.BaseURL at it explicitly.
	doubaoTTSDefaultBase = "https://openspeech.bytedance.com"

	// doubaoTTSUnidirectionalPath is the Seed-TTS unidirectional HTTP path.
	doubaoTTSUnidirectionalPath = "/api/v3/tts/unidirectional"

	// doubaoTTSDefaultModel is the Doubao Seed-TTS model/endpoint identifier used
	// when the account/request omits Model. It is the deployment selector sent
	// as X-Api-Resource-Id; a project with a specific Seed-TTS entitlement sets
	// Model to its resource id (e.g. "seed-tts-2.0" / "seed-tts-1.0").
	doubaoTTSDefaultModel = "doubao-seed-tts"

	// doubaoDefaultVoice is the default speaker (timbre) id.
	doubaoDefaultVoice = "zh_female_qingxin"

	// doubaoDefaultFormat is the default output container.
	doubaoDefaultFormat = "mp3"

	// doubaoSampleRate is the fixed output sample rate.
	doubaoSampleRate = 24000

	// doubaoStreamEndCode is the code that marks the final frame of the
	// unidirectional stream. Code 0 frames carry audio chunks.
	doubaoStreamEndCode = 20000000
)

// doubaoTTSPayload is the unidirectional TTS request body.
type doubaoTTSPayload struct {
	User      doubaoUser      `json:"user"`
	ReqParams doubaoReqParams `json:"req_params"`
}

type doubaoUser struct {
	UID string `json:"uid"`
}

type doubaoReqParams struct {
	Text        string            `json:"text"`
	Speaker     string            `json:"speaker"`
	AudioParams doubaoAudioParams `json:"audio_params"`
}

type doubaoAudioParams struct {
	Format     string `json:"format"`
	SampleRate int    `json:"sample_rate"`
}

// doubaoFrame is one frame of the concatenated-JSON response stream.
type doubaoFrame struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data"`
}

// doubaoTTSBaseURL returns the Seed Speech base, falling back to the Volcano
// Engine mainland host when the account has no custom BaseURL configured.
func doubaoTTSBaseURL(cfg gen.VoiceAccount) string {
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return b
	}
	return doubaoTTSDefaultBase
}

// doubaoTTSRequestID returns a random request id for the X-Api-Request-Id header.
func doubaoTTSRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// synthesizeDoubao synthesizes speech from text via the Volcano Engine Doubao
// Seed-TTS unidirectional HTTP API. Returns the concatenated audio bytes and
// the container format ("mp3" or "wav").
func synthesizeDoubao(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("doubao tts: api_key not configured")
	}

	model := req.Model
	if model == "" {
		model = doubaoTTSDefaultModel
	}
	voice := req.Voice
	if voice == "" {
		voice = doubaoDefaultVoice
	}
	format := req.Format
	if format == "" {
		format = doubaoDefaultFormat
	}

	body, err := json.Marshal(doubaoTTSPayload{
		User: doubaoUser{UID: "sporemind"},
		ReqParams: doubaoReqParams{
			Text:    req.Input,
			Speaker: voice,
			AudioParams: doubaoAudioParams{
				Format:     format,
				SampleRate: doubaoSampleRate,
			},
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("doubao tts: marshal body: %w", err)
	}

	url := doubaoTTSBaseURL(acc) + doubaoTTSUnidirectionalPath
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("doubao tts: create request: %w", err)
	}
	httpReq.Header.Set("X-Api-Key", acc.APIKey)
	httpReq.Header.Set("X-Api-Resource-Id", model)
	httpReq.Header.Set("X-Api-Request-Id", doubaoTTSRequestID())
	httpReq.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("doubao tts: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("doubao tts: http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("doubao tts: http %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	audio, err := decodeDoubaoStream(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return audio, format, nil
}

// decodeDoubaoStream reads the concatenated-JSON frame stream, concatenates the
// base64 audio carried by each "data" frame, and returns the assembled bytes.
// A frame whose code is neither 0 (audio chunk) nor doubaoStreamEndCode (final
// frame) is surfaced as an error.
func decodeDoubaoStream(r io.Reader) ([]byte, error) {
	dec := json.NewDecoder(r)
	var audio []byte
	lastCode, lastMsg := 0, ""
	for {
		var f doubaoFrame
		if err := dec.Decode(&f); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("doubao tts: decode frame: %w", err)
		}
		lastCode, lastMsg = f.Code, f.Message
		if f.Code != 0 && f.Code != doubaoStreamEndCode {
			return nil, fmt.Errorf("doubao tts: stream error %d: %s", f.Code, truncate(f.Message))
		}
		if f.Data == "" {
			continue
		}
		chunk, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return nil, fmt.Errorf("doubao tts: decode base64 chunk: %w", err)
		}
		audio = append(audio, chunk...)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("doubao tts: response contained no audio (code=%d message=%s)", lastCode, truncate(lastMsg))
	}
	return audio, nil
}
