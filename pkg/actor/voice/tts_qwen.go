package voice

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// qwenTTSDefaultBase is the public DashScope (Model Studio) host. The
	// Qwen-Audio-TTS / CosyVoice routes are workspace-scoped
	// ("https://{WorkspaceId}.cn-beijing.maas.aliyuncs.com"), so an account must
	// set BaseUrl to its workspace host to reach them; the Qwen-TTS
	// (qwen3-tts-*) route works against this default.
	qwenTTSDefaultBase = "https://dashscope.aliyuncs.com"

	// The non-realtime synthesis routes differ by model series and are not
	// interchangeable: Qwen-Audio-TTS (and CosyVoice) are served by the
	// SpeechSynthesizer route, Qwen-TTS (qwen3-tts-*) by multimodal-generation.
	qwenTTSSpeechPath = "/api/v1/services/audio/tts/SpeechSynthesizer"
	qwenTTSMultiPath  = "/api/v1/services/aigc/multimodal-generation/generation"

	qwenTTSModel      = "qwen-audio-3.0-tts-flash"
	qwenTTSVoice      = "longanfengyue"
	qwenTTSFormat     = "wav"
	qwenTTSSampleRate = 24000
)

// qwenTTSInput is the request "input" object shared by both Qwen TTS routes.
// The SpeechSynthesizer route uses format/sample_rate; the multimodal-generation
// route uses language_type/instructions. Unused fields are omitted.
type qwenTTSInput struct {
	Text         string `json:"text"`
	Voice        string `json:"voice"`
	Format       string `json:"format,omitempty"`
	SampleRate   int    `json:"sample_rate,omitempty"`
	LanguageType string `json:"language_type,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

// qwenTTSPayload mirrors the DashScope TTS request body.
type qwenTTSPayload struct {
	Model string       `json:"model"`
	Input qwenTTSInput `json:"input"`
}

// qwenTTSResp captures only the fields needed to extract the synthesized audio.
// Non-streaming returns a URL (valid 24h); streaming returns base64 data. The
// top-level audio_url variant is tolerated for the SpeechSynthesizer route.
type qwenTTSResp struct {
	StatusCode int    `json:"status_code"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id"`
	Output     struct {
		Audio struct {
			Data string `json:"data"`
			URL  string `json:"url"`
		} `json:"audio"`
		AudioURL string `json:"audio_url"`
	} `json:"output"`
}

// synthesizeQwen synthesizes speech via Alibaba Cloud Model Studio (DashScope)
// Qwen TTS. Qwen TTS is not compatible with the OpenAI chat-completions audio
// standard, so this issues the native DashScope request directly and returns the
// decoded audio bytes plus the container format.
func synthesizeQwen(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("qwen tts: api_key not configured")
	}

	model := req.Model
	if model == "" {
		model = qwenTTSModel
	}
	voice := req.Voice
	if voice == "" {
		voice = qwenTTSVoice
	}
	format := req.Format
	if format == "" {
		format = qwenTTSFormat
	}

	path := qwenTTSPath(model)
	input := qwenTTSInput{Text: req.Input, Voice: voice}
	if path == qwenTTSSpeechPath {
		// Qwen-Audio-TTS/CosyVoice honour an explicit container + sample rate.
		input.Format = format
		input.SampleRate = qwenTTSSampleRate
	} else {
		// Qwen-TTS takes an optional language hint; the natural-language style
		// instruction is accepted only by the Qwen3-TTS-Instruct-Flash series,
		// so it is forwarded only for instruct models to avoid InvalidParameter
		// on the plain presets. The output container is fixed (wav).
		input.LanguageType = qwenLanguageType(acc.Language)
		if strings.Contains(strings.ToLower(model), "instruct") {
			input.Instructions = req.Instruction
		}
	}

	body, err := json.Marshal(qwenTTSPayload{Model: model, Input: input})
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: marshal body: %w", err)
	}

	endpoint := qwenTTSEndpoint(acc.BaseURL, path)
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+acc.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: http: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("qwen tts: http %d: %s", resp.StatusCode, truncate(string(raw)))
	}

	var parsed qwenTTSResp
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, "", fmt.Errorf("qwen tts: decode body: %w", err)
	}
	if parsed.StatusCode >= 400 {
		return nil, "", fmt.Errorf("qwen tts: api %d: %s", parsed.StatusCode, qwenErrorText(parsed, raw))
	}

	// Streaming deployments return the audio inline as base64; decode directly.
	if data := parsed.Output.Audio.Data; data != "" {
		audio, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, "", fmt.Errorf("qwen tts: decode base64 audio: %w", err)
		}
		return audio, format, nil
	}

	audioURL := parsed.Output.Audio.URL
	if audioURL == "" {
		audioURL = parsed.Output.AudioURL
	}
	if audioURL == "" {
		return nil, "", fmt.Errorf("qwen tts: response missing audio: %s", qwenErrorText(parsed, raw))
	}

	audio, err := fetchAudioURL(audioURL, acc.Proxy)
	if err != nil {
		return nil, "", fmt.Errorf("qwen tts: %w", err)
	}
	outFormat := qwenAudioFormatFromURL(audioURL)
	if outFormat == "" {
		outFormat = format
	}
	return audio, outFormat, nil
}

// qwenTTSPath selects the non-realtime synthesis route for the given model.
func qwenTTSPath(model string) string {
	m := strings.ToLower(model)
	if strings.Contains(m, "cosyvoice") || strings.Contains(m, "audio") {
		return qwenTTSSpeechPath
	}
	return qwenTTSMultiPath
}

// qwenTTSEndpoint joins the account base host (or the DashScope default) with
// the route path. A base that already carries the "/api/v1" suffix (the form
// used by the DashScope SDK's base_http_api_url) is normalized so the version
// segment is not duplicated.
func qwenTTSEndpoint(base, path string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if b == "" {
		b = qwenTTSDefaultBase
	}
	if strings.HasSuffix(b, "/api/v1") {
		b = strings.TrimSuffix(b, "/api/v1")
	}
	return b + path
}

// qwenLanguageType maps a BCP-47 locale to the Qwen-TTS language_type enum.
// Empty/unknown values yield "" (the service default, Auto).
func qwenLanguageType(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if l == "" {
		return ""
	}
	primary := l
	if i := strings.IndexAny(primary, "-_"); i >= 0 {
		primary = primary[:i]
	}
	switch primary {
	case "zh":
		return "Chinese"
	case "en":
		return "English"
	case "de":
		return "German"
	case "it":
		return "Italian"
	case "pt":
		return "Portuguese"
	case "es":
		return "Spanish"
	case "ja":
		return "Japanese"
	case "ko":
		return "Korean"
	case "fr":
		return "French"
	case "ru":
		return "Russian"
	default:
		return ""
	}
}

// qwenAudioFormatFromURL extracts the container format from the audio file URL
// returned by the non-streaming response.
func qwenAudioFormatFromURL(raw string) string {
	u := raw
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u = u[:i]
	}
	dot := strings.LastIndexByte(u, '.')
	if dot < 0 {
		return ""
	}
	switch ext := strings.ToLower(u[dot+1:]); ext {
	case "wav", "mp3", "pcm", "ogg", "m4a", "aac", "opus":
		return ext
	default:
		return ""
	}
}

// qwenErrorText renders the most useful diagnostic from a DashScope response.
func qwenErrorText(parsed qwenTTSResp, raw []byte) string {
	msg := strings.TrimSpace(parsed.Message)
	if msg == "" {
		msg = strings.TrimSpace(parsed.Code)
	}
	if msg == "" {
		msg = truncate(string(raw))
	}
	return msg
}

// fetchAudioURL downloads the synthesized audio file from the pre-signed URL
// returned in non-streaming mode.
func fetchAudioURL(url, proxy string) ([]byte, error) {
	client, err := sttHTTPClient(proxy)
	if err != nil {
		return nil, err
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("download audio: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download audio: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download audio: http %d: %s", resp.StatusCode, truncate(string(data)))
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("download audio: empty body")
	}
	return data, nil
}
