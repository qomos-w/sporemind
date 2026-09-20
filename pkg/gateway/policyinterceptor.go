package gateway

import (
	"context"
	"log/slog"
	"time"

	gosporeGateway "github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/policy"
)

const permissionUpdateCallable = "user.permission_update"

// PolicyReloadInterceptor keeps the App's PolicyStore in sync with the user
// actor permission matrix by reloading after successful gateway calls to
// user.permission_update.
//
// It only covers calls that go through the HTTP gateway. Direct Wails calls to
// user.permission_update do not pass through the gateway interceptor, so a
// process restart is required for those edits to take effect in PolicyStore.
type PolicyReloadInterceptor struct {
	host policy.StoreHost
}

var _ gosporeGateway.GatewayInterceptor = (*PolicyReloadInterceptor)(nil)

// NewPolicyReloadInterceptor returns a gateway interceptor.
func NewPolicyReloadInterceptor(host policy.StoreHost) *PolicyReloadInterceptor {
	return &PolicyReloadInterceptor{host: host}
}

// SetHost updates the policy host after the App has been constructed.
func (i *PolicyReloadInterceptor) SetHost(host policy.StoreHost) {
	i.host = host
}

// Before is a no-op; the interceptor does not block requests.
func (i *PolicyReloadInterceptor) Before(ctx context.Context, req *gosporeGateway.GatewayRequest) error {
	return nil
}

// After reloads the PolicyStore when user.permission_update succeeds.
func (i *PolicyReloadInterceptor) After(ctx context.Context, req *gosporeGateway.GatewayRequest, resp *gosporeGateway.GatewayResponse) {
	if req.CallID != permissionUpdateCallable {
		return
	}
	if resp.Error != nil {
		slog.Debug("policy interceptor: permission_update failed; skipping reload", "error", resp.Error)
		return
	}
	if i.host == nil {
		slog.Warn("policy interceptor: policy host not set; skipping reload")
		return
	}

	// Detach from the request context so the reload survives the HTTP response.
	reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if err := policy.ReloadPolicyStoreFromUser(reloadCtx, i.host); err != nil {
		slog.Warn("policy interceptor: failed to reload permission matrix after update", "error", err)
		return
	}
	slog.Info("policy interceptor: permission matrix reloaded after update")
}
