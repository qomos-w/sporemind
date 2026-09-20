package browserinstance

import (
	"testing"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type fakeWindowOperator struct {
	created        []string
	closed         []string
	deletedProfile []string
	navigated      map[string]string
	updated        map[string]domain.BrowserInstanceConfig
}

func (f *fakeWindowOperator) Create(id string, cfg domain.BrowserInstanceConfig) error {
	f.created = append(f.created, id)
	if f.updated == nil {
		f.updated = make(map[string]domain.BrowserInstanceConfig)
	}
	f.updated[id] = cfg
	return nil
}

func (f *fakeWindowOperator) Close(id string) error {
	f.closed = append(f.closed, id)
	return nil
}

func (f *fakeWindowOperator) Navigate(id, url string) error {
	if f.navigated == nil {
		f.navigated = make(map[string]string)
	}
	f.navigated[id] = url
	return nil
}

func (f *fakeWindowOperator) UpdateConfig(id string, cfg domain.BrowserInstanceConfig) error {
	if f.updated == nil {
		f.updated = make(map[string]domain.BrowserInstanceConfig)
	}
	f.updated[id] = cfg
	return nil
}

func (f *fakeWindowOperator) DeleteProfile(id string) error {
	f.deletedProfile = append(f.deletedProfile, id)
	return nil
}

func (f *fakeWindowOperator) Observe(id string) (*domain.BrowserPageObservation, error) {
	return nil, nil
}

func (f *fakeWindowOperator) Use(id string, req domain.BrowserUseReq) (*domain.BrowserUseResp, error) {
	return nil, nil
}

func (f *fakeWindowOperator) ExportCookies(id string) (map[string][]domain.BrowserCookieEntry, error) {
	return map[string][]domain.BrowserCookieEntry{
		"example.com": {{Name: "x", Value: "y", Domain: "example.com"}},
	}, nil
}

func (f *fakeWindowOperator) ImportCookies(id string, cookies map[string][]domain.BrowserCookieEntry) (int64, error) {
	var n int64
	for _, group := range cookies {
		n += int64(len(group))
	}
	return n, nil
}

func (f *fakeWindowOperator) RegisterExternal(id string, win ExternalWindow) error {
	return nil
}

func freshBI(t *testing.T, cfg domain.BrowserInstanceConfig) (*Actor, *testutil.FakeCtx, *fakeWindowOperator) {
	t.Helper()
	op := &fakeWindowOperator{}
	SetWindowOperator(op)
	t.Cleanup(func() { SetWindowOperator(nil) })

	a := NewActor(cfg)().(*Actor)
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "browsermanager" {
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "browsermanager.sync_instance" {
					return payload
				}
				return nil
			}), true
		}
		return nil, false
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx, op
}

func TestOnStart_CreatesWindowWhenOpen(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	_, _, op := freshBI(t, cfg)
	if len(op.created) != 1 || op.created[0] != "inst-0" {
		t.Errorf("expected window created for inst-0, got %v", op.created)
	}
}

func TestOnStart_SkipsWindowWhenClosed(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	_, _, op := freshBI(t, cfg)
	if len(op.created) != 0 {
		t.Errorf("expected no window created, got %v", op.created)
	}
}

func TestHandleNavigate(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	_, err := a.handleNavigate(ctx, domain.BrowserManagerNavigateReq{ID: "inst-0", URL: "https://other.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	// Navigation moves the current page (State.URL) only; the settings page
	// (Config.URL) must survive.
	if a.cfg.URL != "https://example.com" {
		t.Errorf("expected settings-page URL preserved, got %q", a.cfg.URL)
	}
	if a.cfg.State.URL != "https://other.example.com" {
		t.Errorf("expected current-page URL updated, got %q", a.cfg.State.URL)
	}
	if op.navigated["inst-0"] != "https://other.example.com" {
		t.Errorf("expected operator navigate, got %q", op.navigated["inst-0"])
	}
}

func TestHandleOpen(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{X: 120, Y: 90, Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	if len(op.created) != 0 {
		t.Fatalf("expected no window created on start for closed instance, got %v", op.created)
	}

	_, err := a.handleOpen(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !a.cfg.Open {
		t.Errorf("expected instance to be open after handleOpen")
	}
	if len(op.created) != 1 || op.created[0] != "inst-0" {
		t.Errorf("expected window created for inst-0, got %v", op.created)
	}
	if op.updated["inst-0"].State.X != 120 {
		t.Errorf("expected state preserved, got X=%d", op.updated["inst-0"].State.X)
	}
}

func TestHandleUpdateState(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		Mode:  "window",
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	updated := cfg
	updated.State.Width = 1920
	updated.State.Height = 1080
	updated.State.PageState = domain.BrowserPageState{
		ScrollX: 123,
		ScrollY: 456,
		Zoom:    1.25,
		Data:    `{"form":{"q":"hello"}}`,
	}
	updated.Open = false

	_, err := a.handleUpdateState(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.State.Width != 1920 {
		t.Errorf("expected width updated, got %d", a.cfg.State.Width)
	}
	if len(op.closed) != 1 || op.closed[0] != "inst-0" {
		t.Errorf("expected window closed when Open becomes false, got %v", op.closed)
	}
	if a.cfg.State.PageState.ScrollX != 123 {
		t.Errorf("expected page state scrollX preserved, got %d", a.cfg.State.PageState.ScrollX)
	}
	if a.cfg.State.PageState.Data != `{"form":{"q":"hello"}}` {
		t.Errorf("expected page state data preserved, got %q", a.cfg.State.PageState.Data)
	}
}

func TestHandleUpdateState_ClosedInstanceDoesNotTouchWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-closed",
		Name:  "closed",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	updated := cfg
	updated.URL = "https://other.example.com"
	updated.State.URL = updated.URL

	if _, err := a.handleUpdateState(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if a.cfg.URL != updated.URL {
		t.Errorf("expected config saved, got %q", a.cfg.URL)
	}
	if len(op.created) != 0 || len(op.closed) != 0 || len(op.updated) != 0 {
		t.Errorf("closed instance must not touch native window, got created=%v closed=%v updated=%v", op.created, op.closed, op.updated)
	}
}

func TestOnStart_TabModeDoesNotCreateWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:   "inst-tab",
		Name: "tab-inst",
		URL:  "https://example.com",
		Open: true,
		Mode: "tab",
	}
	_, _, op := freshBI(t, cfg)
	if len(op.created) != 0 {
		t.Errorf("tab-mode instance should not create a standalone window, got %v", op.created)
	}
}

func TestHandleExportCookies(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, _ := freshBI(t, cfg)
	resp, err := a.handleExportCookies(ctx, gen.BrowserManagerExportCookiesReq{ID: "inst-0"})
	if err != nil {
		t.Fatal(err)
	}
	got := resp.Cookies["example.com"]
	if len(got) != 1 || got[0].Name != "x" {
		t.Errorf("expected exported cookie x on example.com, got %#v", got)
	}
}

func TestHandleExportCookies_DefaultIDAndMismatch(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, _ := freshBI(t, cfg)
	// Empty ID defaults to the instance ID.
	if _, err := a.handleExportCookies(ctx, gen.BrowserManagerExportCookiesReq{}); err != nil {
		t.Fatalf("empty id should default to own id: %v", err)
	}
	// Foreign ID is rejected.
	if _, err := a.handleExportCookies(ctx, gen.BrowserManagerExportCookiesReq{ID: "inst-other"}); err == nil {
		t.Fatal("expected id mismatch error")
	}
}

func TestHandleImportCookies(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, _ := freshBI(t, cfg)
	cookies := map[string][]gen.BrowserCookieEntry{
		"example.com": {
			{Name: "a", Value: "1", Domain: "example.com"},
			{Name: "b", Value: "2", Domain: "example.com"},
		},
	}
	resp, err := a.handleImportCookies(ctx, gen.BrowserManagerImportCookiesReq{ID: "inst-0", Cookies: cookies})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Imported != 2 {
		t.Errorf("expected 2 imported cookies, got %d", resp.Imported)
	}
}

func TestHandleSync_ProxyChangeRecreatesWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		Proxy: "http://old-proxy:8080",
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	// OnStart creates the window, so reset recorded operations.
	op.created = nil
	op.closed = nil
	op.updated = nil

	updated := cfg
	updated.Proxy = "http://new-proxy:9090"

	_, err := a.handleSync(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Proxy != "http://new-proxy:9090" {
		t.Errorf("expected proxy updated, got %q", a.cfg.Proxy)
	}
	if len(op.closed) != 1 || op.closed[0] != "inst-0" {
		t.Errorf("expected window closed for proxy change, got %v", op.closed)
	}
	if len(op.created) != 1 || op.created[0] != "inst-0" {
		t.Errorf("expected window recreated for proxy change, got %v", op.created)
	}
	// The recreated window should carry the new proxy in its config.
	if op.updated["inst-0"].Proxy != "http://new-proxy:9090" {
		t.Errorf("expected recreated window config with new proxy, got %q", op.updated["inst-0"].Proxy)
	}
}

func TestHandleSync_ProxyChangeClosedInstanceDoesNotTouchWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		Proxy: "http://old-proxy:8080",
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	op.created = nil
	op.closed = nil
	op.updated = nil

	updated := cfg
	updated.Proxy = "http://new-proxy:9090"

	_, err := a.handleSync(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Proxy != "http://new-proxy:9090" {
		t.Errorf("expected proxy updated, got %q", a.cfg.Proxy)
	}
	if len(op.created) != 0 {
		t.Errorf("closed instance must not create window, got %v", op.created)
	}
	if len(op.closed) != 0 {
		t.Errorf("closed instance must not close window, got %v", op.closed)
	}
}

func TestHandleSync_SameProxyDoesNotRecreateWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		Proxy: "http://proxy:8080",
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	op.created = nil
	op.closed = nil
	op.updated = nil

	// Same proxy, just different URL.
	updated := cfg
	updated.URL = "https://other.example.com"

	_, err := a.handleSync(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if len(op.closed) != 0 {
		t.Errorf("same proxy must not close window, got %v", op.closed)
	}
}

func TestHandleUpdateState_ProxyChangeRecreatesWindow(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  true,
		Mode:  "window",
		Proxy: "http://old-proxy:8080",
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, op := freshBI(t, cfg)
	op.created = nil
	op.closed = nil
	op.updated = nil

	updated := cfg
	updated.Proxy = "http://new-proxy:9090"

	_, err := a.handleUpdateState(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if a.cfg.Proxy != "http://new-proxy:9090" {
		t.Errorf("expected proxy updated, got %q", a.cfg.Proxy)
	}
	if len(op.closed) != 1 || op.closed[0] != "inst-0" {
		t.Errorf("expected window closed for proxy change, got %v", op.closed)
	}
	if len(op.created) != 1 || op.created[0] != "inst-0" {
		t.Errorf("expected window recreated for proxy change, got %v", op.created)
	}
	if op.updated["inst-0"].Proxy != "http://new-proxy:9090" {
		t.Errorf("expected recreated window config with new proxy, got %q", op.updated["inst-0"].Proxy)
	}
}

func TestHandleImportCookies_NotFound(t *testing.T) {
	cfg := domain.BrowserInstanceConfig{
		ID:    "inst-0",
		Name:  "primary",
		URL:   "https://example.com",
		Open:  false,
		State: domain.BrowserWindowState{Width: 1024, Height: 768},
	}
	a, ctx, _ := freshBI(t, cfg)
	if _, err := a.handleImportCookies(ctx, gen.BrowserManagerImportCookiesReq{ID: "inst-other", Cookies: nil}); err == nil {
		t.Fatal("expected id mismatch error")
	}
}
