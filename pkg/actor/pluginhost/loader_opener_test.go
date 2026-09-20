package pluginhost

import (
	"encoding/json"
	"testing"
)

// TestSanitizeInProcessOnLoadConfig pins the in-process transport's config
// hygiene: the subprocess-only bootstrap fields (httpAddr, sessionSecret)
// must never reach an in-process plugin — the SDK would bind an HTTP
// listener inside the host process — while the transport-neutral grants
// (staticDir, dataDir) pass through. Empty config stays empty; malformed
// config is dropped rather than half-delivered.
func TestSanitizeInProcessOnLoadConfig(t *testing.T) {
	in := []byte(`{"httpAddr":"127.0.0.1:0","sessionSecret":"deadbeef","staticDir":"D/app","dataDir":"D/app/.sporecode/appdata"}`)
	out := sanitizeInProcessOnLoadConfig(in)
	if len(out) == 0 {
		t.Fatal("sanitized config must not be empty")
	}
	var got struct {
		HTTPAddr      string `json:"httpAddr"`
		SessionSecret string `json:"sessionSecret"`
		StaticDir     string `json:"staticDir"`
		DataDir       string `json:"dataDir"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("sanitized config must be valid JSON: %v", err)
	}
	if got.HTTPAddr != "" || got.SessionSecret != "" {
		t.Fatalf("subprocess-only fields leaked into in-process config: %+v", got)
	}
	if got.StaticDir != "D/app" || got.DataDir != "D/app/.sporecode/appdata" {
		t.Fatalf("transport-neutral grants must pass through: %+v", got)
	}

	if got := sanitizeInProcessOnLoadConfig(nil); got != nil {
		t.Fatalf("nil config must stay nil, got %q", got)
	}
	if got := sanitizeInProcessOnLoadConfig([]byte("  ")); got == nil {
		t.Fatal("empty config must pass through as-is")
	}
	if got := sanitizeInProcessOnLoadConfig([]byte("{not json")); got != nil {
		t.Fatalf("malformed config must be dropped, got %q", got)
	}
}
