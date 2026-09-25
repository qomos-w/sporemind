package pluginhost

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	// panelOpPollInterval paces the pluginhost.panel_op poll loop while
	// waiting for the iframe's result push (panel_op_put).
	panelOpPollInterval = 50 * time.Millisecond
	// panelOpBudget bounds the whole op round-trip. Ops run inside the
	// panel's own bounded eval/wait timeouts (≤15s), so the host-side budget
	// sits just above that ceiling.
	panelOpBudget = 18 * time.Second
	// panelOpPendingCap bounds the pending-result map; entries are matched
	// by RequestId and evicted on access when older than the budget.
	panelOpPendingCap = 64
	// panelOpMaxResultBytes is the receiver-side backstop mirroring the
	// iframe-side MaxChars cap (131072 chars).
	panelOpMaxResultBytes = 192 * 1024
	// panelOpIframeEvalTimeoutMs / panelOpIframeWaitTimeoutMs are the
	// defaults forwarded to the iframe when TimeoutMs is unset.
	panelOpIframeEvalTimeoutMs = 5000
	panelOpIframeWaitTimeoutMs = 5000
	// panelOpDefaultDomChars / panelOpDefaultEvalChars are the default result
	// bounds forwarded to the iframe when MaxChars is unset.
	panelOpDefaultDomChars  = 16384
	panelOpDefaultEvalChars = 4096
	// panelOpMaxChars is the hard ceiling for MaxChars, matching the
	// dom-snapshot store backstop.
	panelOpMaxChars = 131072
)

var (
	panelOpCounter  atomic.Int64
	panelOpValidOps = map[string]bool{"dom": true, "eval": true, "click": true, "type": true, "wait": true}
)

// pendingPanelOp is one in-flight panel operation awaiting the iframe result.
type pendingPanelOp struct {
	createdAt time.Time
	res       gen.PluginPanelOpPutReq
	done      bool
}

// nextPanelOpRequestID mints a collision-free request id: millisecond wall
// clock plus a process-unique counter.
func nextPanelOpRequestID() string {
	return fmt.Sprintf("panop-%d-%d", time.Now().UnixMilli(), panelOpCounter.Add(1))
}

// registerPanelOpPending records an in-flight request so result pushes can
// land against it; the entry is removed when the poll consumes it or it
// expires.
func (a *Actor) registerPanelOpPending(requestID string) {
	a.panelOpsMu.Lock()
	defer a.panelOpsMu.Unlock()
	if a.panelOps == nil {
		a.panelOps = map[string]*pendingPanelOp{}
	}
	if _, ok := a.panelOps[requestID]; !ok {
		a.panelOps[requestID] = &pendingPanelOp{createdAt: time.Now()}
	}
}

// putPanelOpResult stores an iframe result push for requestId. Later pushes
// for the same id are ignored (first result wins).
func (a *Actor) putPanelOpResult(put gen.PluginPanelOpPutReq) {
	a.panelOpsMu.Lock()
	defer a.panelOpsMu.Unlock()
	if a.panelOps == nil {
		a.panelOps = map[string]*pendingPanelOp{}
	}
	cur, ok := a.panelOps[put.RequestID]
	if ok && cur.done {
		return
	}
	if len(put.Result) > panelOpMaxResultBytes {
		put.Result = put.Result[:panelOpMaxResultBytes]
	}
	if !ok {
		cur = &pendingPanelOp{createdAt: time.Now()}
		if len(a.panelOps) >= panelOpPendingCap {
			a.evictExpiredPanelOpsLocked()
			if len(a.panelOps) >= panelOpPendingCap {
				// Still full: drop the oldest entries to make room.
				var oldestID string
				var oldestAt time.Time
				for id, p := range a.panelOps {
					if oldestID == "" || p.createdAt.Before(oldestAt) {
						oldestID, oldestAt = id, p.createdAt
					}
				}
				delete(a.panelOps, oldestID)
			}
		}
		a.panelOps[put.RequestID] = cur
	}
	cur.res = put
	cur.done = true
}

// evictExpiredPanelOpsLocked drops entries older than the budget window.
func (a *Actor) evictExpiredPanelOpsLocked() {
	cutoff := time.Now().Add(-2 * panelOpBudget)
	for id, p := range a.panelOps {
		if p.createdAt.Before(cutoff) {
			delete(a.panelOps, id)
		}
	}
}

// takePanelOpResult consumes the result for requestId once it has landed;
// an entry that is still pending a push stays in the map.
func (a *Actor) takePanelOpResult(requestID string) (gen.PluginPanelOpPutReq, bool) {
	a.panelOpsMu.Lock()
	defer a.panelOpsMu.Unlock()
	p, ok := a.panelOps[requestID]
	if !ok || !p.done {
		return gen.PluginPanelOpPutReq{}, false
	}
	res := p.res
	delete(a.panelOps, requestID)
	return res, true
}

// panelOpDevEnabled reports whether the plugin is loaded with the dev flag
// (appmanager dev loop). Panel ops are dev-only: installed third-party
// plugins never expose their views to remote control.
func (a *Actor) panelOpDevEnabled(pluginID string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.Plugins {
		if p.ID == pluginID {
			return p.Dev
		}
	}
	return false
}

// markPanelOpDev upserts the dev flag onto a loaded plugin descriptor.
// Sticky: once dev, idempotent re-loads (spawn verification, reload commits)
// without the flag keep the plugin dev, so the gate survives restarts and
// re-loads while the dev registration persists.
func (a *Actor) markPanelOpDev(pluginID string, dev bool) {
	if !dev {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.Plugins {
		if a.Plugins[i].ID == pluginID {
			a.Plugins[i].Dev = true
			return
		}
	}
}

// validatePanelOp checks op-specific parameter requirements.
func validatePanelOp(req gen.PluginPanelOpReq) error {
	if !panelOpValidOps[req.Op] {
		return fmt.Errorf("unknown op %q (expected dom | eval | click | type | wait)", req.Op)
	}
	switch req.Op {
	case "eval":
		if req.Expr == "" {
			return fmt.Errorf("op %q requires Expr", req.Op)
		}
	case "click", "type":
		if req.Selector == "" {
			return fmt.Errorf("op %q requires Selector", req.Op)
		}
		if req.Op == "type" && req.Text == "" {
			return fmt.Errorf("op %q requires Text", req.Op)
		}
	case "wait":
		if req.Selector == "" {
			return fmt.Errorf("op %q requires Selector", req.Op)
		}
	}
	if req.TimeoutMs < 0 || req.TimeoutMs > 15000 {
		return fmt.Errorf("TimeoutMs out of range (0..15000)")
	}
	if req.MaxChars < 0 || req.MaxChars > panelOpMaxChars {
		return fmt.Errorf("MaxChars out of range (0..%d)", panelOpMaxChars)
	}
	return nil
}

// handlePanelOp is the agent-facing panel control surface for dev-registered
// plugins: it relays the op through interfacemanager to the host webview and
// on to the plugin iframe's bridge port, then polls for the result pushed by
// handlePanelOpPut. PureContext handler (stateless invoke goroutine) — same
// exposure rationale as plugin_dom.
func (a *Actor) handlePanelOp(ctx actor.PureContext, req gen.PluginPanelOpReq) (gen.PluginPanelOpResp, error) {
	fail := func(reason string) (gen.PluginPanelOpResp, error) {
		return gen.PluginPanelOpResp{PluginID: req.PluginID, Ok: false, Reason: reason}, nil
	}
	if !a.pluginExists(req.PluginID) {
		return fail("plugin not found")
	}
	if !a.panelOpDevEnabled(req.PluginID) {
		return fail("panel ops are dev-only: plugin is not dev-registered (register_project / reload_project)")
	}
	if err := validatePanelOp(req); err != nil {
		return fail(err.Error())
	}
	ref, found := ctx.LookupService("interfacemanager")
	if !found {
		return fail("interfacemanager service not available")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fail("planner not available")
	}

	requestID := nextPanelOpRequestID()
	spec := gen.PanelOpSpec{
		RequestID: requestID,
		Op:        req.Op,
		Selector:  req.Selector,
		Text:      req.Text,
		Expr:      req.Expr,
		TimeoutMs: req.TimeoutMs,
		MaxChars:  req.MaxChars,
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, ref, "interfacemanager.control", domain.InterfaceManagerControlReq{
		Action:  "request_plugin_panel_op",
		AppID:   req.PluginID,
		PanelOp: &spec,
	}).Await()
	if err != nil {
		return fail(fmt.Sprintf("interfacemanager.control request_plugin_panel_op failed: %v", err))
	}
	if resp, ok := result.(domain.InterfaceManagerControlResp); ok && !resp.Accepted {
		if resp.Error == "" {
			resp.Error = "interface manager rejected request_plugin_panel_op"
		}
		return fail(resp.Error)
	}
	a.registerPanelOpPending(requestID)

	deadline := time.Now().Add(panelOpBudget)
	for {
		if put, ok := a.takePanelOpResult(requestID); ok {
			return gen.PluginPanelOpResp{
				PluginID:  req.PluginID,
				RequestID: requestID,
				Ok:        put.Ok,
				Result:    put.Result,
				Reason:    put.Reason,
			}, nil
		}
		if time.Now().After(deadline) {
			return gen.PluginPanelOpResp{
				PluginID:  req.PluginID,
				RequestID: requestID,
				Ok:        false,
				Reason:    "panel op timed out (no bridge port: panel not mounted or handshake incomplete, or the op exceeded its budget)",
			}, nil
		}
		time.Sleep(panelOpPollInterval)
	}
}

// handlePanelOpPut is the host-shell push carrying an iframe op result,
// matched by RequestId. PureContext: puts arrive on stateless invoke
// goroutines. AdminOnly? No — same Public exposure as plugin_dom_put: the
// frontend shell is the only practical caller and result spoofing is bounded
// (worst case a stale/unmatched RequestId is ignored).
func (a *Actor) handlePanelOpPut(ctx actor.PureContext, req gen.PluginPanelOpPutReq) (gen.PluginPanelOpPutResp, error) {
	if req.RequestID == "" {
		return gen.PluginPanelOpPutResp{}, fmt.Errorf("pluginhost.panel_op_put: RequestId required")
	}
	a.putPanelOpResult(req)
	return gen.PluginPanelOpPutResp{}, nil
}
