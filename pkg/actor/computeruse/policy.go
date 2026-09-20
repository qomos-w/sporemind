package computeruse

// Policy scopes for the gated (non-public) computeruse callables. These are
// declared explicitly here — one entry per gated callable — instead of
// living only in comments, per the project's explicit-constraint rule.
//
// Enforcement chain: gated callables are registered actor.Internal() (never
// exported to frontend codegen). Agents reach them only through the
// computeruse tool bundle, which an administrator must enable explicitly;
// at execution time pkg/actor/agent/toolregistry.go classifies the
// side-effecting ones EffectIrreversible, so the turn engine's
// PermissionPolicy pauses for user confirmation (default "confirm" mode).
const (
	// ScopeInputControl gates full mouse/keyboard/window control.
	ScopeInputControl = "computeruse.input_control"
	// ScopeContinuousCapture gates the continuous screen stream.
	ScopeContinuousCapture = "computeruse.continuous_capture"
	// ScopeClipboardRead gates reading user clipboard contents.
	ScopeClipboardRead = "computeruse.clipboard_read"
	// ScopeClipboardWrite gates writing the user clipboard.
	ScopeClipboardWrite = "computeruse.clipboard_write"
)

// gatedCallable is the explicit policy declaration for one non-public
// callable.
type gatedCallable struct {
	Scope  string
	Reason string
}
