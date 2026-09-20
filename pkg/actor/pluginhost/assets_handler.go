package pluginhost

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

// handleAssetsPut installs or replaces the HTTP-served asset bundle for a
// plugin (AdminOnly). AppManager calls this when a plugin registers so
// the app's manifest-declared frontend assets are served under
// /plugin/{pluginId}/. The bundle is persisted alongside ArtifactLoads, so
// OnStart recovery re-registers the route without an appmanager push.
func (a *Actor) handleAssetsPut(_ actor.Context, req gen.PluginAssetsPutReq) (gen.PluginAssetsPutResp, error) {
	if req.PluginID == "" {
		return gen.PluginAssetsPutResp{}, fmt.Errorf("pluginhost: plugin id is required")
	}

	bundle := make(map[string][]byte, len(req.Assets))
	for p, data := range req.Assets {
		bundle[p] = data
	}

	a.mu.Lock()
	old, hadOld := a.AssetStores[req.PluginID]
	a.AssetStores[req.PluginID] = bundle
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		// Roll back the staged store so persisted and in-memory state agree.
		a.mu.Lock()
		if hadOld {
			a.AssetStores[req.PluginID] = old
		} else {
			delete(a.AssetStores, req.PluginID)
		}
		a.mu.Unlock()
		return gen.PluginAssetsPutResp{}, fmt.Errorf("pluginhost: persist asset bundle: %w", err)
	}
	a.applyAssetsRouter(req.PluginID, bundle)
	return gen.PluginAssetsPutResp{Registered: int32(len(bundle))}, nil
}

// handleAssetsRemove drops a plugin's asset bundle and unregisters the
// /plugin/{pluginId}/ route (AdminOnly). Idempotent: removing an unknown
// plugin succeeds with Removed=0.
func (a *Actor) handleAssetsRemove(_ actor.Context, req gen.PluginAssetsRemoveReq) (gen.PluginAssetsRemoveResp, error) {
	if req.PluginID == "" {
		return gen.PluginAssetsRemoveResp{}, fmt.Errorf("pluginhost: plugin id is required")
	}

	a.mu.Lock()
	_, existed := a.AssetStores[req.PluginID]
	delete(a.AssetStores, req.PluginID)
	a.mu.Unlock()
	if existed {
		if err := a.Save(); err != nil {
			return gen.PluginAssetsRemoveResp{}, fmt.Errorf("pluginhost: persist asset removal: %w", err)
		}
	}
	a.dropAssetsRouter(req.PluginID)
	removed := int32(0)
	if existed {
		removed = 1
	}
	return gen.PluginAssetsRemoveResp{Removed: removed}, nil
}

// restoreAssetRoutes re-registers the gateway router handler for every
// persisted asset bundle. Called from OnStart so plugin assets survive a
// process restart (wiring point A): pluginhost starts after appmanager in
// the runtime tree and therefore restores its own serving state.
func (a *Actor) restoreAssetRoutes() {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for pluginID, assets := range a.AssetStores {
		a.applyAssetsRouter(pluginID, assets)
	}
}

// applyAssetsRouter installs the in-memory asset handler for pluginID on the
// gateway router. Safe to call when no router is bound (unit tests): the
// bundle stays persisted and is served after the next OnStart.
func (a *Actor) applyAssetsRouter(pluginID string, assets map[string][]byte) {
	if a.router == nil || len(assets) == 0 {
		return
	}
	a.router.Register(pluginID, pluginhost.AssetsHandler(assets))
}

// dropAssetsRouter removes pluginID's route (and thus its asset handler)
// from the gateway router.
func (a *Actor) dropAssetsRouter(pluginID string) {
	if a.router == nil {
		return
	}
	a.router.Unregister(pluginID)
}
