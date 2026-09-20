package auth

import (
	"errors"
	"sync"
)

// Error sentinels
var (
	// ErrInvalidToken is returned when a token fails validation.
	ErrInvalidToken = errors.New("auth: invalid token")
	// ErrTokenExpired is returned when a token has expired.
	ErrTokenExpired = errors.New("auth: token expired")
)

// Mutable singletons
// TODO(actor-ownership): migrate to actor-owned state
var (
	defaultSecretOnce sync.Once
	defaultSecret     []byte
)
