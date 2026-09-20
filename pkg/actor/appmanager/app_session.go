package appmanager

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// appSessionTTL is the default lifetime of an issued session token.
const appSessionTTL = 24 * time.Hour

// GenerationStaleCode is the stable, machine-readable error code embedded in
// the error message when a session token's generation no longer matches the
// app's current generation (app was reloaded). The frontend plugin-bridge
// matches on this code (bracketed) to trigger a transparent session rebind.
// Keep in sync with GENERATION_STALE_CODE in web/src/application/plugin-bridge.ts.
const GenerationStaleCode = "generation_stale"

// sessionTokenSep separates the base64url payload from the HMAC signature in
// an app session token: "<payload>.<signature>".
const sessionTokenSep = "."

// newSessionNonce returns a fresh random hex nonce for replay uniqueness.
func newSessionNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// newSignedMAC computes HMAC-SHA256(key, payload) and returns the digest. It
// is the shared signing primitive for all self-contained signed credentials
// (session tokens, route tokens).
func newSignedMAC(key, payload []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// encodeSignedToken produces the wire form base64url(payload).base64url(mac).
func encodeSignedToken(payload, mac []byte) string {
	return base64.RawURLEncoding.EncodeToString(payload) + sessionTokenSep + base64.RawURLEncoding.EncodeToString(mac)
}

// decodeAndVerify splits a "payload.signature" token, base64-decodes both
// halves, verifies the HMAC in constant time, and returns the verified
// payload bytes. A missing key, a malformed token, or a signature mismatch
// all yield an error — a tampered or forged token never returns a payload.
func decodeAndVerify(token string, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, fmt.Errorf("appmanager: signing key not initialized")
	}
	parts := strings.SplitN(token, sessionTokenSep, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("appmanager: malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("appmanager: decode token payload: %w", err)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("appmanager: decode token signature: %w", err)
	}
	if !hmac.Equal(sig, newSignedMAC(key, payload)) {
		return nil, fmt.Errorf("appmanager: invalid token signature")
	}
	return payload, nil
}

// issueSessionToken signs the session claims with the supplied key and returns
// a self-contained token of the form base64url(payload).base64url(sig). The
// payload is the JSON-encoded appSession; the signature is HMAC-SHA256(key,
// payload). An empty key is rejected so tokens can never be issued before the
// actor has initialized its signing material.
func issueSessionToken(s appSession, key []byte) (string, error) {
	if len(key) == 0 {
		return "", fmt.Errorf("appmanager: session signing key not initialized")
	}
	payload, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("appmanager: marshal session: %w", err)
	}
	return encodeSignedToken(payload, newSignedMAC(key, payload)), nil
}

// parseSessionToken verifies the token's HMAC signature (constant-time) and
// decodes the embedded session claims. A missing key, a malformed token, or a
// signature mismatch all yield an error — a tampered or forged token never
// resolves to a session.
func parseSessionToken(token string, key []byte) (appSession, error) {
	var s appSession
	payload, err := decodeAndVerify(token, key)
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(payload, &s); err != nil {
		return s, fmt.Errorf("appmanager: unmarshal session: %w", err)
	}
	return s, nil
}

// sessionExpired reports whether an ExpiresAt claim (unix seconds) is in the
// past. ExpiresAt == 0 means the token never expires.
func sessionExpired(expiresAt int64, now time.Time) bool {
	return expiresAt != 0 && now.Unix() > expiresAt
}

func (a *Actor) handleSessionCreate(ctx actor.Context, req gen.AppSessionCreateReq) (gen.AppSessionCreateResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	manifest, ok := a.Apps[req.AppID]
	if !ok {
		return gen.AppSessionCreateResp{}, fmt.Errorf("appmanager: app %q not found", req.AppID)
	}
	viewFound := false
	for _, entrypoint := range manifest.Entrypoints {
		if entrypoint.ID == req.ViewID && (entrypoint.Kind == "view" || entrypoint.Kind == "panel") {
			viewFound = true
			break
		}
	}
	if !viewFound {
		return gen.AppSessionCreateResp{}, fmt.Errorf("appmanager: view %q not found for app %q", req.ViewID, req.AppID)
	}
	// Dual-track authorization for session creation. In local/single-user
	// desktop mode the actor context carries no identity, so we trust the
	// caller (any local process can mint a session). In authenticated/gateway
	// mode the caller must present a verified role (agent/system/human/admin);
	// an anonymous identity with a forged subject is rejected. There is no
	// per-agent surface binding: any authenticated caller can create a
	// session for a declared view entrypoint.
	ident := id.Identity{}
	if ctx != nil {
		ident = ctx.Identity()
	}
	if !ident.IsZero() {
		if err := policy.RequireAgentOrHuman(ident.Role); err != nil || ident.Subject == "" {
			return gen.AppSessionCreateResp{}, appbinding.Deny(appbinding.CodeAgentScopeDenied, fmt.Sprintf("caller %q is not authorized to create session for app %q", ident.Subject, req.AppID))
		}
	}
	nonce, err := newSessionNonce()
	if err != nil {
		return gen.AppSessionCreateResp{}, fmt.Errorf("appmanager: generate session nonce: %w", err)
	}
	record := a.Records[req.AppID]
	session := appSession{
		ID:         uuid.NewString(),
		AppID:      req.AppID,
		ViewID:     req.ViewID,
		Origin:     normalizeOrigin(req.Origin),
		ExpiresAt:  time.Now().Add(appSessionTTL).Unix(),
		Nonce:      nonce,
		Generation: record.Generation,
	}
	// Session identity comes from the caller, not from a manifest-declared
	// agent binding. The identity is what invoke authorization will use for
	// identity resolution; callable.Permission is checked against the host
	// security policy.
	if !ident.IsZero() {
		session.AgentID = ident.Subject
	}
	token, err := issueSessionToken(session, a.sessionKey)
	if err != nil {
		return gen.AppSessionCreateResp{}, err
	}
	a.sessions[session.ID] = session
	resp := gen.AppSessionCreateResp{
		SessionID:  session.ID,
		AppID:      session.AppID,
		ViewID:     session.ViewID,
		AgentID:    session.AgentID,
		ProjectID:  session.ProjectID,
		Origin:     session.Origin,
		ExpiresAt:  session.ExpiresAt,
		Nonce:      session.Nonce,
		Generation: session.Generation,
		Token:      token,
	}
	// Direct-HTTP bootstrap (dual-track): when the app's plugin process runs
	// an SDK HTTP listener, hand the iframe its origin plus the spore_session
	// cookie value minted from the same per-instance secret the process
	// verifies against. Without a listener the fields stay empty and the
	// frontend keeps using the bridge token and the gateway asset route.
	if record.SessionSecret != "" {
		resp.BackendURL = record.BackendUrl
		resp.CookieToken = mintSessionCookie(record.SessionSecret, session.ID)
	}
	return resp, nil
}

// resolveSession validates a presented token and returns the corresponding
// live session. It is the single chokepoint enforcing the trusted-identity
// invariants: signature, expiry, active (non-revoked) map membership, nonce
// parity, and generation parity with the app's current generation. Callers
// must hold a.mu.
func (a *Actor) resolveSession(token string, now time.Time) (appSession, error) {
	var empty appSession
	claims, err := parseSessionToken(token, a.sessionKey)
	if err != nil {
		return empty, err
	}
	if sessionExpired(claims.ExpiresAt, now) {
		return empty, fmt.Errorf("appmanager: session token expired")
	}
	stored, ok := a.sessions[claims.ID]
	if !ok {
		return empty, fmt.Errorf("appmanager: app session not found")
	}
	// The token must correspond to the exact live session: a matching nonce
	// proves the token is the one issued for this entry (not a stale token
	// that happens to share a session id).
	if claims.Nonce != stored.Nonce {
		return empty, fmt.Errorf("appmanager: session nonce mismatch")
	}
	// Generation parity: a reload (or re-registration) bumps the app's
	// generation, so any token carrying the old generation is now stale.
	if record, ok := a.Records[claims.AppID]; ok {
		if claims.Generation != record.Generation {
			return empty, fmt.Errorf("appmanager: [%s] session generation stale", GenerationStaleCode)
		}
	}
	return stored, nil
}

func (a *Actor) handleSessionResolve(_ actor.PureContext, req gen.AppSessionResolveReq) (gen.AppSessionResolveResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.resolveSession(req.Token, time.Now())
	if err != nil {
		return gen.AppSessionResolveResp{}, err
	}
	return gen.AppSessionResolveResp{
		SessionID:  session.ID,
		AppID:      session.AppID,
		ViewID:     session.ViewID,
		AgentID:    session.AgentID,
		ProjectID:  session.ProjectID,
		Origin:     session.Origin,
		ExpiresAt:  session.ExpiresAt,
		Nonce:      session.Nonce,
		Generation: session.Generation,
	}, nil
}

func (a *Actor) handleSessionRevoke(_ actor.PureContext, req gen.AppSessionRevokeReq) (gen.AppSessionRevokeResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Revoke validates the token first (it must be a real, signed session)
	// then drops the live entry. A subsequent resolve fails on map miss.
	claims, err := parseSessionToken(req.Token, a.sessionKey)
	if err != nil {
		return gen.AppSessionRevokeResp{}, err
	}
	if _, ok := a.sessions[claims.ID]; !ok {
		return gen.AppSessionRevokeResp{}, fmt.Errorf("appmanager: app session not found")
	}
	delete(a.sessions, claims.ID)
	return gen.AppSessionRevokeResp{}, nil
}

// revokeAppSessions invalidates every live session bound to appID. It is the
// cascade hook for unregister: dropping the app tears down all of its trusted
// identities so dangling tokens can no longer resolve.
func (a *Actor) revokeAppSessions(appID string) {
	for sessionID, session := range a.sessions {
		if session.AppID == appID {
			delete(a.sessions, sessionID)
		}
	}
}
