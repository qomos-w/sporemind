package pluginhost

import (
	"context"
	"log/slog"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// reportProcessStateToAppManager is the T2 onTransition target wired in
// OnStart: every actual running/stopped/crashed process-state transition is
// forwarded to appmanager.report_process_state as a fire-and-forget Tell so
// the app registry reflects the real transport verdict and crashes surface
// as app_lifecycle events + problems there. A "running" report carries the
// plugin's fresh HTTP listener addr so cold-start restore re-attaches the
// gateway proxy without waiting for a reload/plugin_load round-trip, plus
// the session secret the process was ACTUALLY spawned with (the
// ArtifactLoads entry the restore replayed — appmanager adopts it when its
// persisted record drifted, healing the two-actors-two-files crash window
// that otherwise 401s every /plugin/{id} request until a manual
// unload/load).
//
// This runs on transport goroutines (processOpener spawn/exit/unload), not on
// the owner lane, so it must stay a cheap resolve + send: failures — above
// all appmanager not yet exposed (pluginhostFirst boot order) — are logged
// and never retried or awaited; the next transition re-reports.
func (a *Actor) reportProcessStateToAppManager(pluginID, state, crash, httpAddr string) {
	amRef := a.streamServiceRef("appmanager")
	if amRef == nil {
		slog.Debug("pluginhost: appmanager service not available; process state report dropped",
			"plugin", pluginID, "state", state)
		return
	}
	var secret string
	if state == "running" {
		secret = a.liveOnLoadSecret(pluginID)
	}
	callCtx, cancel := context.WithTimeout(a.actorCtx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := amRef.Invoke(callCtx, "appmanager.report_process_state", gen.AppManagerReportProcessStateReq{
		PluginID:      pluginID,
		State:         state,
		CrashCause:    crash,
		HttpAddr:      httpAddr,
		SessionSecret: secret,
	})
	_ = call.Close()
}

// liveOnLoadSecret returns the session secret embedded in the plugin's
// persisted ArtifactLoads entry — the config the current process was spawned
// from (restore replay) or the last committed load. Empty when no entry or
// no secret applies.
func (a *Actor) liveOnLoadSecret(pluginID string) string {
	a.mu.RLock()
	req, ok := a.ArtifactLoads[pluginID]
	a.mu.RUnlock()
	if !ok {
		return ""
	}
	return pluginhost.OnLoadConfigSecret(req.OnLoadConfig)
}

// notifyAppManagerOnline tells appmanager that pluginhost finished its OnStart
// (domain exposed, restore spawns done), so it reconciles gateway proxy routes
// for running native apps. This exists because the restore-loop "running"
// reports are handled while this actor is still unexposed: the proxy_attach
// tells they trigger cannot resolve pluginhost and are dropped. Same
// fire-and-forget discipline as reportProcessStateToAppManager: a missing
// appmanager (pluginhost-first boot order) is logged and never retried — the
// appmanager's own spawn-loop artifact_load verification covers that order.
func (a *Actor) notifyAppManagerOnline() {
	amRef := a.streamServiceRef("appmanager")
	if amRef == nil {
		slog.Debug("pluginhost: appmanager service not available; online notification dropped")
		return
	}
	callCtx, cancel := context.WithTimeout(a.actorCtx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	call := amRef.Invoke(callCtx, "appmanager.pluginhost_online", gen.AppManagerPluginhostOnlineReq{})
	_ = call.Close()
}
