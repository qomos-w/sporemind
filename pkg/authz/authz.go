// Package authz provides shared authorization helpers that bridge JWT-based
// auth (used by WebSocket clients that cannot set custom headers) with
// gospore's role-based visibility system.
package authz

import (
	"github.com/qomos-w/sporemind/pkg/auth"
)

// Claims is a re-export of auth.Claims for convenience.
type Claims = auth.Claims

// VerifyToken parses and validates a JWT access token using the default
// workspace signing configuration. Returns the embedded claims or an error.
func VerifyToken(token string) (auth.Claims, error) {
	return defaultManager.Verify(token)
}

// IsAdmin returns true if the claims carry the "admin" role.
func IsAdmin(claims auth.Claims) bool {
	for _, r := range claims.Roles {
		if r == "admin" {
			return true
		}
	}
	return false
}
