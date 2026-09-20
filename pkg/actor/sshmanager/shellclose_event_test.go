package sshmanager

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleShellCloseEmitsCloseSession pins the multi-client alignment rule:
// an explicit shell_close broadcasts a close_session event so every connected
// frontend removes the tab pointing at that session. Natural session death
// (SSH drop) deliberately does not emit — other clients keep the tab for the
// reconnect affordance — which is covered by not emitting anywhere in the
// shell.Wait goroutine.
func TestHandleShellCloseEmitsCloseSession(t *testing.T) {
	sess := &session{
		id:        "sess-close-1",
		hostID:    "host-1",
		hostName:  "prod",
		connected: true,
	}
	a := &Actor{sessions: map[string]*session{"sess-close-1": sess}}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Role: "admin", Subject: "user-1"}}

	if _, err := a.handleShellClose(ctx, domain.SshShellCloseReq{SessionID: "sess-close-1"}); err != nil {
		t.Fatalf("handleShellClose failed: %v", err)
	}

	events := ctx.EmittedEventsSnapshot()
	if len(events) != 1 {
		t.Fatalf("emitted %d events, want 1: %+v", len(events), events)
	}
	ev := events[0]
	if ev.Kind != "ssh_manager_event" {
		t.Fatalf("event kind = %q, want ssh_manager_event", ev.Kind)
	}
	sshEv, ok := ev.Payload.(domain.SshManagerEvent)
	if !ok {
		t.Fatalf("payload type = %T, want domain.SshManagerEvent", ev.Payload)
	}
	if sshEv.Kind != "close_session" {
		t.Fatalf("ssh event kind = %q, want close_session", sshEv.Kind)
	}
	if sshEv.SessionID != "sess-close-1" || sshEv.HostID != "host-1" || sshEv.HostName != "prod" {
		t.Fatalf("ssh event = %+v, want session/host identity preserved", sshEv)
	}

	// The closed session must be gone from the actor map.
	if _, exists := a.sessions["sess-close-1"]; exists {
		t.Fatal("session still present in actor map after close")
	}
}

// TestHandleShellCloseRejectsUnknownSession guards the no-event path: a close
// for an unknown session errors and emits nothing (clients never see phantom
// close_session events for sessions they never had).
func TestHandleShellCloseRejectsUnknownSession(t *testing.T) {
	a := &Actor{sessions: map[string]*session{}}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Role: "admin", Subject: "user-1"}}

	if _, err := a.handleShellClose(ctx, domain.SshShellCloseReq{SessionID: "nope"}); err == nil {
		t.Fatal("expected error closing unknown session")
	}
	if events := ctx.EmittedEventsSnapshot(); len(events) != 0 {
		t.Fatalf("emitted %d events for unknown session, want 0", len(events))
	}
}
