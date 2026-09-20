package appmanager

import (
	"context"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// handleOpenView opens an app's view entrypoint in the frontend. It validates
// app existence, runtime state, and view resolution here, then drives the UI
// through interfacemanager.control (action "open_app_view") so the effect is
// identical to a user clicking the app tile. Business errors come back as a
// structured resp (Opened=false + Error) rather than transport errors.
func (a *Actor) handleOpenView(ctx actor.PureContext, req gen.AppManagerOpenViewReq) (gen.AppManagerOpenViewResp, error) {
	fail := func(format string, args ...any) (gen.AppManagerOpenViewResp, error) {
		return gen.AppManagerOpenViewResp{Opened: false, Error: fmt.Sprintf(format, args...)}, nil
	}

	var manifest gen.AppManifest
	var ok bool
	var record appRecord
	a.withMu(func() {
		manifest, ok = a.Apps[req.ID]
		record = a.Records[req.ID]
	})
	if !ok {
		return fail("appmanager: app %q not found", req.ID)
	}
	if state := appRecordState(record); state != "running" {
		return fail("appmanager: app %q is %q, not running", req.ID, state)
	}

	var target *gen.AppEntrypoint
	for i := range manifest.Entrypoints {
		entry := &manifest.Entrypoints[i]
		if entry.Kind != "view" {
			continue
		}
		if req.ViewID == "" || entry.ID == req.ViewID {
			target = entry
			break
		}
	}
	if target == nil {
		if req.ViewID == "" {
			return fail("appmanager: app %q has no view entrypoints", req.ID)
		}
		return fail("appmanager: app %q has no view entrypoint %q", req.ID, req.ViewID)
	}

	ref, found := ctx.LookupService("interfacemanager")
	if !found {
		return fail("appmanager: interfacemanager service not available")
	}
	planner := ctx.Planner()
	if planner == nil {
		return fail("appmanager: planner not available")
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, ref, "interfacemanager.control", domain.InterfaceManagerControlReq{
		Action: "open_app_view",
		AppID:  req.ID,
		ViewID: target.ID,
	}).Await()
	if err != nil {
		return fail("appmanager: interfacemanager.control open_app_view failed: %v", err)
	}
	resp, ok := result.(domain.InterfaceManagerControlResp)
	if !ok {
		return fail("appmanager: interfacemanager.control returned unexpected response")
	}
	if !resp.Accepted {
		if resp.Error == "" {
			resp.Error = "interface manager rejected open_app_view"
		}
		return gen.AppManagerOpenViewResp{Opened: false, ViewID: target.ID, Error: resp.Error}, nil
	}
	return gen.AppManagerOpenViewResp{Opened: true, ViewID: target.ID}, nil
}

// appRecordState mirrors the default of statusFromRecord: an unset record
// state still shows as "registered".
func appRecordState(record appRecord) string {
	if record.State == "" {
		return "registered"
	}
	return record.State
}
