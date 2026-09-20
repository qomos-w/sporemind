//go:build darwin

package cgmac

import "github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"

// Capabilities declares the macOS backend support matrix. cgmac shells out
// to screencapture / osascript / cliclick, so several features are absent
// or degraded relative to the Windows backend.
func (c *Capturer) Capabilities() capture.CapabilitySet {
	return capture.BuildCapabilities(capture.CapabilitySpec{
		Backend: "cgmac",
		Features: []capture.Capability{
			{Key: "feature.element_tree", Detail: "no accessibility tree; list_elements returns window-level entries only"},
			{Key: "feature.rich_clipboard", Detail: "text/html read; image write via osascript; file lists degrade to text"},
			{Key: "feature.multi_display", Detail: "primary display only"},
		},
		UnsupportedActions: map[string]string{
			"middle_click":      "cliclick has no middle button",
			"element_set_value": "no accessibility backend on the shell-out path",
		},
	})
}
