package appmanager

import (
	"fmt"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// pushNativeAssets installs a plugin's manifest-declared asset bundle on
// the pluginhost. The appmanager owns the bundle as part of appRecord.Assets;
// the pluginhost owns the /plugin/{pluginId}/ HTTP route. Communication is
// actor-callable only (Actor-first constraint).
//
// Removal is deliberately NOT performed here: teardown (pluginhost.artifact_unload
// drops the bundle together with the artifact) and hot-reload
// (pluginhost.artifact_reload_prepare swaps it atomically) own the unregister
// side. A fresh register with an empty bundle therefore stays silent, so
// asset-less plugins produce no pluginhost traffic.
func (a *Actor) pushNativeAssets(ctx actor.PureContext, appID string, assets map[string][]byte) error {
	if ctx == nil {
		// Handlers unit-tested without an actor context (nil ctx) cannot
		// reach the pluginhost; production paths always carry one.
		return nil
	}
	if len(assets) == 0 {
		return nil
	}
	pluginRef, found := ctx.LookupService(pluginhostServiceName)
	if !found || pluginRef == nil {
		return fmt.Errorf("appmanager: pluginhost service not available for native assets of %q", appID)
	}
	planner := ctx.Planner()
	if planner == nil {
		return fmt.Errorf("appmanager: planner not available for native assets of %q", appID)
	}
	if _, err := planner.Call(ctx.Lifecycle(), pluginRef, "pluginhost.assets_put", gen.PluginAssetsPutReq{
		PluginID: appID,
		Assets:   assets,
	}).Await(); err != nil {
		return fmt.Errorf("appmanager: push native assets: %w", err)
	}
	return nil
}
