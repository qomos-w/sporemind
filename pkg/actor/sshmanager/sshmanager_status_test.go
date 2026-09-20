package sshmanager

import (
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// agentCtx is the agent-role counterpart of adminCtx/anonCtx: status_get and
// status_list were switched from requireSSHCaller (human-only) to
// requireSSHToolCaller so an agent can monitor remote hosts. These tests
// guard that switch against an accidental revert.
func agentCtx() sshTestCtx {
	return sshTestCtx{ident: id.Identity{Role: "agent"}}
}

func TestStatusGetRejectsAnonymous(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleStatusGet(anonCtx(), domain.SshStatusReq{HostID: "h1"}); err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

func TestStatusGetAcceptsAgentRole(t *testing.T) {
	a := newTestActor(t)
	// Agent role passes the auth gate and reaches host lookup; no host is
	// configured, so the handler returns a not-found error without dialing.
	if _, err := a.handleStatusGet(agentCtx(), domain.SshStatusReq{HostID: "missing"}); err == nil {
		t.Fatal("expected host-not-found error for agent caller (auth gate bypassed)")
	}
}

func TestStatusListRejectsAnonymous(t *testing.T) {
	a := newTestActor(t)
	if _, err := a.handleStatusList(anonCtx(), domain.SshStatusListReq{}); err == nil {
		t.Fatal("anonymous caller must be rejected")
	}
}

func TestStatusListAcceptsAgentRole(t *testing.T) {
	a := newTestActor(t)
	// Agent role passes the auth gate; with no hosts configured the handler
	// returns an empty list without dialing any host.
	resp, err := a.handleStatusList(agentCtx(), domain.SshStatusListReq{})
	if err != nil {
		t.Fatalf("agent caller rejected: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected empty status list, got %v", resp.Items)
	}
}
