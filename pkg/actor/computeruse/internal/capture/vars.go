package capture

// InteractActions is the canonical list of computeruse.interact actions,
// in declaration order. Backends report support per action so agents can
// avoid invoking actions that would fail on the current platform.
var InteractActions = []string{
	"click", "double_click", "right_click", "middle_click",
	"move", "scroll", "type",
	"key", "key_down", "key_up",
	"copy", "paste", "drag", "wait",
	"focus_window", "window_state", "window_move", "window_close",
	"element_click", "element_focus", "element_set_value",
}

// NewWindowsCapturer is set by the windows build to the GDI capturer
// constructor. nil on non-windows builds.
var NewWindowsCapturer func() (Capturer, error)

// NewLinuxCapturer is set by the linux build to the X11 capturer
// constructor. nil on non-linux builds.
var NewLinuxCapturer func() (Capturer, error)

// NewDarwinCapturer is set by the darwin build to the CoreGraphics
// capturer constructor. nil on non-darwin builds.
var NewDarwinCapturer func() (Capturer, error)
