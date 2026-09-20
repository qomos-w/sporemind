package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// reverseDispatchFunc is the host-side backing service for reverse calls,
// the same role as HostDispatchFunc in
// pkg/actor/pluginhost/host_bridge.go (and NewServiceDispatch's result).
type reverseDispatchFunc func(callID string, req []byte) ([]byte, error)

// processTransport is the host side of the subprocess transport. It stands
// in for the planned processOpener + IPC host bridge (T5/T6): spawn the
// plugin as an independent process, own the duplex pipes, write invoke-reqs,
// and read frames with interleaved reverse-req dispatch.
type processTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser // host -> plugin direction
	stdout io.Reader      // plugin -> host direction

	// invokeMu serializes in-flight invoke-reqs per plugin process. This is
	// the "初版串行化" decision from the design card: the frame header has no
	// request-id field, so at most one invoke-req may be outstanding; the
	// mutex makes concurrent host goroutines safe without multiplexing.
	invokeMu sync.Mutex

	// allowed is the granted capability set (EffectiveCapabilities). The
	// reverse gate is per-plugin, mirroring HostBridge's per-load instance.
	allowed  map[string]struct{}
	dispatch reverseDispatchFunc

	killMu sync.Mutex // guards kill/close against concurrent invoke failure paths

	mu    sync.Mutex
	trace []string // event sequence recorded during invokes, for assertions
}

// newProcessTransport spawns the plugin process (this test binary re-executed
// with pluginProcessEnv set) and returns the host-side transport.
func newProcessTransport(allowed map[string]struct{}, dispatch reverseDispatchFunc) (*processTransport, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate test binary: %w", err)
	}
	// host->plugin pipe: host writes hostStdinW, child reads pluginStdinR.
	pluginStdinR, hostStdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// plugin->host pipe: child writes pluginStdoutW, host reads hostStdoutR.
	hostStdoutR, pluginStdoutW, err := os.Pipe()
	if err != nil {
		_ = pluginStdinR.Close()
		_ = hostStdinW.Close()
		return nil, err
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), pluginProcessEnv+"=1")
	cmd.Stdin = pluginStdinR
	cmd.Stdout = pluginStdoutW
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = pluginStdinR.Close()
		_ = hostStdinW.Close()
		_ = hostStdoutR.Close()
		_ = pluginStdoutW.Close()
		return nil, err
	}
	// Parent drops the child-owned ends; from here the host holds hostStdinW
	// (write) and hostStdoutR (read).
	_ = pluginStdinR.Close()
	_ = pluginStdoutW.Close()

	tr := &processTransport{
		cmd:      cmd,
		stdin:    hostStdinW,
		stdout:   bufio.NewReader(hostStdoutR),
		allowed:  allowed,
		dispatch: dispatch,
	}
	tr.tracePush("spawned")
	return tr, nil
}

// invoke sends one invoke-req and waits for the matching invoke-resp. While
// waiting it may receive interleaved reverse-req frames (the design's
// message flow); each is capability-checked and answered with a reverse-resp
// before the loop keeps reading for the invoke-resp.
//
// If ctx expires, the plugin process is killed so the blocked read unblocks
// and no process leaks ("超时即 kill+respawn").
func (t *processTransport) invoke(ctx context.Context, callable string, request any, requestID string) ([]byte, error) {
	t.invokeMu.Lock()
	defer t.invokeMu.Unlock()

	rawReq, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	t.tracePush("invoke-req " + callable)
	env, _ := json.Marshal(invokeEnvelope{Callable: callable, Payload: json.RawMessage(rawReq), RequestID: requestID})
	if err := writeFrame(t.stdin, msgInvokeReq, env); err != nil {
		return nil, fmt.Errorf("invoke %s: write: %w", callable, err)
	}

	for {
		typ, payload, err := readFrameWithContext(ctx, t.stdout)
		if err != nil {
			if ctx.Err() != nil {
				t.kill()
				return nil, fmt.Errorf("invoke %s: %w (plugin killed)", callable, ctx.Err())
			}
			t.kill()
			return nil, fmt.Errorf("invoke %s: pipe closed unexpectedly (plugin died?): %w", callable, err)
		}
		switch typ {
		case msgInvokeResp:
			t.tracePush("invoke-resp")
			return payload, nil
		case msgReverseReq:
			if err := t.serveReverse(payload); err != nil {
				return nil, err
			}
		case msgLog:
			t.tracePush("log")
		case msgError:
			t.kill()
			return nil, fmt.Errorf("invoke %s: plugin fatal: %s", callable, payload)
		default:
			t.kill()
			return nil, fmt.Errorf("invoke %s: unexpected message type 0x%02x", callable, typ)
		}
	}
}

// serveReverse answers an interleaved 0x03 reverse-req. The capability gate
// and the __host_error__ error envelope mirror HostBridge.Allows/Dispatch
// and sdk bridgeResult: a positive dispatch failure is encoded as
// {"__host_error__": "..."} in the reverse-resp, never as a dropped pipe.
func (t *processTransport) serveReverse(payload []byte) error {
	var rev reverseReq
	if err := json.Unmarshal(payload, &rev); err != nil {
		return fmt.Errorf("decode reverse-req: %w", err)
	}
	t.tracePush("reverse-req " + rev.CallID)
	if !allowsReverse(t.allowed, rev.CallID) {
		t.tracePush("reverse-deny")
		errResp, _ := json.Marshal(map[string]string{"__host_error__": fmt.Sprintf("capability not granted for %q", rev.CallID)})
		return writeFrame(t.stdin, msgReverseResp, errResp)
	}
	resp, err := t.dispatch(rev.CallID, []byte(rev.Payload))
	if err != nil {
		t.tracePush("reverse-error")
		errResp, _ := json.Marshal(map[string]string{"__host_error__": err.Error()})
		return writeFrame(t.stdin, msgReverseResp, errResp)
	}
	if len(resp) == 0 {
		resp = []byte("{}")
	}
	t.tracePush("reverse-resp")
	return writeFrame(t.stdin, msgReverseResp, resp)
}

// allowsReverse replicates the callID -> capability gate from
// pkg/actor/pluginhost/host_bridge.go (callIDToCapability + Allows):
// known prefixes map to host capabilities, anything unknown resolves to ""
// and is always denied. Standalone here so the spike imports no production
// package.
func allowsReverse(allowed map[string]struct{}, callID string) bool {
	capability := ""
	switch {
	case strings.HasPrefix(callID, "llm."):
		capability = "llm.invoke"
	case callID == "project.read_file":
		capability = "fs.read"
	case callID == "project.write_file":
		capability = "fs.write"
	case strings.HasPrefix(callID, "config."):
		capability = "config.read"
	case strings.HasPrefix(callID, "provider."):
		capability = "provider.read"
	case strings.HasPrefix(callID, "state."):
		capability = "app.state"
	}
	if capability == "" {
		return false
	}
	_, ok := allowed[capability]
	return ok
}

// kill terminates the plugin process and reaps it. Idempotent; safe to call
// from concurrent invoke failure paths and from test cleanup.
func (t *processTransport) kill() {
	t.killMu.Lock()
	defer t.killMu.Unlock()
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}
}

// closeGracefully is the normal unload path: close the host stdin so the
// plugin's read loop sees EOF and exits with status 0, then reap it.
func (t *processTransport) closeGracefully() error {
	t.killMu.Lock()
	defer t.killMu.Unlock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
	if t.cmd != nil && t.cmd.Process != nil {
		err := t.cmd.Wait()
		t.cmd = nil
		return err
	}
	return nil
}

func (t *processTransport) tracePush(ev string) {
	t.mu.Lock()
	t.trace = append(t.trace, ev)
	t.mu.Unlock()
}

// events returns a copy of the recorded event trace.
func (t *processTransport) events() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.trace...)
}

// readFrameWithContext runs a single frame read in a goroutine so an invoke
// can be bounded by a caller-provided context deadline. On expiry the caller
// is expected to kill the plugin, which unblocks the goroutine via pipe EOF;
// the buffered channel prevents the goroutine from leaking.
func readFrameWithContext(ctx context.Context, r io.Reader) (byte, []byte, error) {
	type frameResult struct {
		typ     byte
		payload []byte
		err     error
	}
	ch := make(chan frameResult, 1)
	go func() {
		typ, payload, err := readFrame(r)
		ch <- frameResult{typ, payload, err}
	}()
	select {
	case res := <-ch:
		return res.typ, res.payload, res.err
	case <-ctx.Done():
		return 0, nil, ctx.Err()
	}
}
