package appbinding

import (
	"context"
	"errors"
	"testing"
)

func TestDispatcherForwardsAndAudits(t *testing.T) {
	called := false
	var audit []AuditRecord
	d := Dispatcher{
		Audit: func(record AuditRecord) { audit = append(audit, record) },
		Call:  func(context.Context, string, any) (any, error) { called = true; return "ok", nil },
	}
	res, err := d.Invoke(context.Background(), DispatchContext{AppID: "app", AgentID: "agent", Role: "coder", ProjectID: "project", RequestID: "req-1", SessionID: "sess-1", CallSeq: 7}, "ping", nil)
	if err != nil {
		t.Fatalf("dispatch should succeed: %v", err)
	}
	if !called {
		t.Fatal("call was not dispatched")
	}
	if got, _ := res.(string); got != "ok" {
		t.Fatalf("result = %v, want ok", got)
	}
	if len(audit) != 1 || !audit[0].Allowed || audit[0].RequestID != "req-1" || audit[0].SessionID != "sess-1" || audit[0].CallSeq != 7 {
		t.Fatalf("unexpected audit records: %+v", audit)
	}
}

func TestDispatcherWrapsTimeout(t *testing.T) {
	d := Dispatcher{
		Call: func(context.Context, string, any) (any, error) { return nil, context.DeadlineExceeded },
	}
	_, err := d.Invoke(context.Background(), DispatchContext{AppID: "app"}, "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if DiagnosticOf(err) != DiagInvokeTimeout {
		t.Fatalf("expected DiagInvokeTimeout, got %v", DiagnosticOf(err))
	}
}

func TestDispatcherWrapsInternalError(t *testing.T) {
	d := Dispatcher{
		Call: func(context.Context, string, any) (any, error) { return nil, errors.New("boom") },
	}
	_, err := d.Invoke(context.Background(), DispatchContext{AppID: "app"}, "ping", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if DiagnosticOf(err) != DiagInternalError {
		t.Fatalf("expected DiagInternalError, got %v", DiagnosticOf(err))
	}
}
