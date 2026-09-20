package desktop

import (
	"context"
	"os"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
)

func TestParseThemePreference(t *testing.T) {
	cases := []struct {
		name     string
		pref     string
		wantMode string
		wantJSON string
	}{
		{"dark with font size", `{"mode":"dark","fontSize":18}`, "dark", `{"fontSize":18,"mode":"dark"}`},
		{"light with hue", `{"mode":"light","hue":210}`, "light", `{"hue":210,"mode":"light"}`},
		{"hue out of range is dropped", `{"mode":"light","hue":999}`, "light", `{"mode":"light"}`},
		{"dark mode only", `{"mode":"dark"}`, "dark", `{"mode":"dark"}`},
		{"light", `{"mode":"light"}`, "light", `{"mode":"light"}`},
		{"empty falls back to light", "", "light", `{"mode":"light"}`},
		{"malformed json falls back to light", `{not json`, "light", `{"mode":"light"}`},
		{"unknown mode falls back to light", `{"mode":"sepia"}`, "light", `{"mode":"light"}`},
		{"null mode falls back to light", `{"fontSize":14}`, "light", `{"mode":"light"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode, themeJSON := parseThemePreference(tc.pref)
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", mode, tc.wantMode)
			}
			if themeJSON != tc.wantJSON {
				t.Fatalf("themeJSON = %q, want %q", themeJSON, tc.wantJSON)
			}
		})
	}
}

func TestInitialThemeFastPathFromBootCache(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	if err := os.WriteFile(config.BootThemeCachePath(), []byte(`{"mode":"dark","fontSize":14}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Nil handle: the fast path must resolve without any actor tree.
	mode, themeJSON := InitialTheme(context.Background(), nil)
	if mode != "dark" {
		t.Fatalf("mode = %q, want dark (from boot cache)", mode)
	}
	if themeJSON != `{"fontSize":14,"mode":"dark"}` {
		t.Fatalf("themeJSON = %q", themeJSON)
	}
}

func TestInitialThemeCacheMissingFallsBackToLightWithoutTree(t *testing.T) {
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	mode, themeJSON := InitialTheme(context.Background(), nil)
	if mode != "light" || themeJSON != `{"mode":"light"}` {
		t.Fatalf("got (%q, %q), want light defaults", mode, themeJSON)
	}
}
