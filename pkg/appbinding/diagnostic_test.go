package appbinding

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDiagnosticErrorFormatting(t *testing.T) {
	e := &DiagnosticError{Code: DiagCapabilityDenied, Message: "nope"}
	if got, want := e.Error(), "capability_denied: nope"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	// Empty message is still joined with the separator per Error() impl.
	if got := (&DiagnosticError{Code: DiagInvokeTimeout}).Error(); got != "invoke_timeout: " {
		t.Fatalf("empty-message Error() = %q", got)
	}
	// String form of the code itself equals the documented constant.
	if got := string(DiagCallableNotFound); got != "callable_not_found" {
		t.Fatalf("DiagCallableNotFound string = %q", got)
	}
}

func TestDiagnosticErrorUnwrapExposesCause(t *testing.T) {
	cause := errors.New("boom")
	e := &DiagnosticError{Code: DiagInternalError, Message: "x", Cause: cause}
	if !errors.Is(e, cause) {
		t.Fatal("errors.Is should reach cause via Unwrap")
	}
	var target *DiagnosticError
	if !errors.As(e, &target) {
		t.Fatal("errors.As should match DiagnosticError")
	}
	// The underlying DeniedError must remain reachable so existing callers
	// that use DenialCode keep working.
	denied := Deny(CodeCallableDenied, "answer")
	wrapped := &DiagnosticError{Code: DiagCallableNotFound, Cause: denied}
	var d *DeniedError
	if !errors.As(wrapped, &d) {
		t.Fatal("errors.As should reach underlying DeniedError via Unwrap")
	}
	if d.Code != CodeCallableDenied {
		t.Fatalf("underlying code = %q, want %q", d.Code, CodeCallableDenied)
	}
	if DenialCode(wrapped) != CodeCallableDenied {
		t.Fatal("DenialCode should still resolve through DiagnosticError cause")
	}
}

func TestDiagnosticOf(t *testing.T) {
	if got := DiagnosticOf(nil); got != "" {
		t.Fatalf("DiagnosticOf(nil) = %q, want \"\"", got)
	}
	if got := DiagnosticOf(errors.New("plain")); got != "" {
		t.Fatalf("DiagnosticOf(plain) = %q, want \"\"", got)
	}
	if got := DiagnosticOf(&DiagnosticError{Code: DiagInvokeTimeout}); got != DiagInvokeTimeout {
		t.Fatalf("DiagnosticOf = %q, want %q", got, DiagInvokeTimeout)
	}
	// A wrapped DiagnosticError must still be extractable.
	wrapped := fmt.Errorf("outer: %w", &DiagnosticError{Code: DiagProjectMismatch})
	if got := DiagnosticOf(wrapped); got != DiagProjectMismatch {
		t.Fatalf("DiagnosticOf(wrapped) = %q, want %q", got, DiagProjectMismatch)
	}
}

func TestNewDiagnosticErrorPreservesCause(t *testing.T) {
	cause := Deny(CodePermissionDenied, "fs.write not allowed")
	de := NewDiagnosticError(DiagCapabilityDenied, cause.Error(), cause)
	if de.Code != DiagCapabilityDenied {
		t.Fatalf("code = %q", de.Code)
	}
	if !errors.Is(de, cause) {
		t.Fatal("errors.Is should reach the DeniedError cause")
	}
	if DenialCode(de) != CodePermissionDenied {
		t.Fatalf("DenialCode = %q, want %q", DenialCode(de), CodePermissionDenied)
	}
}

func TestDiagnosticFromDenialMapping(t *testing.T) {
	cases := []struct {
		code string
		want DiagnosticCode
	}{
		{CodeAgentScopeDenied, DiagAgentNotAuthorized},
		{CodeCallableDenied, DiagCallableNotFound},
		{CodeProjectScopeDenied, DiagProjectMismatch},
		{CodeRoleDenied, DiagCapabilityDenied},
		{CodeIdentityIncomplete, DiagCapabilityDenied},
		{CodeBindingMissing, DiagCapabilityDenied},
		{CodeFreeAgentDenied, DiagCapabilityDenied},
		{CodePermissionDenied, DiagCapabilityDenied},
		{"appbinding.unknown", DiagCapabilityDenied},
		{"", DiagCapabilityDenied},
	}
	for _, c := range cases {
		if got := diagnosticFromDenial(c.code); got != c.want {
			t.Errorf("diagnosticFromDenial(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// CapabilityBinding.Authorize is the only path that can produce
// CodeAgentScopeDenied. Verify the agent-scope denial maps to
// DiagAgentNotAuthorized end to end.
func TestCapabilityBindingAgentScopeMapsToAgentNotAuthorized(t *testing.T) {
	b := CapabilityBinding{AppID: "app", AgentID: "agent-a", Role: "coder", Callables: map[string]struct{}{"ping": {}}}
	err := b.Authorize("agent-b", "coder", "", "ping")
	if err == nil {
		t.Fatal("expected agent-scope denial")
	}
	if code := DenialCode(err); code != CodeAgentScopeDenied {
		t.Fatalf("fine-grained code = %q, want %q", code, CodeAgentScopeDenied)
	}
	if got := diagnosticFromDenial(DenialCode(err)); got != DiagAgentNotAuthorized {
		t.Fatalf("diagnostic = %q, want %q", got, DiagAgentNotAuthorized)
	}
}

func TestDispatcherInvokeTimeoutDiagnostic(t *testing.T) {
	d := Dispatcher{Call: func(context.Context, string, any) (any, error) {
		return nil, context.DeadlineExceeded
	}}
	_, err := d.Invoke(context.Background(), DispatchContext{AppID: "app", AgentID: "agent", Role: "coder", ProjectID: "proj"}, "ping", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if code := DiagnosticOf(err); code != DiagInvokeTimeout {
		t.Fatalf("code = %q, want %q (%v)", code, DiagInvokeTimeout, err)
	}
}

// Mirrors the real appmanager closure which wraps the timeout sentinel.
func TestDispatcherInvokeWrappedTimeoutDiagnostic(t *testing.T) {
	d := Dispatcher{Call: func(context.Context, string, any) (any, error) {
		return nil, fmt.Errorf("invoke timeout after 30s: %w", context.DeadlineExceeded)
	}}
	_, err := d.Invoke(context.Background(), DispatchContext{AppID: "app", AgentID: "agent", Role: "coder", ProjectID: "proj"}, "ping", nil)
	if code := DiagnosticOf(err); code != DiagInvokeTimeout {
		t.Fatalf("wrapped-timeout code = %q, want %q (%v)", code, DiagInvokeTimeout, err)
	}
}

func TestDispatcherInvokeInternalErrorDiagnostic(t *testing.T) {
	d := Dispatcher{Call: func(context.Context, string, any) (any, error) {
		return nil, errors.New("boom")
	}}
	_, err := d.Invoke(context.Background(), DispatchContext{AppID: "app", AgentID: "agent", Role: "coder", ProjectID: "proj"}, "ping", nil)
	if err == nil {
		t.Fatal("expected internal error")
	}
	if code := DiagnosticOf(err); code != DiagInternalError {
		t.Fatalf("code = %q, want %q (%v)", code, DiagInternalError, err)
	}
}