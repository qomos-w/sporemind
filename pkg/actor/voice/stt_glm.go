package voice

import (
	"bytes"
	"fmt"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"net/http"
	"strings"
)

const (
	glmAPIURL       = "https://open.bigmodel.cn/api/paas/v4/audio/transcriptions"
	glmDefaultModel = "glm-asr-2512"
)

func recognizeGLM(audioData []byte, format string, cfg gen.VoiceAccount, hotwords []string) (string, error) {
	apiKey := cfg.APIKey
	if apiKey == "" {
		return "", fmt.Errorf("glm: api_key not configured")
	}

	model := cfg.Model
	if model == "" {
		model = glmDefaultModel
	}

	fields := map[string]string{
		"model":  model,
		"stream": "false",
	}
	if len(hotwords) > 0 {
		fields["hotwords"] = strings.Join(hotwords, ",")
	}

	body, contentType, err := buildMultipartBody(audioData, format, fields)
	if err != nil {
		return "", fmt.Errorf("glm: build body: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, glmAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("glm: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", contentType)

	return doSTTRequest(req, cfg.Proxy)
}
