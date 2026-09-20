// Package main implements the T1 subprocess-framing spike for the Native
// Plugin dual-transport architecture ([[Native Plugin 双模式传输架构]]).
//
// It is a standalone feasibility probe (gating gate): a "hello plugin"
// spawned as an independent OS process, talking to a host over stdin/stdout
// duplex pipes with length-prefix framing:
//
//	[4-byte big-endian length] [1-byte message type] [payload bytes]
//
// No production package is imported or modified. The framing, the capability
// gate and the reverse-call dispatch here are minimal mirrors of:
//
//   - planned sporemind-plugin-sdk/process_main.go (plugin side)
//   - planned host process_opener.go + IPC host bridge (host side)
//   - existing sporemind-plugin-sdk/bridge.go wire shape {callID, JSON payload}
//   - pkg/actor/pluginhost/host_bridge.go Allows/Dispatch gate
//
// Run with: go test ./cmd/plugin-spike/ -v
package main

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Message types for the duplex framing protocol proposed in the design card.
//
//	A single frame is [4-byte BE length][1-byte type][payload].
const (
	msgInvokeReq   byte = 0x01 // host -> plugin: forward invoke request
	msgInvokeResp  byte = 0x02 // plugin -> host: forward invoke response
	msgReverseReq  byte = 0x03 // plugin -> host: reverse bridge request
	msgReverseResp byte = 0x04 // host -> plugin: reverse bridge response
	msgLog         byte = 0x05 // plugin -> host: log line (replaces PluginLog ABI)
	msgError       byte = 0x06 // either direction: fatal / process-level error
)

// maxFrameLen caps a single frame payload at 64 MiB, mirroring the host's
// MaxResponseBytes ceiling in pkg/pluginhost/abi.go. Pipes are variable
// length; this is a sanity guard, not a protocol limit.
const maxFrameLen = 64 << 20

// writeFrame writes one frame. Each direction of the duplex pipe has exactly
// one writer (host for stdin, plugin for stdout), so a single Write of the
// whole frame is atomic with respect to other frames.
func writeFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("frame payload %d exceeds max %d", len(payload), maxFrameLen)
	}
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(payload)))
	buf[4] = typ
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

// readFrame reads exactly one frame. It blocks until the full payload of the
// next frame has arrived or the pipe is closed (io.EOF / pipe-ended error).
func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:4])
	if n > maxFrameLen {
		return 0, nil, fmt.Errorf("frame payload %d exceeds max %d", n, maxFrameLen)
	}
	payload := make([]byte, int(n))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[4], payload, nil
}
