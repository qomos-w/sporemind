package desktop

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"time"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// initialThemeStartTimeout bounds how long the desktop shell waits for the
// actor tree before falling back to the light pre-paint theme.
const initialThemeStartTimeout = 5 * time.Second

// initialThemeHeaders give the read an authenticated system identity (same
// shape as pkg/policy's bridge headers; "system" is absent from the permission
// matrix so it cannot be denied).
var initialThemeHeaders = map[string]string{
	"gospore.caller_role":    "system",
	"gospore.caller_subject": "sporemind-desktop-theme",
}

// InitialTheme resolves the persisted ai-shell theme preference so the main
// window and the HTML shell can pre-paint the correct colours before React
// loads. mode is always "light" or "dark"; themeJSON is the canonical
// persisted payload, parseable by the frontend's parseThemeJSON. Any failure
// degrades to the light defaults — the async loadTheme flow stays authoritative.
func InitialTheme(ctx context.Context, handle *runtime.Handle) (mode string, themeJSON string) {
	mode, themeJSON = "light", `{"mode":"light"}`
	// Fast path: the workspace actor's write-through boot cache, readable
	// before any actor exists — window creation never waits for the tree.
	if pref, ok := readBootThemeCache(); ok {
		return parseThemePreference(pref)
	}
	// Legacy fallback (first boot after upgrade, or a pruned cache): wait
	// for the tree and ask the workspace actor directly.
	if handle == nil || handle.App() == nil {
		return mode, themeJSON
	}
	app := handle.App()
	if err := app.WaitForAllCellsStart(initialThemeStartTimeout); err != nil {
		slog.Warn("initial theme: actor tree not ready in time, falling back to light", "error", err)
		return mode, themeJSON
	}
	wsRef, ok := app.LookupService("workspace")
	if !ok {
		slog.Warn("initial theme: workspace service not found, falling back to light")
		return mode, themeJSON
	}
	call := wsRef.Invoke(ctx, "workspace.preferences_get", nil, initialThemeHeaders)
	defer call.Close()
	raw, err := call.RecvRaw()
	if err != nil {
		slog.Warn("initial theme: preferences_get failed, falling back to light", "error", err)
		return mode, themeJSON
	}
	var snap domain.AccountPreferencesSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		slog.Warn("initial theme: decode snapshot failed, falling back to light", "error", err)
		return mode, themeJSON
	}
	return parseThemePreference(snap.Preferences[domain.AccountPrefKeyAiShellTheme])
}

// readBootThemeCache returns the cached ai-shell theme preference verbatim.
// false means "no usable cache" (missing, unreadable) — callers fall back.
func readBootThemeCache() (string, bool) {
	data, err := os.ReadFile(config.BootThemeCachePath())
	if err != nil {
		return "", false
	}
	return string(data), true
}

// parseThemePreference validates the persisted theme preference string and
// returns the canonical (mode, themeJSON) pair. Malformed or absent values
// resolve to light.
func parseThemePreference(pref string) (mode string, themeJSON string) {
	var raw struct {
		Mode     string   `json:"mode"`
		FontSize *float64 `json:"fontSize"`
		Hue      *float64 `json:"hue"`
	}
	if err := json.Unmarshal([]byte(pref), &raw); err != nil || (raw.Mode != "dark" && raw.Mode != "light") {
		return "light", `{"mode":"light"}`
	}
	payload := map[string]any{"mode": raw.Mode}
	if raw.FontSize != nil {
		payload["fontSize"] = *raw.FontSize
	}
	if raw.Hue != nil {
		if hue := math.Round(*raw.Hue); hue >= 0 && hue <= 360 {
			payload["hue"] = hue
		}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "light", `{"mode":"light"}`
	}
	return raw.Mode, string(b)
}

// ShellBackgroundColour returns the main-window pre-paint colour matching the
// shell's --bg-base token (light #f7f7f7, dark #1e1e1e) so the splash paints
// onto the colour the native surface already shows. Distinct from
// browserBackgroundColour, which approximates plain page backgrounds for the
// right-panel browser windows.
func ShellBackgroundColour(dark bool) application.RGBA {
	if dark {
		return application.RGBA{Red: 30, Green: 30, Blue: 30, Alpha: 255}
	}
	return application.RGBA{Red: 247, Green: 247, Blue: 247, Alpha: 255}
}
