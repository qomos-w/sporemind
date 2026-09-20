package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	gosporeactor "github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// browsermanagerLookupCtx resolves only the browsermanager service, standing
// in for the actor context the OnStart snapshot would have provided.
type browsermanagerLookupCtx struct {
	gosporeactor.Context
	ref ref.Ref
}

func (c *browsermanagerLookupCtx) LookupService(name string) (ref.Ref, bool) {
	if name != "browsermanager" {
		return nil, false
	}
	return c.ref, true
}

// rawBytesStream is a unary stream whose RecvRaw yields a fixed payload.
type rawBytesStream struct{ payload []byte }

func (s *rawBytesStream) Recv() (any, error)       { return nil, io.EOF }
func (s *rawBytesStream) RecvRaw() ([]byte, error) { return s.payload, nil }
func (s *rawBytesStream) Close() error             { return nil }

type bmCall struct {
	callID  string
	payload []byte
	headers map[string]string
}

// bmFakeRef records every invoke (callID, payload, headers) and answers from
// a per-callID canned response table.
type bmFakeRef struct {
	mu       sync.Mutex
	calls    []bmCall
	byCallID map[string][]byte
}

func (f *bmFakeRef) ID() id.ActorID          { return id.ActorID{} }
func (f *bmFakeRef) Service() (string, bool) { return "browsermanager", true }

func (f *bmFakeRef) Invoke(_ context.Context, callID string, payload any, headers ...map[string]string) *invoke.Call {
	var body []byte
	switch p := payload.(type) {
	case []byte:
		body = p
	case nil:
	default:
		body, _ = json.Marshal(p)
	}
	var h map[string]string
	if len(headers) > 0 {
		h = headers[0]
	}
	f.mu.Lock()
	f.calls = append(f.calls, bmCall{callID: callID, payload: body, headers: h})
	resp := f.byCallID[callID]
	f.mu.Unlock()
	return invoke.NewCall(invoke.CallModeUnary, &rawBytesStream{payload: resp})
}

func (f *bmFakeRef) recorded() []bmCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bmCall(nil), f.calls...)
}

func cookiesListResp(items ...domain.BrowserInstanceConfig) []byte {
	out := domain.BrowserManagerListResp{}
	for _, c := range items {
		out.Items = append(out.Items, domain.BrowserInstance{Config: c})
	}
	b, _ := json.Marshal(out)
	return b
}

func cookiesCannedFake(t *testing.T) *bmFakeRef {
	t.Helper()
	bm := &bmFakeRef{byCallID: map[string][]byte{}}
	bm.byCallID["browsermanager.list"] = cookiesListResp(
		domain.BrowserInstanceConfig{ID: "inst-1", Name: "Ollama 登录"},
	)
	bm.byCallID["browsermanager.export_cookies"] = marshalOrDie(t, gen.BrowserManagerExportCookiesResp{
		Cookies: map[string][]gen.BrowserCookieEntry{
			"https://ollama.com":  {{Name: "wos-session", Value: "s3cr3t", Domain: ".ollama.com"}},
			"https://example.com": {{Name: "session", Value: "keepout", Domain: "example.com"}},
		},
	})
	return bm
}

func marshalOrDie(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// TestHostBridgeBrowserCookiesDispatch pins the full authorize→local-dispatch
// chain for browser.cookies_export: the callID is denied without the
// browser.cookies.read grant, allowed with it; the local handler resolves the
// instance through browsermanager.list, forwards browsermanager.export_cookies
// with the internal admin caller identity, and applies the Domain filter
// before any cookie bytes leave the host.
func TestHostBridgeBrowserCookiesDispatch(t *testing.T) {
	bm := cookiesCannedFake(t)
	a := &Actor{actorCtx: &browsermanagerLookupCtx{ref: bm}}
	dispatch := func(callID string, req []byte) ([]byte, error) {
		if callID != "browser.cookies_export" {
			return nil, fmt.Errorf("unexpected callID %q", callID)
		}
		return a.handleHostBridgeBrowserCookiesExport(req)
	}

	denied := NewHostBridge(map[string]struct{}{}, dispatch, "app.planusage", nil)
	if denied.Allows("browser.cookies_export") {
		t.Fatal("Allows(browser.cookies_export) = true without browser.cookies.read grant")
	}
	if _, err := denied.Dispatch("browser.cookies_export", []byte(`{"Id":"inst-1"}`)); err == nil || !strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("dispatch without grant: err = %v, want capability not granted", err)
	}

	granted := NewHostBridge(map[string]struct{}{appbinding.CapBrowserCookiesRead: {}}, dispatch, "app.planusage", nil)
	resp, err := granted.Dispatch("browser.cookies_export", []byte(`{"Id":"inst-1","Domain":"ollama.com"}`))
	if err != nil {
		t.Fatalf("dispatch with grant: %v", err)
	}
	var got gen.BrowserManagerExportCookiesResp
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode resp %s: %v", resp, err)
	}
	if len(got.Cookies) != 1 {
		t.Fatalf("cookies sites = %v, want only the ollama.com site after Domain filter", got.Cookies)
	}
	entries := got.Cookies["https://ollama.com"]
	if len(entries) != 1 || entries[0].Name != "wos-session" || entries[0].Value != "s3cr3t" {
		t.Errorf("ollama cookies = %+v, want the wos-session entry", entries)
	}

	calls := bm.recorded()
	if len(calls) != 2 || calls[0].callID != "browsermanager.list" || calls[1].callID != "browsermanager.export_cookies" {
		t.Fatalf("forwarded calls = %+v, want list then export_cookies", calls)
	}
	var fwd gen.BrowserManagerExportCookiesReq
	if err := json.Unmarshal(calls[1].payload, &fwd); err != nil {
		t.Fatalf("decode export payload %s: %v", calls[1].payload, err)
	}
	if fwd.ID != "inst-1" {
		t.Errorf("export payload Id = %q, want inst-1", fwd.ID)
	}
	if calls[1].headers["gospore.caller_role"] != "admin" || calls[1].headers["gospore.caller_subject"] != "sporemind-pluginhost" {
		t.Errorf("export headers = %v, want internal pluginhost admin identity", calls[1].headers)
	}

	// Name-based addressing resolves to the instance id before forwarding.
	resp, err = granted.Dispatch("browser.cookies_export", []byte(`{"Id":"Ollama 登录"}`))
	if err != nil {
		t.Fatalf("dispatch by name: %v", err)
	}
	calls = bm.recorded()
	var byName gen.BrowserManagerExportCookiesReq
	if err := json.Unmarshal(calls[len(calls)-1].payload, &byName); err != nil {
		t.Fatalf("decode export payload %s: %v", calls[len(calls)-1].payload, err)
	}
	if byName.ID != "inst-1" {
		t.Errorf("name-resolved export Id = %q, want inst-1", byName.ID)
	}
	var unfiltered gen.BrowserManagerExportCookiesResp
	if err := json.Unmarshal(resp, &unfiltered); err != nil {
		t.Fatalf("decode unfiltered resp: %v", err)
	}
	if len(unfiltered.Cookies) != 2 {
		t.Errorf("without Domain filter cookies sites = %d, want 2", len(unfiltered.Cookies))
	}
}

// TestHostBridgeBrowserCookiesWhitelist pins the independent-only whitelist:
// reserved profile identifiers (shared global host profile, app-* panel
// profiles) are refused with explicit policy errors whether they arrive as
// the request id or as a resolved instance id, and unknown ids fail closed.
func TestHostBridgeBrowserCookiesWhitelist(t *testing.T) {
	a := &Actor{}

	for _, tc := range []struct {
		req  string
		want string
	}{
		{`{"Id":"global"}`, "shared host browser profile"},
		{`{"Id":"app-panel-1"}`, "app panel profile"},
		{`{}`, "instance id is required"},
	} {
		if _, err := a.handleHostBridgeBrowserCookiesExport([]byte(tc.req)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("req %s: err = %v, want %q", tc.req, err, tc.want)
		}
	}

	// No browsermanager service in range: resolvable-looking ids still fail
	// closed instead of bypassing the manager.
	if _, err := a.handleHostBridgeBrowserCookiesExport([]byte(`{"Id":"inst-1"}`)); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("missing service: err = %v, want not available", err)
	}

	bm := &bmFakeRef{byCallID: map[string][]byte{
		"browsermanager.list": cookiesListResp(domain.BrowserInstanceConfig{ID: "app-panel-1", Name: "Panel"}),
	}}
	withSvc := &Actor{actorCtx: &browsermanagerLookupCtx{ref: bm}}
	if _, err := withSvc.handleHostBridgeBrowserCookiesExport([]byte(`{"Id":"Panel"}`)); err == nil || !strings.Contains(err.Error(), "app panel profile") {
		t.Errorf("resolved app profile: err = %v, want app panel profile", err)
	}

	bm.byCallID["browsermanager.list"] = cookiesListResp(domain.BrowserInstanceConfig{ID: "inst-1", Name: "Real"})
	if _, err := withSvc.handleHostBridgeBrowserCookiesExport([]byte(`{"Id":"inst-42"}`)); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown id: err = %v, want not found", err)
	}
}

// TestHostBridgeBrowserInstancesList pins browser_instances_list: the grant
// gate, the projection onto the four-field refs (Proxy and other config
// fields must not leak even though browsermanager returns them), and that
// the Public browsermanager.list call carries no admin caller headers.
func TestHostBridgeBrowserInstancesList(t *testing.T) {
	listResp := domain.BrowserManagerListResp{
		Items: []domain.BrowserInstance{
			{
				Config: domain.BrowserInstanceConfig{ID: "inst-1", Name: "Ollama 登录", URL: "https://ollama.com", Proxy: "socks5://127.0.0.1:1080", ProxyMode: "custom"},
				Status: domain.BrowserInstanceStatus{ID: "inst-1", Title: "Ollama"},
			},
			{
				Config: domain.BrowserInstanceConfig{ID: "inst-2", Name: "查阅窗", URL: "https://example.com", Mode: "tab"},
				Status: domain.BrowserInstanceStatus{ID: "inst-2", Title: "Example Domain"},
			},
		},
	}
	bm := &bmFakeRef{byCallID: map[string][]byte{
		"browsermanager.list": marshalOrDie(t, listResp),
	}}
	a := &Actor{actorCtx: &browsermanagerLookupCtx{ref: bm}}
	dispatch := func(callID string, req []byte) ([]byte, error) {
		if callID != "browser_instances_list" {
			return nil, fmt.Errorf("unexpected callID %q", callID)
		}
		return a.handleHostBridgeBrowserInstancesList(req)
	}

	denied := NewHostBridge(map[string]struct{}{}, dispatch, "app.planusage", nil)
	if _, err := denied.Dispatch("browser_instances_list", nil); err == nil || !strings.Contains(err.Error(), "capability not granted") {
		t.Fatalf("dispatch without grant: err = %v, want capability not granted", err)
	}

	granted := NewHostBridge(map[string]struct{}{appbinding.CapBrowserInstancesList: {}}, dispatch, "app.planusage", nil)
	resp, err := granted.Dispatch("browser_instances_list", nil)
	if err != nil {
		t.Fatalf("dispatch with grant: %v", err)
	}
	var got appbinding.BrowserInstancesListResp
	if err := json.Unmarshal(resp, &got); err != nil {
		t.Fatalf("decode resp %s: %v", resp, err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(got.Items))
	}
	first := got.Items[0]
	if first.ID != "inst-1" || first.Name != "Ollama 登录" || first.URL != "https://ollama.com" || first.Title != "Ollama" {
		t.Errorf("first ref = %+v, field mismatch", first)
	}
	if got.Items[1].ID != "inst-2" || got.Items[1].Title != "Example Domain" {
		t.Errorf("second ref = %+v, field mismatch", got.Items[1])
	}
	if strings.Contains(string(resp), "socks5") || strings.Contains(string(resp), "Proxy") || strings.Contains(string(resp), "ProxyMode") {
		t.Errorf("resp %s leaks proxy/config fields", resp)
	}

	calls := bm.recorded()
	if len(calls) != 1 || calls[0].callID != "browsermanager.list" {
		t.Fatalf("forwarded calls = %+v, want exactly one browsermanager.list", calls)
	}
	if calls[0].payload != nil {
		t.Errorf("list payload = %s, want nil", calls[0].payload)
	}
	if len(calls[0].headers) != 0 {
		t.Errorf("list headers = %v, want none — Public callable, no admin identity", calls[0].headers)
	}

	// Empty list marshals as Items: [] rather than null so pickers can map it.
	empty := &bmFakeRef{byCallID: map[string][]byte{
		"browsermanager.list": marshalOrDie(t, domain.BrowserManagerListResp{}),
	}}
	emptyA := &Actor{actorCtx: &browsermanagerLookupCtx{ref: empty}}
	raw, err := emptyA.handleHostBridgeBrowserInstancesList(nil)
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if !strings.Contains(string(raw), `"Items":[]`) {
		t.Errorf("empty resp = %s, want Items: []", raw)
	}
}
