package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestResolveBundleCallableIDs_MixedSources verifies the core gap fix:
// resolveBundleCallableIDs must resolve BOTH builtin bundles (embedded assets)
// and virtual app-bundle components (appmanager.component_get tool
// contributions), returning the union — where the old agentkit
// CallableIDsForBundles only ever saw the embedded asset source and silently
// dropped app card IDs.
func TestResolveBundleCallableIDs_MixedSources(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "appmanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
				if callID != "appmanager.component_get" {
					return nil, fmt.Errorf("unexpected call %s", callID)
				}
				var req gen.AppManagerComponentGetReq
				switch p := payload.(type) {
				case gen.AppManagerComponentGetReq:
					req = p
				case []byte:
					_ = json.Unmarshal(p, &req)
				}
				if req.CardID != "app-bundle:testapp:tools" {
					return nil, fmt.Errorf("unexpected card %q", req.CardID)
				}
				return gen.AppManagerComponentGetResp{
					Component: domain.ComponentDescriptor{
						Ref:   domain.ComponentRef{CardID: "app-bundle:testapp:tools", Kind: "bundle", Source: "appmanager"},
						Title: "Test App Tools",
						Tools: []domain.ComponentToolContribution{
							{ID: "t1", CardID: "app-bundle:testapp:tools", CallableID: "app.testapp.first"},
							{ID: "t2", CardID: "app-bundle:testapp:tools", CallableID: "app.testapp.second"},
						},
					},
				}, nil
			},
		}
	}

	// builtin:bundle:project-wiki resolves from embedded assets; the virtual
	// app-bundle component resolves via the fake appmanager.component_get. The
	// union must contain tools from both sources, deduped.
	got := a.resolveBundleCallableIDs(ctx, []string{
		"builtin:bundle:project-wiki",
		"app-bundle:testapp:tools",
	})

	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, want := range []string{"app.testapp.first", "app.testapp.second"} {
		if !seen[want] {
			t.Errorf("resolveBundleCallableIDs missing project bundle callable %q; got %v", want, got)
		}
	}
	// The builtin project-wiki bundle contributes its own callables; assert at
	// least one known entry survives (project.wiki_get_card is declared in the
	// embedded project-wiki bundle card).
	if !seen["project.wiki_get_card"] {
		t.Errorf("resolveBundleCallableIDs dropped builtin bundle callables; got %v", got)
	}
	// The two app callables plus the builtin union must all be present; ensure
	// dedup keeps length consistent (no duplicate app callables).
	appCount := 0
	for _, id := range got {
		if id == "app.testapp.first" || id == "app.testapp.second" {
			appCount++
		}
	}
	if appCount != 2 {
		t.Errorf("project bundle callables not deduped: got %d occurrences, want 2 (%v)", appCount, got)
	}
}

// TestResolveBundleCallableIDs_ProjectUnreachable verifies the fail-safe: when
// the project cannot be reached (no parent/planner), non-builtin bundle IDs are
// silently skipped and the builtin-only result is preserved — exactly the old
// agentkit behavior, so a stale DefaultBundleIDs entry never breaks the tool
// surface.
func TestResolveBundleCallableIDs_ProjectUnreachable(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	// No ParentRef, no PlannerFn: componentDescriptor returns (zero,false) for
	// the project card, which must be silently skipped.

	got := a.resolveBundleCallableIDs(ctx, []string{
		"builtin:bundle:project-wiki",
		"app-bundle:testapp:tools",
	})

	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	for _, banned := range []string{"app.testapp.first", "app.testapp.second"} {
		if seen[banned] {
			t.Errorf("project bundle callable %q leaked when project unreachable: %v", banned, got)
		}
	}
	if !seen["project.wiki_get_card"] {
		t.Errorf("builtin bundle callables must survive when project unreachable; got %v", got)
	}
}

// TestResolveBundleCallableIDs_UnknownCardSkipped verifies that a project card
// that does not resolve (card missing) is silently skipped, preserving the
// existing behavior for unknown IDs.
func TestResolveBundleCallableIDs_UnknownCardSkipped(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AnonCtx(testutil.GenActorID())
	ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "project.component_get" {
					// Card missing: return an empty component (decode yields ok=false).
					return domain.ProjectComponentGetResp{Component: domain.ComponentDescriptor{}}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	got := a.resolveBundleCallableIDs(ctx, []string{
		"builtin:bundle:project-wiki",
		"app-bundle:missing:tools",
	})

	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	if !seen["project.wiki_get_card"] {
		t.Errorf("builtin callables must be present; got %v", got)
	}
	for _, id := range got {
		if id == "app.testapp.first" || id == "app.testapp.second" {
			t.Errorf("missing card's callables must not appear; got %v", got)
		}
	}
}
