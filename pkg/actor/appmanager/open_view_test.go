package appmanager

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// openViewEnv records planner calls and answers interfacemanager.control with
// a configurable ack.
type openViewEnv struct {
	t       *testing.T
	accept  bool
	ackErr  string
	mu      chan struct{}
	calls   []string
	payload []any
}

func (e *openViewEnv) ctx() *testutil.FakeCtx {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ifaceRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "interfacemanager" {
			return ifaceRef, true
		}
		return nil, false
	}
	ctx.PlannerFn = func() actor.Planner {
		return lifecyclePlanner{call: func(callID string, payload any) (any, error) {
			e.calls = append(e.calls, callID)
			e.payload = append(e.payload, payload)
			return domain.InterfaceManagerControlResp{Accepted: e.accept, Error: e.ackErr}, nil
		}}
	}
	return ctx
}

func (e *openViewEnv) hasCall(callID string) bool {
	for _, c := range e.calls {
		if c == callID {
			return true
		}
	}
	return false
}

func seedOpenViewActor(t *testing.T) *Actor {
	t.Helper()
	a := newBundleCardsActor(t)
	a.Apps["app.viewdemo"] = gen.AppManifest{
		ID: "app.viewdemo", Name: "ViewDemo", Namespace: "app.viewdemo",
		Version: "1.0.0", Runtime: "spore",
		Entrypoints: []gen.AppEntrypoint{
			{ID: "settings", Kind: "view", Title: "Settings", Route: "/settings"},
			{ID: "main", Kind: "view", Title: "Main", Route: "/"},
			{ID: "hook", Kind: "panel", Title: "Hook"},
		},
	}
	a.Records["app.viewdemo"] = appRecord{State: "running"}
	return a
}

func TestOpenViewUnknownApp(t *testing.T) {
	a := newBundleCardsActor(t)
	env := &openViewEnv{t: t, accept: true}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.missing"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if resp.Opened {
		t.Fatalf("Opened = true, want false")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("Error = %q, want contains \"not found\"", resp.Error)
	}
	if env.hasCall("interfacemanager.control") {
		t.Errorf("unexpected interfacemanager.control call")
	}
}

func TestOpenViewNotRunning(t *testing.T) {
	a := seedOpenViewActor(t)
	a.Records["app.viewdemo"] = appRecord{State: "stopped"}
	env := &openViewEnv{t: t, accept: true}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.viewdemo"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if resp.Opened || !strings.Contains(resp.Error, "not running") {
		t.Fatalf("resp = %+v, want Opened=false with \"not running\" error", resp)
	}
}

func TestOpenViewDefaultResolvesFirstView(t *testing.T) {
	a := seedOpenViewActor(t)
	env := &openViewEnv{t: t, accept: true}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.viewdemo"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if !resp.Opened || resp.ViewID != "settings" {
		t.Fatalf("resp = %+v, want Opened=true ViewID=settings (first kind=view entrypoint)", resp)
	}
	if len(env.calls) != 1 || env.calls[0] != "interfacemanager.control" {
		t.Fatalf("calls = %v, want [interfacemanager.control]", env.calls)
	}
	ctrl, ok := env.payload[0].(domain.InterfaceManagerControlReq)
	if !ok {
		t.Fatalf("payload type = %T, want InterfaceManagerControlReq", env.payload[0])
	}
	if ctrl.Action != "open_app_view" || ctrl.AppID != "app.viewdemo" || ctrl.ViewID != "settings" {
		t.Errorf("ctrl = %+v, want action=open_app_view app=app.viewdemo view=settings", ctrl)
	}
}

func TestOpenViewExplicitViewId(t *testing.T) {
	a := seedOpenViewActor(t)
	env := &openViewEnv{t: t, accept: true}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.viewdemo", ViewID: "main"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if !resp.Opened || resp.ViewID != "main" {
		t.Fatalf("resp = %+v, want Opened=true ViewID=main", resp)
	}
}

func TestOpenViewUnknownViewId(t *testing.T) {
	a := seedOpenViewActor(t)
	env := &openViewEnv{t: t, accept: true}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.viewdemo", ViewID: "hook"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if resp.Opened || !strings.Contains(resp.Error, "no view entrypoint") {
		t.Fatalf("resp = %+v, want Opened=false with \"no view entrypoint\" error", resp)
	}
}

func TestOpenViewControlRejected(t *testing.T) {
	a := seedOpenViewActor(t)
	env := &openViewEnv{t: t, accept: false, ackErr: "boom"}
	resp, err := a.handleOpenView(env.ctx(), gen.AppManagerOpenViewReq{ID: "app.viewdemo"})
	if err != nil {
		t.Fatalf("handleOpenView: %v", err)
	}
	if resp.Opened || resp.Error != "boom" || resp.ViewID != "settings" {
		t.Fatalf("resp = %+v, want Opened=false Error=boom ViewID=settings", resp)
	}
}
