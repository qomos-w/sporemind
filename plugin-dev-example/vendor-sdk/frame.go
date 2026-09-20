package sdk

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types for the subprocess duplex framing protocol. These constants
// are the plugin-side mirror of pkg/actor/pluginhost/process_frame.go on the
// host side and MUST stay in sync with it. The SDK module cannot import the
// host package (module cycle), so the codec is duplicated here by design.
const (
	msgInvokeReq     byte = 0x01 // host -> plugin: forward invoke request
	msgInvokeResp    byte = 0x02 // plugin -> host: forward invoke response
	msgReverseReq    byte = 0x03 // plugin -> host: reverse bridge request
	msgReverseResp   byte = 0x04 // host -> plugin: reverse bridge response (terminal frame of a reverse call)
	msgLog           byte = 0x05 // plugin -> host: log line (replaces PluginLog ABI)
	msgError         byte = 0x06 // either direction: fatal / process-level error
	msgReverseChunk  byte = 0x07 // host -> plugin: reverse stream chunk (intermediate; correlated by callID, terminated by the 0x04)
	msgForwardChunk  byte = 0x08 // plugin -> host: forward stream chunk (intermediate; correlated by callID, terminated by the 0x02 invoke-resp)
	msgReverseCancel byte = 0x09 // plugin -> host: abort one reverse call (correlated by callID; the consumer abandoned the stream or its context expired)
)

const msgFlagCallID byte = 0x80

const maxCallIDLen = 64

// maxProcessFrameLen caps a single frame payload at 64 MiB, mirroring the
// host's MaxResponseBytes ceiling (pkg/pluginhost/abi.go, and the shared
// codec's MaxTransportFrameLen). Pipes are variable length; this is a sanity
// guard, not a protocol limit.
const maxProcessFrameLen = 64 << 20

// writeFrame writes one frame with a correlation callID in the header. Each
// direction of the duplex pipe has exactly one writer (the host writes the
// plugin's stdin, the plugin writes its stdout), so a single Write of the
// whole frame is atomic with respect to other frames as long as the caller
// serializes writes (processTransport provides the stdout write lock).
func writeFrame(w io.Writer, typ byte, callID string, payload []byte) error {
	if len(callID) > maxCallIDLen {
		return fmt.Errorf("frame callID %q exceeds max %d", callID, maxCallIDLen)
	}
	if len(payload) > maxProcessFrameLen {
		return fmt.Errorf("frame payload %d exceeds max %d", len(payload), maxProcessFrameLen)
	}
	total := 2 + len(callID) + len(payload)
	buf := make([]byte, 5+total)
	binary.BigEndian.PutUint32(buf[:4], uint32(total))
	buf[4] = typ | msgFlagCallID
	binary.BigEndian.PutUint16(buf[5:7], uint16(len(callID)))
	copy(buf[7:], callID)
	copy(buf[7+len(callID):], payload)
	_, err := w.Write(buf)
	return err
}

// readFrame reads exactly one frame. It blocks until the full payload of the
// next frame has arrived or the pipe is closed (io.EOF / pipe-ended error).
// Every frame carries a correlation callID in the header.
func readFrame(r io.Reader) (typ byte, callID string, payload []byte, err error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, "", nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n > maxProcessFrameLen {
		return 0, "", nil, fmt.Errorf("frame payload %d exceeds max %d", n, maxProcessFrameLen)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, "", nil, err
	}
	if hdr[4]&msgFlagCallID == 0 {
		return 0, "", nil, fmt.Errorf("frame type 0x%02x missing callID flag", hdr[4])
	}
	typ = hdr[4] &^ msgFlagCallID
	if len(body) < 2 {
		return 0, "", nil, fmt.Errorf("frame 0x%02x correlation header truncated", typ)
	}
	cidLen := int(binary.BigEndian.Uint16(body[:2]))
	if 2+cidLen > len(body) {
		return 0, "", nil, fmt.Errorf("frame 0x%02x invalid callID length %d", typ, cidLen)
	}
	return typ, string(body[2 : 2+cidLen]), body[2+cidLen:], nil
}
