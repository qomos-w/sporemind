package agent

import (
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
)

// safeGo runs fn in a new goroutine with panic recovery. The recovered value
// is logged to stdout; no oracle diagnostic is sent from this path.
//
// Prefer panicprobe.SafeGo(ctx, name, fn) at sites where an actor.Context is
// in scope — that variant also reports the panic as a diagnostic. This helper
// remains for turnEngine and similar paths that don't carry an actor context.
func safeGo(name string, fn func()) {
	panicprobe.SafeGoBackground(name, fn)
}
