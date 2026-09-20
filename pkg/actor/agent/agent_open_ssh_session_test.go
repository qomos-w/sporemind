package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// openSshSessionTestHarness builds a FakeCtx whose planner records the
// cross-actor calls made by handleOpenSshSession and dispatches canned
// responses per callable id.
func openSshSessionTestHarness(t *testing.T, respond func(callID string) (any, error)) (*testutil.FakeCtx, *[]string) {
	t.Helper()
	var calls []string
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "sshmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				calls = append(calls, callID)
				return respond(callID)
			},
		}
	}
	return ctx, &calls
}

func agentOpenSshSession(ctx *testutil.FakeCtx, hostID string) gen.AgentOpenSshSessionResp {
	resp, err := (&Actor{}).handleOpenSshSession(ctx, gen.AgentOpenSshSessionReq{HostID: hostID})
	if err != nil {
		// Error transport is via the Error field; a Go error is a bug.
		return gen.AgentOpenSshSessionResp{Error: fmt.Sprintf("unexpected go error: %v", err)}
	}
	return resp
}

func TestHandleOpenSshSessionReusesExistingSession(t *testing.T) {
	ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
		switch callID {
		case "sshmanager.session_list":
			return domain.SshSessionListResp{Items: []domain.SshSessionInfo{
				{SessionID: "sess-human", HostID: "host-1", Connected: true},
				{SessionID: "sess-agent", HostID: "host-1", Connected: false},
			}}, nil
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	})

	resp := agentOpenSshSession(ctx, "host-1")

	if resp.SessionID != "sess-human" {
		t.Fatalf("expected reused session id, got %q", resp.SessionID)
	}
	if resp.HostID != "host-1" || !resp.Connected {
		t.Fatalf("expected HostID host-1 and Connected=true, got %+v", resp)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected error: %s", resp.Error)
	}
	if len(*calls) != 1 || (*calls)[0] != "sshmanager.session_list" {
		t.Fatalf("reuse path must only call session_list, no shell_open; got calls %v", *calls)
	}
}

func TestHandleOpenSshSessionOpensWhenNoReusableSession(t *testing.T) {
	t.Run("empty session list", func(t *testing.T) {
		ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
			switch callID {
			case "sshmanager.session_list":
				return domain.SshSessionListResp{}, nil
			case "sshmanager.shell_open":
				return domain.SshShellOpenResp{SessionID: "sess-new", Connected: true}, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		})

		resp := agentOpenSshSession(ctx, "host-1")

		if resp.SessionID != "sess-new" || !resp.Connected || resp.Error != "" {
			t.Fatalf("unexpected response: %+v", resp)
		}
		if len(*calls) != 2 || (*calls)[1] != "sshmanager.shell_open" {
			t.Fatalf("expected session_list then shell_open, got calls %v", *calls)
		}
	})

	t.Run("session on other host only", func(t *testing.T) {
		ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
			switch callID {
			case "sshmanager.session_list":
				return domain.SshSessionListResp{Items: []domain.SshSessionInfo{
					{SessionID: "sess-other", HostID: "host-2", Connected: true},
				}}, nil
			case "sshmanager.shell_open":
				return domain.SshShellOpenResp{SessionID: "sess-new", Connected: true}, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		})

		resp := agentOpenSshSession(ctx, "host-1")

		if resp.SessionID != "sess-new" {
			t.Fatalf("expected new session, got %q", resp.SessionID)
		}
		if len(*calls) != 2 || (*calls)[1] != "sshmanager.shell_open" {
			t.Fatalf("expected session_list then shell_open, got calls %v", *calls)
		}
	})

	t.Run("disconnected session on host", func(t *testing.T) {
		ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
			switch callID {
			case "sshmanager.session_list":
				return domain.SshSessionListResp{Items: []domain.SshSessionInfo{
					{SessionID: "sess-dead", HostID: "host-1", Connected: false},
				}}, nil
			case "sshmanager.shell_open":
				return domain.SshShellOpenResp{SessionID: "sess-new", Connected: true}, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		})

		resp := agentOpenSshSession(ctx, "host-1")

		if resp.SessionID != "sess-new" {
			t.Fatalf("disconnected session must not be reused, got %q", resp.SessionID)
		}
		if len(*calls) != 2 {
			t.Fatalf("expected exactly 2 calls, got %v", *calls)
		}
	})
}

func TestHandleOpenSshSessionSessionListError(t *testing.T) {
	ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
		switch callID {
		case "sshmanager.session_list":
			return nil, fmt.Errorf("forbidden")
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	})

	resp := agentOpenSshSession(ctx, "host-1")

	if resp.Error == "" {
		t.Fatal("expected error field on session_list failure, got empty")
	}
	if len(*calls) != 1 {
		t.Fatalf("session_list failure must not fall through to shell_open, got calls %v", *calls)
	}
}

func TestHandleOpenSshSessionUnexpectedListType(t *testing.T) {
	ctx, _ := openSshSessionTestHarness(t, func(callID string) (any, error) {
		switch callID {
		case "sshmanager.session_list":
			return "not-a-list-response", nil
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	})

	resp := agentOpenSshSession(ctx, "host-1")

	if resp.Error == "" {
		t.Fatal("expected error field on unexpected session_list response, got empty")
	}
}

func TestHandleOpenSshSessionEmptyHostID(t *testing.T) {
	ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
		return nil, fmt.Errorf("unexpected call %s", callID)
	})

	resp := agentOpenSshSession(ctx, "")

	if resp.Error == "" {
		t.Fatal("expected error field for empty hostId, got empty")
	}
	if len(*calls) != 0 {
		t.Fatalf("empty hostId must not trigger any call, got %v", *calls)
	}
}

func TestHandleOpenSshSessionShellOpenError(t *testing.T) {
	ctx, calls := openSshSessionTestHarness(t, func(callID string) (any, error) {
		switch callID {
		case "sshmanager.session_list":
			return domain.SshSessionListResp{}, nil
		case "sshmanager.shell_open":
			return nil, fmt.Errorf("host not found")
		}
		return nil, fmt.Errorf("unexpected call %s", callID)
	})

	resp := agentOpenSshSession(ctx, "host-1")

	if resp.Error == "" {
		t.Fatal("expected error field on shell_open failure, got empty")
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 calls, got %v", *calls)
	}
}
