//go:build windows

package winx

import "github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"

// Capabilities declares the Windows backend support matrix. The winx
// backend (GDI capture + SendInput + UIA helper subprocess) supports the
// full feature set.
func (c *Capturer) Capabilities() capture.CapabilitySet {
	return capture.BuildCapabilities(capture.CapabilitySpec{
		Backend: "winx",
		Features: []capture.Capability{
			{Key: "feature.element_tree", Supported: true, Detail: "UI Automation tree via helper subprocess"},
			{Key: "feature.rich_clipboard", Supported: true, Detail: "text/image/files/html via Win32 clipboard formats"},
			{Key: "feature.multi_display", Supported: true, Detail: "all monitors enumerated with per-monitor DPI"},
		},
	})
}
