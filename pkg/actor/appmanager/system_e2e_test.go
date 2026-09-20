package appmanager_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/ref"
	sporesch "github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/transport"
	"github.com/qomos-w/sporemind/pkg/actor/appmanager"
	"github.com/qomos-w/sporemind/pkg/builtin/demoapp"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// bootstrapAppManagerSystem boots a minimal real actor system (root +
// appmanager) the same way cmd/sporemind does, for end-to-end acceptance
// tests.
func bootstrapAppManagerSystem(t *testing.T) ref.Ref {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	h, err := runtime.Bootstrap(ctx, runtime.Config{
		Children: []runtime.ChildSpec{
			{Name: "appmanager", Factory: func() actor.Actor { return &appmanager.Actor{} }, RequirePersistent: true, Role: "system"},
		},
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = h.Wait()
	})

	// The app boots in the background; wait on the actual condition (the
	// appmanager service registering) rather than a fixed sleep. Cancelling
	// mid-boot is safe (gospore 5f95dd8). callApp below already retries
	// unregistered callables, so only LookupService needs an explicit wait.
	deadline := time.Now().Add(10 * time.Second)
	for {
		svc, ok := h.App().LookupService("appmanager")
		if ok {
			return svc
		}
		if time.Now().After(deadline) {
			t.Fatal("appmanager service not registered within 10s")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// callApp invokes an appmanager callable and decodes the reply into out via
// the generic decoded form (map / struct) using a JSON round-trip. Optional
// headers are forwarded to the actor invocation as gospore caller metadata.
func callApp(t *testing.T, svc ref.Ref, callID string, payload any, out any, headers ...map[string]string) {
	t.Helper()
	for attempt := 0; attempt < 20; attempt++ {
		call := svc.Invoke(context.Background(), callID, payload, headers...)
		if call == nil {
			t.Fatalf("%s: invoke returned nil", callID)
		}
		v, err := call.Recv()
		_ = call.Close()
		if err == nil {
			raw, marshalErr := json.Marshal(v)
			if marshalErr != nil {
				t.Fatalf("%s: marshal reply: %v", callID, marshalErr)
			}
			if unmarshalErr := json.Unmarshal(raw, out); unmarshalErr != nil {
				t.Fatalf("%s: decode reply %s: %v", callID, raw, unmarshalErr)
			}
			return
		}
		if !strings.Contains(err.Error(), `call ID "sporeapp.invoke" not registered`) || attempt == 19 {
			t.Fatalf("%s: %v", callID, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func registerDemoApp(t *testing.T, svc ref.Ref) string {
	t.Helper()
	var status gen.AppStatus
	callApp(t, svc, "appmanager.register", demoapp.RegisterReq(), &status)
	if status.ID != demoapp.Manifest.ID {
		t.Fatalf("register returned status for %q, want %q", status.ID, demoapp.Manifest.ID)
	}
	// Create a session for the demo-agent caller. The session token is the
	// authorization credential for subsequent appmanager.invoke calls.
	var session gen.AppSessionCreateResp
	callApp(t, svc, "appmanager.session_create", gen.AppSessionCreateReq{AppID: demoapp.Manifest.ID, ViewID: "home"}, &session,
		map[string]string{"gospore.caller_role": "agent", "gospore.caller_subject": "demo-agent"})
	if session.Token == "" {
		t.Fatal("session_create returned empty token")
	}
	return session.Token
}

var echoDesc = sporesch.TypeDesc{Kind: sporesch.TypeKindStruct, Name: "DemoEchoReq", TypeID: sporesch.TypeID(gen.DemoEchoReqSchemaID)}

// encodeEchoTBC produces the wire payload a BinaryCodec client would send
// for the demo app's echo callable.
func encodeEchoTBC(t *testing.T, req gen.DemoEchoReq) []byte {
	t.Helper()
	data, err := codec.NewBinary().Encode(echoDesc, req)
	if err != nil {
		t.Fatalf("encode TBC: %v", err)
	}
	if !codec.IsTBCData(data) {
		t.Fatal("expected TBC wire data")
	}
	return data
}

func invokeDemoApp(t *testing.T, svc ref.Ref, sessionToken, callable string, payload []byte) gen.AppManagerInvokeResp {
	t.Helper()
	var resp gen.AppManagerInvokeResp
	callApp(t, svc, "appmanager.invoke", gen.AppManagerInvokeReq{
		ID:           demoapp.Manifest.ID,
		Callable:     callable,
		AgentID:      "demo-agent",
		SessionToken: sessionToken,
		Payload:      payload,
	}, &resp)
	return resp
}

// TestSystemE2EDemoAppBinaryCodecRoundTrip is the §8 acceptance test over a
// real actor system: register the builtin demo app through AppManager, then
// invoke its typed echo callable with TBC-encoded wire bytes and verify the
// response comes back BinaryCodec-encoded with identical content — the script
// executed with a natively decoded struct parameter.
func TestSystemE2EDemoAppBinaryCodecRoundTrip(t *testing.T) {
	svc := bootstrapAppManagerSystem(t)
	sessionToken := registerDemoApp(t, svc)

	resp := invokeDemoApp(t, svc, sessionToken, "echo", encodeEchoTBC(t, gen.DemoEchoReq{Text: "hello", N: 21}))

	if !codec.IsTBCData(resp.Payload) {
		t.Fatalf("expected TBC response, got %q", resp.Payload)
	}
	var out gen.DemoEchoReq
	view := transport.View{Kind: transport.ViewKindFull, Schema: echoDesc, Data: resp.Payload}
	if err := (&transport.BinaryCodec{}).DecodeInto(view, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.Text != "hello" || out.N != 21 {
		t.Fatalf("echo mismatch: got %+v", out)
	}

	// The invocation must be audited as allowed.
	var audit gen.AppManagerAuditResp
	callApp(t, svc, "appmanager.audit", gen.AppManagerAuditReq{AppID: demoapp.Manifest.ID}, &audit)
	found := false
	for _, rec := range audit.Records {
		if rec.Callable == "echo" && rec.Allowed {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected allowed audit record for echo, got %+v", audit.Records)
	}
}

// TestSystemE2EDemoAppJSONPath verifies the positional JSON calling
// convention keeps working over the real system for scalar callables.
func TestSystemE2EDemoAppJSONPath(t *testing.T) {
	svc := bootstrapAppManagerSystem(t)
	sessionToken := registerDemoApp(t, svc)

	pong := invokeDemoApp(t, svc, sessionToken, "ping", nil)
	if codec.IsTBCData(pong.Payload) {
		t.Fatal("ping should stay on the JSON path (no declared struct schema)")
	}
	if string(pong.Payload) != `"pong"` {
		t.Fatalf("ping = %s, want %q", pong.Payload, `"pong"`)
	}

	twice := invokeDemoApp(t, svc, sessionToken, "twice", []byte(`[21]`))
	if string(twice.Payload) != "42" {
		t.Fatalf("twice = %s, want 42", twice.Payload)
	}
}
