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

// TestAppBundleChain_AuthenticatorMountRevealsTools is the S4 agent-side chain
// segment of the authenticator e2e. It wires the exact card identity appmanager
// publishes for the authenticator appdef bundle (app-bundle:app.authenticator:
// authenticator-tools) through the full resolveAppTools path:
//
//   - cardRefs holds the published app-bundle card (component_mount /
//     DefaultBundleIDs both land here) →
//     appToolSpecsFromCatalog must expose ONLY the bundle's four callables
//     (provision/codes/verify/remove); the app's fifth callable (update) stays
//     out because it is not in the bundle.
//   - declared but not mounted → the bundle-declaring app contributes zero
//     tools.
//   - no app-bundle card in the catalog → zero tools (fail-closed: apps are
//     visible only through mounted bundles).
func TestAppBundleChain_AuthenticatorMountRevealsTools(t *testing.T) {
	const bundleCard = "app-bundle:app.authenticator:authenticator-tools"
	appList := gen.AppManagerListResp{Items: []gen.AppStatus{
		{
			ID:    "app.authenticator",
			State: "running",
			Callables: []gen.AppCallableDescriptor{
				{ID: "provision", RequestSchema: `{"type":"object"}`},
				{ID: "codes", RequestSchema: `{"type":"object"}`},
				{ID: "verify", RequestSchema: `{"type":"object"}`},
				{ID: "remove", RequestSchema: `{"type":"object"}`},
				// update exists on the app but is intentionally NOT part of the
				// authenticator bundle: it must never surface as a tool.
				{ID: "update", RequestSchema: `{"type":"object"}`},
			},
		},
	}}

	bundleDescriptor := domain.ComponentDescriptor{
		Ref:   domain.ComponentRef{CardID: bundleCard, Kind: "bundle", Source: "appmanager"},
		Title: "Authenticator Tools",
		Tools: []domain.ComponentToolContribution{
			{ID: "t1", CardID: bundleCard, CallableID: "app.app.authenticator.provision"},
			{ID: "t2", CardID: bundleCard, CallableID: "app.app.authenticator.codes"},
			{ID: "t3", CardID: bundleCard, CallableID: "app.app.authenticator.verify"},
			{ID: "t4", CardID: bundleCard, CallableID: "app.app.authenticator.remove"},
		},
	}

	fake := func(catalogHasBundle bool) actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
			switch callID {
			case "appmanager.list":
				return appList, nil
			case "appmanager.component_list":
				if !catalogHasBundle {
					return gen.AppManagerComponentListResp{Items: nil}, nil
				}
				return gen.AppManagerComponentListResp{Items: []domain.ComponentDescriptor{bundleDescriptor}}, nil
			case "appmanager.component_get":
				var req gen.AppManagerComponentGetReq
				switch p := payload.(type) {
				case gen.AppManagerComponentGetReq:
					req = p
				case []byte:
					_ = json.Unmarshal(p, &req)
				}
				if req.CardID != bundleCard {
					return nil, fmt.Errorf("unexpected card %q", req.CardID)
				}
				return gen.AppManagerComponentGetResp{Component: bundleDescriptor}, nil
			}
			return nil, fmt.Errorf("unexpected call %s", callID)
		}}
	}

	bundleCallables := map[string]bool{
		"app.app.authenticator.provision": true,
		"app.app.authenticator.codes":     true,
		"app.app.authenticator.verify":    true,
		"app.app.authenticator.remove":    true,
	}

	scenarios := []struct {
		name             string
		cardRefs         []gen.CardRef
		catalogHasBundle bool
		want             map[string]bool
		notWant          map[string]bool
	}{
		{
			name:             "bundle card mounted → only bundle callables appear",
			cardRefs:         []gen.CardRef{{ID: bundleCard}},
			catalogHasBundle: true,
			want:             bundleCallables,
			notWant:          map[string]bool{"app.app.authenticator.update": true},
		},
		{
			name:             "bundle declared but not mounted → zero tools",
			cardRefs:         nil,
			catalogHasBundle: true,
			want:             map[string]bool{},
			notWant:          map[string]bool{"app.app.authenticator.provision": true, "app.app.authenticator.update": true},
		},
		{
			name:             "no app-bundle card → zero tools (fail-closed)",
			cardRefs:         nil,
			catalogHasBundle: false,
			want:             map[string]bool{},
			notWant: map[string]bool{
				"app.app.authenticator.provision": true,
				"app.app.authenticator.codes":     true,
				"app.app.authenticator.verify":    true,
				"app.app.authenticator.remove":    true,
				"app.app.authenticator.update":    true,
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			a := &Actor{cardRefs: sc.cardRefs}
			ctx := testutil.AnonCtx(testutil.GenActorID())
			ctx.ParentRef = testutil.NewFakeRef(testutil.GenActorID(), nil)
			ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
				if name == "appmanager" {
					return testutil.NewFakeRef(testutil.GenActorID(), nil), true
				}
				return nil, false
			}
			ctx.PlannerFn = func() actor.Planner { return fake(sc.catalogHasBundle) }

			specs := a.resolveAppTools(ctx)
			seen := make(map[string]bool, len(specs))
			for _, s := range specs {
				seen[s.CallableID] = true
			}
			for id := range sc.want {
				if !seen[id] {
					t.Errorf("missing expected callable %q in %v", id, seen)
				}
			}
			for id := range sc.notWant {
				if seen[id] {
					t.Errorf("unexpected excluded callable %q present in %v", id, seen)
				}
			}
			if len(sc.want) == 0 && len(specs) != 0 {
				t.Errorf("expected zero tools, got %d: %v", len(specs), seen)
			}
		})
	}
}
