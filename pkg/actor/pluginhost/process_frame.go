package pluginhost

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types for the subprocess duplex framing protocol. A single frame is
//
//	[4-byte BE length][type|0x80][2-byte BE callIDLen][callID][payload]
//
// The type byte always has bit 7 set (frameFlagCallID): every frame carries a
// correlation callID. This is the protocol-v2 wire format — there is no v1
// fallback. The callID correlates a 0x03 reverse-req with its 0x04
// reverse-resp (and, for future transports, any request/response pair) so
// both directions can be fully multiplexed: reverse bridge calls no longer
// need to happen strictly inside an invoke dispatch window. This is the wire
// seam shared with the distributed (host-to-host) transport: the same
// envelope rides a stdio lane here and a gospore Transport lane there.
const (
	MsgInvokeReq     byte = 0x01 // host -> plugin: forward invoke request
	MsgInvokeResp    byte = 0x02 // plugin -> host: forward invoke response
	MsgReverseReq    byte = 0x03 // plugin -> host: reverse bridge request
	MsgReverseResp   byte = 0x04 // host -> plugin: reverse bridge response (terminal frame of a reverse call)
	MsgLog           byte = 0x05 // plugin -> host: log line (replaces PluginLog ABI)
	MsgError         byte = 0x06 // either direction: fatal / process-level error
	MsgReverseChunk  byte = 0x07 // host -> plugin: reverse stream chunk (intermediate; correlated by callID, terminated by the 0x04)
	MsgForwardChunk  byte = 0x08 // plugin -> host: forward stream chunk (intermediate; correlated by callID, terminated by the 0x02 invoke-resp)
	MsgReverseCancel byte = 0x09 // plugin -> host: abort one reverse call (correlated by callID; the consumer abandoned the stream or its context expired)
)

const frameFlagCallID byte = 0x80

// MaxTransportFrameLen caps a single frame payload, mirroring the FFI path's
// pluginhost.MaxResponseBytes ceiling. Pipes are variable length; this is a
// sanity guard, not a protocol limit.
const MaxTransportFrameLen = 64 << 20

// maxCallIDLen bounds the correlation header; callIDs are short counters.
const maxCallIDLen = 64

// WriteTransportFrame writes one frame with a correlation callID in the
// header. Each direction of the duplex pipe has exactly one writer (host for
// the plugin's stdin, plugin for its stdout), so a single Write of the whole
// frame is atomic with respect to other frames.
func WriteTransportFrame(w io.Writer, typ byte, callID string, payload []byte) error {
	if len(callID) > maxCallIDLen {
		return fmt.Errorf("frame callID %q exceeds max %d", callID, maxCallIDLen)
	}
	if len(payload) > MaxTransportFrameLen {
		return fmt.Errorf("frame payload %d exceeds max %d", len(payload), MaxTransportFrameLen)
	}
	total := 2 + len(callID) + len(payload)
	buf := make([]byte, 5+total)
	binary.BigEndian.PutUint32(buf[:4], uint32(total))
	buf[4] = typ | frameFlagCallID
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(callID)))
	copy(buf[7:], callID)
	copy(buf[7+len(callID):], payload)
	_, err := w.Write(buf)
	return err
}

// Frame is one decoded transport frame.
type Frame struct {
	Type    byte
	CallID  string
	Payload []byte
}

// ReadTransportFrame reads exactly one frame. It blocks until the full
// payload of the next frame has arrived or the pipe is closed (io.EOF).
func ReadTransportFrame(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n > MaxTransportFrameLen {
		return Frame{}, fmt.Errorf("frame payload %d exceeds max %d", n, MaxTransportFrameLen)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, err
	}
	typ := hdr[4]
	if typ&frameFlagCallID == 0 {
		return Frame{}, fmt.Errorf("frame type 0x%02x missing callID flag", typ)
	}
	typ &^= frameFlagCallID
	if len(body) < 2 {
		return Frame{}, fmt.Errorf("frame 0x%02x correlation header truncated", typ)
	}
	cidLen := int(binary.BigEndian.Uint16(body[:2]))
	if 2+cidLen > len(body) {
		return Frame{}, fmt.Errorf("frame 0x%02x invalid callID length %d", typ, cidLen)
	}
	return Frame{
		Type:    typ,
		CallID:  string(body[2 : 2+cidLen]),
		Payload: body[2+cidLen:],
	}, nil
}
