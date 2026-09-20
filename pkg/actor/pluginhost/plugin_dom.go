package pluginhost

import (
	"context"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// domSnapshotPollInterval paces the pluginhost.plugin_dom poll loop; the
	// frontend round-trip (bridge request → iframe serialization → push) is
	// expected to complete well inside the budget.
	domSnapshotPollInterval = 50 * time.Millisecond
	// domSnapshotBudget bounds how long plugin_dom blocks on a reply. PureContext
	// handlers may sleep, but the budget must stay small enough that an agent
	// calling this per turn never stalls long.
	domSnapshotBudget = 3 * time.Second
	// domSnapshotMaxBytes guards the store against oversized pushes. The iframe
	// serializer truncates at source (32KB); this is the receiver-side backstop.
	domSnapshotMaxBytes = 128 * 1024
)

// domSnapshot returns the stored push for pluginID if it is newer than floor.
func (a *Actor) domSnapshot(pluginID string, floor time.Time) (gen.PluginDomPutReq, bool) {
	a.domSnapshotsMu.Lock()
	defer a.domSnapshotsMu.Unlock()
	push, ok := a.domSnapshots[pluginID]
	if !ok || push.Ts < floor.UnixMilli() {
		return gen.PluginDomPutReq{}, false
	}
	return push, true
}

// pluginExists reports whether a plugin with the given ID is currently loaded.
func (a *Actor) pluginExists(pluginID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.Plugins {
		if p.ID == pluginID {
			return true
		}
	}
	return false
}

// handlePluginDom is the agent-facing DOM observability surface: it asks the
// host webview (via interfacemanager.control) to have the plugin iframe
// serialize its DOM through the bridge port, then polls the store until the
// frontend push (handlePluginDomPut) lands or the budget expires.
func (a *Actor) handlePluginDom(ctx actor.PureContext, req gen.PluginDomReq) (gen.PluginDomResp, error) {
	fail := func(reason string) (gen.PluginDomResp, error) {
		return gen.PluginDomResp{PluginID: req.PluginID, Found: false, Reason: reason}, nil
	}
	if !a.pluginExists(req.PluginID) {
		return fail("plugin not found")
	}

	ref, found := ctx.LookupService("interfacemanager")
	if !found {
		return fail("interfacemanager service not available")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fail("planner not available")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, ref, "interfacemanager.control", domain.InterfaceManagerControlReq{
		Action: "request_plugin_dom_snapshot",
		AppID:  req.PluginID,
	}).Await()
	if err != nil {
		return fail(fmt.Sprintf("interfacemanager.control request_plugin_dom_snapshot failed: %v", err))
	}
	if resp, ok := result.(domain.InterfaceManagerControlResp); ok && !resp.Accepted {
		if resp.Error == "" {
			resp.Error = "interface manager rejected request_plugin_dom_snapshot"
		}
		return fail(resp.Error)
	}

	callStart := time.Now()
	deadline := callStart.Add(domSnapshotBudget)
	for {
		if push, ok := a.domSnapshot(req.PluginID, callStart); ok {
			return gen.PluginDomResp{
				PluginID:   req.PluginID,
				Found:      true,
				Snapshot:   push.Snapshot,
				CapturedAt: push.Ts,
			}, nil
		}
		if time.Now().After(deadline) {
			return fail("no panel mounted or snapshot timed out")
		}
		time.Sleep(domSnapshotPollInterval)
	}
}

// handlePluginDomPut stores the DOM snapshot pushed by the host shell on
// behalf of a plugin iframe. State is one bounded string per plugin: a single
// mutex-guarded map slot, mirroring the log ring's isolation rationale.
func (a *Actor) handlePluginDomPut(_ actor.PureContext, req gen.PluginDomPutReq) (gen.PluginDomPutResp, error) {
	if len(req.Snapshot) > domSnapshotMaxBytes {
		return gen.PluginDomPutResp{}, fmt.Errorf("pluginhost: dom snapshot for %q exceeds %d bytes (got %d)", req.PluginID, domSnapshotMaxBytes, len(req.Snapshot))
	}
	a.domSnapshotsMu.Lock()
	if a.domSnapshots == nil {
		a.domSnapshots = map[string]gen.PluginDomPutReq{}
	}
	a.domSnapshots[req.PluginID] = req
	a.domSnapshotsMu.Unlock()
	return gen.PluginDomPutResp{}, nil
}
