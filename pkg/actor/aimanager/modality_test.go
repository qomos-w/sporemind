package aimanager

import "testing"

// TestInferModality locks the heuristic classification contract: explicit
// override wins; image/video name patterns; everything else defaults to chat.
func TestInferModality(t *testing.T) {
	cases := []struct {
		name     string
		explicit string
		want     string
	}{
		// Explicit override takes precedence regardless of the name.
		{"gpt-image-2", "chat", "chat"},
		{"whisper-1", "image", "image"},
		{"anything", "video", "video"},

		// Image patterns.
		{"gpt-image-2", "", "image"},
		{"dall-e-3", "", "image"},
		{"imagen-4.0-generate-001", "", "image"},
		{"gemini-3.1-flash-image-preview", "", "image"},
		{"seedream-4.0", "", "image"},
		{"doubao-seedream-5-0-260128", "", "image"},
		{"grok-imagine-image", "", "image"},
		{"grok-imagine-image-lite", "", "image"},

		// Nano Banana family (OpenRouter catalog: architecture.output_modalities
		// includes "image") and relay aliases.
		{"gemini-2.5-flash-image", "", "image"},   // Nano Banana
		{"gemini-3.1-flash-image", "", "image"},   // Nano Banana 2
		{"gemini-3.1-flash-lite-image", "", "image"},
		{"gemini-3-pro-image", "", "image"},       // Nano Banana Pro
		{"google/gemini-2.5-flash-image", "", "image"},   // org/ prefix kept
		{"google/gemini-2.5-flash-image:free", "", "image"}, // :variant stripped
		{"google/gemini-3-pro-image-preview:free", "", "image"},
		{"gpt-5-image", "", "image"},
		{"gpt-5-image-mini", "", "image"},
		{"gpt-5.4-image-2", "", "image"},
		{"nano-banana", "", "image"},
		{"nano-banana-pro", "", "image"},
		{"cogview-4", "", "image"},
		{"image-01", "", "image"},

		// Video patterns.
		{"veo-3.0-generate-001", "", "video"},
		{"Veo-3.1", "", "video"},
		{"dreamina-seedance-2-0-260128", "", "video"},
		{"seedance2.5", "", "video"},
		{"cogvideox-3", "", "video"},
		{"kling-v2", "", "video"},
		{"sora-2", "", "video"},
		{"wan-2.5-t2v", "", "video"},
		{"grok-imagine-video", "", "video"},
		{"grok-imagine-video-pro", "", "video"},
		{"some-video-model", "", "video"},

		// Chat defaults.
		{"gpt-5.2", "", "chat"},
		{"claude-sonnet-5", "", "chat"},
		{"glm-4.7", "", "chat"},
		{"whisper-1", "", "chat"},
	}
	for _, tc := range cases {
		if got := inferModality(tc.name, tc.explicit); got != tc.want {
			t.Errorf("inferModality(%q, %q) = %q, want %q", tc.name, tc.explicit, got, tc.want)
		}
	}
}