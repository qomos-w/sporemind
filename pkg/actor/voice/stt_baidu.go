package voice

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"net/http"
	"net/url"
	"strings"
)

const baiduTokenURL = "https://aip.baidubce.com/oauth/2.0/token"
const baiduASRURL = "https://vop.baidu.com/server_api"

func recognizeBaidu(audioData []byte, format string, cfg gen.VoiceAccount, _ []string) (string, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("baidu: api_key not configured")
	}

	// Baidu requires api_key to be in "appKey:secretKey" format.
	parts := splitAPIKey(apiKey)
	if len(parts) != 2 {
		return "", fmt.Errorf("baidu: api_key must be 'appKey:secretKey'")
	}
	appKey, secretKey := parts[0], parts[1]

	// Get access token.
	token, err := getBaiduToken(appKey, secretKey, cfg.Proxy)
	if err != nil {
		return "", fmt.Errorf("baidu: get token: %w", err)
	}

	// Build ASR request.
	param := map[string]interface{}{
		"format":  baiduFormat(format),
		"rate":    16000,
		"channel": 1,
		"cuid":    "sporemind",
		"token":   token,
		"speech":  base64.StdEncoding.EncodeToString(audioData),
		"len":     len(audioData),
	}
	if cfg.Language != "" {
		param["dev_pid"] = baiduDevPid(cfg.Language)
	}

	paramJSON, _ := json.Marshal(param)
	req, err := http.NewRequest(http.MethodPost, baiduASRURL, bytes.NewReader(paramJSON))
	if err != nil {
		return "", fmt.Errorf("baidu: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client, err := sttHTTPClient(cfg.Proxy)
	if err != nil {
		return "", fmt.Errorf("baidu: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("baidu: http: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		ErrNo  int      `json:"err_no"`
		ErrMsg string   `json:"err_msg"`
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("baidu: decode: %w", err)
	}
	if result.ErrNo != 0 {
		return "", fmt.Errorf("baidu: %d %s", result.ErrNo, result.ErrMsg)
	}
	if len(result.Result) == 0 {
		return "", fmt.Errorf("baidu: no result")
	}
	text := strings.TrimSpace(result.Result[0])
	if text == "" || text == "#" {
		return "", fmt.Errorf("baidu: no result")
	}
	return result.Result[0], nil
}

func getBaiduToken(appKey, secretKey, proxy string) (string, error) {
	u, _ := url.Parse(baiduTokenURL)
	q := u.Query()
	q.Set("grant_type", "client_credentials")
	q.Set("client_id", appKey)
	q.Set("client_secret", secretKey)
	u.RawQuery = q.Encode()

	client, err := sttHTTPClient(proxy)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.Error != "" {
		return "", fmt.Errorf("%s: %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}

func splitAPIKey(key string) []string {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

func baiduFormat(format string) string {
	switch format {
	case "wav":
		return "wav"
	case "mp3":
		return "mp3"
	case "aac", "m4a":
		return "m4a"
	case "amr":
		return "amr"
	default:
		return "pcm"
	}
}

func baiduDevPid(lang string) int {
	switch lang {
	case "en-US", "en":
		return 1737 // English
	case "zh-CN", "zh":
		return 1537 // Mandarin
	case "yue":
		return 1637 // Cantonese
	case "sichuan":
		return 1837 // Sichuan dialect
	default:
		return 1537
	}
}
