package appmanager

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// bundleProjectEnv is a fake project actor for appmanager bundle-card
// lifecycle tests. It serves the project package files (project.info / read /
// read_base64 / list_json) and answers the wiki callables used for bundle card
// publish/upsert/revoke (component_get / wiki_create_card / wiki_edit_card /
// wiki_delete_card), keeping an in-memory card store so probes see created
// cards.
type bundleProjectEnv struct {
	t    *testing.T
	mu   sync.Mutex
	root string

	calls []string
	cards map[string]string // cardID -> raw

	// childRef is the spawned spore child actor ref returned by ctx.Spawn.
	childRef ref.Ref
	// childActorID is the actor id of the spawned spore child.
	childActorID id.ActorID
}

func newBundleProjectEnv(t *testing.T, manifestJSON string) *bundleProjectEnv {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.manifest.json"), []byte(manifestJSON), 0644); err != nil {
		t.Fatalf("write app.manifest.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.spore"), []byte("export default {}"), 0644); err != nil {
		t.Fatalf("write main.spore: %v", err)
	}
	return &bundleProjectEnv{
		t:        t,
		root:     root,
		cards:    map[string]string{},
		childRef: testutil.NewFakeRef(testutil.GenActorID(), nil),
	}
}

func (e *bundleProjectEnv) childID() id.ActorID {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.childRef.ID()
}

func (e *bundleProjectEnv) callSeq() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.calls...)
}

func (e *bundleProjectEnv) hasCall(suffix string) bool {
	for _, c := range e.callSeq() {
		if c == suffix {
			return true
		}
	}
	return false
}

func (e *bundleProjectEnv) cardCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cards)
}

func (e *bundleProjectEnv) recordCall(callID string) {
	e.mu.Lock()
	e.calls = append(e.calls, callID)
	e.mu.Unlock()
}

func (e *bundleProjectEnv) planner() actor.Planner {
	return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
		e.recordCall(callID)
		switch callID {
		case "project.info":
			return gen.ProjectInfoResp{Roots: []gen.ProjectInfoRoot{{Name: "test", Path: e.root}}}, nil
		case "project.list":
			return "", nil
		case "project.read":
			req := payload.(gen.FileSystemReadReq)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadResp{}, err
			}
			return gen.FileSystemReadResp{Content: string(data)}, nil
		case "project.read_base64":
			req := payload.(gen.FileSystemReadBase64Req)
			data, err := os.ReadFile(filepath.FromSlash(req.Path))
			if err != nil {
				return gen.FileSystemReadBase64Resp{}, err
			}
			return gen.FileSystemReadBase64Resp{Content: base64.StdEncoding.EncodeToString(data)}, nil
		case "project.component_get":
			req := payload.(gen.ProjectComponentGetReq)
			e.mu.Lock()
			_, ok := e.cards[req.CardID]
			e.mu.Unlock()
			if !ok {
				return gen.ProjectComponentGetResp{}, &projectCardNotFound{cardID: req.CardID}
			}
			return gen.ProjectComponentGetResp{Component: gen.ComponentDescriptor{
				Ref:   gen.ComponentRef{CardID: req.CardID, Kind: "bundle", Source: "appmanager"},
				Title: "bundle",
			}}, nil
		case "project.wiki_create_card":
			req := payload.(gen.WikiCreateCardReq)
			e.mu.Lock()
			e.cards[req.ID] = req.Raw
			e.mu.Unlock()
			return gen.WikiCreateCardResp{Card: gen.MonoCardListItem{ID: req.ID, Type: "bundle", Source: "appmanager"}}, nil
		case "project.wiki_edit_card":
			req := payload.(gen.WikiEditCardReq)
			e.mu.Lock()
			e.cards[req.ID] = req.Raw
			e.mu.Unlock()
			return gen.WikiEditCardResp{Card: gen.MonoCardListItem{ID: req.ID, Type: "bundle", Source: "appmanager"}}, nil
		case "project.wiki_delete_card":
			req := payload.(gen.WikiDeleteCardReq)
			e.mu.Lock()
			delete(e.cards, req.ID)
			e.mu.Unlock()
			return gen.WikiDeleteCardResp{ID: req.ID}, nil
		case "sporeapp.reload":
			return gen.SporeAppReloadResp{ID: "app.bundledemo", Version: "2.0.0", StateVersion: 2}, nil
		default:
			e.t.Fatalf("unexpected planner call %s", callID)
			return nil, nil
		}
	}}
}

// projectCardNotFound mirrors the project actor's not-found error so the probe
// path (err != nil → create) behaves like the real component_get.
type projectCardNotFound struct{ cardID string }

func (e *projectCardNotFound) Error() string { return "component card \"" + e.cardID + "\" not found" }

func (e *bundleProjectEnv) ctx(projectID string) *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	canonical, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		e.t.Fatal(err)
	}
	projectActorID := id.From(canonical)
	projectRef := testutil.NewFakeRef(projectActorID, nil)
	childID := e.childID()
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch aid {
		case projectActorID:
			return projectRef, true
		case childID:
			return e.childRef, true
		}
		return nil, false
	}
	ctx.SpawnFn = func(props actor.Props, name string) (ref.Ref, error) {
		return e.childRef, nil
	}
	ctx.DestroyFn = func(target ref.Ref) error { return nil }
	ctx.PlannerFn = func() actor.Planner { return e.planner() }
	return ctx
}

func newBundleCardsActor(t *testing.T) *Actor {
	t.Helper()
	return &Actor{
		actorID:  "appmanager-bundle-cards-test",
		store:    persist.NewFSPersist(t.TempDir()),
		bindings: appbinding.NewRegistry(),
		Apps:     map[string]gen.AppManifest{},
		Records:  map[string]appRecord{},
		children: map[string]string{},
		sessions: map[string]appSession{},
	}
}

func bundleManifestJSON(appID string, bundles []gen.AppBundle) string {
	m := gen.AppManifest{
		ID:              appID,
		Name:            "BundleDemo",
		Namespace:       appID,
		Version:         "1.0.0",
		Runtime:         "spore",
		ProtocolVersion: 1,
		Callables:       []gen.AppCallableDescriptor{{ID: "main", RequestSchema: "Req", ResponseSchema: "Resp"}},
		Bundles:         bundles,
	}
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func bundleProjectID(t *testing.T) string {
	t.Helper()
	cid, err := identity.NewCanonicalID(1700000000000, 1, 1, 90)
	if err != nil {
		t.Fatal(err)
	}
	return cid.String()
}

func TestBundleIconOrFallback(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty falls back", "", "package"},
		{"unknown falls back", "not-a-real-glyph", "package"},
		{"known icon passes through", "shield-check", "shield-check"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bundleIconOrFallback(tc.in); got != tc.want {
				t.Fatalf("bundleIconOrFallback(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRegisterProjectsVirtualBundleComponents verifies register_project with a
// manifest declaring bundles surfaces the bundle as a VIRTUAL component via
// appmanager.component_list/get — projected from the registry, with the
// declared icon, the app-tool contribution, and the description as the
// tool_guidance prompt (external priority floor) — while making zero wiki
// calls (no card entity is written anywhere).
func TestRegisterProjectsVirtualBundleComponents(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Icon: "shield-check", Description: "Time-based one-time password helpers for 2FA flows.", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)

	resp, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID})
	if err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	if resp.Status.State != "running" {
		t.Fatalf("state = %q, want running", resp.Status.State)
	}

	listResp, err := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResp.Items))
	}
	d := listResp.Items[0]
	want := "app-bundle:app.bundledemo:core-tools"
	if d.Ref.CardID != want || d.Ref.Kind != "bundle" || d.Ref.Source != "appmanager" {
		t.Errorf("ref = %+v, want card %s bundle/appmanager", d.Ref, want)
	}
	if d.Title != "Core Tools" || d.Icon != "shield-check" || d.Visual == nil || d.Visual.Icon != "shield-check" {
		t.Errorf("title/icon/visual = %q/%q/%+v", d.Title, d.Icon, d.Visual)
	}
	if len(d.Tools) != 1 || d.Tools[0].ID != "app.app.bundledemo.main" || d.Tools[0].CallableID != "app.app.bundledemo.main" {
		t.Errorf("tools = %+v, want app.app.bundledemo.main", d.Tools)
	}
	if len(d.Prompts) != 1 || d.Prompts[0].Text != "Time-based one-time password helpers for 2FA flows." || d.Prompts[0].Placement != "tool_guidance" || d.Prompts[0].Priority != appBundlePromptPriority {
		t.Errorf("prompts = %+v, want description as tool_guidance contribution with external priority floor", d.Prompts)
	}

	getResp, err := a.handleComponentGet(nil, gen.AppManagerComponentGetReq{CardID: want})
	if err != nil {
		t.Fatalf("handleComponentGet: %v", err)
	}
	if getResp.Component.Ref.CardID != want {
		t.Errorf("get returned %q, want %q", getResp.Component.Ref.CardID, want)
	}

	// No card entity may be written: the fake project env records every call.
	for _, c := range env.callSeq() {
		if strings.HasPrefix(c, "project.wiki_") || c == "project.component_get" {
			t.Errorf("virtual bundles must not touch the project wiki, saw %s (calls=%v)", c, env.callSeq())
		}
	}
	if env.cardCount() != 0 {
		t.Errorf("card store = %d cards, want 0", env.cardCount())
	}
}

// TestVirtualBundleIconFallback: a bundle without a declared icon falls back
// to the host default glyph instead of an empty icon (composer badges require
// a non-empty icon).
func TestVirtualBundleIconFallback(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	listResp, _ := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(listResp.Items))
	}
	d := listResp.Items[0]
	if d.Icon != "package" || d.Visual == nil || d.Visual.Icon != "package" {
		t.Errorf("icon fallback = %q/%+v, want package", d.Icon, d.Visual)
	}
}

// TestRegisterNoBundlesProjectsNothing: apps without bundles stay invisible in
// the virtual component catalog.
func TestRegisterNoBundlesProjectsNothing(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.plain", nil))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	listResp, err := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if err != nil {
		t.Fatalf("handleComponentList: %v", err)
	}
	if len(listResp.Items) != 0 {
		t.Fatalf("items = %d, want 0", len(listResp.Items))
	}
}

// TestNativeActiveStateProjectsBundles: the native register path stores
// record.State "active" (from the pluginhost load response), not the spore
// path's "running". Both live states must project their bundles — live
// verification against the running host showed "active" apps being filtered
// out of the virtual catalog.
func TestNativeActiveStateProjectsBundles(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	a.mu.Lock()
	record := a.Records["app.bundledemo"]
	record.State = "active"
	a.Records["app.bundledemo"] = record
	a.mu.Unlock()

	listResp, _ := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 1 || listResp.Items[0].Ref.CardID != "app-bundle:app.bundledemo:core-tools" {
		t.Fatalf("items = %+v, want the active-state app's bundle", listResp.Items)
	}
	if !recordLive("running") || !recordLive("active") {
		t.Fatal("recordLive must accept running and active")
	}
	for _, dead := range []string{"stopped", "unload_failed", "restart_pending", ""} {
		if recordLive(dead) {
			t.Fatalf("recordLive(%q) must be false", dead)
		}
	}
}

// TestStoppedAppHidesVirtualBundles: the virtual catalog tracks the artifact
// load state — a stopped (unloaded) app's bundles disappear, which is the
// "provided while mounted" lifetime.
func TestStoppedAppHidesVirtualBundles(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	a.mu.Lock()
	record := a.Records["app.bundledemo"]
	record.State = "stopped"
	a.Records["app.bundledemo"] = record
	a.mu.Unlock()

	listResp, _ := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 0 {
		t.Fatalf("items after stop = %d, want 0", len(listResp.Items))
	}
	if _, err := a.handleComponentGet(nil, gen.AppManagerComponentGetReq{CardID: "app-bundle:app.bundledemo:core-tools"}); err == nil {
		t.Fatal("component_get on stopped app must fail")
	}
}

// TestUnregisterDropsVirtualBundles: unregister removes the record, so the
// virtual projection disappears without any wiki cleanup calls.
func TestUnregisterDropsVirtualBundles(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
		{Title: "Extra Tools", Tools: []gen.AppBundleTool{{CallableID: "extra"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("register: %v", err)
	}
	listResp, _ := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 2 {
		t.Fatalf("items after register = %d, want 2", len(listResp.Items))
	}

	if err := a.handleUnregister(ctx, gen.AppManagerUnregisterReq{ID: "app.bundledemo"}); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	listResp, _ = a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 0 {
		t.Fatalf("items after unregister = %d, want 0", len(listResp.Items))
	}
	if _, ok := a.Records["app.bundledemo"]; ok {
		t.Fatal("record should be removed after unregister")
	}
}

// TestBundleSlugCollision verifies that two bundles in the same app whose
// titles slugify identically get distinct virtual card IDs via the -{i}
// fallback.
func TestBundleSlugCollision(t *testing.T) {
	projectID := bundleProjectID(t)
	env := newBundleProjectEnv(t, bundleManifestJSON("app.bundledemo", []gen.AppBundle{
		{Title: "Core Tools", Tools: []gen.AppBundleTool{{CallableID: "main"}}},
		{Title: "Core Tools!", Tools: []gen.AppBundleTool{{CallableID: "extra"}}},
	}))
	a := newBundleCardsActor(t)
	ctx := env.ctx(projectID)
	if _, err := a.handleRegisterProject(ctx, gen.AppManagerRegisterProjectReq{ProjectID: projectID}); err != nil {
		t.Fatalf("handleRegisterProject: %v", err)
	}
	listResp, _ := a.handleComponentList(nil, gen.AppManagerComponentListReq{})
	if len(listResp.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(listResp.Items))
	}
	got := []string{listResp.Items[0].Ref.CardID, listResp.Items[1].Ref.CardID}
	want := []string{"app-bundle:app.bundledemo:core-tools", "app-bundle:app.bundledemo:core-tools-2"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("collision ids = %v, want %v", got, want)
		}
	}
}
