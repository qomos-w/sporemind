//go:build windows

package winx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"image"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

const (
	uiaTestHelperEnv  = "SPOREMIND_UIA_HELPER_TEST"
	uiaTestHelperRole = "SPOREMIND_UIA_HELPER_TEST_ROLE"
)

// TestUIAHelperSubprocess is not a real test: when the client tests
// re-invoke the test binary with uiaTestHelperEnv=1 it acts as a
// scriptable fake helper over stdin/stdout.
func TestUIAHelperSubprocess(t *testing.T) {
	if os.Getenv(uiaTestHelperEnv) != "1" {
		return
	}
	switch os.Getenv(uiaTestHelperRole) {
	case "crash":
		os.Exit(3)
	case "hang":
		time.Sleep(5 * time.Minute)
	case "echo":
		reader := bufio.NewReader(os.Stdin)
		writer := bufio.NewWriter(os.Stdout)
		for {
			line, err := readFrame(reader, maxFrameSize)
			if err != nil {
				return
			}
			var req uiaRequest
			if err := json.Unmarshal(line, &req); err != nil {
				_ = writeFrame(writer, &uiaResponse{Error: "malformed request"})
				_ = writer.Flush()
				continue
			}
			resp := uiaResponse{ID: req.ID}
			switch req.Op {
			case uiaOpList:
				resp.Nodes = []capture.ElementNode{
					{ID: "e1", Name: "Fake Button", ControlType: "button", Bounds: image.Rect(10, 20, 30, 40)},
					{ID: "e2", Name: "Fake Edit", ControlType: "edit"},
				}
			case uiaOpInfo:
				if req.ElementID == "e1" {
					resp.Node = &capture.ElementNode{ID: "e1", Name: "Fake Button", ControlType: "button"}
				} else {
					resp.Error = "element not found: " + req.ElementID
				}
			case uiaOpClick:
				switch req.ElementID {
				case "e1":
					// invokable: no fallback
				case "e3":
					resp.Fallback = true
					resp.Node = &capture.ElementNode{ID: "e3", Bounds: image.Rect(10, 20, 30, 40)}
				default:
					resp.Error = "element not found: " + req.ElementID
				}
			case uiaOpFocus:
				if req.ElementID != "e1" {
					resp.Error = "element not found: " + req.ElementID
				}
			case uiaOpSetValue:
				if req.ElementID != "e2" {
					resp.Error = "element not found: " + req.ElementID
				}
			default:
				resp.Error = "unexpected op: " + string(req.Op)
			}
			_ = writeFrame(writer, &resp)
			_ = writer.Flush()
		}
	}
}

// testUIAHelperClient builds a client whose helper is the test binary
// acting in the given fake role.
func testUIAHelperClient(t *testing.T, role string) *uiaClient {
	t.Helper()
	c := newUIAClient()
	c.timeout = 5 * time.Second
	c.spawn = func() (*exec.Cmd, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, "-test.run=^TestUIAHelperSubprocess$")
		cmd.Env = append(os.Environ(),
			uiaTestHelperEnv+"=1",
			uiaTestHelperRole+"="+role,
		)
		return cmd, nil
	}
	return c
}

func TestUIAHelperList(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	nodes, err := c.ListElements(capture.ElementFilter{WindowID: 1, MaxDepth: 2, NameContains: "Fake"})
	if err != nil {
		t.Fatalf("ListElements: %v", err)
	}
	if len(nodes) != 2 || nodes[0].ID != "e1" || nodes[0].Name != "Fake Button" {
		t.Fatalf("unexpected nodes: %+v", nodes)
	}
	if got := nodes[0].Bounds; got != image.Rect(10, 20, 30, 40) {
		t.Fatalf("bounds: %v", got)
	}
}

func TestUIAHelperInfo(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	node, err := c.ElementInfo("e1")
	if err != nil {
		t.Fatalf("ElementInfo: %v", err)
	}
	if node.ID != "e1" || node.Name != "Fake Button" {
		t.Fatalf("unexpected node: %+v", node)
	}
}

func TestUIAHelperIllegalElementID(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	_, err := c.ElementInfo("nope")
	if err == nil || !strings.Contains(err.Error(), "element not found") {
		t.Fatalf("expected element not found error, got %v", err)
	}
	if err := c.FocusElement("nope"); err == nil {
		t.Fatal("expected FocusElement error for unknown element")
	}
	if err := c.SetElementValue("nope", "x"); err == nil {
		t.Fatal("expected SetElementValue error for unknown element")
	}
	if err := c.ClickElement("nope", "left", func(x, y int, button string) error { return nil }); err == nil {
		t.Fatal("expected ClickElement error for unknown element")
	}
}

func TestUIAHelperClickInvoke(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	clicked := false
	if err := c.ClickElement("e1", "left", func(x, y int, button string) error {
		clicked = true
		return nil
	}); err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	if clicked {
		t.Fatal("invokable element must not trigger the fallback click")
	}
}

func TestUIAHelperClickFallback(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	var gotX, gotY int
	err := c.ClickElement("e3", "left", func(x, y int, button string) error {
		gotX, gotY = x, y
		return nil
	})
	if err != nil {
		t.Fatalf("ClickElement: %v", err)
	}
	if gotX != 20 || gotY != 30 {
		t.Fatalf("fallback click at (%d,%d), want (20,30)", gotX, gotY)
	}
}

func TestUIAHelperFocusAndSetValue(t *testing.T) {
	c := testUIAHelperClient(t, "echo")
	if err := c.FocusElement("e1"); err != nil {
		t.Fatalf("FocusElement: %v", err)
	}
	if err := c.SetElementValue("e2", "hello"); err != nil {
		t.Fatalf("SetElementValue: %v", err)
	}
}

func TestUIAHelperCrashReturnsError(t *testing.T) {
	c := testUIAHelperClient(t, "crash")
	if _, err := c.ListElements(capture.ElementFilter{}); err == nil {
		t.Fatal("expected error when the helper crashes")
	}
}

func TestUIAHelperRespawnAfterCrash(t *testing.T) {
	c := newUIAClient()
	c.timeout = 5 * time.Second
	first := true
	c.spawn = func() (*exec.Cmd, error) {
		exe, err := os.Executable()
		if err != nil {
			return nil, err
		}
		cmd := exec.Command(exe, "-test.run=^TestUIAHelperSubprocess$")
		env := []string{uiaTestHelperEnv + "=1"}
		if first {
			first = false
			env = append(env, uiaTestHelperRole+"=crash")
		} else {
			env = append(env, uiaTestHelperRole+"=echo")
		}
		cmd.Env = append(os.Environ(), env...)
		return cmd, nil
	}
	if _, err := c.ListElements(capture.ElementFilter{}); err == nil {
		t.Fatal("expected error from the crashing helper")
	}
	nodes, err := c.ListElements(capture.ElementFilter{})
	if err != nil {
		t.Fatalf("respawned helper should succeed: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("unexpected nodes after respawn: %+v", nodes)
	}
}

func TestUIAHelperTimeoutReturnsError(t *testing.T) {
	c := testUIAHelperClient(t, "hang")
	c.timeout = 300 * time.Millisecond
	c.listTimeout = 300 * time.Millisecond
	start := time.Now()
	if _, err := c.ListElements(capture.ElementFilter{}); err == nil {
		t.Fatal("expected timeout error from the hanging helper")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
}

func TestUIAHelperServeDispatch(t *testing.T) {
	var out bytes.Buffer
	in := bytes.NewBufferString(`{"id":42,"op":"list","window_id":5}` + "\n")
	err := uiaHelperServe(in, &out, func(r *uiaRequest) uiaResponse {
		if r.ID != 42 || r.Op != uiaOpList || r.WindowID != 5 {
			t.Errorf("bad request: %+v", r)
		}
		return uiaResponse{ID: r.ID, Nodes: []capture.ElementNode{{ID: "x", Name: "served"}}}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	line, err := readFrame(bufio.NewReader(&out), maxFrameSize)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp uiaResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != 42 || len(resp.Nodes) != 1 || resp.Nodes[0].Name != "served" {
		t.Fatalf("bad response: %+v", resp)
	}
}

func TestUIAHelperServeMalformedRequest(t *testing.T) {
	var out bytes.Buffer
	in := bytes.NewBufferString("{not json}\n")
	err := uiaHelperServe(in, &out, func(r *uiaRequest) uiaResponse {
		t.Fatal("dispatch must not be called for malformed input")
		return uiaResponse{}
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	line, err := readFrame(bufio.NewReader(&out), maxFrameSize)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp uiaResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("expected error response for malformed request")
	}
}

func TestUIAHelperServeDispatchPanic(t *testing.T) {
	var out bytes.Buffer
	in := bytes.NewBufferString(`{"id":3,"op":"list"}` + "\n")
	err := uiaHelperServe(in, &out, func(r *uiaRequest) uiaResponse {
		panic("boom")
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	line, err := readFrame(bufio.NewReader(&out), maxFrameSize)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	var resp uiaResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != 3 || !strings.Contains(resp.Error, "boom") {
		t.Fatalf("expected panic error response, got %+v", resp)
	}
}

func TestUIAHelperDispatchUnknownOp(t *testing.T) {
	resp := uiaHelperDispatch(&uiaRequest{ID: 9, Op: uiaOp("bogus")})
	if resp.ID != 9 || resp.Error == "" {
		t.Fatalf("expected error for unknown op, got %+v", resp)
	}
}

// The following guard tests assert that the host process cannot execute
// the COM path: without helper mode they must return errors, not hang or
// crash.
func TestOnCOMThreadRefusedOutsideHelper(t *testing.T) {
	err := onCOMThread(func() { t.Error("fn must not run outside helper mode") })
	if err == nil {
		t.Fatal("expected onCOMThread to refuse outside helper mode")
	}
}

func TestRequireHelperRefusedOutsideHelper(t *testing.T) {
	if err := requireHelper(); err == nil {
		t.Fatal("expected requireHelper to refuse outside helper mode")
	}
}
