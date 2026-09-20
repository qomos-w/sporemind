// Package gatewayauth bridges JWT-based WebSocket authentication with
// gospore's role-based identity system.
//
// The URLAuth implementation verifies the token query parameter, extracts
// the user's primary role from the JWT claims, and returns it along with
// the user ID as the caller subject.
package gatewayauth

import (
	"context"
	"fmt"
	"net/url"

	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/sporemind/pkg/auth"
)

// New returns a gateway.URLAuth that validates JWT tokens from the
// WebSocket URL query parameter "token".
//
// Flow:
//   1. Read "token" from URL query.
//   2. If absent → anonymous (role="anonymous", subject="").
//   3. If present → verify JWT, extract uid + roles.
//   4. Return primary role (first in claims.Roles, or "viewer" default)
//     and uid as subject.
//
// Glass session tokens are handled first: a token with scope/kind "glass"
// authenticates as role="glass" with the server-generated session_id as
// subject, so Glass callables can bind frames to the active session.
func New(jwt *auth.Manager) gateway.URLAuth {
	return func(ctx context.Context, q url.Values, app gateway.AppHost) (string, string, error) {
		token := q.Get("token")
		if token == "" {
			return "anonymous", "", nil
		}

		if g, err := jwt.VerifyGlass(token); err == nil && g.Scope == auth.GlassScope {
			return "glass", g.SessionID, nil
		}

		claims, err := jwt.Verify(token)
		if err != nil {
			return "", "", fmt.Errorf("invalid token: %w", err)
		}

		role := "viewer"
		if len(claims.Roles) > 0 {
			role = claims.Roles[0]
		}

		return role, claims.UserID, nil
	}
}
