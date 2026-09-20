package voice

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// minimaxDefaultBase is the international MiniMax API base; mainland accounts
// can override it via the account BaseURL (e.g. https://api.minimax.cn/v1).
const minimaxDefaultBase = "https://api.minimaxi.com/v1"

// minimaxBaseURL returns the MiniMax API base, falling back to the
// international endpoint when the account has no custom BaseURL configured.
// A custom base without a version segment gets /v1 appended so bare hosts
// (api.minimaxi.com) and versioned bases (.../v1) both work.
func minimaxBaseURL(cfg gen.VoiceAccount) string {
	b := strings.TrimRight(cfg.BaseURL, "/")
	if b == "" {
		return minimaxDefaultBase
	}
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	return b
}

// minimaxBaseResp is the shared status envelope of every MiniMax API response.
type minimaxBaseResp struct {
	StatusCode int    `json:"status_code"`
	StatusMsg  string `json:"status_msg"`
}

// minimaxCheckBase turns a non-zero base_resp into an error; prefix carries
// the call site (e.g. "minimax tts").
func minimaxCheckBase(prefix string, base *minimaxBaseResp) error {
	if base == nil || base.StatusCode == 0 {
		return nil
	}
	return fmt.Errorf("%s: api error %d: %s", prefix, base.StatusCode, base.StatusMsg)
}

// minimaxDo executes one HTTP request and returns the raw response body,
// mapping non-200 statuses to truncated errors.
func minimaxDo(prefix string, req *http.Request, proxy string) ([]byte, error) {
	client, err := sttHTTPClient(proxy)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", prefix, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: http: %w", prefix, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read body: %w", prefix, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d: %s", prefix, resp.StatusCode, truncate(string(body)))
	}
	return body, nil
}
