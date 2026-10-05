// Package glasssim is a simulated MentraOS glasses device: it speaks the
// exact wire protocol the real client uses — HTTP bootstrap → glass JWT →
// /ws?token= binary-only WebSocket → GSF v2 WireFrames with TBC payloads —
// against any running sporemind gateway. It exists so the full device cycle
// (claim, event subscription, telemetry, speech uplink, interaction report)
// can be exercised in tests and during development without hardware.
package glasssim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qomos-w/gospore/codec"
	"github.com/qomos-w/gospore/gateway"
	"github.com/qomos-w/gospore/message"
	"github.com/qomos-w/gospore/schema"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/spore/transport"

	sporegen "github.com/qomos-w/sporemind/gen"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
	tschema "github.com/qomos-w/spore/schema"
)

// Callables and event kinds of the glass surface (mirror of the MentraOS
// client's CALLABLES / GLASS_EVENT_KINDS tables).
const (
	CallBootstrap   = "glass_interact.bootstrap"
	CallClaim       = "glass_interact.session_claim"
	CallGetState    = "glass_interact.session_get_state"
	CallTelemetry   = "glass_interact.telemetry_report"
	CallSpeechStart = "glass_interact.speech_start"
	CallSpeechChunk = "glass_interact.speech_chunk"
	CallSpeechEnd   = "glass_interact.speech_end"
	CallInteraction = "glass_interact.interaction_report"

	CallSubscribeService = "gospore.events.subscribe_service"
	ServiceName          = "glass_interact"
)

var EventKinds = []string{
	"glass.render",
	"glass.speak",
	"glass.transcript",
	"glass.interaction",
	"glass.hud.update",
	"glass.online",
	"glass.offline",
	"glass.replaced",
	"glass.reconnected",
}

// kindSchemaID maps an event kind to the schema its payload decodes against.
var kindSchemaID = map[string]uint64{
	"glass.render":       gen.GlassRenderEventSchemaID,
	"glass.speak":        gen.GlassSpeakEventSchemaID,
	"glass.transcript":   gen.GlassTranscriptEventSchemaID,
	"glass.interaction":  gen.GlassInteractionEventSchemaID,
	"glass.hud.update":   gen.GlassHudStatusSchemaID,
	"glass.online":       gen.GlassLifecycleEventSchemaID,
	"glass.offline":      gen.GlassLifecycleEventSchemaID,
	"glass.replaced":     gen.GlassLifecycleEventSchemaID,
	"glass.reconnected":  gen.GlassLifecycleEventSchemaID,
}

// Event is one decoded server push: the event kind and its payload
// unmarshalled into the generated event struct (use a type assertion or the
// Raw JSON for printing).
type Event struct {
	Kind    string
	Payload any
	Raw     json.RawMessage
}

// Device is one simulated glasses connection. Not safe for concurrent use
// except reading from Events(); method calls are serialized by the caller.
type Device struct {
	Addr     string // gateway host:port
	Key      string
	DeviceID string

	SessionID  string
	Generation int64

	ws       *websocket.Conn
	codec    codec.Codec
	resolver codec.SchemaResolver
	bin      *transport.BinaryCodec
	encID    identity.CanonicalID

	corID  uint64
	subSeq int

	mu      sync.Mutex
	pending map[uint64]chan *gateway.WireFrame
	kindBySub map[string]string

	events    chan Event
	errs      chan error
	closed    chan struct{}
	closeOnce sync.Once
}

// buildSchemas constructs a standalone schema set from the same static
// fragment the runtime imports, so the simulator can TBC-encode against
// schema IDs without booting a runtime.
func buildSchemas() (schema.Set, error) {
	var fragment protocol.StaticFragment
	if err := json.Unmarshal(sporegen.StaticSchemaFragmentJSON(), &fragment); err != nil {
		return nil, fmt.Errorf("glasssim: parse static fragment: %w", err)
	}
	mgr, err := protocol.NewManager(fragment)
	if err != nil {
		return nil, fmt.Errorf("glasssim: protocol manager: %w", err)
	}
	manifestJSON, err := mgr.ToManifestImportJSON()
	if err != nil {
		return nil, fmt.Errorf("glasssim: manifest import: %w", err)
	}
	var manifest tschema.Manifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return nil, fmt.Errorf("glasssim: parse manifest: %w", err)
	}
	set, err := schema.New("glasssim")
	if err != nil {
		return nil, fmt.Errorf("glasssim: schema set: %w", err)
	}
	if err := schema.ImportFromManifest(set, manifest); err != nil {
		return nil, fmt.Errorf("glasssim: import schemas: %w", err)
	}
	return set, nil
}

// Connect bootstraps over HTTP, dials the /ws endpoint with the returned
// glass JWT, and starts the frame reader pump.
func Connect(ctx context.Context, addr, key, deviceID string) (*Device, error) {
	set, err := buildSchemas()
	if err != nil {
		return nil, err
	}
	encID, err := identity.NewCanonicalID(1000, 1, 0, 1)
	if err != nil {
		return nil, fmt.Errorf("glasssim: canonical id: %w", err)
	}

	body := fmt.Sprintf(`{"Key":%q,"DeviceId":%q}`, key, deviceID)
	respHTTP, err := http.Post("http://"+addr+"/api/"+CallBootstrap, "application/json", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("glasssim: bootstrap: %w", err)
	}
	defer respHTTP.Body.Close()
	if respHTTP.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("glasssim: bootstrap status %d", respHTTP.StatusCode)
	}
	var boot gen.GlassBootstrapResp
	if err := json.NewDecoder(respHTTP.Body).Decode(&boot); err != nil {
		return nil, fmt.Errorf("glasssim: bootstrap resp: %w", err)
	}
	if boot.SessionID == "" || boot.Token == "" {
		return nil, fmt.Errorf("glasssim: bootstrap resp missing session/token: %+v", boot)
	}

	ws, _, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+addr+"/ws?token="+boot.Token, nil)
	if err != nil {
		return nil, fmt.Errorf("glasssim: ws dial: %w", err)
	}

	d := &Device{
		Addr:      addr,
		Key:       key,
		DeviceID:  deviceID,
		SessionID: boot.SessionID,
		ws:        ws,
		codec:     codec.NewMulti(set),
		resolver:  set,
		bin:       &transport.BinaryCodec{},
		encID:     encID,
		pending:   make(map[uint64]chan *gateway.WireFrame),
		kindBySub: make(map[string]string),
		events:    make(chan Event, 64),
		errs:      make(chan error, 4),
		closed:    make(chan struct{}),
	}
	go d.pump()
	return d, nil
}

// pump is the single reader goroutine: routes reply/error frames to the
// waiting invoke, event chunks to the Events channel, and answers pings.
func (d *Device) pump() {
	for {
		_ = d.ws.SetReadDeadline(time.Now().Add(10 * time.Minute))
		mt, data, err := d.ws.ReadMessage()
		if err != nil {
			select {
			case <-d.closed:
			default:
				select {
				case d.errs <- fmt.Errorf("glasssim: read: %w", err):
				default:
				}
			}
			return
		}
		if mt != websocket.BinaryMessage {
			continue
		}
		frame, err := gateway.UnmarshalWireFrame(data)
		if err != nil {
			continue
		}
		switch frame.Type {
		case gateway.FrameTypeReply, gateway.FrameTypeError:
			d.mu.Lock()
			ch, ok := d.pending[frame.CorID]
			delete(d.pending, frame.CorID)
			d.mu.Unlock()
			if ok {
				ch <- frame
			}
		case gateway.FrameTypeChunk:
			if frame.SubID == "" || len(frame.Payload) == 0 {
				continue
			}
			d.mu.Lock()
			kind := d.kindBySub[frame.SubID]
			d.mu.Unlock()
			if kind == "" {
				continue
			}
			if ev, ok := d.decodeEvent(kind, frame.Payload, frame.Flags.Compression()); ok {
				select {
				case d.events <- ev:
				default: // drop when the consumer is slow; device has no backpressure
				}
			}
		case gateway.FrameTypePing:
			_ = d.ws.WriteMessage(websocket.BinaryMessage, mustMarshalWireFrame(&gateway.WireFrame{
				Type:  gateway.FrameTypePong,
				CorID: frame.CorID,
			}))
		}
	}
}

func mustMarshalWireFrame(f *gateway.WireFrame) []byte {
	raw, err := gateway.MarshalWireFrame(f)
	if err != nil {
		return nil
	}
	return raw
}

// decodeEvent decompresses, sniffs TBC vs JSON, and decodes into the
// generated event struct.
func (d *Device) decodeEvent(kind string, payload []byte, comp gateway.PayloadCompression) (Event, bool) {
	data, err := gateway.Decompress(payload, comp)
	if err != nil {
		return Event{}, false
	}
	schemaID, known := kindSchemaID[kind]
	if !known {
		return Event{}, false
	}
	var dest any
	switch kind {
	case "glass.render":
		dest = &gen.GlassRenderEvent{}
	case "glass.speak":
		dest = &gen.GlassSpeakEvent{}
	case "glass.transcript":
		dest = &gen.GlassTranscriptEvent{}
	case "glass.interaction":
		dest = &gen.GlassInteractionEvent{}
	case "glass.hud.update":
		dest = &gen.GlassHudStatus{}
	default:
		dest = &gen.GlassLifecycleEvent{}
	}
	enc := message.EncodingBinary
	if !bytes.HasPrefix(data, []byte("TBC")) {
		enc = message.EncodingJSON
	}
	if err := d.codec.DecodeByIDInto(schemaID, enc, data, dest); err != nil {
		return Event{}, false
	}
	raw, _ := json.Marshal(dest)
	return Event{Kind: kind, Payload: dest, Raw: raw}, true
}

// Encode TBC-encodes value against a schema ID from the static fragment.
func (d *Device) Encode(schemaID uint64, value any) ([]byte, error) {
	desc, ok := d.resolver.LookupSchema(schemaID)
	if !ok {
		return nil, fmt.Errorf("glasssim: schema %d not registered", schemaID)
	}
	view, err := d.bin.Encode(desc, d.encID, value)
	if err != nil {
		return nil, fmt.Errorf("glasssim: tbc encode %d: %w", schemaID, err)
	}
	return view.Data, nil
}

// Invoke sends one TBC-encoded invoke frame and decodes the reply into resp
// (resp may be nil to discard).
func (d *Device) Invoke(callID string, reqSchemaID uint64, req any, respSchemaID uint64, resp any) error {
	payload, err := d.Encode(reqSchemaID, req)
	if err != nil {
		return err
	}
	return d.InvokeView(callID, payload, respSchemaID, resp)
}

// InvokeView sends a pre-encoded TBC payload (e.g. a forced-ClassID typed
// struct) and decodes the reply.
func (d *Device) InvokeView(callID string, payload []byte, respSchemaID uint64, resp any) error {
	d.corID++
	cor := d.corID
	wire := &gateway.WireFrame{
		Flags:   gateway.MakeFlags(gateway.EncodingBinary, gateway.CompressionNone),
		Type:    gateway.FrameTypeInvoke,
		CorID:   cor,
		CallID:  callID,
		Payload: payload,
	}
	raw, err := gateway.MarshalWireFrame(wire)
	if err != nil {
		return fmt.Errorf("glasssim: marshal wire frame: %w", err)
	}
	ch := make(chan *gateway.WireFrame, 1)
	d.mu.Lock()
	d.pending[cor] = ch
	d.mu.Unlock()
	if err := d.ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		d.mu.Lock()
		delete(d.pending, cor)
		d.mu.Unlock()
		return fmt.Errorf("glasssim: write frame: %w", err)
	}
	select {
	case frame := <-ch:
		if frame.Type == gateway.FrameTypeError {
			return fmt.Errorf("%s: %s", callID, frame.ErrorMsg)
		}
		if frame.Type != gateway.FrameTypeReply {
			return fmt.Errorf("%s: unexpected frame type %v", callID, frame.Type)
		}
		if resp == nil {
			return nil
		}
		data, err := gateway.Decompress(frame.Payload, frame.Flags.Compression())
		if err != nil {
			return fmt.Errorf("glasssim: decompress reply: %w", err)
		}
		if err := d.codec.DecodeByIDInto(respSchemaID, encodingOf(data), data, resp); err != nil {
			return fmt.Errorf("glasssim: decode reply: %w", err)
		}
		return nil
	case <-time.After(15 * time.Second):
		d.mu.Lock()
		delete(d.pending, cor)
		d.mu.Unlock()
		return fmt.Errorf("%s: timeout waiting for reply", callID)
	}
}

// Claim performs session_claim and records the generation.
func (d *Device) Claim() (gen.GlassSessionClaimResp, error) {
	var claim gen.GlassSessionClaimResp
	err := d.Invoke(CallClaim, gen.GlassSessionClaimReqSchemaID,
		gen.GlassSessionClaimReq{SessionID: d.SessionID, DeviceID: d.DeviceID},
		gen.GlassSessionClaimRespSchemaID, &claim)
	if err == nil {
		d.Generation = claim.Generation
	}
	return claim, err
}

// SubscribeEvents sends one subscribe frame per event kind (the gospore
// events system callable; payload is a TBC anonymous struct with
// {kind, serviceName}, mirroring the MentraOS encoder).
func (d *Device) SubscribeEvents() error {
	for _, kind := range EventKinds {
		d.subSeq++
		subID := fmt.Sprintf("glasssim:%s:%d", kind, d.subSeq)
		payload, err := d.bin.Encode(tschema.TypeDesc{Kind: tschema.TypeKindStruct},
			d.encID, subscribeReq{Kind: kind, ServiceName: ServiceName})
		if err != nil {
			return fmt.Errorf("glasssim: encode subscribe: %w", err)
		}
		d.mu.Lock()
		d.kindBySub[subID] = kind
		d.mu.Unlock()
		wire := &gateway.WireFrame{
			Flags:   gateway.MakeFlags(gateway.EncodingBinary, gateway.CompressionNone),
			Type:    gateway.FrameTypeSubscribe,
			SubID:   subID,
			CallID:  CallSubscribeService,
			Payload: payload.Data,
		}
		raw, err := gateway.MarshalWireFrame(wire)
		if err != nil {
			return fmt.Errorf("glasssim: marshal subscribe: %w", err)
		}
		if err := d.ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
			return fmt.Errorf("glasssim: send subscribe: %w", err)
		}
	}
	return nil
}

type subscribeReq struct {
	Kind        string `json:"kind"`
	ServiceName string `json:"serviceName"`
}

// Telemetry reports battery state; the server fans it out as a
// glass.hud.update push.
func (d *Device) Telemetry(batteryLevel int, charging bool) error {
	var resp gen.GlassTelemetryResp
	return d.Invoke(CallTelemetry, gen.GlassTelemetryReqSchemaID,
		gen.GlassTelemetryReq{SessionID: d.SessionID, Generation: d.Generation,
			BatteryLevel: int32(batteryLevel), Charging: charging},
		gen.GlassTelemetryRespSchemaID, &resp)
}

// Speech performs one full utterance cycle (start → chunk × N → end) over
// PCM bytes (16 kHz mono s16le), verifying the cumulative ACK progression.
func (d *Device) Speech(pcm []byte, chunkBytes int) error {
	if chunkBytes <= 0 {
		chunkBytes = 1280 // 40 ms at 16 kHz s16le — the server's per-chunk cap
	}
	utterance := fmt.Sprintf("sim-%d", time.Now().UnixNano())

	var start gen.GlassSpeechStartResp
	if err := d.Invoke(CallSpeechStart, gen.GlassSpeechStartReqSchemaID,
		gen.GlassSpeechStartReq{SessionID: d.SessionID, Generation: d.Generation,
			UtteranceID: utterance, SampleRate: 16000, Channels: 1, BitsPerSample: 16},
		gen.GlassSpeechStartRespSchemaID, &start); err != nil {
		return err
	}

	// Server sequence numbers are 0-based; the gap loop advances from 0.
	var seq int64 = -1
	chunkInterval := time.Duration(chunkBytes/32) * time.Millisecond // real-time pace: 32 bytes/ms at 16 kHz s16le
	for off := 0; off < len(pcm); off += chunkBytes {
		end := off + chunkBytes
		if end > len(pcm) {
			end = len(pcm)
		}
		if seq >= 0 {
			time.Sleep(chunkInterval)
		}
		seq++
		var ack gen.GlassSpeechAck
		if err := d.Invoke(CallSpeechChunk, gen.GlassSpeechChunkReqSchemaID,
			gen.GlassSpeechChunkReq{SessionID: d.SessionID, Generation: d.Generation,
				UtteranceID: utterance, Sequence: seq, Pcm: pcm[off:end]},
			gen.GlassSpeechAckSchemaID, &ack); err != nil {
			return err
		}
		if ack.HighestContiguousSeq < seq {
			return fmt.Errorf("glasssim: chunk %d acked only up to %d (%s)", seq, ack.HighestContiguousSeq, ack.Reason)
		}
	}

	var endResp gen.GlassSpeechEndResp
	if err := d.Invoke(CallSpeechEnd, gen.GlassSpeechEndReqSchemaID,
		gen.GlassSpeechEndReq{SessionID: d.SessionID, Generation: d.Generation, UtteranceID: utterance},
		gen.GlassSpeechEndRespSchemaID, &endResp); err != nil {
		return err
	}
	if !endResp.Ack.EndReceived {
		return fmt.Errorf("glasssim: speech end not acknowledged: %+v", endResp.Ack)
	}
	return nil
}

// Interact reports one confirmed user interaction (select/cancel).
func (d *Device) Interact(elementID, action, value string) error {
	var resp gen.GlassInteractionReportResp
	return d.Invoke(CallInteraction, gen.GlassInteractionReportReqSchemaID,
		gen.GlassInteractionReportReq{SessionID: d.SessionID, Generation: d.Generation,
			ElementID: elementID, Action: action, Value: value},
		gen.GlassInteractionReportRespSchemaID, &resp)
}

// State reads the current session state (lastFrame etc.) like the web panel.
func (d *Device) State() (gen.GlassGetStateResp, error) {
	var state gen.GlassGetStateResp
	err := d.Invoke(CallGetState, gen.GlassGetStateReqSchemaID,
		gen.GlassGetStateReq{}, gen.GlassGetStateRespSchemaID, &state)
	return state, err
}

// Events is the decoded server-push stream; it is closed after Close.
func (d *Device) Events() <-chan Event { return d.events }

// Err returns the first reader error after a disconnect.
func (d *Device) Err() error {
	select {
	case err := <-d.errs:
		return err
	default:
		return nil
	}
}

// Close tears the connection down.
func (d *Device) Close() {
	d.closeOnce.Do(func() {
		close(d.closed)
		_ = d.ws.Close()
		close(d.events)
	})
}

// Silence returns n milliseconds of 16 kHz mono s16le silence.
func Silence(ms int) []byte {
	return make([]byte, ms*32)
}

// encodingOf sniffs the TBC magic to pick the decode encoding.
func encodingOf(data []byte) message.Encoding {
	if bytes.HasPrefix(data, []byte("TBC")) {
		return message.EncodingBinary
	}
	return message.EncodingJSON
}
