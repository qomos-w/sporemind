package appmanager_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/builtin/sporecall"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestSystemE2ESporecallBundle drives the "spore 调用能力" bundle over a real
// actor system: register builtin.sporecall, then exercise its relays —
// generic host.invoke against a real host callable (appmanager.list),
// host.invoke_app against a second registered app (builtin.demo), and the
// explicit-error negative path.
func TestSystemE2ESporecallBundle(t *testing.T) {
	svc := bootstrapAppManagerSystem(t)

	// Boot registration: OnStart's ensureBuiltinSporeApps registers the
	// builtin bundle without any explicit register call (mount-gating remains
	// the only path to the capability — this just makes the card mountable).
	// The builtin app reaches State=running asynchronously after appmanager's
	// OnStart returns; the first list snapshot can still show "starting"
	// under rerun load (spore v0.1.2's 4 MiB default VM heap boots slower),
	// so poll until it converges.
	var boot gen.AppManagerListResp
	deadline := time.Now().Add(10 * time.Second)
	for {
		callApp(t, svc, "appmanager.list", gen.AppManagerListReq{}, &boot)
		booted := false
		for _, app := range boot.Items {
			if app.ID == sporecall.Manifest.ID && app.State == "running" {
				booted = true
			}
		}
		if booted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("builtin.sporecall not auto-registered at boot: %+v", boot.Items)
		}
		time.Sleep(50 * time.Millisecond)
	}
	registerDemoApp(t, svc)

	invoke := func(callable string, args string) gen.AppManagerInvokeResp {
		t.Helper()
		var resp gen.AppManagerInvokeResp
		callApp(t, svc, "appmanager.invoke", gen.AppManagerInvokeReq{
			ID:       sporecall.Manifest.ID,
			Callable: callable,
			AgentID:  "sporecall-e2e",
			Payload:  []byte(args),
		}, &resp)
		return resp
	}

	// ping — no host call.
	if got := string(invoke("ping", `[]`).Payload); got != `"pong"` {
		t.Fatalf("ping = %s, want \"pong\"", got)
	}

	// call — generic host.invoke against a real host callable.
	list := invoke("call", `["appmanager.list", {}]`)
	if !strings.Contains(string(list.Payload), `"Items"`) {
		t.Fatalf("appmanager.list relay payload = %s, want Items array", list.Payload)
	}
	var apps gen.AppManagerListResp
	if err := json.Unmarshal(list.Payload, &apps); err != nil {
		t.Fatalf("decode relayed list response: %v", err)
	}
	ids := map[string]bool{}
	for _, app := range apps.Items {
		ids[app.ID] = true
	}
	if !ids[sporecall.Manifest.ID] || !ids["builtin.demo"] {
		t.Fatalf("relayed app list missing registered apps: %+v", ids)
	}

	// call_app — host.invoke_app wrapping appmanager.invoke → builtin.demo.
	// ping (zero-arg, nil nested payload) and twice (positional [21]).
	pong := invoke("call_app", `["builtin.demo", "ping", [], "sporecall-e2e"]`)
	if !strings.Contains(string(pong.Payload), "pong") {
		t.Fatalf("call_app ping payload = %s, want pong", pong.Payload)
	}
	twice := invoke("call_app", `["builtin.demo", "twice", [21], "sporecall-e2e"]`)
	if !strings.Contains(string(twice.Payload), "42") {
		t.Fatalf("call_app twice payload = %s, want 42", twice.Payload)
	}

	// negative — unknown service surfaces an explicit error, not empty success.
	call := svc.Invoke(context.Background(), "appmanager.invoke", gen.AppManagerInvokeReq{
		ID: sporecall.Manifest.ID, Callable: "call", AgentID: "sporecall-e2e",
		Payload: []byte(`["missing.echo", {}]`),
	})
	if call == nil {
		t.Fatal("invoke returned nil")
	}
	v, err := call.Recv()
	_ = call.Close()
	if err == nil {
		raw, _ := json.Marshal(v)
		t.Fatalf("expected error for unknown service, got %s", raw)
	}
	if !strings.Contains(err.Error(), "missing.echo") {
		t.Fatalf("error = %v, want it to name the unknown callID", err)
	}

	// bundle mount projection — the manifest's Bundles entry must surface as a
	// virtual app-bundle component (the mounting surface agents consume, same
	// role builtin:bundle:browser-use plays for host callables).
	var comps gen.AppManagerComponentListResp
	callApp(t, svc, "appmanager.component_list", gen.AppManagerComponentListReq{}, &comps)
	const cardID = "app-bundle:builtin.sporecall:sporecall"
	var listed *gen.ComponentDescriptor
	for i := range comps.Items {
		if comps.Items[i].Ref.CardID == cardID {
			listed = &comps.Items[i]
		}
	}
	if listed == nil {
		t.Fatalf("component_list has no %s (items: %d)", cardID, len(comps.Items))
	}
	wantTools := map[string]bool{
		"app.builtin.sporecall.call":     false,
		"app.builtin.sporecall.call_mcp": false,
		"app.builtin.sporecall.call_app": false,
	}
	for _, tool := range listed.Tools {
		if _, ok := wantTools[tool.ID]; ok {
			wantTools[tool.ID] = true
		}
	}
	for id, seen := range wantTools {
		if !seen {
			t.Errorf("bundle %s missing tool %q", cardID, id)
		}
	}
	var one gen.AppManagerComponentGetResp
	callApp(t, svc, "appmanager.component_get", gen.AppManagerComponentGetReq{CardID: cardID}, &one)
	if one.Component.Ref.CardID != cardID {
		t.Fatalf("component_get resolved %q, want %q", one.Component.Ref.CardID, cardID)
	}
}
