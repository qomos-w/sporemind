package crawl

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/actor/browserinstance"
	"github.com/qomos-w/sporemind/pkg/actor/browsermanager"
	"github.com/qomos-w/sporemind/pkg/domain"
	browserusegen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// mockOperator is a WindowOperator stub that lets the crawl engine run without
// a real WebView2 window. It records created instances and navigations and
// returns canned observations per URL.
type mockOperator struct {
	mu          sync.Mutex
	created     []string
	closed      []string
	deleted     []string
	currentURL  map[string]string
	configs     map[string]domain.BrowserInstanceConfig
	pages       map[string]*browserusegen.BrowserPageObservation
	navTimes    map[string][]time.Time
}

func newMockOperator(pages map[string]*browserusegen.BrowserPageObservation) *mockOperator {
	return &mockOperator{
		currentURL: make(map[string]string),
		configs:    make(map[string]domain.BrowserInstanceConfig),
		pages:      pages,
		navTimes:   make(map[string][]time.Time),
	}
}

func (m *mockOperator) Create(id string, cfg domain.BrowserInstanceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.created = append(m.created, id)
	m.configs[id] = cfg
	return nil
}

func (m *mockOperator) Close(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = append(m.closed, id)
	return nil
}
func (m *mockOperator) Navigate(id, url string) error { return nil }
func (m *mockOperator) UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error { return nil }

func (m *mockOperator) DeleteProfile(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = append(m.deleted, id)
	return nil
}

func (m *mockOperator) Observe(id string) (*browserusegen.BrowserPageObservation, error) {
	m.mu.Lock()
	url := m.currentURL[id]
	obs := m.pages[url]
	m.mu.Unlock()
	return obs, nil
}

func (m *mockOperator) ExportCookies(id string) (map[string][]domain.BrowserCookieEntry, error) { return nil, nil }
func (m *mockOperator) ImportCookies(id string, cookies map[string][]domain.BrowserCookieEntry) (int64, error) { return 0, nil }

func (m *mockOperator) Use(id string, req browserusegen.BrowserUseReq) (*browserusegen.BrowserUseResp, error) {
	if req.Action == "navigate" && req.URL != "" {
		m.mu.Lock()
		m.currentURL[id] = req.URL
		m.navTimes[id] = append(m.navTimes[id], time.Now())
		m.mu.Unlock()
	}
	return &browserusegen.BrowserUseResp{Success: true, Message: "ok"}, nil
}

func (m *mockOperator) RegisterExternal(id string, win browserinstance.ExternalWindow) error {
	return nil
}

func (m *mockOperator) createdContains(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.created {
		if c == id {
			return true
		}
	}
	return false
}

func (m *mockOperator) closedContains(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.closed {
		if c == id {
			return true
		}
	}
	return false
}

func (m *mockOperator) deletedContains(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.deleted {
		if d == id {
			return true
		}
	}
	return false
}

func (m *mockOperator) isHidden(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg, ok := m.configs[id]
	return ok && cfg.Hidden
}

// navigationCount returns how many navigate actions were issued for an id.
func (m *mockOperator) navigationCount(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.navTimes[id])
}

func (m *mockOperator) navigateIntervals(id string) []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	times := m.navTimes[id]
	if len(times) < 2 {
		return nil
	}
	var intervals []time.Duration
	for i := 1; i < len(times); i++ {
		intervals = append(intervals, times[i].Sub(times[i-1]))
	}
	return intervals
}

func fakeBrowserManager(t *testing.T, op browserinstance.WindowOperator) (*browsermanager.Actor, *testutil.FakeCtx) {
	t.Helper()
	browserinstance.SetWindowOperator(op)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	bm := &browsermanager.Actor{}
	if err := bm.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := bm.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	// Replace the context's self reference with a dispatcher that calls the
	// registered handlers so external actors can invoke browsermanager.
	ctx.SelfRef = actorRefFromRegs(ctx)
	return bm, ctx
}

func actorRefFromRegs(ctx *testutil.FakeCtx) ref.Ref {
	return testutil.NewFakeRef(ctx.Self().ID(), func(callID string, payload any) any {
		handler, ok := ctx.Regs[callID]
		if !ok {
			return fmt.Errorf("actor: no handler registered for %s", callID)
		}
		h := reflect.ValueOf(handler)
		if h.Kind() != reflect.Func {
			return fmt.Errorf("actor: handler %s is not a func", callID)
		}
		in := make([]reflect.Value, h.Type().NumIn())
		for i := 0; i < h.Type().NumIn(); i++ {
			argType := h.Type().In(i)
			switch argType.String() {
			case "actor.Context":
				in[i] = reflect.ValueOf(ctx)
			default:
				if payload == nil {
					in[i] = reflect.Zero(argType)
				} else {
					in[i] = reflect.ValueOf(payload)
				}
			}
		}
		out := h.Call(in)
		if len(out) != 2 {
			return fmt.Errorf("actor: handler %s returned %d values, want 2", callID, len(out))
		}
		if err := out[1].Interface(); err != nil {
			return err.(error)
		}
		return out[0].Interface()
	})
}

func actorWithBrowser(t *testing.T, op browserinstance.WindowOperator) (*Actor, *testutil.FakeCtx, *mockOperator) {
	t.Helper()
	_, bmCtx := fakeBrowserManager(t, op)

	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
	}
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "browsermanager" {
			return bmCtx.Self(), true
		}
		return nil, false
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx, op.(*mockOperator)
}

func waitForState(t *testing.T, a *Actor, ctx *testutil.FakeCtx, id string, target State, timeout time.Duration) Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := a.handleGet(ctx, GetReq{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if task.State == target {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s within %v", id, target, timeout)
	return Task{}
}

func sampleObservation(url, title string, links []string) *browserusegen.BrowserPageObservation {
	elems := []browserusegen.BrowserElement{
		{ID: "h1", TagName: "h1", Selector: "h1", Text: title},
	}
	for i, href := range links {
		elems = append(elems, browserusegen.BrowserElement{
			ID:      "a" + string(rune('0'+i)),
			TagName: "a",
			Selector: "a:nth-of-type(" + string(rune('1'+i)) + ")",
			Href:    href,
			Text:    "link " + string(rune('1'+i)),
		})
	}
	return &browserusegen.BrowserPageObservation{
		ObservationID: "obs-" + url,
		URL:           url,
		Title:         title,
		Elements:      elems,
	}
}

func TestEngine_HiddenWindowCrawlsAndExtracts(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":         sampleObservation("https://example.com", "Home", []string{"/about", "/contact"}),
		"https://example.com/about":   sampleObservation("https://example.com/about", "About", nil),
		"https://example.com/contact": sampleObservation("https://example.com/contact", "Contact", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	cfg := Config{
		Seeds:           []string{"https://example.com"},
		MaxDepth:        2,
		MaxPages:        10,
		SameDomain:      true,
		Mode:            "hidden_window",
		ExtractSchema:   "BrowserCrawlPageResult",
		ExtractSelectors: map[string]any{"title": "h1"},
	}
	resp, err := a.handleSubmit(ctx, SubmitReq{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}

	task := waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)

	if len(task.CrawledURLs) != 3 {
		t.Errorf("expected 3 crawled URLs, got %v", task.CrawledURLs)
	}
	if len(task.Results) != 3 {
		t.Errorf("expected 3 results, got %d", len(task.Results))
	}
	if task.Results[0].Title != "Home" {
		t.Errorf("expected title Home, got %v", task.Results[0].Title)
	}
}

func TestEngine_OperatorNotBound(t *testing.T) {
	// No WindowOperator bound; browsermanager.use will return operator_not_bound.
	_, bmCtx := fakeBrowserManager(t, nil)
	a := &Actor{store: persist.NewFSPersist(t.TempDir())}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "browsermanager" {
			return bmCtx.Self(), true
		}
		return nil, false
	}
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:    []string{"https://example.com"},
		MaxPages: 10,
		Mode:     "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}

	task := waitForState(t, a, ctx, resp.Task.ID, StateFailed, 5*time.Second)
	if task.Error != "operator_not_bound" {
		t.Errorf("expected operator_not_bound failure, got state=%q error=%q", task.State, task.Error)
	}
}

func TestEngine_RateLimit(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":       sampleObservation("https://example.com", "Home", []string{"/about"}),
		"https://example.com/about": sampleObservation("https://example.com/about", "About", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		RateLimit:  1,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)
	intervals := op.navigateIntervals(resp.Task.ID)
	if len(intervals) < 1 {
		t.Fatalf("expected at least one recorded interval, got %d navigations", len(intervals)+1)
	}
	if intervals[0] < time.Second {
		t.Errorf("expected rate limit interval >= 1s, got %v", intervals[0])
	}
}

func TestEngine_ResumesFromPersistedFrontier(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":       sampleObservation("https://example.com", "Home", []string{"/about"}),
		"https://example.com/about": sampleObservation("https://example.com/about", "About", nil),
	}

	dir := t.TempDir()
	actorID := testutil.GenActorID()

	// First actor: start a crawl and let it finish one page, then stop.
	{
		op := newMockOperator(pages)
		_, bmCtx := fakeBrowserManager(t, op)
		a := &Actor{store: persist.NewFSPersist(dir)}
		ctx := testutil.HumanCtx(actorID)
		ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
		}
		ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
			if name == "browsermanager" {
				return bmCtx.Self(), true
			}
			return nil, false
		}
		if err := a.OnInit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.OnStart(ctx); err != nil {
			t.Fatal(err)
		}
		resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
			Seeds:      []string{"https://example.com"},
			MaxPages:   10,
			SameDomain: true,
			Mode:       "hidden_window",
		}})
		if err != nil {
			t.Fatal(err)
		}
		// Wait for the first page to be crawled and recorded.
		waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)
		if err := a.OnStop(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// Second actor with same store: task should still be done and not re-crawl.
	{
		op := newMockOperator(pages)
		_, bmCtx := fakeBrowserManager(t, op)
		a := &Actor{store: persist.NewFSPersist(dir)}
		ctx := testutil.HumanCtx(actorID)
		ctx.SpawnFn = func(_ actor.Props, _ string) (ref.Ref, error) {
			return testutil.NewFakeRef(testutil.GenActorID(), nil), nil
		}
		ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
			if name == "browsermanager" {
				return bmCtx.Self(), true
			}
			return nil, false
		}
		if err := a.OnInit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := a.OnStart(ctx); err != nil {
			t.Fatal(err)
		}
		list, err := a.handleList(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Tasks) != 1 {
			t.Fatalf("expected 1 restored task, got %d", len(list.Tasks))
		}
		if list.Tasks[0].State != StateDone {
			t.Errorf("expected restored task done, got %q", list.Tasks[0].State)
		}
		if len(list.Tasks[0].CrawledURLs) != 2 {
			t.Errorf("expected 2 crawled URLs after resume, got %v", list.Tasks[0].CrawledURLs)
		}
	}
}

// TestEngine_LoginWallSuspends verifies the sequential handoff lifecycle:
// engine hits a login wall -> task suspends as awaiting_login and releases the
// hidden window -> crawl.handoff opens a visible window on the same profile ->
// login_done closes the visible window and resumes the hidden crawl to
// completion.
func TestEngine_LoginWallSuspends(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":          sampleObservation("https://example.com", "Home", []string{"/login", "/dashboard"}),
		"https://example.com/login":    sampleObservation("https://example.com/login", "Login", nil),
		"https://example.com/dashboard": sampleObservation("https://example.com/dashboard", "Dashboard", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}

	task := waitForState(t, a, ctx, resp.Task.ID, StateAwaitingLogin, 5*time.Second)
	if task.LoginWallURL != "https://example.com/login" {
		t.Errorf("expected login wall url recorded, got %q", task.LoginWallURL)
	}
	// The hidden window was released (closed) but the profile was not deleted.
	// The close happens just after the state transition, so poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !op.closedContains(resp.Task.ID) {
		time.Sleep(10 * time.Millisecond)
	}
	if !op.closedContains(resp.Task.ID) {
		t.Errorf("expected hidden window to be closed on login wall")
	}
	if op.deletedContains(resp.Task.ID) {
		t.Errorf("profile must not be deleted while awaiting login")
	}
	// The seed is crawled, the login page is not, and the dashboard is still queued.
	latest, _ := a.handleGet(ctx, GetReq{ID: resp.Task.ID})
	if len(latest.CrawledURLs) != 1 {
		t.Errorf("expected 1 crawled url before suspension, got %v", latest.CrawledURLs)
	}
	if len(latest.Frontier) != 1 || latest.Frontier[0] != "https://example.com/dashboard" {
		t.Errorf("expected dashboard queued, got %v", latest.Frontier)
	}
}

func TestHandoff_OpensVisibleWindowThenResumes(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":          sampleObservation("https://example.com", "Home", []string{"/login", "/dashboard"}),
		"https://example.com/login":    sampleObservation("https://example.com/login", "Login", nil),
		"https://example.com/dashboard": sampleObservation("https://example.com/dashboard", "Dashboard", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}
	task := waitForState(t, a, ctx, resp.Task.ID, StateAwaitingLogin, 5*time.Second)

	// Handoff may only be requested while awaiting_login.
	_, err = a.handleBrowserCrawlHandoff(ctx, browserusegen.BrowserCrawlHandoffReq{TaskID: task.ID})
	if err != nil {
		t.Fatalf("handoff failed: %v", err)
	}
	if !op.createdContains(resp.Task.ID) {
		t.Errorf("expected handoff to open a window on the same profile")
	}
	if op.isHidden(resp.Task.ID) {
		t.Errorf("handoff window must be visible")
	}

	// login_done closes the visible window and resumes the hidden crawl.
	_, err = a.handleLoginDone(ctx, LoginDoneReq{ID: task.ID})
	if err != nil {
		t.Fatalf("login_done failed: %v", err)
	}

	final := waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)
	if len(final.CrawledURLs) != 2 {
		t.Errorf("expected 2 crawled urls after resume, got %v", final.CrawledURLs)
	}
	if len(final.Results) != 2 {
		t.Errorf("expected 2 results after resume, got %d", len(final.Results))
	}
	// After completion the profile is removed and deleted (async cleanup after the
	// done transition).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !op.deletedContains(resp.Task.ID) {
		time.Sleep(10 * time.Millisecond)
	}
	if !op.deletedContains(resp.Task.ID) {
		t.Errorf("expected profile deleted after task completion")
	}
}

func TestHandoff_RejectsNonAwaitingTask(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com": sampleObservation("https://example.com", "Home", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The task runs to done without ever hitting a login wall.
	waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)

	_, err = a.handleBrowserCrawlHandoff(ctx, browserusegen.BrowserCrawlHandoffReq{TaskID: resp.Task.ID})
	if err == nil {
		t.Fatal("expected error when requesting handoff for a non-awaiting task")
	}
}

func TestCancel_AwaitingLoginRemovesInstance(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":       sampleObservation("https://example.com", "Home", []string{"/login"}),
		"https://example.com/login": sampleObservation("https://example.com/login", "Login", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}
	task := waitForState(t, a, ctx, resp.Task.ID, StateAwaitingLogin, 5*time.Second)

	cancelled, err := a.handleCancel(ctx, CancelReq{ID: task.ID, Reason: "abandoned"})
	if err != nil {
		t.Fatalf("cancel failed: %v", err)
	}
	if cancelled.State != StateCancelled {
		t.Errorf("expected cancelled, got %q", cancelled.State)
	}
	if !op.deletedContains(resp.Task.ID) {
		t.Errorf("expected profile deleted after cancelling an awaiting-login task")
	}
}

// waitForEngineExit blocks until the background engine goroutine for the task
// has fully finished (including its deferred browser-instance cleanup), which
// is the point where create/remove side effects are complete.
func waitForEngineExit(t *testing.T, a *Actor, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.enginesMu.Lock()
		_, ok := a.engines[id]
		a.enginesMu.Unlock()
		if !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("engine for task %s did not exit", id)
}

// mountInstance registers a pre-existing user-mounted browser instance with
// the browsermanager, mimicking a window the user opened before the crawl
// task mounted it.
func mountInstance(t *testing.T, a *Actor, ctx *testutil.FakeCtx, id, url string) {
	t.Helper()
	_, err := a.createBrowserInstanceRaw(ctx, domain.BrowserInstanceConfig{
		ID:     id,
		Name:   "mount-" + id,
		URL:    url,
		Open:   true,
		Hidden: false,
		Mode:   "window",
		State:  domain.BrowserWindowState{URL: url, Title: "mount-" + id, Width: 1024, Height: 768},
	})
	if err != nil {
		t.Fatalf("mount instance: %v", err)
	}
}

func TestEngine_ReusesMountedInstance(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":       sampleObservation("https://example.com", "Home", []string{"/about"}),
		"https://example.com/about": sampleObservation("https://example.com/about", "About", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	// The user-mounted window exists before the crawl task starts.
	const mounted = "mounted-window-1"
	mountInstance(t, a, ctx, mounted, "https://example.com")

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
		InstanceID: mounted,
	}})
	if err != nil {
		t.Fatal(err)
	}

	task := waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)
	if len(task.CrawledURLs) != 2 {
		t.Errorf("expected 2 crawled urls, got %v", task.CrawledURLs)
	}
	waitForEngineExit(t, a, resp.Task.ID)

	// The crawl ran on the mounted instance: navigations were issued against
	// it, and its profile survived task completion.
	if n := op.navigationCount(mounted); n != 2 {
		t.Errorf("expected 2 navigations on mounted instance, got %d", n)
	}
	if op.deletedContains(mounted) {
		t.Errorf("mounted instance must not be removed after task completion")
	}
	// No task-dedicated instance lifecycle either.
	if op.deletedContains(resp.Task.ID) {
		t.Errorf("task-dedicated instance must not be removed when InstanceID is set")
	}
	if n := op.navigationCount(resp.Task.ID); n != 0 {
		t.Errorf("expected no navigations on task id, got %d", n)
	}
}

func TestEngine_MountedInstanceLoginWallKeepsWindow(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com":          sampleObservation("https://example.com", "Home", []string{"/login", "/dashboard"}),
		"https://example.com/login":    sampleObservation("https://example.com/login", "Login", nil),
		"https://example.com/dashboard": sampleObservation("https://example.com/dashboard", "Dashboard", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	// The user-mounted window exists before the crawl task starts.
	const mounted = "mounted-window-2"
	mountInstance(t, a, ctx, mounted, "https://example.com")

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
		InstanceID: mounted,
	}})
	if err != nil {
		t.Fatal(err)
	}

	task := waitForState(t, a, ctx, resp.Task.ID, StateAwaitingLogin, 5*time.Second)
	if task.LoginWallURL != "https://example.com/login" {
		t.Errorf("expected login wall url recorded, got %q", task.LoginWallURL)
	}
	waitForEngineExit(t, a, resp.Task.ID)

	// The mounted window is user-owned: the engine must leave it open and
	// must not delete its profile while awaiting login.
	if op.closedContains(mounted) {
		t.Errorf("mounted window must stay open on login wall")
	}
	if op.deletedContains(mounted) {
		t.Errorf("mounted instance profile must not be deleted on login wall")
	}
}

func TestEngine_EmptyInstanceIDCreatesAndRemovesTaskInstance(t *testing.T) {
	pages := map[string]*browserusegen.BrowserPageObservation{
		"https://example.com": sampleObservation("https://example.com", "Home", nil),
	}
	op := newMockOperator(pages)
	a, ctx, _ := actorWithBrowser(t, op)

	resp, err := a.handleSubmit(ctx, SubmitReq{Config: Config{
		Seeds:      []string{"https://example.com"},
		MaxPages:   10,
		SameDomain: true,
		Mode:       "hidden_window",
	}})
	if err != nil {
		t.Fatal(err)
	}

	waitForState(t, a, ctx, resp.Task.ID, StateDone, 5*time.Second)
	waitForEngineExit(t, a, resp.Task.ID)

	// Empty InstanceID keeps the legacy lifecycle: a task-dedicated hidden
	// instance is created up front and removed (profile deleted) at the end.
	// The window Create call lives in the browserinstance child actor (faked
	// in tests), so only the remove side effect is observable here.
	if !op.deletedContains(resp.Task.ID) {
		t.Errorf("expected task-dedicated instance to be removed after completion")
	}
}
