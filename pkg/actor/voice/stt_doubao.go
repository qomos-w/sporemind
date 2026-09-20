package voice

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/version"
)

// Doubao (Volcengine Seed ASR) speech-to-text backend.
//
// The official non-streaming HTTP interface is the "Audio File Standard Mode"
// two-step async flow: POST the audio to submit, then POST an empty body to
// query with the same X-Api-Request-Id until the status header reports
// completion. Both live under the openspeech (mainland) / voice (overseas)
// hosts; the account BaseURL overrides the host for either region.
//
// Contract (Volcengine / BytePlus "ASR - Audio File Standard Mode"):
//
//	POST {base}/api/v3/auc/bigmodel/submit
//	POST {base}/api/v3/auc/bigmodel/query
//	Headers: X-Api-Request-Id (UUID task id, echoed on query),
//	         X-Api-Resource-Id (model selector: volc.seedasr.auc = Seed ASR 2.0,
//	                             volc.bigasr.auc = Seed ASR 1.0),
//	         X-Api-Sequence: -1,
//	         X-Api-Key (new console) OR X-Api-App-Key + X-Api-Access-Key (legacy).
//	Response header X-Api-Status-Code: 20000000 done, 20000001 processing,
//	                                   20000002 queued.
const (
	doubaoDefaultBase = "https://openspeech.bytedance.com"
	doubaoSubmitPath  = "/api/v3/auc/bigmodel/submit"
	doubaoQueryPath   = "/api/v3/auc/bigmodel/query"

	// doubaoDefaultModel is the Seed ASR 2.0 model reported to the user; the
	// wire request selects it via the resource id, not a model field.
	doubaoDefaultModel = "doubao-seed-asr-2.0"

	// Resource ids returned in X-Api-Resource-Id.
	doubaoResourceSeedASR2 = "volc.seedasr.auc" // Seed ASR 2.0
	doubaoResourceSeedASR1 = "volc.bigasr.auc"  // Seed ASR 1.0

	// doubaoModelName is the fixed request.model_name value for this API.
	doubaoModelName = "bigmodel"

	// doubaoStatusSuccess is the X-Api-Status-Code meaning the result is ready.
	doubaoStatusSuccess = "20000000"
)

// doubaoPollInterval is the delay between query polls. It is a var so tests can
// shorten it; production waits 2s per poll for a total budget bounded by
// doubaoMaxPolls.
var doubaoPollInterval = 2 * time.Second

// doubaoMaxPolls bounds the polling loop so a stalled task cannot block the
// recognize handler indefinitely.
const doubaoMaxPolls = 45

// doubaoBaseURL returns the API host, falling back to the official mainland
// endpoint when the account has no custom BaseURL configured.
func doubaoBaseURL(cfg gen.VoiceAccount) string {
	if b := strings.TrimRight(cfg.BaseURL, "/"); b != "" {
		return b
	}
	return doubaoDefaultBase
}

// doubaoResourceID maps the account model name to the X-Api-Resource-Id model
// selector. An explicit "volc.*" value passes through unchanged, letting power
// users pick a specific resource directly.
func doubaoResourceID(model string) string {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "", doubaoDefaultModel, "doubao-seed-asr", "seed-asr-2.0", "seedasr", "seedasr.auc", "auc":
		return doubaoResourceSeedASR2
	case "doubao-seed-asr-1.0", "seed-asr-1.0", "bigasr", "bigasr.auc":
		return doubaoResourceSeedASR1
	}
	if strings.HasPrefix(model, "volc.") {
		return model
	}
	return doubaoResourceSeedASR2
}

// doubaoFormat maps the prepared audio format to the container string accepted
// by the API (raw / wav / mp3 / ogg / pcm / spx / amr / aac / m4a). webm/opus is
// sent as ogg, its container parent; unknown formats fall back to wav.
func doubaoFormat(format string) string {
	switch format {
	case "wav", "mp3", "ogg", "pcm", "spx", "amr", "m4a", "aac":
		return format
	case "raw":
		return "pcm"
	case "webm":
		return "ogg"
	default:
		return "wav"
	}
}

// doubaoRequestID returns a random RFC-4122 v4 UUID used as X-Api-Request-Id,
// which doubles as the async task id echoed back on query.
func doubaoRequestID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	h := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

// doubaoAudio is the audio object of the submit body. Exactly one of Data
// (base64) or URL identifies the payload.
type doubaoAudio struct {
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	Format   string `json:"format"`
	Language string `json:"language,omitempty"`
	Rate     int    `json:"rate,omitempty"`
	Bits     int    `json:"bits,omitempty"`
	Channel  int    `json:"channel,omitempty"`
}

// doubaoSubmitUser is the caller identity object {"uid": "..."}.
type doubaoSubmitUser struct {
	UID string `json:"uid"`
}

// doubaoSubmitRequest carries the fixed model name and optional corpus context
// (hotword hint) for the recognition task.
type doubaoSubmitRequest struct {
	ModelName string        `json:"model_name"`
	Corpus    *doubaoCorpus `json:"corpus,omitempty"`
}

// doubaoCorpus is the hotword/context hint object.
type doubaoCorpus struct {
	Context string `json:"context"`
}

// doubaoSubmitBody is the POST body of the submit endpoint.
type doubaoSubmitBody struct {
	User    doubaoSubmitUser    `json:"user"`
	Audio   doubaoAudio         `json:"audio"`
	Request doubaoSubmitRequest `json:"request"`
}

// doubaoHeader mirrors the {"header":{"code","message"}} envelope that the API
// returns on errors and, on success, alongside the result.
type doubaoHeader struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// doubaoResult is the transcription payload of a completed task.
type doubaoResult struct {
	Text       string            `json:"text"`
	Utterances []json.RawMessage `json:"utterances,omitempty"`
}

// doubaoQueryResp is the query response body. Result is present only once the
// task completes; while processing the body is {}.
type doubaoQueryResp struct {
	Header *doubaoHeader `json:"header,omitempty"`
	Result *doubaoResult `json:"result,omitempty"`
}

// doubaoSubmitResp is the submit response body: an empty {} on success.
type doubaoSubmitResp struct {
	Header *doubaoHeader `json:"header,omitempty"`
}

// recognizeDoubao transcribes audio through the Seed ASR "Audio File Standard
// Mode" async HTTP interface. It submits the audio, then polls the query
// endpoint until the transcription is ready. Hotwords are not wired: the API
// exposes a corpus-context hint whose acceptance is version dependent, so it is
// left unset rather than risk a rejected request.
func recognizeDoubao(audioData []byte, format string, cfg gen.VoiceAccount) (string, error) {
	if cfg.APIKey == "" {
		return "", fmt.Errorf("doubao stt: api_key not configured")
	}

	base := doubaoBaseURL(cfg)
	resource := doubaoResourceID(cfg.Model)
	requestID := doubaoRequestID()

	client, err := sttHTTPClient(cfg.Proxy)
	if err != nil {
		return "", fmt.Errorf("doubao stt: %w", err)
	}

	if err := doubaoSubmit(client, base, resource, requestID, audioData, format, cfg); err != nil {
		return "", err
	}
	return doubaoPollResult(client, base, resource, requestID, cfg.APIKey)
}

// doubaoSubmit posts the audio to the submit endpoint and verifies acceptance.
func doubaoSubmit(client *http.Client, base, resource, requestID string, audioData []byte, format string, cfg gen.VoiceAccount) error {
	payload := doubaoSubmitBody{
		User: doubaoSubmitUser{UID: "sporemind"},
		Audio: doubaoAudio{
			Data:     base64.StdEncoding.EncodeToString(audioData),
			Format:   doubaoFormat(format),
			Language: cfg.Language,
			Rate:     16000,
			Bits:     16,
			Channel:  1,
		},
		Request: doubaoSubmitRequest{ModelName: doubaoModelName},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("doubao stt: marshal submit: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, base+doubaoSubmitPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("doubao stt: create submit: %w", err)
	}
	setDoubaoHeaders(req, cfg.APIKey, resource, requestID)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("doubao stt: submit http: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("doubao stt: submit read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("doubao stt: submit http %d: %s", resp.StatusCode, doubaoErrorMessage(raw))
	}
	if code := resp.Header.Get("X-Api-Status-Code"); doubaoStatusFailed(code) {
		return fmt.Errorf("doubao stt: submit status %s: %s", code, doubaoErrorMessage(raw))
	}
	return nil
}

// doubaoPollResult repeatedly queries the task until it succeeds, fails, or the
// poll budget is exhausted.
func doubaoPollResult(client *http.Client, base, resource, requestID, apiKey string) (string, error) {
	for i := 0; i < doubaoMaxPolls; i++ {
		time.Sleep(doubaoPollInterval)

		req, err := http.NewRequest(http.MethodPost, base+doubaoQueryPath, bytes.NewReader([]byte("{}")))
		if err != nil {
			return "", fmt.Errorf("doubao stt: create query: %w", err)
		}
		setDoubaoHeaders(req, apiKey, resource, requestID)

		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("doubao stt: query http: %w", err)
		}
		raw, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return "", fmt.Errorf("doubao stt: query read: %w", readErr)
		}
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("doubao stt: query http %d: %s", resp.StatusCode, doubaoErrorMessage(raw))
		}

		status := resp.Header.Get("X-Api-Status-Code")
		var parsed doubaoQueryResp
		_ = json.Unmarshal(raw, &parsed)

		switch {
		case status == doubaoStatusSuccess || (status == "" && parsed.Result != nil):
			text := ""
			if parsed.Result != nil {
				text = strings.TrimSpace(parsed.Result.Text)
			}
			if text == "" {
				return "", fmt.Errorf("doubao stt: task completed with empty text")
			}
			return text, nil
		case status == "20000001" || status == "20000002" || status == "":
			// Processing or queued (or no status reported): keep polling.
			continue
		default:
			return "", fmt.Errorf("doubao stt: %s", doubaoStatusError(status, raw))
		}
	}
	return "", fmt.Errorf("doubao stt: timed out waiting for transcription after %d polls", doubaoMaxPolls)
}

// setDoubaoHeaders applies the shared auth and task headers. The api key is
// either a single X-Api-Key (new console) or a legacy "appKey:accessKey" pair.
func setDoubaoHeaders(req *http.Request, apiKey, resource, requestID string) {
	if apiKey != "" {
		if parts := splitAPIKey(apiKey); len(parts) == 2 {
			req.Header.Set("X-Api-App-Key", parts[0])
			req.Header.Set("X-Api-Access-Key", parts[1])
		} else {
			req.Header.Set("X-Api-Key", apiKey)
		}
	}
	req.Header.Set("X-Api-Resource-Id", resource)
	req.Header.Set("X-Api-Request-Id", requestID)
	req.Header.Set("X-Api-Sequence", "-1")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "SporeMind/"+version.Version)
}

// doubaoStatusFailed reports whether an X-Api-Status-Code is a terminal error
// (anything other than a success or in-progress code).
func doubaoStatusFailed(code string) bool {
	switch code {
	case "", doubaoStatusSuccess, "20000001", "20000002":
		return false
	default:
		return true
	}
}

// doubaoStatusError renders an error for a failing status code, preferring the
// message carried in the response body when present.
func doubaoStatusError(code string, body []byte) string {
	if msg := doubaoBodyMessage(body); msg != "" {
		return fmt.Sprintf("status %s (%s): %s", code, doubaoStatusText(code), msg)
	}
	return fmt.Sprintf("status %s: %s", code, doubaoStatusText(code))
}

// doubaoStatusText maps known status/error codes to a short description.
func doubaoStatusText(code string) string {
	switch code {
	case "20000003":
		return "audio is mute"
	case "45000001":
		return "invalid request parameters"
	case "45000002":
		return "empty audio"
	case "45000151":
		return "incorrect audio format"
	case "55000031":
		return "service busy"
	}
	if strings.HasPrefix(code, "550") {
		return "service internal error"
	}
	return "unexpected status"
}

// doubaoErrorMessage extracts a readable error from a submit/query body,
// truncating so an oversized upstream body never floods logs.
func doubaoErrorMessage(body []byte) string {
	if msg := doubaoBodyMessage(body); msg != "" {
		var parsed doubaoSubmitResp
		if json.Unmarshal(body, &parsed) == nil && parsed.Header != nil && parsed.Header.Code != 0 {
			return fmt.Sprintf("%d %s", parsed.Header.Code, msg)
		}
		return msg
	}
	return truncate(string(body))
}

// doubaoBodyMessage returns the {"header":{"message":...}} text, if any.
func doubaoBodyMessage(body []byte) string {
	var parsed struct {
		Header *doubaoHeader `json:"header"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Header != nil {
		return strings.TrimSpace(parsed.Header.Message)
	}
	return ""
}
