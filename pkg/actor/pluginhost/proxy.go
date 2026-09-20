package pluginhost

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/qomos-w/gospore/actor"
	sdk "github.com/qomos-w/sporemind-plugin-sdk"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// The reverse proxy injects sdk.GatewayTokenHeader (imported single source
// from sporemind-plugin-sdk) on every proxied request so the plugin's loopback
// listener treats the gateway as a trusted internal caller.
const gatewayTokenHeader = sdk.GatewayTokenHeader

// proxyAttach records one attached plugin backend: the reverse-proxy handler
// installed on the gateway router plus the attach material (addr/token) for
// bookkeeping and detach decisions.
type proxyAttach struct {
	addr    string
	token   string
	handler http.Handler
}

// newPluginProxy builds the reverse-proxy handler forwarding a plugin's
// gateway route (/plugin/{id}/*, prefix already stripped by the Router) to
// the plugin backend's loopback listener at addr (host:port).
//
// FlushInterval -1 disables ResponseWriter buffering so SSE events from the
// backend's /events stream are delivered to the browser immediately. The
// Rewrite director scrubs the browser's Cookie and Origin headers (the
// backend authorizes via the injected gateway token, not the browser's
// session) and stamps X-Spore-Gateway-Token on every request.
func newPluginProxy(addr, token string) http.Handler {
	target, err := url.Parse("http://" + addr)
	if err != nil {
		// Unparseable addr surfaces as a 502 at request time rather than a
		// nil handler; attach validates addr before constructing the proxy.
		target = &url.URL{Scheme: "http", Host: addr}
	}
	return &httputil.ReverseProxy{
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Set(gatewayTokenHeader, token)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "plugin backend unavailable", http.StatusBadGateway)
		},
	}
}

// handleProxyAttach installs (or replaces) the reverse-proxy route for a
// plugin backend (AdminOnly; appmanager calls this after the backend
// commits). Router.Register has replace semantics: a live backend proxy
// supersedes the plugin's static assets handler for the same route. Re-attach
// (reload with a new backend addr/token) is the same call.
func (a *Actor) handleProxyAttach(_ actor.Context, req gen.PluginProxyAttachReq) (gen.PluginProxyAttachResp, error) {
	if req.PluginID == "" {
		return gen.PluginProxyAttachResp{}, fmt.Errorf("pluginhost: plugin id is required")
	}
	if req.Addr == "" {
		return gen.PluginProxyAttachResp{}, fmt.Errorf("pluginhost: proxy addr is required")
	}
	proxy := newPluginProxy(req.Addr, req.Token)
	a.mu.Lock()
	if a.proxies == nil {
		a.proxies = map[string]proxyAttach{}
	}
	a.proxies[req.PluginID] = proxyAttach{addr: req.Addr, token: req.Token, handler: proxy}
	a.mu.Unlock()
	if a.router != nil {
		a.router.Register(req.PluginID, proxy)
	}
	return gen.PluginProxyAttachResp{}, nil
}

// handleProxyDetach removes pluginID's reverse-proxy route (AdminOnly;
// appmanager calls this when the backend is cleared). When the plugin still
// has a persisted asset bundle the static assets handler is re-registered as
// the fallback; otherwise the route returns 404. Idempotent for plugins
// without an attached proxy.
func (a *Actor) handleProxyDetach(_ actor.Context, req gen.PluginProxyDetachReq) (gen.PluginProxyDetachResp, error) {
	if req.PluginID == "" {
		return gen.PluginProxyDetachResp{}, fmt.Errorf("pluginhost: plugin id is required")
	}
	a.detachProxy(req.PluginID)
	return gen.PluginProxyDetachResp{}, nil
}

// detachProxy drops pluginID's proxy from the attach table and the router.
// When the plugin has a persisted asset bundle the route falls back to the
// static assets handler; otherwise it is removed (404). No-op when no proxy
// is attached or no router is bound (unit tests).
func (a *Actor) detachProxy(pluginID string) {
	a.mu.Lock()
	_, attached := a.proxies[pluginID]
	delete(a.proxies, pluginID)
	assets, hasAssets := a.AssetStores[pluginID]
	a.mu.Unlock()
	if !attached || a.router == nil {
		return
	}
	if hasAssets && len(assets) > 0 {
		a.router.Register(pluginID, pluginhost.AssetsHandler(assets))
	} else {
		a.router.Unregister(pluginID)
	}
}
