package agent

import (
	"encoding/json"

	"github.com/qomos-w/gospore/actor"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// forwardToPlugins pushes one agent event (step / agent_message_received) to
// pluginhost.event_deliver as a Tell (invoke + immediate close): no child
// actor, no response wait. pluginhost fans it out to plugins whose manifest
// listens to the kind; the per-plugin agent.observe capability gate lives on
// the pluginhost side. Missing pluginhost (early boot, headless, tests) is
// silently skipped — event delivery is best-effort by contract, mirroring
// appmanager's forwardHostEvent.
func (a *Actor) forwardToPlugins(ctx actor.Context, kind string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ref, found := ctx.LookupService("pluginhost")
	if !found || ref == nil {
		return
	}
	if call := ref.Invoke(ctx.Lifecycle(), "pluginhost.event_deliver", gen.PluginEventDeliverReq{
		Kind:    kind,
		Payload: string(raw),
	}); call != nil {
		_ = call.Close()
	}
}
