package agent

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func mountedCardIDs(a *Actor) map[string]bool {
	out := make(map[string]bool)
	for _, m := range a.ComponentMounts {
		out[m.CardID] = m.Enabled
	}
	return out
}

func TestHandleBindApp_ReconcilesMounts(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	a := &Actor{agentKind: domain.AgentKindPlugin}

	// First bind: two desired bundles.
	err := a.handleBindApp(ctx, gen.AgentBindAppReq{
		AppID:         "app.x",
		SystemPrompt:  "Operate app.x.",
		BundleCardIds: []string{"app-bundle:app.x:main", "builtin:bundle:web-search"},
	})
	if err != nil {
		t.Fatalf("first bind: %v", err)
	}
	if a.BoundAppID() != "app.x" || a.BoundAppPrompt() != "Operate app.x." {
		t.Fatalf("binding fields = %q/%q", a.BoundAppID(), a.BoundAppPrompt())
	}
	mounted := mountedCardIDs(a)
	if !mounted["app-bundle:app.x:main"] || !mounted["builtin:bundle:web-search"] {
		t.Fatalf("after first bind: mounts = %v", mounted)
	}

	// Second bind with a changed list: keep main, drop web-search, add extra.
	err = a.handleBindApp(ctx, gen.AgentBindAppReq{
		AppID:         "app.x",
		BundleCardIds: []string{"app-bundle:app.x:main", "app-bundle:app.x:extra"},
	})
	if err != nil {
		t.Fatalf("second bind: %v", err)
	}
	mounted = mountedCardIDs(a)
	if !mounted["app-bundle:app.x:main"] || !mounted["app-bundle:app.x:extra"] {
		t.Fatalf("after second bind: mounts = %v", mounted)
	}
	if mounted["builtin:bundle:web-search"] {
		t.Fatalf("web-search should be unmounted after it left the desired list: %v", mounted)
	}
}

func TestHandleBindApp_RejectsNonPluginKindAndEmptyApp(t *testing.T) {
	ctx := testutil.AnonCtx(testutil.GenActorID())
	coder := &Actor{agentKind: domain.AgentKindCoder}
	if err := coder.handleBindApp(ctx, gen.AgentBindAppReq{AppID: "app.x"}); err == nil {
		t.Fatal("expected error binding a non-plugin agent kind")
	}
	plugin := &Actor{agentKind: domain.AgentKindPlugin}
	if err := plugin.handleBindApp(ctx, gen.AgentBindAppReq{}); err == nil {
		t.Fatal("expected error for empty AppId")
	}
}
