package pluginhost

import (
	"strings"
	"testing"
	"time"

	pluginhost "github.com/qomos-w/sporemind/pkg/pluginhost"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/domain"
)

func pluginDescriptorData(id string) pluginhost.PluginDescriptorData {
	return pluginhost.PluginDescriptorData{ID: id, Name: id, Runtime: "native", Status: "active"}
}

func newPanelOpActor(dev bool) *Actor {
	return &Actor{Plugins: []PluginDescriptor{{ID: "app.test", Dev: dev}}}
}

func TestPanelOpRejectsUnknownPlugin(t *testing.T) {
	a := &Actor{}
	resp, err := a.handlePanelOp(newDomTestCtx(nil, nil), gen.PluginPanelOpReq{PluginID: "app.missing", Op: "dom"})
	if err != nil {
		t.Fatalf("panel_op: %v", err)
	}
	if resp.Ok || resp.Reason != "plugin not found" {
		t.Errorf("want ok=false reason=plugin not found, got %+v", resp)
	}
}

func TestPanelOpRejectsNonDevPlugin(t *testing.T) {
	a := newPanelOpActor(false)
	resp, err := a.handlePanelOp(newDomTestCtx(nil, nil), gen.PluginPanelOpReq{PluginID: "app.test", Op: "dom"})
	if err != nil {
		t.Fatalf("panel_op: %v", err)
	}
	if resp.Ok || !strings.Contains(resp.Reason, "dev-only") {
		t.Errorf("want ok=false dev-only reason, got %+v", resp)
	}
}

func TestPanelOpValidate(t *testing.T) {
	cases := []struct {
		name string
		req  gen.PluginPanelOpReq
		want string
	}{
		{"unknown op", gen.PluginPanelOpReq{PluginID: "app.test", Op: "screenshot"}, "unknown op"},
		{"eval without expr", gen.PluginPanelOpReq{PluginID: "app.test", Op: "eval"}, "requires Expr"},
		{"click without selector", gen.PluginPanelOpReq{PluginID: "app.test", Op: "click"}, "requires Selector"},
		{"type without text", gen.PluginPanelOpReq{PluginID: "app.test", Op: "type", Selector: "#i"}, "requires Text"},
		{"wait without selector", gen.PluginPanelOpReq{PluginID: "app.test", Op: "wait"}, "requires Selector"},
		{"timeout over cap", gen.PluginPanelOpReq{PluginID: "app.test", Op: "eval", Expr: "1", TimeoutMs: 99999}, "TimeoutMs out of range"},
		{"maxchars over cap", gen.PluginPanelOpReq{PluginID: "app.test", Op: "dom", MaxChars: 9999999}, "MaxChars out of range"},
	}
	a := newPanelOpActor(true)
	for _, tc := range cases {
		resp, err := a.handlePanelOp(newDomTestCtx(nil, nil), tc.req)
		if err != nil {
			t.Fatalf("%s: panel_op: %v", tc.name, err)
		}
		if resp.Ok || !strings.Contains(resp.Reason, tc.want) {
			t.Errorf("%s: want reason containing %q, got %+v", tc.name, tc.want, resp)
		}
	}
}

func TestPanelOpRejectsControlRefusal(t *testing.T) {
	a := newPanelOpActor(true)
	ctx := newDomTestCtx(domain.InterfaceManagerControlResp{Accepted: false, Error: "boom"}, nil)
	resp, err := a.handlePanelOp(ctx, gen.PluginPanelOpReq{PluginID: "app.test", Op: "dom"})
	if err != nil {
		t.Fatalf("panel_op: %v", err)
	}
	if resp.Ok || !strings.Contains(resp.Reason, "boom") {
		t.Errorf("want rejected reason boom, got %+v", resp)
	}
}

func TestPanelOpPutResultRoundTrip(t *testing.T) {
	a := newPanelOpActor(true)
	done := make(chan gen.PluginPanelOpResp, 1)
	ctx := newDomTestCtx(domain.InterfaceManagerControlResp{Accepted: true}, nil)
	go func() {
		resp, err := a.handlePanelOp(ctx, gen.PluginPanelOpReq{PluginID: "app.test", Op: "eval", Expr: "return 1+1"})
		if err != nil {
			t.Errorf("panel_op: %v", err)
		}
		done <- resp
	}()
	// Poll the pending store until the handler's request lands, then push.
	pushed := false
	for i := 0; i < 100 && !pushed; i++ {
		time.Sleep(20 * time.Millisecond)
		a.panelOpsMu.Lock()
		var id string
		for k := range a.panelOps {
			id = k
		}
		a.panelOpsMu.Unlock()
		if id == "" {
			continue
		}
		a.handlePanelOpPut(nil, gen.PluginPanelOpPutReq{PluginID: "app.test", RequestID: id, Ok: true, Result: "2", Ts: time.Now().UnixMilli()})
		pushed = true
	}
	if !pushed {
		select {
		case resp := <-done:
			t.Fatalf("no pending panel op appeared; handler returned early: %+v", resp)
		default:
		}
		t.Fatal("no pending panel op appeared and handler still blocked")
	}
	select {
	case resp := <-done:
		if !resp.Ok || resp.Result != "2" || resp.RequestID == "" {
			t.Errorf("want ok result 2, got %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("panel_op did not return after result push")
	}
}

func TestPanelOpPutRequiresRequestId(t *testing.T) {
	a := &Actor{}
	if _, err := a.handlePanelOpPut(nil, gen.PluginPanelOpPutReq{PluginID: "app.test"}); err == nil {
		t.Fatal("put without RequestId accepted")
	}
}

func TestPanelOpPutFirstResultWins(t *testing.T) {
	a := &Actor{}
	a.handlePanelOpPut(nil, gen.PluginPanelOpPutReq{RequestID: "r1", Ok: true, Result: "first", Ts: 1})
	a.handlePanelOpPut(nil, gen.PluginPanelOpPutReq{RequestID: "r1", Ok: false, Reason: "second", Ts: 2})
	got, ok := a.takePanelOpResult("r1")
	if !ok || !got.Ok || got.Result != "first" {
		t.Errorf("want first result to win, got %+v ok=%v", got, ok)
	}
	if _, ok := a.takePanelOpResult("r1"); ok {
		t.Error("take must remove the entry")
	}
}

func TestPanelOpDevStickyAcrossReregistration(t *testing.T) {
	a := newPanelOpActor(true)
	a.RegisterDescriptor(pluginDescriptorData("app.test"))
	if !a.panelOpDevEnabled("app.test") {
		t.Fatal("dev flag lost across RegisterDescriptor")
	}
	// Dev upsert via markPanelOpDev is a no-op when already set, and never
	// clears (install_local loads pass Dev=false and must not demote).
	a.markPanelOpDev("app.test", false)
	if !a.panelOpDevEnabled("app.test") {
		t.Fatal("markPanelOpDev(dev=false) must never clear the flag")
	}
}
