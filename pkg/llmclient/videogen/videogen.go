package videogen

import (
	"context"
	"fmt"
)

// Generate routes to the appropriate video-generation backend based on the
// provider protocol (Kind). Direct account paths may keep calling the
// backend-specific entry points; the dispatcher exists for protocol-routed
// call sites (aggregator resolve).
func Generate(ctx context.Context, p Params) (*Result, error) {
	switch p.Protocol {
	case "ark":
		return GenerateArk(ctx, p)
	case "gemini":
		return GenerateGemini(ctx, p)
	case "qwen":
		return GenerateQwen(ctx, p)
	case "openai", "endpoint":
		return nil, fmt.Errorf("videogen: protocol %q has no dedicated backend; use a provider with Kind ark, gemini, or qwen", p.Protocol)
	default:
		return nil, fmt.Errorf("videogen: unsupported protocol %q", p.Protocol)
	}
}
