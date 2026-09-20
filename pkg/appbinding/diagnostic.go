package appbinding

import (
	"errors"
)

// Stable diagnostic codes for binding/authorization failures. Callers must
// match on the code (via DenialCode), never on the detail text.
const (
	// CodeIdentityIncomplete — binding or caller identity is missing required fields.
	CodeIdentityIncomplete = "appbinding.identity_incomplete"
	// CodeBindingMissing — no capability binding exists for (app, agent).
	CodeBindingMissing = "appbinding.denied.binding_missing"
	// CodeAgentScopeDenied — caller agent does not match the bound agent.
	CodeAgentScopeDenied = "appbinding.denied.agent_scope"
	// CodeRoleDenied — caller role does not match the bound role.
	CodeRoleDenied = "appbinding.denied.role"
	// CodeProjectScopeDenied — caller project does not match the bound project scope.
	CodeProjectScopeDenied = "appbinding.denied.project_scope"
	// CodeCallableDenied — callable is not in the bound capability allowlist.
	CodeCallableDenied = "appbinding.denied.callable"
	// CodeFreeAgentDenied — free-agent action (create/switch/message) not allowed.
	CodeFreeAgentDenied = "appbinding.denied.free_agent"
	// CodePermissionDenied — manifest permission not allowed by security policy.
	CodePermissionDenied = "appbinding.denied.permission"
	// CodeManifestInvalid — manifest failed registration-time validation.
	CodeManifestInvalid = "appbinding.denied.manifest_invalid"
	// CodeDependencyMissing — dependency app/version/hash not satisfied.
	CodeDependencyMissing = "appbinding.denied.dependency_missing"
	// CodeSessionRequired — an external (RouteDepth==0) invoke was received
	// without a session token. This is the coarse signal that the caller must
	// present a valid app session.
	CodeSessionRequired = "appbinding.denied.session_required"
	// CodeSessionInvalid — session token failed signature, expiry, nonce, or
	// generation validation, or the session was revoked.
	CodeSessionInvalid = "appbinding.denied.session_invalid"
	// CodeSessionScopeMismatch — session token is valid but scoped to a
	// different app than the one being invoked.
	CodeSessionScopeMismatch = "appbinding.denied.session_scope"
	// CodeRouteTokenInvalid — route token failed HMAC signature verification
	// or was malformed.
	CodeRouteTokenInvalid = "appbinding.denied.route_token_invalid"
	// CodeRouteTokenAudienceMismatch — route token is valid but its target app
	// does not match the invoked app.
	CodeRouteTokenAudienceMismatch = "appbinding.denied.route_token_audience"
	// CodeRouteTokenCallableMismatch — route token is valid but its callable
	// does not match the invoked callable.
	CodeRouteTokenCallableMismatch = "appbinding.denied.route_token_callable"
	// CodeRouteTokenExpired — route token signature is valid but the token
	// has passed its expiry.
	CodeRouteTokenExpired = "appbinding.denied.route_token_expired"
)

// DeniedError is a typed authorization failure carrying a stable diagnostic
// code plus human-readable detail.
type DeniedError struct {
	Code   string
	Detail string
}

func (e *DeniedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

// Deny builds a DeniedError with the given stable code and detail.
func Deny(code, detail string) *DeniedError {
	return &DeniedError{Code: code, Detail: detail}
}

// DenialCode extracts the stable diagnostic code from an error, or "" if the
// error is not a DeniedError.
func DenialCode(err error) string {
	var de *DeniedError
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

// IsDenied reports whether err carries any appbinding denial code.
func IsDenied(err error) bool { return DenialCode(err) != "" }

// ---------------------------------------------------------------------------
// Dispatcher-level diagnostic codes
//
// The granular Code* / DeniedError values above describe why a specific
// capability or free-agent binding refused a request. The DiagnosticCode set
// below is the coarse, stable categorization surfaced by Dispatcher.Invoke at
// the app<->agent boundary so frontends can branch on a small, documented set
// of reasons instead of the finer-grained (and occasionally reorganized)
// binding codes. Each dispatcher denial carries a DiagnosticError whose Cause
// is the underlying DeniedError, so DenialCode still works for callers that
// want the fine-grained code.
// ---------------------------------------------------------------------------

// DiagnosticCode is a coarse, stable category returned at the dispatcher
// boundary. Frontends should switch on these values rather than error strings.
type DiagnosticCode string

const (
	// DiagCapabilityDenied — the caller failed a capability check that is not a
	// more specific agent, callable, or project denial (e.g. role mismatch or a
	// missing capability binding).
	DiagCapabilityDenied DiagnosticCode = "capability_denied"
	// DiagCallableNotFound — the requested callable is not in the bound
	// capability allowlist.
	DiagCallableNotFound DiagnosticCode = "callable_not_found"
	// DiagAgentNotAuthorized — the caller agent does not match the bound agent.
	DiagAgentNotAuthorized DiagnosticCode = "agent_not_authorized"
	// DiagProjectMismatch — the caller project does not match the bound project
	// scope.
	DiagProjectMismatch DiagnosticCode = "project_mismatch"
	// DiagInvokeTimeout — the underlying callable call exceeded its deadline.
	DiagInvokeTimeout DiagnosticCode = "invoke_timeout"
	// DiagInternalError — an unexpected error occurred during dispatch/call.
	DiagInternalError DiagnosticCode = "internal_error"
	// DiagSessionRejected — the session token presented with the invoke was
	// invalid, expired, revoked, or scoped to a different app.
	DiagSessionRejected DiagnosticCode = "session_rejected"
	// DiagRouteRejected — the route token presented with a depth>0 invoke was
	// invalid, forged, expired, or scoped to the wrong app/callable.
	DiagRouteRejected DiagnosticCode = "route_rejected"
)

// DiagnosticError carries a stable DiagnosticCode, a human-readable message,
// and the underlying cause. It is returned by Dispatcher.Invoke so callers can
// switch on Code instead of matching error strings.
type DiagnosticError struct {
	Code    DiagnosticCode
	Message string
	Cause   error
}

func (e *DiagnosticError) Error() string { return string(e.Code) + ": " + e.Message }

// NewDiagnosticError builds a DiagnosticError carrying a stable coarse code,
// a human-readable message, and the underlying cause. Callers outside the
// dispatcher (e.g. appmanager.invoke permission denials) can use it to keep
// the stable diagnostic surface uniform.
func NewDiagnosticError(code DiagnosticCode, message string, cause error) *DiagnosticError {
	return &DiagnosticError{Code: code, Message: message, Cause: cause}
}

// Unwrap exposes the underlying cause so errors.Is / errors.As (and the
// fine-grained DenialCode helper) can still reach the original error.
func (e *DiagnosticError) Unwrap() error { return e.Cause }

// DiagnosticOf extracts the DiagnosticCode from err, or "" if err is not (or
// does not wrap) a DiagnosticError.
func DiagnosticOf(err error) DiagnosticCode {
	var de *DiagnosticError
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

// diagnosticFromDenial maps a fine-grained binding-level denial code to the
// coarse DiagnosticCode the dispatcher exposes.
func diagnosticFromDenial(code string) DiagnosticCode {
	switch code {
	case CodeAgentScopeDenied:
		return DiagAgentNotAuthorized
	case CodeCallableDenied:
		return DiagCallableNotFound
	case CodeProjectScopeDenied:
		return DiagProjectMismatch
	case CodeSessionRequired, CodeSessionInvalid, CodeSessionScopeMismatch:
		return DiagSessionRejected
	case CodeRouteTokenInvalid, CodeRouteTokenAudienceMismatch, CodeRouteTokenCallableMismatch, CodeRouteTokenExpired:
		return DiagRouteRejected
	default:
		return DiagCapabilityDenied
	}
}

// denialDetail returns the human-readable detail of a DeniedError, or the
// generic error text when err is not a DeniedError. Used to populate
// DiagnosticError.Message without duplicating the diagnostic code prefix.
func denialDetail(err error) string {
	var de *DeniedError
	if errors.As(err, &de) {
		return de.Detail
	}
	return err.Error()
}
