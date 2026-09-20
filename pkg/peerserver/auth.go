package peerserver

import (
	"net/http"
)

// HeaderPeerToken is the default HTTP header used by TokenAuthenticator.
const HeaderPeerToken = "X-Peer-Token"

// TokenAuthenticator authenticates peers by a static token header. It is the
// simplest production-ready authenticator: the server maintains a token ->
// peerID mapping and rejects unknown tokens.
type TokenAuthenticator struct {
	tokens map[string]string // token -> peerID
}

// NewTokenAuthenticator creates an authenticator from a token mapping.
func NewTokenAuthenticator(tokens map[string]string) *TokenAuthenticator {
	cp := make(map[string]string, len(tokens))
	for k, v := range tokens {
		cp[k] = v
	}
	return &TokenAuthenticator{tokens: cp}
}

// Authenticate extracts the token from HeaderPeerToken and returns the mapped
// peerID.
func (a *TokenAuthenticator) Authenticate(r *http.Request) (string, bool) {
	token := r.Header.Get(HeaderPeerToken)
	if token == "" {
		return "", false
	}
	peerID, ok := a.tokens[token]
	return peerID, ok
}

// AllowAllAuthenticator is a development authenticator that accepts every
// request and reports a fixed peerID derived from a header or fallback. It
// must not be used in production.
type AllowAllAuthenticator struct {
	DefaultPeerID string
}

// Authenticate returns the peerID from the X-Peer-ID header, or DefaultPeerID,
// or "anonymous".
func (a *AllowAllAuthenticator) Authenticate(r *http.Request) (string, bool) {
	if peerID := r.Header.Get("X-Peer-ID"); peerID != "" {
		return peerID, true
	}
	if a.DefaultPeerID != "" {
		return a.DefaultPeerID, true
	}
	return "anonymous", true
}
