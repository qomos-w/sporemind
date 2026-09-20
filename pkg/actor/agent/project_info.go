package agent

import (
	"encoding/json"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// decodeProjectInfo normalizes a planner.Call result into ProjectInfoResp.
// Accepts []byte (raw JSON), ProjectInfoResp (typed passthrough), or
// map[string]interface{} (generic map) — the three forms project.info may
// return depending on the codec path. Failures yield an empty struct; callers
// must treat empty Roots as "service unavailable" and degrade accordingly.
func decodeProjectInfo(result any) domain.ProjectInfoResp {
	var info domain.ProjectInfoResp
	switch v := result.(type) {
	case []byte:
		_ = json.Unmarshal(v, &info)
	case domain.ProjectInfoResp:
		info = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &info)
	}
	return info
}
