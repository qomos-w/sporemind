package browsermanager

import (
	"sync"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/domain"
	browserusegen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func freshBM(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func validCreateReq() domain.BrowserManagerCreateReq {
	return domain.BrowserManagerCreateReq{
		Name: "primary",
		URL:  "https://example.com",
	}
}

func TestHandleList_Empty(t *testing.T) {
	a, ctx := freshBM(t)
	resp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp.Items))
	}
}

func TestHandleCreate_Valid(t *testing.T) {
	a, ctx := freshBM(t)
	resp, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instance.Config.ID != "inst-0" {
		t.Errorf("expected first ID inst-0, got %q", resp.Instance.Config.ID)
	}
	if resp.Instance.Config.Name != "primary" {
		t.Errorf("expected name primary, got %q", resp.Instance.Config.Name)
	}
	if resp.Instance.Config.URL != "https://example.com" {
		t.Errorf("expected URL propagated, got %q", resp.Instance.Config.URL)
	}
	if !resp.Instance.Config.Open {
		t.Errorf("expected new instance to be open")
	}
	if len(a.Instances) != 1 {
		t.Fatalf("expected 1 persisted instance, got %d", len(a.Instances))
	}
	if a.nextID != 1 {
		t.Errorf("nextID should advance to 1, got %d", a.nextID)
	}
}

func TestHandleCreate_Hidden(t *testing.T) {
	a, ctx := freshBM(t)
	req := domain.BrowserManagerCreateReq{
		Name:   "hidden-crawl",
		URL:    "https://example.com",
		Hidden: true,
	}
	resp, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Instance.Config.Hidden {
		t.Errorf("expected hidden=true, got %v", resp.Instance.Config.Hidden)
	}
}

func TestHandleCreate_DuplicateName(t *testing.T) {
	a, ctx := freshBM(t)
	if _, err := a.handleCreate(ctx, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	_, err := a.handleCreate(ctx, validCreateReq())
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestHandleRemove(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	removed, err := a.handleRemove(ctx, domain.BrowserManagerRemoveReq{ID: created.Instance.Config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if removed.Instance.Config.ID != created.Instance.Config.ID {
		t.Errorf("removed wrong instance %q", removed.Instance.Config.ID)
	}
	if len(a.Instances) != 0 {
		t.Errorf("expected 0 instances after remove, got %d", len(a.Instances))
	}
}

func TestHandleNavigate(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := a.handleNavigate(ctx, domain.BrowserManagerNavigateReq{
		ID:  created.Instance.Config.ID,
		URL: "https://other.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Navigation moves the current page (State.URL) only; the settings page
	// (Config.URL) must survive.
	if updated.Instance.Config.URL != "https://example.com" {
		t.Errorf("expected settings-page URL preserved, got %q", updated.Instance.Config.URL)
	}
	if updated.Instance.Config.State.URL != "https://other.example.com" {
		t.Errorf("expected current-page URL updated, got %q", updated.Instance.Config.State.URL)
	}
	if updated.Instance.Status.URL != "https://other.example.com" {
		t.Errorf("expected status URL to track current page, got %q", updated.Instance.Status.URL)
	}
	if a.Instances[0].URL != "https://example.com" {
		t.Errorf("expected persisted settings-page URL preserved, got %q", a.Instances[0].URL)
	}
	if a.Instances[0].State.URL != "https://other.example.com" {
		t.Errorf("expected persisted current-page URL updated, got %q", a.Instances[0].State.URL)
	}
}

func TestHandleSyncInstance(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	cfg := created.Instance.Config
	cfg.State.Title = "New Title"
	cfg.State.Width = 1920
	cfg.State.Height = 1080

	updated, err := a.handleSyncInstance(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config.State.Title != "New Title" {
		t.Errorf("expected title synced, got %q", updated.Config.State.Title)
	}
	if a.Instances[0].State.Width != 1920 {
		t.Errorf("expected width synced, got %d", a.Instances[0].State.Width)
	}
}

func TestHandleOpen(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}

	// Simulate restart: reset Open to false.
	a.Instances[0].Open = false
	if a.Instances[0].State.Width == 0 {
		a.Instances[0].State.Width = 1024
		a.Instances[0].State.Height = 768
		a.Instances[0].State.X = 100
		a.Instances[0].State.Y = 80
	}

	opened, err := a.handleOpen(ctx, domain.BrowserManagerOpenReq{ID: created.Instance.Config.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !opened.Instance.Config.Open {
		t.Errorf("expected reopened instance to be open")
	}
	if !a.Instances[0].Open {
		t.Errorf("expected persisted instance to be open")
	}
	if opened.Instance.Config.State.X != a.Instances[0].State.X {
		t.Errorf("expected state to be preserved, got X=%d", opened.Instance.Config.State.X)
	}
}

func TestHandleOpen_NotFound(t *testing.T) {
	a, ctx := freshBM(t)
	_, err := a.handleOpen(ctx, domain.BrowserManagerOpenReq{ID: "missing"})
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestHandleSyncInstance_PageState(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	cfg := created.Instance.Config
	cfg.State.PageState = domain.BrowserPageState{
		ScrollX: 100,
		ScrollY: 200,
		Zoom:    1.25,
		Data:    `{"form":{"q":"hello"}}`,
	}

	updated, err := a.handleSyncInstance(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Config.State.PageState.ScrollX != 100 {
		t.Errorf("expected scrollX 100, got %d", updated.Config.State.PageState.ScrollX)
	}
	if updated.Config.State.PageState.Zoom != 1.25 {
		t.Errorf("expected zoom 1.25, got %f", updated.Config.State.PageState.Zoom)
	}
	if a.Instances[0].State.PageState.Data != `{"form":{"q":"hello"}}` {
		t.Errorf("expected page data persisted, got %q", a.Instances[0].State.PageState.Data)
	}
}

func TestSaveLoad_PageState(t *testing.T) {
	dir := t.TempDir()
	a1 := &Actor{store: persist.NewFSPersist(dir)}
	ctx1 := testutil.AdminCtx(testutil.GenActorID())
	ctx1.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	if err := a1.OnStart(ctx1); err != nil {
		t.Fatal(err)
	}
	if _, err := a1.handleCreate(ctx1, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	a1.Instances[0].State.PageState = domain.BrowserPageState{
		ScrollX: 50,
		ScrollY: 75,
		Zoom:    1.5,
	}
	if err := a1.Save(); err != nil {
		t.Fatal(err)
	}

	a2 := &Actor{store: a1.store}
	ctx2 := testutil.AdminCtx(testutil.GenActorID())
	ctx2.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.Instances) != 1 {
		t.Fatalf("expected 1 instance after load, got %d", len(a2.Instances))
	}
	ps := a2.Instances[0].State.PageState
	if ps.ScrollX != 50 || ps.ScrollY != 75 || ps.Zoom != 1.5 {
		t.Errorf("page state not restored: %+v", ps)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	a1 := &Actor{store: persist.NewFSPersist(dir)}
	ctx1 := testutil.AdminCtx(testutil.GenActorID())
	ctx1.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a1.OnInit(ctx1); err != nil {
		t.Fatal(err)
	}
	if err := a1.OnStart(ctx1); err != nil {
		t.Fatal(err)
	}
	if _, err := a1.handleCreate(ctx1, validCreateReq()); err != nil {
		t.Fatal(err)
	}
	if err := a1.Save(); err != nil {
		t.Fatal(err)
	}

	a2 := &Actor{store: a1.store}
	ctx2 := testutil.AdminCtx(testutil.GenActorID())
	ctx2.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	if err := a2.OnInit(ctx2); err != nil {
		t.Fatal(err)
	}
	if len(a2.Instances) != 1 {
		t.Fatalf("expected 1 instance after load, got %d", len(a2.Instances))
	}
	if a2.Instances[0].Name != "primary" {
		t.Errorf("expected name primary after load, got %q", a2.Instances[0].Name)
	}
	if a2.nextID != 1 {
		t.Errorf("expected nextID 1 after load, got %d", a2.nextID)
	}
	// Open flag is now preserved across restart so windows can be restored.
	if !a2.Instances[0].Open {
		t.Errorf("expected instance to remain open after restart")
	}
}

func TestHandleUpdate_EmitsUpdatedEvent(t *testing.T) {
	a, ctx := freshBM(t)
	created, err := a.handleCreate(ctx, validCreateReq())
	if err != nil {
		t.Fatal(err)
	}
	cfg := created.Instance.Config
	cfg.Name = "renamed"
	_, err = a.handleUpdate(ctx, domain.BrowserManagerUpdateReq{ID: cfg.ID, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "browser_manager_event" {
			continue
		}
		payload, ok := ev.Payload.(domain.BrowserManagerEvent)
		if !ok {
			continue
		}
		if payload.Kind != "updated" {
			continue
		}
		inst := payload.Instance
		if inst.Config.Name == "renamed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected updated event with name 'renamed', events: %+v", ctx.EmittedEvents)
	}
}

func TestHandleOpenGlobal_EmitsEventWithoutPersistingInstance(t *testing.T) {
	a, ctx := freshBM(t)
	resp, err := a.handleOpenGlobal(ctx, domain.BrowserManagerOpenGlobalReq{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Opened {
		t.Errorf("expected opened=true")
	}
	if resp.URL != "https://example.com" {
		t.Errorf("expected URL propagated, got %q", resp.URL)
	}
	// The global browser is shared workspace state, not a BrowserManager
	// instance: nothing may be persisted or it would respawn as an
	// "independent" browser named Global on restart.
	if len(a.Instances) != 0 {
		t.Fatalf("expected no persisted instance, got %d", len(a.Instances))
	}
	listResp, err := a.handleList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listResp.Items) != 0 {
		t.Errorf("expected empty instance list, got %d items", len(listResp.Items))
	}

	var found bool
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != "browser_manager_event" {
			continue
		}
		payload, ok := ev.Payload.(domain.BrowserManagerEvent)
		if !ok {
			continue
		}
		if payload.Kind != "open_global" {
			continue
		}
		inst := payload.Instance
		if inst.Config.URL == "https://example.com" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected open_global event, events: %+v", ctx.EmittedEvents)
	}

	// A second call must also stay side-effect free while emitting the new URL.
	resp2, err := a.handleOpenGlobal(ctx, domain.BrowserManagerOpenGlobalReq{URL: "https://other.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if resp2.URL != "https://other.example.com" {
		t.Errorf("expected URL updated, got %q", resp2.URL)
	}
	if len(a.Instances) != 0 {
		t.Errorf("expected still no persisted instance, got %d", len(a.Instances))
	}
}

func TestLoad_DropsLegacyGlobalPseudoInstance(t *testing.T) {
	dir := t.TempDir()
	store := persist.NewFSPersist(dir)
	actorID := testutil.GenActorID()
	err := store.Save(actorID.String(), managerSnapshot{
		Instances: []domain.BrowserInstanceConfig{
			{ID: "global", Name: "Global", URL: "https://legacy.example.com", Mode: "tab", Open: true},
			{ID: "inst-0", Name: "primary", URL: "https://example.com", Mode: "tab", Open: true},
		},
		NextID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	a := &Actor{store: persist.NewFSPersist(dir), actorID: actorID.String()}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	if len(a.Instances) != 1 {
		t.Fatalf("expected only the user instance to survive, got %d", len(a.Instances))
	}
	if a.Instances[0].ID != "inst-0" {
		t.Errorf("expected inst-0 to survive, got %q", a.Instances[0].ID)
	}
	if a.nextID != 1 {
		t.Errorf("expected nextID preserved, got %d", a.nextID)
	}
}

func TestLoad_MigratesWindowModeInstancesToTab(t *testing.T) {
	dir := t.TempDir()
	store := persist.NewFSPersist(dir)
	actorID := testutil.GenActorID()
	err := store.Save(actorID.String(), managerSnapshot{
		Instances: []domain.BrowserInstanceConfig{
			{ID: "inst-0", Name: "win", URL: "https://example.com", Mode: "window", Open: true},
			{ID: "inst-1", Name: "legacy-empty", URL: "https://example.com", Mode: "", Open: false},
			{ID: "inst-2", Name: "tab", URL: "https://example.com", Mode: "tab", Open: true},
		},
		NextID: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	a := &Actor{store: persist.NewFSPersist(dir), actorID: actorID.String()}
	if err := a.Load(); err != nil {
		t.Fatal(err)
	}
	for _, c := range a.Instances {
		if c.Mode != "tab" {
			t.Errorf("instance %s (%s): expected Mode migrated to \"tab\", got %q", c.ID, c.Name, c.Mode)
		}
	}
}

func TestCreate_AlwaysTabMode(t *testing.T) {
	a, ctx := freshBM(t)
	req := domain.BrowserManagerCreateReq{Name: "n", URL: "https://example.com", Hidden: false}
	resp, err := a.handleCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Instance.Config.Mode != "tab" {
		t.Errorf("expected create to force Mode \"tab\", got %q", resp.Instance.Config.Mode)
	}
}

func TestNormalizeGlobalURL(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"https://example.com", "https://example.com"},
		{"http://example.com", "http://example.com"},
		{"file:///C:/docs/readme.md", "file:///C:/docs/readme.md"},
		{"C:\\docs\\readme.md", "file:///C:/docs/readme.md"},
		{"D:/docs/readme.md", "file:///D:/docs/readme.md"},
		{"/usr/share/doc/readme.md", "file:///usr/share/doc/readme.md"},
		{"example.com", "example.com"},
	}
	for _, c := range cases {
		got, err := normalizeGlobalURL(c.input)
		if err != nil {
			t.Fatalf("normalizeGlobalURL(%q): unexpected error: %v", c.input, err)
		}
		if got != c.want {
			t.Errorf("normalizeGlobalURL(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestIsObserveAction(t *testing.T) {
	observeActions := []string{"observe", "Observe", "OBSERVE", "get_observation", "snapshot", "read", "inspect", "screenshot", "Screenshot"}
	for _, action := range observeActions {
		if !isObserveAction(action) {
			t.Errorf("isObserveAction(%q) = false, want true", action)
		}
	}

	interactiveActions := []string{"click", "type", "scroll", "navigate", "wait", "submit", "CLEAR"}
	for _, action := range interactiveActions {
		if isObserveAction(action) {
			t.Errorf("isObserveAction(%q) = true, want false", action)
		}
	}
}

func TestHandleUse_InstanceNotFound(t *testing.T) {
	a, ctx := freshBM(t)
	resp, err := a.handleUse(ctx, browserusegen.BrowserUseReq{
		InstanceID: "nonexistent",
		Action:    "observe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Error("expected success=false for nonexistent instance")
	}
	if resp.ErrorCode != "instance_not_found" {
		t.Errorf("expected error_code instance_not_found, got %q", resp.ErrorCode)
	}
}

func TestHandleUse_InteractiveActionExecutesWithoutHumanRole(t *testing.T) {
	// Verify interactive actions route and execute without human role.
	// Security is handled by toolregistry effect classification + turn engine permission flow.

	// Reset the global operator after the test
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)

	// Set up a mock operator
	useCalled := false
	observeCalled := false
	useResp := &browserusegen.BrowserUseResp{
		Success: true,
		Message: "click executed",
	}
	observeResp := &browserusegen.BrowserPageObservation{
		ObservationID: "obs-after-click",
		URL:           "https://example.com/clicked",
		Title:         "Clicked",
	}
	mockOp := &mockWindowOperator{
		useFn: func(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error) {
			useCalled = true
			return useResp, nil
		},
		observeFn: func(id string) (*browserusegen.BrowserPageObservation, error) {
			observeCalled = true
			return observeResp, nil
		},
	}
	browserinstance.SetWindowOperator(mockOp)

	a, ctx := freshBM(t)
	_ = ctx // unused but keeps context alive

	// Directly add an instance to the actor
	instID := "inst-test-1"
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID:   instID,
		Name: "test",
		URL:  "https://example.com",
		Open: true,
	})
	a.childActorIDs[instID] = "test-actor-id"
	a.mu.Unlock()

	// Use an anonymous (empty role) identity - should NOT block interactive actions
	anonCtx := testutil.AnonCtx(testutil.GenActorID())

	// Interactive action should succeed
	resp, err := a.handleUse(anonCtx, browserusegen.BrowserUseReq{
		InstanceID: instID,
		Action:    "click",
		ElementID: "btn-submit",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for interactive action without human role, got: %s - %s", resp.ErrorCode, resp.Message)
	}
	if !useCalled {
		t.Error("expected WindowOperator.Use() to be called")
	}
	if !observeCalled {
		t.Error("expected WindowOperator.Observe() to be called after successful Use")
	}
	if resp.Observation == nil {
		t.Error("expected resp.Observation to be non-empty after successful interactive action")
	}
}

func TestHandleUse_ObserveWithoutHumanRole(t *testing.T) {
	// This test verifies observe (read-only) works without human role

	// Reset the global operator after the test
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)

	// Set up a mock operator
	mockOp := &mockWindowOperator{
		observeResp: &browserusegen.BrowserPageObservation{
			ObservationID: "obs-1",
			URL:           "https://example.com",
			Title:         "Example",
		},
	}
	browserinstance.SetWindowOperator(mockOp)

	a, ctx := freshBM(t)
	_ = ctx // unused but keeps context alive

	// Directly add an instance to the actor (avoiding the finishCreate goroutine)
	instID := "inst-test-1"
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID:   instID,
		Name: "test",
		URL:  "https://example.com",
		Open: true,
	})
	a.childActorIDs[instID] = "test-actor-id"
	a.mu.Unlock()

	// Use an anonymous (non-human) identity
	anonCtx := testutil.AnonCtx(testutil.GenActorID())

	// Observe should work without human role
	resp, err := a.handleUse(anonCtx, browserusegen.BrowserUseReq{
		InstanceID: instID,
		Action:    "observe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Errorf("expected success=true for observe action, got error: %s - %s", resp.ErrorCode, resp.Message)
	}
	if resp.Observation == nil {
		t.Error("expected observation in response")
	}
}

func TestHandleUse_NoOperatorBound(t *testing.T) {
	// Ensure no operator is bound
	origOp := browserinstance.GetWindowOperator()
	browserinstance.SetWindowOperator(nil)
	defer browserinstance.SetWindowOperator(origOp)

	a, ctx := freshBM(t)
	_ = ctx // unused but keeps context alive

	// Directly add an instance to the actor (avoiding the finishCreate goroutine)
	instID := "inst-test-1"
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID:   instID,
		Name: "test",
		URL:  "https://example.com",
		Open: true,
	})
	a.childActorIDs[instID] = "test-actor-id"
	a.mu.Unlock()

	resp, err := a.handleUse(testutil.AdminCtx(testutil.GenActorID()), browserusegen.BrowserUseReq{
		InstanceID: instID,
		Action:    "observe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Error("expected success=false when no operator is bound")
	}
	if resp.ErrorCode != "operator_not_bound" {
		t.Errorf("expected error_code operator_not_bound, got %q", resp.ErrorCode)
	}
}

// mockWindowOperator implements browserinstance.WindowOperator for testing
type mockWindowOperator struct {
	mu            sync.Mutex
	observeResp   *browserusegen.BrowserPageObservation
	observeErr    error
	observeFn     func(id string) (*browserusegen.BrowserPageObservation, error)
	useResp       *browserusegen.BrowserUseResp
	useErr        error
	useFn         func(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error)
	exportCookies map[string][]browserusegen.BrowserCookieEntry
	exportErr     error
	imported      int64
	importErr     error
}

func (m *mockWindowOperator) Create(id string, cfg domain.BrowserInstanceConfig) error {
	return nil
}

func (m *mockWindowOperator) Close(id string) error {
	return nil
}

func (m *mockWindowOperator) Navigate(id, url string) error {
	return nil
}

func (m *mockWindowOperator) UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error {
	return nil
}

func (m *mockWindowOperator) DeleteProfile(id string) error {
	return nil
}

func (m *mockWindowOperator) Observe(id string) (*browserusegen.BrowserPageObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.observeFn != nil {
		return m.observeFn(id)
	}
	return m.observeResp, m.observeErr
}

func (m *mockWindowOperator) Use(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.useFn != nil {
		return m.useFn(id, req)
	}
	return m.useResp, m.useErr
}

func (m *mockWindowOperator) ExportCookies(id string) (map[string][]browserusegen.BrowserCookieEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exportCookies, m.exportErr
}

func (m *mockWindowOperator) ImportCookies(id string, cookies map[string][]browserusegen.BrowserCookieEntry) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.imported, m.importErr
}

func (m *mockWindowOperator) RegisterExternal(id string, win browserinstance.ExternalWindow) error {
	return nil
}

func TestHandleUse_DefaultInstanceID(t *testing.T) {
	// Empty InstanceID defaults to "global". The global tab is a shared
	// desktop browser session that is never a browsermanager child actor, so
	// use() skips the child-actor existence check for it. With no operator
	// bound it must report operator_not_bound instead of instance_not_found.
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)
	browserinstance.SetWindowOperator(nil)

	a, ctx := freshBM(t)
	_ = ctx

	resp, err := a.handleUse(testutil.AdminCtx(testutil.GenActorID()), browserusegen.BrowserUseReq{
		InstanceID: "",
		Action:     "observe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Error("expected success=false when no operator is bound")
	}
	if resp.ErrorCode != "operator_not_bound" {
		t.Errorf("expected error_code operator_not_bound, got %q", resp.ErrorCode)
	}
}

func TestHandleUse_GlobalRoutesToOperator(t *testing.T) {
	// "global" (explicit or defaulted) resolves purely via the operator: the
	// child-actor check is skipped and Observe("global") is invoked on the
	// registered Global tab window.
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)

	observedID := ""
	mockOp := &mockWindowOperator{
		observeFn: func(id string) (*browserusegen.BrowserPageObservation, error) {
			observedID = id
			return &browserusegen.BrowserPageObservation{URL: "https://example.com"}, nil
		},
	}
	browserinstance.SetWindowOperator(mockOp)

	a, _ := freshBM(t)

	for _, instanceID := range []string{"", "global"} {
		resp, err := a.handleUse(testutil.AdminCtx(testutil.GenActorID()), browserusegen.BrowserUseReq{
			InstanceID: instanceID,
			Action:     "observe",
		})
		if err != nil {
			t.Fatalf("InstanceID=%q: %v", instanceID, err)
		}
		if !resp.Success {
			t.Errorf("InstanceID=%q: expected success=true, got %s", instanceID, resp.ErrorCode)
		}
		if observedID != "global" {
			t.Errorf("InstanceID=%q: expected Observe(\"global\"), got Observe(%q)", instanceID, observedID)
		}
	}
}

func TestHandleUse_FailureDoesNotReobserve(t *testing.T) {
	// When an interactive action fails (Success=false), the manager must NOT
	// issue a re-observe call. Only successful actions trigger the observe→act
	// closed loop.
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)

	useCalled := false
	observeCalled := false
	mockOp := &mockWindowOperator{
		useFn: func(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error) {
			useCalled = true
			return &browserusegen.BrowserUseResp{
				Success:   false,
				ErrorCode: "element_not_found",
				Message:   "element not found",
			}, nil
		},
		observeFn: func(id string) (*browserusegen.BrowserPageObservation, error) {
			observeCalled = true
			return &browserusegen.BrowserPageObservation{}, nil
		},
	}
	browserinstance.SetWindowOperator(mockOp)

	a, _ := freshBM(t)

	instID := "inst-fail"
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID:   instID,
		Name: "fail-test",
		URL:  "https://example.com",
		Open: true,
	})
	a.childActorIDs[instID] = "actor-fail"
	a.mu.Unlock()

	resp, err := a.handleUse(testutil.AdminCtx(testutil.GenActorID()), browserusegen.BrowserUseReq{
		InstanceID: instID,
		Action:     "click",
		ElementID:  "e1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Success {
		t.Error("expected success=false for failed interactive action")
	}
	if !useCalled {
		t.Error("expected Use() to be called")
	}
	if observeCalled {
		t.Error("expected Observe() NOT to be called on failed action")
	}
	if resp.Observation != nil {
		t.Error("expected nil Observation on failed action")
	}
}

func TestHandleExportCookies(t *testing.T) {
	a, ctx := freshBM(t)
	instID := "inst-export"
	actorIDStr := testutil.GenActorID().String()
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID: instID, Name: "export-test", URL: "https://example.com", Open: true,
	})
	a.childActorIDs[instID] = actorIDStr
	a.mu.Unlock()

	canonical, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		t.Fatal(err)
	}
	childRef := testutil.NewFakeRef(id.From(canonical), func(callID string, payload any) any {
		if callID == "browserinstance.export_cookies" {
			return browserusegen.BrowserManagerExportCookiesResp{
				Cookies: map[string][]browserusegen.BrowserCookieEntry{
					"example.com": {{Name: "x", Value: "y", Domain: "example.com"}},
				},
			}
		}
		return nil
	})
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == actorIDStr {
			return childRef, true
		}
		return nil, false
	}

	resp, err := a.handleExportCookies(ctx, domain.BrowserManagerExportCookiesReq{ID: instID})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Cookies["example.com"]) != 1 || resp.Cookies["example.com"][0].Name != "x" {
		t.Errorf("unexpected cookies: %#v", resp.Cookies)
	}
}

func TestHandleImportCookies(t *testing.T) {
	a, ctx := freshBM(t)
	instID := "inst-import"
	actorIDStr := testutil.GenActorID().String()
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID: instID, Name: "import-test", URL: "https://example.com", Open: true,
	})
	a.childActorIDs[instID] = actorIDStr
	a.mu.Unlock()

	canonical, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		t.Fatal(err)
	}
	childRef := testutil.NewFakeRef(id.From(canonical), func(callID string, payload any) any {
		if callID == "browserinstance.import_cookies" {
			return browserusegen.BrowserManagerImportCookiesResp{Imported: 2}
		}
		return nil
	})
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == actorIDStr {
			return childRef, true
		}
		return nil, false
	}

	cookies := map[string][]domain.BrowserCookieEntry{
		"example.com": {
			{Name: "a", Value: "1", Domain: "example.com"},
			{Name: "b", Value: "2", Domain: "example.com"},
		},
	}
	resp, err := a.handleImportCookies(ctx, domain.BrowserManagerImportCookiesReq{ID: instID, Cookies: cookies})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Imported != 2 {
		t.Errorf("expected 2 imported cookies, got %d", resp.Imported)
	}
}

func TestHandleUse_ResolvesByName(t *testing.T) {
	// InstanceID that matches an instance Name should resolve to that
	// instance's ID and execute the action against it.
	origOp := browserinstance.GetWindowOperator()
	defer browserinstance.SetWindowOperator(origOp)

	var usedID string
	mockOp := &mockWindowOperator{
		observeFn: func(id string) (*browserusegen.BrowserPageObservation, error) {
			return &browserusegen.BrowserPageObservation{
				ObservationID: "obs-name",
				URL:           "https://example.com",
			}, nil
		},
		useFn: func(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error) {
			usedID = id
			return &browserusegen.BrowserUseResp{Success: true, Message: "ok"}, nil
		},
	}
	browserinstance.SetWindowOperator(mockOp)

	a, _ := freshBM(t)

	instID := "inst-7"
	instName := "mybrowser"
	a.mu.Lock()
	a.Instances = append(a.Instances, domain.BrowserInstanceConfig{
		ID:   instID,
		Name: instName,
		URL:  "https://example.com",
		Open: true,
	})
	a.childActorIDs[instID] = "actor-7"
	a.mu.Unlock()

	// Call by name, not by ID.
	resp, err := a.handleUse(testutil.AdminCtx(testutil.GenActorID()), browserusegen.BrowserUseReq{
		InstanceID: instName,
		Action:     "click",
		ElementID:  "e1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Errorf("expected success=true, got %s - %s", resp.ErrorCode, resp.Message)
	}
	if usedID != instID {
		t.Errorf("expected action resolved to ID %q, got %q", instID, usedID)
	}
}
