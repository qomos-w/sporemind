package appmanager

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handlePanelTopology is the read-only answer to "where is this app's panel
// actually served and what does a 401 mean". Agents previously had to netstat
// port-scan for the gateway and then guess whether a 401 from the plugin's
// listener was auth working or a bug; this callable prints the full three-hop
// picture (gateway route -> per-app proxy -> plugin loopback listener) with
// expected outcomes per hop.
func (a *Actor) handlePanelTopology(_ actor.PureContext, req gen.AppManagerPanelTopologyReq) (gen.AppManagerPanelTopologyResp, error) {
	var record appRecord
	var ok bool
	a.withMu(func() {
		record, ok = a.Records[req.ID]
	})
	if !ok {
		return gen.AppManagerPanelTopologyResp{}, fmt.Errorf("app %q is not registered", req.ID)
	}

	resp := gen.AppManagerPanelTopologyResp{
		AppID:      record.Manifest.ID,
		State:      record.State,
		Generation: record.Generation,
	}

	if addr := config.GatewayAddr(); addr != "" {
		resp.GatewayBase = "http://" + addr
	}
	route := firstViewRoute(record)
	if resp.GatewayBase != "" && route != "" {
		suffix := ""
		if record.Generation > 0 {
			suffix = fmt.Sprintf("?v=%d", record.Generation)
		}
		resp.PanelURL = fmt.Sprintf("%s/plugin/%s/%s%s", resp.GatewayBase, record.Manifest.ID, route, suffix)
	}
	resp.PluginListener = record.BackendUrl

	resp.AuthNotes = []string{
		"Gateway hop (/plugin/{id}/...): no auth needed from you — the reverse proxy attaches the per-app X-Gateway-Token header itself. A 200 here means the whole chain works.",
		"Plugin listener hop (direct " + record.BackendUrl + " access): /invoke, /events and non-index static routes REQUIRE the spore_session cookie. A 401/403 from a direct browser/curl hit is auth WORKING, not a bug — cookies are only minted for same-origin panel contexts.",
		"Inside a mounted panel iframe everything is same-origin: fetch/<img>/<video> carry the cookie automatically; generated clients await window.__sporemindAppBaseReady so URLs are prefixed with /plugin/{id}.",
	}
	if record.SessionSecret == "" {
		resp.AuthNotes = append(resp.AuthNotes, "This instance has no session secret recorded: the SDK listener runs in dev mode with auth disabled — a direct hit would 200, which is also expected.")
	}
	resp.ExpectedCodes = []string{
		"GET  {PanelUrl} -> 200 (HTML, Cache-Control: no-store); 404 = route or app id wrong, or app not running",
		"GET  {PluginListener}/ without cookie -> 401/403 (auth on) or 404 for directory requests without index.html (fail-closed static)",
		"POST {PluginListener}/invoke/{callableId} without cookie -> 401; with cookie, unknown/agent-only callable -> 404; handler error -> 500 {\"error\":...}",
		"GET  {PluginListener}/events without cookie -> 401; with cookie -> text/event-stream",
	}
	return resp, nil
}

// firstViewRoute returns the route of the first view-kind entrypoint, the
// same one appmanager.open_view defaults to.
func firstViewRoute(record appRecord) string {
	for _, ep := range record.Manifest.Entrypoints {
		if ep.Kind == "view" && ep.Route != "" {
			return ep.Route
		}
	}
	return ""
}
