package computeruse

import (
	"runtime"
	"testing"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func capIndex(items []domain.ComputerUseCapability) map[string]domain.ComputerUseCapability {
	out := make(map[string]domain.ComputerUseCapability, len(items))
	for _, it := range items {
		out[it.Key] = it
	}
	return out
}

func TestHandleCapabilities_Matrix(t *testing.T) {
	a, _, ctx := freshActor(t)
	resp, err := a.handleCapabilities(ctx, domain.ComputerUseCapabilitiesReq{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Platform != runtime.GOOS {
		t.Errorf("Platform=%q, want %q", resp.Platform, runtime.GOOS)
	}
	if resp.Backend != "fake" {
		t.Errorf("Backend=%q, want fake", resp.Backend)
	}

	idx := capIndex(resp.Items)

	// Backend features are surfaced.
	if c, ok := idx["feature.element_tree"]; !ok || !c.Supported {
		t.Errorf("feature.element_tree = %+v, want supported", c)
	}

	// Every interact action is reported, with per-backend support flags.
	for _, action := range capture.InteractActions {
		c, ok := idx["action."+action]
		if !ok {
			t.Errorf("action.%s missing from matrix", action)
			continue
		}
		if action == "middle_click" {
			if c.Supported || c.Detail == "" {
				t.Errorf("action.middle_click = %+v, want unsupported with detail", c)
			}
		} else if !c.Supported {
			t.Errorf("action.%s unsupported on fake backend", action)
		}
	}

	// OCR engine entries exist (availability itself is host-dependent).
	for _, key := range []string{"ocr.paddle", "ocr.tesseract"} {
		if _, ok := idx[key]; !ok {
			t.Errorf("%s missing from matrix", key)
		}
	}

	// Every gated callable is surfaced with its declared policy scope.
	for id, g := range gatedCallables {
		c, ok := idx["callable."+id]
		if !ok {
			t.Errorf("callable.%s missing from matrix", id)
			continue
		}
		if c.Scope != g.Scope {
			t.Errorf("callable.%s scope=%q, want %q", id, c.Scope, g.Scope)
		}
		if c.Detail == "" {
			t.Errorf("callable.%s has no detail", id)
		}
	}

	// Total: features + actions + ocr engines + gated callables.
	want := 3 + len(capture.InteractActions) + 2 + len(gatedCallables)
	if len(resp.Items) != want {
		t.Errorf("len(Items)=%d, want %d", len(resp.Items), want)
	}
}
