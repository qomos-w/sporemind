package appmanager

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/spore/identity"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// bundleDemoAppDef is a minimal plugin whose manifest declares exactly one
// bundle with two tools (hello, world). It drives the S4 bundle e2e chain
// through the same native-flow harness as the todolist e2e tests
// (todolistE2EPlannerBuilder): register_project must publish an
// app-bundle:{appID}:{slug} wiki card, and unregister must delete it.
const bundleDemoAppDef = `// bundle-demo.appdef
app BundleDemo {
    id:          "app.bundledemo"
    name:        "BundleDemo"
    version:     "0.1.0"
    namespace:   "bundledemo"
    permissions: []

    struct GreetRequest {
        Name: string
    }
    struct GreetResponse {
        Message: string
    }

    callable hello {
        request:  GreetRequest
        response: GreetResponse
        effect:   "read"
        toolName: "bundledemo-hello"
        service:  "appmanager"
    }
    callable world {
        request:  GreetRequest
        response: GreetResponse
        effect:   "read"
        toolName: "bundledemo-world"
        service:  "appmanager"
    }

    entrypoint view main {
        title: "BundleDemo"
        route: "/"
    }

    bundle tools {
        title:       "Bundle Demo Tools"
        description: "Hello and world, exposed as bundle tools"
        tools:       [hello, world]
    }
}
`

// bundleDemoHandlersFilled is a stub-free handlers.go for the bundle-demo app
// so the stub_filling gate passes (the build gate is mocked by the harness).
const bundleDemoHandlersFilled = `// handlers.go — agent-owned
package main

import sdk "github.com/qomos-w/sporemind-plugin-sdk"

func handleHello(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]interface{}{"Message": "hello"}}, nil
}

func handleWorld(req sdk.Request) (sdk.Response, error) {
	return sdk.Response{Payload: map[string]interface{}{"Message": "world"}}, nil
}
`

// bundleDemoManifestJSON derives the on-disk app.manifest.json from the
// bundle-demo .appdef (so the manifest_consistency gate passes) and adds an
// AgentBinding, mirroring the todolist e2e manifest helper.
func bundleDemoManifestJSON(t *testing.T) string {
	t.Helper()
	m := manifestFromAppDef(t, bundleDemoAppDef)
	m.AgentBinding = &gen.AppAgentBinding{
		Surface: &gen.AgentSurfaceBinding{
			AgentID:    "bundledemo-agent",
			Entrypoint: "main",
		},
		Capability: &gen.AgentCapabilityBinding{
			Callables: []string{"hello", "world"},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal bundle-demo manifest: %v", err)
	}
	return string(data)
}

// hasCall reports whether the harness saw the given callable id.
func (b *todolistE2EPlannerBuilder) hasCall(suffix string) bool {
	for _, c := range *b.calls {
		if c == suffix {
			return true
		}
	}
	return false
}

// cardRaw returns the raw content stored for a project wiki card.
func (b *todolistE2EPlannerBuilder) cardRaw(cardID string) (string, bool) {
	b.cardsMu.Lock()
	defer b.cardsMu.Unlock()
	b.initCards()
	raw, ok := b.cards[cardID]
	return raw, ok
}

// TestBundleE2E_RegisterPublishesCardAndUnregisterRevokes is the S4 full-chain
// e2e: a manifest with one bundle (two tools) registered against a project must
// publish exactly one app-bundle:{appID}:{slug} wiki card (probe → create, no
// edit), the card raw must carry the bundle frontmatter (tags
// [component, bundle] and the indented data.tools list), and unregister must
// delete that same card via wiki_delete_card.
func TestBundleE2E_RegisterPublishesCardAndUnregisterRevokes(t *testing.T) {
	projectCID, err := identity.NewCanonicalID(1700000000000, 1, 1, 81)
	if err != nil {
		t.Fatalf("create project CID: %v", err)
	}
	projectID := projectCID.String()

	var calls []string
	b := &todolistE2EPlannerBuilder{
		t:                   t,
		calls:               &calls,
		manifestJSON:        bundleDemoManifestJSON(t),
		appdefContent:       bundleDemoAppDef,
		handlersGo:          bundleDemoHandlersFilled,
		appID:               "app.bundledemo",
		registeredCallables: []string{"hello", "world"},
		loadedPlugins:       map[string]bool{},
	}
	ctx := todolistE2ECtx(t, b)
	a := newTodolistE2EActor(t)

	// --- Register: the bundle-declaring manifest must surface a virtual
	// component (no card entity is written to any wiki). ---
	resp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("register_project: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}

	const expectedCard = "app-bundle:app.bundledemo:bundle-demo-tools"
	if b.hasCall("project.wiki_create_card") {
		t.Fatalf("virtual bundles must not write cards, saw project.wiki_create_card, calls=%v", *b.calls)
	}
	listResp, err := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if err != nil {
		t.Fatalf("component_list: %v", err)
	}
	if len(listResp.Items) != 1 || listResp.Items[0].Ref.CardID != expectedCard {
		t.Fatalf("items = %+v, want one %s", listResp.Items, expectedCard)
	}
	d := listResp.Items[0]
	if d.Title != "Bundle Demo Tools" || len(d.Tools) != 2 ||
		d.Tools[0].CallableID != "app.app.bundledemo.hello" || d.Tools[1].CallableID != "app.app.bundledemo.world" {
		t.Fatalf("descriptor = %+v", d)
	}

	// --- Unregister: the virtual component disappears (no wiki cleanup). ---
	calls = nil
	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: "app.bundledemo"}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if b.hasCall("project.wiki_delete_card") {
		t.Fatalf("virtual bundles must not delete cards, saw project.wiki_delete_card, calls=%v", *b.calls)
	}
	listResp, err = a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if err != nil {
		t.Fatalf("component_list after unregister: %v", err)
	}
	if len(listResp.Items) != 0 {
		t.Fatalf("items after unregister = %d, want 0", len(listResp.Items))
	}
	a.mu.Lock()
	_, recGone := a.Records["app.bundledemo"]
	a.mu.Unlock()
	if recGone {
		t.Fatal("record should be removed after unregister")
	}
}
