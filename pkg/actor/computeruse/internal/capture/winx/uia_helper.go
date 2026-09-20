//go:build windows

package winx

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/qomos-w/sporemind/pkg/util"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// Request timeouts. Listing the desktop tree can legitimately take a few
// seconds; element operations are expected to be quick.
const (
	uiaListTimeout = 30 * time.Second
	uiaOpTimeout   = 10 * time.Second
)

// uiaClient proxies every UIA operation to a dedicated helper subprocess.
// The host process never creates COM objects and never holds element
// pointers: a helper crash, timeout or illegal request surfaces as a plain
// Go error instead of terminating the host.
type uiaClient struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	reader  *bufio.Reader
	writer  *bufio.Writer
	stderr  *limitedBuffer
	dead    bool
	nextID  uint64
	timeout time.Duration
	// listTimeout is used for enumeration requests, which legitimately take
	// longer than single-element operations.
	listTimeout time.Duration
	spawn       func() (*exec.Cmd, error)
}

// newUIAClient creates a client whose helper is the running executable
// re-invoked in helper mode. No helper process is started until the first
// request.
func newUIAClient() *uiaClient {
	return &uiaClient{
		timeout:     uiaOpTimeout,
		listTimeout: uiaListTimeout,
		spawn:       defaultUIASpawn,
	}
}

// defaultUIASpawn builds the same-binary helper command. The helper is
// identified purely by environment; it never re-spawns itself because it
// exits from init() before the desktop main() runs.
func defaultUIASpawn() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("uia helper: resolve executable: %w", err)
	}
	cmd := util.Command(exe)
	cmd.Env = append(os.Environ(), uiaHelperEnv+"=1")
	return cmd, nil
}

// limitedBuffer keeps the trailing few KB of helper stderr for diagnostics.
type limitedBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const max = 4096
	b.buf = append(b.buf, p...)
	if len(b.buf) > max {
		b.buf = b.buf[len(b.buf)-max:]
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// start spawns the helper and connects its stdio pipes. Callers must hold
// c.mu.
func (c *uiaClient) start() error {
	cmd, err := c.spawn()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("uia helper: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("uia helper: stdout pipe: %w", err)
	}
	var stderr limitedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("uia helper: start: %w", err)
	}
	c.cmd = cmd
	c.reader = bufio.NewReader(stdout)
	c.writer = bufio.NewWriter(stdin)
	c.stderr = &stderr
	c.dead = false
	return nil
}

// killLocked terminates a wedged helper and resets the connection. The
// next request transparently respawns a fresh helper. Callers must hold
// c.mu.
func (c *uiaClient) killLocked() {
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_, _ = c.cmd.Process.Wait()
	}
	c.cmd = nil
	c.reader = nil
	c.writer = nil
	c.dead = true
}

// do sends one request and waits for its response. Any failure — helper
// crash, pipe error, timeout, decode error, id mismatch — kills the helper
// and returns an error; the next call respawns it.
func (c *uiaClient) do(req *uiaRequest) (*uiaResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	req.ID = c.nextID
	c.nextID++
	if c.dead || c.cmd == nil {
		if err := c.start(); err != nil {
			return nil, err
		}
	}
	if err := writeFrame(c.writer, req); err != nil {
		c.killLocked()
		return nil, fmt.Errorf("uia helper: write request: %w", err)
	}
	if err := c.writer.Flush(); err != nil {
		c.killLocked()
		return nil, fmt.Errorf("uia helper: flush request: %w", err)
	}
	type frameResult struct {
		line []byte
		err  error
	}
	ch := make(chan frameResult, 1)
	go func() {
		line, err := readFrame(c.reader, maxFrameSize)
		ch <- frameResult{line: line, err: err}
	}()
	select {
	case fr := <-ch:
		if fr.err != nil {
			c.killLocked()
			return nil, fmt.Errorf("uia helper: read response: %w", fr.err)
		}
		var resp uiaResponse
		if err := json.Unmarshal(fr.line, &resp); err != nil {
			c.killLocked()
			return nil, fmt.Errorf("uia helper: decode response: %w", err)
		}
		if resp.ID != req.ID {
			// Out-of-order or stale response: the helper is out of sync.
			c.killLocked()
			return nil, fmt.Errorf("uia helper: response id mismatch: got %d want %d", resp.ID, req.ID)
		}
		return &resp, nil
	case <-time.After(c.requestTimeout(req.Op)):
		c.killLocked()
		return nil, fmt.Errorf("uia helper: request %d (%s) timed out after %s", req.ID, req.Op, c.requestTimeout(req.Op))
	}
}

// requestTimeout returns the timeout for the given op.
func (c *uiaClient) requestTimeout(op uiaOp) time.Duration {
	if op == uiaOpList {
		return c.listTimeout
	}
	return c.timeout
}

// ListElements proxies the tree enumeration to the helper.
func (c *uiaClient) ListElements(f capture.ElementFilter) ([]capture.ElementNode, error) {
	req := &uiaRequest{
		Op:           uiaOpList,
		WindowID:     f.WindowID,
		MaxDepth:     f.MaxDepth,
		MaxResults:   f.MaxResults,
		NameContains: f.NameContains,
		ControlType:  f.ControlType,
	}
	resp, err := c.do(req)
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("uia: %s", resp.Error)
	}
	return resp.Nodes, nil
}

// ElementInfo proxies the element snapshot to the helper.
func (c *uiaClient) ElementInfo(elementID string) (capture.ElementNode, error) {
	resp, err := c.do(&uiaRequest{Op: uiaOpInfo, ElementID: elementID})
	if err != nil {
		return capture.ElementNode{}, err
	}
	if resp.Error != "" {
		return capture.ElementNode{}, fmt.Errorf("uia: %s", resp.Error)
	}
	if resp.Node == nil {
		return capture.ElementNode{}, errors.New("uia: helper returned no element")
	}
	return *resp.Node, nil
}

// ClickElement asks the helper to invoke the element's pattern; when the
// element has none, the helper returns its bounds and the host performs a
// coordinate click via fallback.
func (c *uiaClient) ClickElement(elementID, button string, fallback func(x, y int, button string) error) error {
	resp, err := c.do(&uiaRequest{Op: uiaOpClick, ElementID: elementID})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("uia: %s", resp.Error)
	}
	if !resp.Fallback {
		return nil
	}
	if resp.Node == nil || resp.Node.Bounds.Empty() {
		return errors.New("uia: element has no bounding rect for fallback click")
	}
	b := resp.Node.Bounds
	return fallback((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2, button)
}

// FocusElement proxies SetFocus to the helper.
func (c *uiaClient) FocusElement(elementID string) error {
	resp, err := c.do(&uiaRequest{Op: uiaOpFocus, ElementID: elementID})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("uia: %s", resp.Error)
	}
	return nil
}

// SetElementValue proxies ValuePattern.SetValue to the helper.
func (c *uiaClient) SetElementValue(elementID, value string) error {
	resp, err := c.do(&uiaRequest{Op: uiaOpSetValue, ElementID: elementID, Value: value})
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("uia: %s", resp.Error)
	}
	return nil
}
