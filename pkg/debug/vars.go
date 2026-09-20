package debug

import "context"

// EvalJS is set by the desktop app on startup; when nil the /debug/eval-js
// endpoint returns 503 because the runtime has no frontend webview to execute
// against (e.g. headless server mode).
//
// TODO(actor-ownership): migrate to actor-owned state.
var EvalJS func(ctx context.Context, script string) (any, error)
