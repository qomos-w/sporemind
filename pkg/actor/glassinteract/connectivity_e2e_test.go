package glassinteract_test

// Real-socket connectivity smoke: the exact transport path the MentraOS
// rework client uses — HTTP bootstrap → glass JWT → /ws?token= handshake →
// GSF v2 binary WireFrames with TBC payloads. Every other glass e2e drives
// in-process service refs; this one proves the wire path (frame codec, URL
// auth → role=glass, TBC encode/decode round-trip, handler auth guards)
// end-to-end.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/message"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/spore/transport"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/runtime"
)

// Glass schema IDs (schemas/glass.*.spore, mirrored in the generated
// clients). The MentraOS TBC codec keys off these; the server side resolves
// the same IDs through the app schema set.
const (
	schemaClaimReq       = 4020
	schemaClaimResp      = 4021
	schemaGetStateReq    = 4024
	schemaGetStateResp   = 4025
	schemaTelemetryReq   = 4208
	schemaTelemetryResp  = 4209
	glassBootstrapCallID = "glass_interact.bootstrap"
	glassClaimCallID     = "glass_interact.session_claim"
	glassGetStateCallID  = "glass_interact.session_get_state"
	glassTelemetryCallID = "glass_interact.telemetry_report"
	glassConnKey         = "conn-e2e-key"
	glassConnDeviceID    = "dev-conn-e2e"
)

// glassWSClient mimics the MentraOS client's wire behavior: every invoke is
// one binary WireFrame whose payload is TBC-encoded against the callable's
// request schema.
type glassWSClient struct {
	t        *testing.T
	ws       *websocket.Conn
	appC     codec.Codec
	resolver codec.SchemaResolver
	bin      *transport.BinaryCodec
	encID    identity.CanonicalID
	addr     string
	corID    uint64
}

func (c *glassWSClient) invoke(callID string, reqSchemaID uint64, req any, respSchemaID uint64, resp any) error {
	c.t.Helper()
	desc, ok := c.resolver.LookupSchema(reqSchemaID)
	if !ok {
		return fmt.Errorf("schema %d not registered", reqSchemaID)
	}
	view, err := c.bin.Encode(desc, c.encID, req)
	if err != nil {
		return fmt.Errorf("tbc encode %s: %w", callID, err)
	}
	return c.invokeView(callID, view.Data, respSchemaID, resp)
}

// invokeView sends a pre-encoded TBC payload as one binary WireFrame and
// decodes the reply. Split out so callers can inject a custom-encoded payload
// (e.g. a schema-typed struct with a forced non-zero ClassID) while reusing
// the same send/read loop.
func (c *glassWSClient) invokeView(callID string, payload []byte, respSchemaID uint64, resp any) error {
	c.t.Helper()
	c.corID++
	wire := &gateway.WireFrame{
		Flags:   gateway.MakeFlags(gateway.EncodingBinary, gateway.CompressionNone),
		Type:    gateway.FrameTypeInvoke,
		CorID:   c.corID,
		CallID:  callID,
		Payload: payload,
	}
	raw, err := gateway.MarshalWireFrame(wire)
	if err != nil {
		return fmt.Errorf("marshal wire frame: %w", err)
	}
	if err := c.ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		return fmt.Errorf("write binary frame: %w", err)
	}
	for {
		reply, err := c.readWire()
		if err != nil {
			return err
		}
		if reply.CorID != wire.CorID {
			continue // unrelated frame; keep reading
		}
		if reply.Type == gateway.FrameTypeError {
			return fmt.Errorf("%s: %s", callID, reply.ErrorMsg)
		}
		if reply.Type != gateway.FrameTypeReply {
			return fmt.Errorf("%s: unexpected frame type %v", callID, reply.Type)
		}
		if resp == nil {
			return nil
		}
		payload, err := gateway.Decompress(reply.Payload, reply.Flags.Compression())
		if err != nil {
			return fmt.Errorf("decompress reply: %w", err)
		}
		if err := c.appC.DecodeByIDInto(respSchemaID, message.EncodingBinary, payload, resp); err != nil {
			return fmt.Errorf("binary decode: %w", err)
		}
		return nil
	}
}

func (c *glassWSClient) readWire() (*gateway.WireFrame, error) {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	mt, data, err := c.ws.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	if mt != websocket.BinaryMessage {
		return nil, fmt.Errorf("expected binary reply, got text frame")
	}
	return gateway.UnmarshalWireFrame(data)
}

func setupGlassClient(t *testing.T) (*glassWSClient, gen.GlassBootstrapResp) {
	t.Helper()
	config.SetDataDirForTest(t.TempDir())
	t.Cleanup(config.ResetForTest)
	t.Setenv("SPOREMIND_GLASS_KEY", glassConnKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	handle, err := runtime.Bootstrap(ctx, runtime.Config{
		GatewayAddr: "127.0.0.1:0",
		Children: []runtime.ChildSpec{
			{
				Name:              "glassinteract",
				Factory:           func() actor.Actor { return &glassinteract.Actor{} },
				RequirePersistent: true,
				Role:              "system",
				Planner:           true,
			},
		},
	})
	if err != nil {
		t.Fatalf("bootstrap runtime: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = handle.Wait()
	})
	select {
	case <-handle.GatewayReady():
	case <-time.After(5 * time.Second):
		t.Fatal("gateway never became ready")
	}
	addr := handle.App().GatewayServer().Addr()
	appCodec := handle.App().Codec()
	var resolver codec.SchemaResolver = handle.App().Schemas()
	encID, err := identity.NewCanonicalID(1000, 1, 0, 1)
	if err != nil {
		t.Fatal(err)
	}

	bootBody := fmt.Sprintf(`{"Key":%q,"DeviceId":%q}`, glassConnKey, glassConnDeviceID)
	respHTTP, err := http.Post("http://"+addr+"/api/"+glassBootstrapCallID, "application/json", strings.NewReader(bootBody))
	if err != nil {
		t.Fatalf("http bootstrap: %v", err)
	}
	t.Cleanup(func() { respHTTP.Body.Close() })
	if respHTTP.StatusCode != http.StatusOK {
		t.Fatalf("http bootstrap status = %d", respHTTP.StatusCode)
	}
	var boot gen.GlassBootstrapResp
	if err := json.NewDecoder(respHTTP.Body).Decode(&boot); err != nil {
		t.Fatalf("bootstrap resp parse: %v", err)
	}
	if boot.SessionID == "" || boot.Token == "" {
		t.Fatalf("bootstrap resp = %+v, want session id + token", boot)
	}

	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?token="+boot.Token, nil)
	if err != nil {
		t.Fatalf("ws dial with glass token: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	client := &glassWSClient{t: t, ws: ws, appC: appCodec, resolver: resolver, bin: &transport.BinaryCodec{}, encID: encID, addr: addr}
	return client, boot
}

func TestGlassConnectivityOverRealGatewayBinaryWS(t *testing.T) {
	client, boot := setupGlassClient(t)

	// 3. session_claim over a binary TBC frame.
	var claim gen.GlassSessionClaimResp
	if err := client.invoke(glassClaimCallID, schemaClaimReq,
		gen.GlassSessionClaimReq{SessionID: boot.SessionID, DeviceID: boot.DeviceID},
		schemaClaimResp, &claim); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claim.Online || claim.Generation != 1 || claim.SessionID != boot.SessionID {
		t.Fatalf("claim resp = %+v", claim)
	}

	// 4. telemetry_report — glass-frame auth path (role+subject+generation).
	var tele gen.GlassTelemetryResp
	if err := client.invoke(glassTelemetryCallID, schemaTelemetryReq,
		gen.GlassTelemetryReq{SessionID: boot.SessionID, Generation: 1, BatteryLevel: 77},
		schemaTelemetryResp, &tele); err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if !tele.Accepted {
		t.Fatalf("telemetry resp = %+v", tele)
	}

	// 5. session_get_state — glass-only read.
	var state gen.GlassGetStateResp
	if err := client.invoke(glassGetStateCallID, schemaGetStateReq,
		gen.GlassGetStateReq{}, schemaGetStateResp, &state); err != nil {
		t.Fatalf("get_state: %v", err)
	}
	if !state.Active || state.Session == nil || state.Session.SessionID != boot.SessionID {
		t.Fatalf("get_state resp = %+v", state)
	}

	// 6. Negative: an anonymous connection (no token) must be rejected by the
	// claim handler's glass-identity guard.
	anonWS, _, err := websocket.DefaultDialer.Dial("ws://"+client.addr+"/ws", nil)
	if err != nil {
		t.Fatalf("anonymous ws dial: %v", err)
	}
	defer anonWS.Close()
	anon := &glassWSClient{t: t, ws: anonWS, appC: client.appC, resolver: client.resolver, bin: &transport.BinaryCodec{}, encID: client.encID}
	if err := anon.invoke(glassClaimCallID, schemaClaimReq,
		gen.GlassSessionClaimReq{SessionID: boot.SessionID, DeviceID: boot.DeviceID},
		schemaClaimResp, nil); err == nil || !strings.Contains(err.Error(), "glass") {
		t.Fatalf("anonymous claim err = %v, want glass-identity rejection", err)
	}
}

// TestGlassConnectivityTypedUplinkOverRealGateway pins the schema-typed
// (field-index, non-zero ClassID) inbound struct path through the live
// gateway. The Go runtime's own encoder emits anonymous structs for glass
// (ClassID=0), and the MentraOS tbc.ts fork emits typed ones
// (classId=SchemaID) — this test proves the decoder accepts the typed form
// on the real wire, not just at the codec unit level
// (TestBinaryDecodeInto_StructByIndexSchemaTyped). It models the fork's
// encoding by forcing ClassID=schemaID through the same canonical encoder.
func TestGlassConnectivityTypedUplinkOverRealGateway(t *testing.T) {
	client, boot := setupGlassClient(t)

	desc, ok := client.resolver.LookupSchema(schemaClaimReq)
	if !ok {
		t.Fatalf("schema %d not registered", schemaClaimReq)
	}
	desc.ClassID = schemaClaimReq
	view, err := client.bin.Encode(desc, client.encID,
		gen.GlassSessionClaimReq{SessionID: boot.SessionID, DeviceID: boot.DeviceID})
	if err != nil {
		t.Fatalf("typed tbc encode: %v", err)
	}
	if !bytes.HasPrefix(view.Data, []byte("TBC\x03")) {
		t.Fatalf("payload not TBC v3: %x", view.Data[:8])
	}

	var claim gen.GlassSessionClaimResp
	if err := client.invokeView(glassClaimCallID, view.Data, schemaClaimResp, &claim); err != nil {
		t.Fatalf("typed claim: %v", err)
	}
	if !claim.Online || claim.Generation != 1 || claim.SessionID != boot.SessionID {
		t.Fatalf("typed claim resp = %+v", claim)
	}
}
