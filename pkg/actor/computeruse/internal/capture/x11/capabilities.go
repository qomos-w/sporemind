//go:build linux

package x11

import (
	"os"
	"strings"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// Capabilities declares the Linux backend support matrix. The x11 backend
// shells out to xdotool / wmctrl / xclip / import; it has no accessibility
// tree and does not work on Wayland sessions.
func (c *Capturer) Capabilities() capture.CapabilitySet {
	wayland := capture.Capability{
		Key:    "feature.wayland",
		Detail: "backend is X11-only; Wayland sessions are not supported",
	}
	if isWaylandSession() {
		wayland.Detail = "current session is Wayland; capture and input will fail"
	}
	return capture.BuildCapabilities(capture.CapabilitySpec{
		Backend: "x11",
		Features: []capture.Capability{
			{Key: "feature.element_tree", Detail: "no accessibility tree; list_elements returns window-level entries only"},
			{Key: "feature.rich_clipboard", Supported: true, Detail: "text/image/files/html via xclip targets"},
			{Key: "feature.multi_display", Supported: true, Detail: "via xrandr"},
			wayland,
		},
		UnsupportedActions: map[string]string{
			"element_set_value": "no accessibility backend on the shell-out path",
		},
	})
}

// isWaylandSession reports whether the current session runs on Wayland.
func isWaylandSession() bool {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return true
	}
	return strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland")
}
