package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestHandleStatus_UserPauseKind verifies that agent_status reports an explicit
// user pause ("user") in both the no-snapshot startup path and the normal
// snapshot path, preserving a.status.PauseKind.
func TestHandleStatus_UserPauseKind(t *testing.T) {
	newActor := func() *Actor {
		return &Actor{
			status: turnStatus{State: "paused", PauseKind: "user"},
		}
	}

	// Startup race: takeSnapshot has not run yet, so handleStatus must derive
	// PauseKind from a.status directly.
	ctx := testutil.HumanCtx(testutil.GenActorID())
	status, err := newActor().handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if status.State != "paused" {
		t.Errorf("State = %q, want paused", status.State)
	}
	if status.PauseKind != "user" {
		t.Errorf("PauseKind = %q, want user", status.PauseKind)
	}

	// Normal path: snapshot taken.
	a := newActor()
	a.takeSnapshot()
	status, err = a.handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if status.PauseKind != "user" {
		t.Errorf("PauseKind (snapshot) = %q, want user", status.PauseKind)
	}
}

// TestHandleStatus_TaskPauseKindFallback verifies that a paused agent with an
// empty PauseKind falls back to the task projection when pending task records
// exist (backward-compatible projection semantics), in both status paths.
func TestHandleStatus_TaskPauseKindFallback(t *testing.T) {
	newActor := func() *Actor {
		return &Actor{
			status: turnStatus{State: "paused"},
			RawSession: domain.RawSession{
				Tasks: []gen.TurnTask{{ID: "t1", Status: "pending"}},
			},
		}
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	status, err := newActor().handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if status.PauseKind != "task" {
		t.Errorf("PauseKind = %q, want task (fallback)", status.PauseKind)
	}

	a := newActor()
	a.takeSnapshot()
	status, err = a.handleStatus(ctx)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if status.PauseKind != "task" {
		t.Errorf("PauseKind (snapshot) = %q, want task (fallback)", status.PauseKind)
	}
}

// TestHandleStatus_PauseKindEmptyCases verifies that no pause kind is surfaced
// when none is derivable: paused with no pending tasks, or not paused at all.
func TestHandleStatus_PauseKindEmptyCases(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	cases := []struct {
		name string
		a    *Actor
	}{
		{"paused no tasks", &Actor{status: turnStatus{State: "paused"}}},
		{"running with kind", &Actor{status: turnStatus{State: "running", PauseKind: "user"}}},
		{"idle with kind", &Actor{status: turnStatus{PauseKind: "user"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, err := c.a.handleStatus(ctx)
			if err != nil {
				t.Fatalf("handleStatus: %v", err)
			}
			if status.PauseKind != "" {
				t.Errorf("PauseKind = %q, want empty", status.PauseKind)
			}

			c.a.takeSnapshot()
			status, err = c.a.handleStatus(ctx)
			if err != nil {
				t.Fatalf("handleStatus: %v", err)
			}
			if status.PauseKind != "" {
				t.Errorf("PauseKind (snapshot) = %q, want empty", status.PauseKind)
			}
		})
	}
}
