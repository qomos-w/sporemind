package im

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// fakePlanner is a fake actor.Planner that answers every Call via fn and
// counts the calls so tests can assert the coordinator fallback was (not)
// reached.
type fakePlanner struct {
	calls int
	fn    func(callID string, payload any) (any, error)
}

func (p *fakePlanner) Plan(_ ref.Ref, _ string, _ any, _ ...plan.Option) (plan.Node, error) {
	return nil, nil
}

func (p *fakePlanner) Call(_ context.Context, _ ref.Ref, callID string, payload any) *promise.Promise[any] {
	p.calls++
	result, err := p.fn(callID, payload)
	if err != nil {
		return promise.Reject[any](err)
	}
	return promise.Resolve[any](result)
}

func (p *fakePlanner) Stream(_ context.Context, _ ref.Ref, _ string, _ any, _ func(any) error) *promise.Promise[any] {
	return nil
}

// routeCtx returns a FakeCtx wired to a planner and a workspace service ref.
func routeCtx(planner actor.Planner) *testutil.FakeCtx {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner { return planner }
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "workspace" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	return ctx
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		name string
		in   string
		cmd  string
		arg  string
		ok   bool
	}{
		{"bind with arg", "/bind agent-1", "bind", "agent-1", true},
		{"status no arg", "/status", "status", "", true},
		{"command lowercased, arg preserved", "/Bind AgentX", "bind", "AgentX", true},
		{"inner arg spacing preserved", "/bind  agent one two", "bind", "agent one two", true},
		{"surrounding whitespace", "  /status  ", "status", "", true},
		{"plain text", "hello there", "", "", false},
		{"empty text", "", "", "", false},
		{"slash only", "/", "", "", false},
		{"space after slash", "/ bind", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, arg, ok := parseCommand(tc.in)
			if cmd != tc.cmd || arg != tc.arg || ok != tc.ok {
				t.Fatalf("parseCommand(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, cmd, arg, ok, tc.cmd, tc.arg, tc.ok)
			}
		})
	}
}

func TestRouteSetListDeleteRoundTrip(t *testing.T) {
	a := newTestActor(t)
	acc1, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "mybot", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	accID := acc1.Account.ID
	acc2, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "otherbot", Provider: "telegram", Token: "t2"})
	if err != nil {
		t.Fatal(err)
	}

	// Validation: required fields and known account.
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: accID}); err == nil {
		t.Fatal("expected error for missing agent actor id")
	}
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AgentActorID: "ag-1"}); err == nil {
		t.Fatal("expected error for missing account id")
	}
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: "ia_nope", AgentActorID: "ag-1"}); err == nil {
		t.Fatal("expected error for unknown account")
	}

	set1, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: accID, AgentActorID: "ag-1"})
	if err != nil {
		t.Fatal(err)
	}
	if set1.Route.ID == "" || set1.Route.AccountID != accID || set1.Route.AgentActorID != "ag-1" {
		t.Fatalf("unexpected set response: %+v", set1.Route)
	}
	if _, err := time.Parse(time.RFC3339, set1.Route.MountedAt); err != nil {
		t.Fatalf("MountedAt must be server-stamped UTC ISO, got %q: %v", set1.Route.MountedAt, err)
	}
	if !strings.HasSuffix(set1.Route.MountedAt, "Z") {
		t.Fatalf("MountedAt must be UTC, got %q", set1.Route.MountedAt)
	}

	// Idempotent remount (same account + same agent) keeps the route id and
	// re-stamps MountedAt.
	set2, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: accID, AgentActorID: "ag-1"})
	if err != nil {
		t.Fatal(err)
	}
	if set2.Route.ID != set1.Route.ID || set2.Route.AgentActorID != "ag-1" {
		t.Fatalf("idempotent remount must keep id and agent: %+v", set2.Route)
	}

	// A different account creates a second mount.
	set3, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc2.Account.ID, AgentActorID: "ag-2"})
	if err != nil {
		t.Fatal(err)
	}
	if set3.Route.ID == set1.Route.ID {
		t.Fatal("different account should create a new route id")
	}

	list, err := a.handleRouteList(nil, gen.ImRouteListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(list.Items), list.Items)
	}

	// Routes persist alongside the accounts.
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 2 || len(st.Accounts) != 2 {
		t.Fatalf("unexpected stored state: %d routes, %d accounts", len(st.Routes), len(st.Accounts))
	}

	// Delete (unmount) removes exactly one binding.
	if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: set2.Route.ID}); err != nil {
		t.Fatal(err)
	}
	list, err = a.handleRouteList(nil, gen.ImRouteListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != set3.Route.ID {
		t.Fatalf("expected only route %s after delete, got %+v", set3.Route.ID, list.Items)
	}

	// After unmounting, the account may mount a different agent.
	set4, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: accID, AgentActorID: "ag-3"})
	if err != nil {
		t.Fatal(err)
	}
	if set4.Route.ID == set3.Route.ID {
		t.Fatal("remount after unmount should create a new route id")
	}

	// Delete validation.
	if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: ""}); err == nil {
		t.Fatal("expected error for empty route id")
	}
	if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: "ir_nope"}); err == nil {
		t.Fatal("expected error for unknown route id")
	}
}

// TestRouteSetMountMutexes covers the exclusive-mount rejections: an account
// mounted on a different agent, and an agent occupied by another account.
func TestRouteSetMountMutexes(t *testing.T) {
	a := newTestActor(t)
	acc1, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "bot1", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	acc2, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "bot2", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}

	set1, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc1.Account.ID, AgentActorID: "ag-1"})
	if err != nil {
		t.Fatal(err)
	}

	// Same account, different agent → rejected with an unmount-first notice.
	_, err = a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc1.Account.ID, AgentActorID: "ag-2"})
	if err == nil || !strings.Contains(err.Error(), "账户已挂载") || !strings.Contains(err.Error(), "请先卸载") {
		t.Fatalf("retarget must be rejected with 账户已挂载/请先卸载, got %v", err)
	}

	// Different account, occupied agent → rejected with an agent-taken notice.
	_, err = a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc2.Account.ID, AgentActorID: "ag-1"})
	if err == nil || !strings.Contains(err.Error(), "agent 已被占用") {
		t.Fatalf("occupied agent must be rejected with agent 已被占用, got %v", err)
	}

	// The rejections changed nothing.
	list, err := a.handleRouteList(nil, gen.ImRouteListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].ID != set1.Route.ID {
		t.Fatalf("rejected sets must not change the route table: %+v", list.Items)
	}

	// Idempotent remount of the same pair stays allowed.
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc1.Account.ID, AgentActorID: "ag-1"}); err != nil {
		t.Fatalf("idempotent remount must be allowed: %v", err)
	}

	// After unmounting acc1, acc2 may take the agent.
	if _, err := a.handleRouteDelete(nil, gen.ImRouteDeleteReq{ID: set1.Route.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc2.Account.ID, AgentActorID: "ag-1"}); err != nil {
		t.Fatalf("freed agent must be mountable: %v", err)
	}
}

func TestResolveAgentActorIDBoundRoute(t *testing.T) {
	a := newTestActor(t)
	acc, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "mybot", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc.Account.ID, AgentActorID: "ag-bound"}); err != nil {
		t.Fatal(err)
	}

	p := &fakePlanner{fn: func(callID string, _ any) (any, error) {
		return nil, fmt.Errorf("planner must not be called for mounted accounts (got %s)", callID)
	}}
	// Account-level resolve: any chat id on the mounted account reaches the
	// same agent.
	for _, chatID := range []string{"42", "43", "private"} {
		got, err := a.resolveAgentActorID(routeCtx(p), acc.Account.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != "ag-bound" {
			t.Fatalf("resolveAgentActorID(chat %q) = %q, want %q", chatID, got, "ag-bound")
		}
	}
	if p.calls != 0 {
		t.Fatalf("planner called %d times for a mounted account", p.calls)
	}
}

func TestResolveAgentActorIDFallsBackToCoordinator(t *testing.T) {
	a := newTestActor(t)
	const coordID = "coord-actor-1"

	// Typed-struct result.
	p := &fakePlanner{fn: func(callID string, _ any) (any, error) {
		if callID != "workspace.coordinator_lookup" {
			return nil, fmt.Errorf("unexpected call %s", callID)
		}
		return gen.WorkspaceCoordinatorLookupResp{Found: true, Nickname: "coord", ActorID: coordID}, nil
	}}
	got, err := a.resolveAgentActorID(routeCtx(p), "ia_unbound")
	if err != nil {
		t.Fatal(err)
	}
	if got != coordID || p.calls != 1 {
		t.Fatalf("fallback = (%q, %d calls), want (%q, 1)", got, p.calls, coordID)
	}

	// Generic-map result (cross-actor decode path).
	p2 := &fakePlanner{fn: func(string, any) (any, error) {
		return map[string]interface{}{"Found": true, "ActorId": coordID}, nil
	}}
	if got, err := a.resolveAgentActorID(routeCtx(p2), "ia_unbound"); err != nil || got != coordID {
		t.Fatalf("map decode fallback = (%q, %v), want (%q, nil)", got, err, coordID)
	}

	// Raw-bytes result.
	p3 := &fakePlanner{fn: func(string, any) (any, error) {
		return []byte(`{"Found":true,"ActorId":"` + coordID + `"}`), nil
	}}
	if got, err := a.resolveAgentActorID(routeCtx(p3), "ia_unbound"); err != nil || got != coordID {
		t.Fatalf("bytes decode fallback = (%q, %v), want (%q, nil)", got, err, coordID)
	}
}

func TestResolveAgentActorIDFallbackFailures(t *testing.T) {
	a := newTestActor(t)

	// No workspace service registered.
	ctx := testutil.AnonCtx(testutil.GenActorID())
	if _, err := a.resolveAgentActorID(ctx, "ia_1"); err == nil {
		t.Fatal("expected error without workspace service")
	}

	// Coordinator not created yet (Found=false).
	p := &fakePlanner{fn: func(string, any) (any, error) {
		return gen.WorkspaceCoordinatorLookupResp{Found: false}, nil
	}}
	if _, err := a.resolveAgentActorID(routeCtx(p), "ia_1"); err == nil {
		t.Fatal("expected error when coordinator is not found")
	}

	// Upstream call failure.
	p2 := &fakePlanner{fn: func(string, any) (any, error) {
		return nil, errors.New("boom")
	}}
	if _, err := a.resolveAgentActorID(routeCtx(p2), "ia_1"); err == nil {
		t.Fatal("expected planner error to propagate")
	}

	// Undecodable payload.
	p3 := &fakePlanner{fn: func(string, any) (any, error) {
		return 42, nil
	}}
	if _, err := a.resolveAgentActorID(routeCtx(p3), "ia_1"); err == nil {
		t.Fatal("expected error for undecodable lookup payload")
	}
}

// TestConvergeRoutes covers the legacy-data migration: stores written before
// exclusive mounts may hold several routes per account; loading converges to
// the newest one so the mount mutexes never see stale duplicates.
func TestConvergeRoutes(t *testing.T) {
	older := gen.ImRoute{ID: "ir_old", AccountID: "ia_1", AgentActorID: "ag-old",
		MountedAt: "2026-01-01T00:00:00Z"}
	newer := gen.ImRoute{ID: "ir_new", AccountID: "ia_1", AgentActorID: "ag-new",
		MountedAt: "2026-02-01T00:00:00Z"}
	other := gen.ImRoute{ID: "ir_other", AccountID: "ia_2", AgentActorID: "ag-x"}

	// Newest by MountedAt wins regardless of insertion order.
	for _, routes := range [][]gen.ImRoute{{older, newer}, {newer, older}} {
		got := convergeRoutes(routes)
		if len(got) != 1 || got[0].ID != "ir_new" {
			t.Fatalf("convergeRoutes(%+v) = %+v, want only ir_new", routes, got)
		}
	}

	// Without comparable MountedAt the later insertion wins.
	legacyA := gen.ImRoute{ID: "ir_a", AccountID: "ia_1", AgentActorID: "ag-a"}
	legacyB := gen.ImRoute{ID: "ir_b", AccountID: "ia_1", AgentActorID: "ag-b"}
	if got := convergeRoutes([]gen.ImRoute{legacyA, legacyB}); len(got) != 1 || got[0].ID != "ir_b" {
		t.Fatalf("insertion-order fallback = %+v, want only ir_b", got)
	}

	// Distinct accounts keep their routes.
	got := convergeRoutes([]gen.ImRoute{older, other, newer})
	if len(got) != 2 {
		t.Fatalf("distinct accounts must be preserved: %+v", got)
	}
}

// TestLoadStoreConvergesLegacyDuplicateRoutes seeds a store with two routes
// for one account (legacy chat-era data) and verifies loadStore converges it
// silently — and that a subsequent mount on that account works without
// tripping the mutexes.
func TestLoadStoreConvergesLegacyDuplicateRoutes(t *testing.T) {
	a := newTestActor(t)
	acc, err := a.handleAccountCreate(nil, gen.ImAccountCreateReq{Name: "mybot", Provider: "telegram", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.saveStore(imStore{
		Accounts: storedAccounts(t, a),
		Routes: []gen.ImRoute{
			{ID: "ir_1", AccountID: acc.Account.ID, AgentActorID: "ag-old", MountedAt: "2026-01-01T00:00:00Z"},
			{ID: "ir_2", AccountID: acc.Account.ID, AgentActorID: "ag-new", MountedAt: "2026-02-01T00:00:00Z"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Routes) != 1 || st.Routes[0].ID != "ir_2" || st.Routes[0].AgentActorID != "ag-new" {
		t.Fatalf("loadStore must converge to the newest route, got %+v", st.Routes)
	}

	// Mounting the surviving agent again is an idempotent remount, not a
	// duplicate-driven rejection.
	if _, err := a.handleRouteSet(nil, gen.ImRouteSetReq{AccountID: acc.Account.ID, AgentActorID: "ag-new"}); err != nil {
		t.Fatalf("post-convergence remount failed: %v", err)
	}
	list, err := a.handleRouteList(nil, gen.ImRouteListReq{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected exactly 1 route after convergence + remount, got %+v", list.Items)
	}
}

// storedAccounts snapshots the actor's stored accounts for reseeding.
func storedAccounts(t *testing.T, a *Actor) []gen.ImAccount {
	t.Helper()
	st, err := a.loadStore()
	if err != nil {
		t.Fatal(err)
	}
	return st.Accounts
}
