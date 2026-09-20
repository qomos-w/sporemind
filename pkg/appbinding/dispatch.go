package appbinding

import (
	"context"
	"errors"
	"time"
)

type DispatchContext struct {
	AppID     string
	AgentID   string
	Role      string
	ProjectID string
	RequestID string
	SessionID string
	CallSeq   int64
}

type Dispatcher struct {
	Call  func(context.Context, string, any) (any, error)
	Audit func(AuditRecord)
}

// Invoke forwards the call to the registered Call closure and records an audit
// entry. It does not perform authorization: the caller (appmanager.invoke) is
// responsible for resolving identity, validating the callable descriptor, and
// checking callable.Permission against the host security policy before
// calling the dispatcher.
//
// Call failures are wrapped in a DiagnosticError carrying a stable DiagnosticCode
// so callers (frontends) can distinguish timeout from internal errors.
func (d Dispatcher) Invoke(ctx context.Context, binding DispatchContext, callable string, payload any) (any, error) {
	result, err := d.Call(ctx, callable, payload)
	if d.Audit != nil {
		record := AuditRecord{Time: time.Now(), RequestID: binding.RequestID, AppID: binding.AppID, AgentID: binding.AgentID, Role: binding.Role, ProjectID: binding.ProjectID, Callable: callable, Allowed: true, SessionID: binding.SessionID, CallSeq: binding.CallSeq}
		if err != nil {
			record.Reason = err.Error()
		}
		d.Audit(record)
	}
	if err != nil {
		return result, diagnosticFromCall(err)
	}
	return result, nil
}

// diagnosticFromCall classifies a Call-closure error into a DiagnosticError.
// A wrapped or direct context.DeadlineExceeded maps to DiagInvokeTimeout;
// anything else is DiagInternalError.
func diagnosticFromCall(err error) *DiagnosticError {
	code := DiagInternalError
	if errors.Is(err, context.DeadlineExceeded) {
		code = DiagInvokeTimeout
	}
	return &DiagnosticError{Code: code, Message: err.Error(), Cause: err}
}
