package runtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/qomos-w/gospore/app"

	"github.com/qomos-w/sporemind/pkg/policy"
)

// compile-time check that app.App satisfies the bridge host interface.
var _ policyBridgeHost = app.App(nil)

// policyBridgeHost is the subset of app.App required by the startup bridge.
type policyBridgeHost interface {
	WaitForAllCellsStart(timeout time.Duration) error
	policy.StoreHost
}

// StartPolicyBridge waits for all cells to finish starting, then loads the user
// actor's permission matrix into the App's PolicyStore. It runs asynchronously
// and logs failures without blocking startup.
func StartPolicyBridge(ctx context.Context, h *Handle, waitTimeout time.Duration) {
	if h == nil || h.App() == nil {
		slog.Warn("policy bridge: app not available; skipping startup reload")
		return
	}
	go runPolicyBridge(ctx, h.App(), waitTimeout)
}

func runPolicyBridge(ctx context.Context, host policyBridgeHost, waitTimeout time.Duration) {
	if err := host.WaitForAllCellsStart(waitTimeout); err != nil {
		slog.Warn(
			"policy bridge: WaitForAllCellsStart did not complete within timeout; continuing with best-effort reload",
			"error", err,
			"timeout", waitTimeout,
		)
	}

	if err := policy.ReloadPolicyStoreFromUser(ctx, host); err != nil {
		slog.Warn("policy bridge: failed to load permission matrix into PolicyStore", "error", err)
		return
	}
	slog.Info("policy bridge: permission matrix loaded into PolicyStore")
}
