package appmanager

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// routeTokenTTL is the default lifetime of an issued route token. Route
// tokens are short-lived because they authorize a single App-to-App
// callable invocation chain, not a persistent session.
const routeTokenTTL = 5 * time.Minute

// routeToken is the signed claim set embedded (JSON) inside a route token.
// It authorizes one source app to invoke one specific callable on one
// target app. Like appSession, it is ephemeral and signed with the
// per-process HMAC key, so all route authority is invalidated on
// appmanager restart.
type routeToken struct {
	SourceAppID string `json:"src"` // the app that requested the route authority
	TargetAppID string `json:"dst"` // the app whose callable will be invoked (audience)
	Callable    string `json:"fn"`  // the specific callable authorized
	ExpiresAt   int64  `json:"exp"` // unix seconds; 0 means no expiry
}

// issueRouteToken signs the route claims with the supplied key and returns
// a self-contained token of the form base64url(payload).base64url(sig),
// mirroring issueSessionToken. An empty key is rejected.
func issueRouteToken(rt routeToken, key []byte) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("appmanager: route signing key not initialized")
	}
	payload, err := json.Marshal(rt)
	if err != nil {
		return "", fmt.Errorf("appmanager: marshal route token: %w", err)
	}
	mac := newSignedMAC(key, payload)
	return encodeSignedToken(payload, mac), nil
}

// parseRouteToken verifies the token's HMAC signature (constant-time) and
// decodes the embedded route claims. A missing key, a malformed token, or a
// signature mismatch all yield an error — a tampered or forged route token
// never resolves.
func parseRouteToken(token string, key []byte) (routeToken, error) {
	var rt routeToken
	payload, err := decodeAndVerify(token, key)
	if err != nil {
		return rt, err
	}
	if err := json.Unmarshal(payload, &rt); err != nil {
		return rt, fmt.Errorf("appmanager: unmarshal route token: %w", err)
	}
	return rt, nil
}

// handleRouteToken issues a signed route token authorizing SourceAppId to
// invoke Callable on TargetAppId. Both apps must be registered and running;
// the callable must be declared in the target app's manifest. The issued
// token carries the resolved caller identity's agent/project scope.
func (a *Actor) handleRouteToken(ctx actor.PureContext, req gen.AppRouteTokenReq) (gen.AppRouteTokenResp, error) {
	sourceAppID := strings.TrimSpace(req.SourceAppID)
	targetAppID := strings.TrimSpace(req.TargetAppID)
	callable := strings.TrimSpace(req.Callable)

	// Validate under the lock, then issue + audit outside it (recordAudit
	// takes the same lock — calling it while holding a.mu deadlocks).
	if sourceAppID == "" || targetAppID == "" || callable == "" {
		return gen.AppRouteTokenResp{}, fmt.Errorf("appmanager: source app, target app, and callable are required")
	}
	var sourceManifest, targetManifest gen.AppManifest
	var key []byte
	var lookupErr error
	var cleanupPending string
	a.withMu(func() {
		var sourceOk, targetOk bool
		sourceManifest, sourceOk = a.Apps[sourceAppID]
		targetManifest, targetOk = a.Apps[targetAppID]
		if !sourceOk {
			lookupErr = fmt.Errorf("appmanager: source app %q not found", sourceAppID)
			return
		}
		if !targetOk {
			lookupErr = fmt.Errorf("appmanager: target app %q not found", targetAppID)
			return
		}
		if _, err := lookupCallable(targetManifest, callable); err != nil {
			lookupErr = err
			return
		}
		if rec, ok := a.Records[sourceAppID]; ok && isCleanupPendingState(rec.State) {
			cleanupPending = rec.State
		}
		key = a.sessionKey
	})
	if lookupErr != nil {
		return gen.AppRouteTokenResp{}, lookupErr
	}
	if cleanupPending != "" {
		return gen.AppRouteTokenResp{}, fmt.Errorf("appmanager: source app %q is pending cleanup (state: %s)", sourceAppID, cleanupPending)
	}

	// Resolve caller identity (authoritative from actor context).
	ci := resolveCaller(ctx, req.AgentID, req.Role, req.ProjectID)

	expiresAt := time.Now().Add(routeTokenTTL).Unix()
	rt := routeToken{
		SourceAppID: sourceAppID,
		TargetAppID: targetAppID,
		Callable:    callable,
		ExpiresAt:   expiresAt,
	}
	token, err := issueRouteToken(rt, key)
	if err != nil {
		return gen.AppRouteTokenResp{}, err
	}

	a.recordAudit(appbinding.AuditRecord{
		AppID:     sourceAppID,
		Runtime:   sourceManifest.Runtime,
		Callable:  "route_token:" + targetAppID + "." + callable,
		AgentID:   ci.AgentID,
		Role:      ci.Role,
		ProjectID: ci.ProjectID,
		Allowed:   true,
		Reason:    "route token issued",
	})

	return gen.AppRouteTokenResp{Token: token, ExpiresAt: expiresAt}, nil
}
