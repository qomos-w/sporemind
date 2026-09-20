package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	userService           = "user"
	permissionGetCallable = "user.permission_get"
)

// internalCallerHeaders gives the bridge an authenticated system identity.
// A non-empty role makes the call externally-shaped for identity purposes,
// which satisfies user.permission_get's requireAuth check, while "system" is
// never present in the permission matrix so it cannot be denied.
var internalCallerHeaders = map[string]string{
	"gospore.caller_role":    "system",
	"gospore.caller_subject": "sporemind-policy-bridge",
}

// StoreHost is the subset of app.App needed to reload the PolicyStore from the
// user actor. It keeps the bridge decoupled from the full App interface so it
// can be reused from the runtime and gateway with minimal, testable surfaces.
type StoreHost interface {
	LookupService(name string) (ref.Ref, bool)
	PolicyStore() actor.PolicyStore
}

// ReloadPolicyStoreFromUser fetches the current permission matrix from the
// user actor and reloads the host's PolicyStore with explicit deny rules.
func ReloadPolicyStoreFromUser(ctx context.Context, host StoreHost) error {
	userRef, ok := host.LookupService(userService)
	if !ok {
		return errors.New("user service not found")
	}

	matrix, err := fetchPermissionMatrix(ctx, userRef)
	if err != nil {
		return fmt.Errorf("fetch permission matrix: %w", err)
	}

	rules := MatrixDenyRules(matrix)
	if err := host.PolicyStore().Reload(rules); err != nil {
		return fmt.Errorf("reload policy store: %w", err)
	}

	slog.Debug("policy store reloaded from user permission matrix", "rules", len(rules))
	return nil
}

func fetchPermissionMatrix(ctx context.Context, userRef ref.Ref) (gen.PermissionMatrix, error) {
	call := userRef.Invoke(ctx, permissionGetCallable, nil, internalCallerHeaders)
	defer call.Close()

	raw, err := call.RecvRaw()
	if err != nil {
		return gen.PermissionMatrix{}, err
	}

	var matrix gen.PermissionMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		return gen.PermissionMatrix{}, fmt.Errorf("unmarshal permission matrix: %w", err)
	}
	return matrix, nil
}
