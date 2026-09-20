package videogen

import (
	"context"
	"strings"
	"testing"
)

// TestGenerate_DispatchRoutesByProtocol locks the dispatcher contract: the
// Protocol field selects the backend, unsupported protocols fail fast with a
// clear error instead of silently hitting the wrong backend.
func TestGenerate_DispatchRoutesByProtocol(t *testing.T) {
	// Supported protocols must reach their backend. The backends fail on the
	// empty model, but the error prefix proves routing happened.
	_, err := Generate(context.Background(), Params{Protocol: "ark"})
	if err == nil || !strings.HasPrefix(err.Error(), "videogen/ark: ") {
		t.Fatalf("ark route error = %v, want videogen/ark prefix", err)
	}

	_, err = Generate(context.Background(), Params{Protocol: "gemini"})
	if err == nil || !strings.HasPrefix(err.Error(), "videogen/gemini: ") {
		t.Fatalf("gemini route error = %v, want videogen/gemini prefix", err)
	}

	_, err = Generate(context.Background(), Params{Protocol: "qwen"})
	if err == nil || !strings.HasPrefix(err.Error(), "videogen/qwen: ") {
		t.Fatalf("qwen route error = %v, want videogen/qwen prefix", err)
	}

	// "openai"-kind providers have no dedicated video backend: explicit clear
	// error instead of falling through to a wrong protocol.
	_, err = Generate(context.Background(), Params{Protocol: "openai"})
	if err == nil || err.Error() != "videogen: protocol \"openai\" has no dedicated backend; use a provider with Kind ark, gemini, or qwen" {
		t.Fatalf("openai route error = %v", err)
	}

	_, err = Generate(context.Background(), Params{Protocol: "anthropic"})
	if err == nil || err.Error() != "videogen: unsupported protocol \"anthropic\"" {
		t.Fatalf("unsupported route error = %v", err)
	}
}
