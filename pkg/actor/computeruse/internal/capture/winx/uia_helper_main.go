//go:build windows

package winx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// uiaHelperEnv selects helper mode: when set to "1" the same binary acts
// as the UIA helper subprocess instead of the desktop app.
const uiaHelperEnv = "SPOREMIND_UIA_HELPER"

// uiaHelperMode is true only inside the helper subprocess. Every direct
// COM entry point (getUIA, onCOMThread) refuses to run when it is false,
// so the host process can never execute the risky UIA code path.
var uiaHelperMode atomic.Bool

// init doubles as the same-binary entry point for the UIA helper. The
// desktop's main() is never reached: we run the request loop on stdin and
// exit. The loop returns when stdin closes (the host exited or crashed),
// which prevents orphaned helper processes.
func init() {
	if os.Getenv(uiaHelperEnv) == "1" {
		uiaHelperMode.Store(true)
		if err := runUIAHelper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "uia helper:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

// runUIAHelper drives the helper: read requests, dispatch, write responses.
func runUIAHelper(r io.Reader, w io.Writer) error {
	return uiaHelperServe(r, w, uiaHelperDispatch)
}

// uiaHelperServe is the protocol loop. dispatch is injectable for tests.
// A panicking dispatch is converted into an error response so a single bad
// request never takes the helper down; a hard access violation still only
// kills the helper, which the host client turns into a plain error.
func uiaHelperServe(r io.Reader, w io.Writer, dispatch func(*uiaRequest) uiaResponse) error {
	reader := bufio.NewReader(r)
	writer := bufio.NewWriter(w)
	for {
		line, err := readFrame(reader, maxFrameSize)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req uiaRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Unparseable request: answer with an error so the client never
			// hangs waiting for a frame.
			resp := uiaResponse{Error: fmt.Sprintf("malformed request: %v", err)}
			if err := writeFrame(writer, &resp); err != nil {
				return err
			}
			if err := writer.Flush(); err != nil {
				return err
			}
			continue
		}
		resp := func() (r uiaResponse) {
			defer func() {
				if rec := recover(); rec != nil {
					r = uiaResponse{ID: req.ID, Error: fmt.Sprintf("uia helper panic: %v", rec)}
				}
			}()
			return dispatch(&req)
		}()
		if err := writeFrame(writer, &resp); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

// uiaHelperDispatch runs one request against the local UIA COM machinery.
func uiaHelperDispatch(req *uiaRequest) uiaResponse {
	resp := uiaResponse{ID: req.ID}
	switch req.Op {
	case uiaOpList:
		nodes, err := uiaHelperList(capture.ElementFilter{
			WindowID:     req.WindowID,
			MaxDepth:     req.MaxDepth,
			MaxResults:   req.MaxResults,
			NameContains: req.NameContains,
			ControlType:  req.ControlType,
		})
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Nodes = nodes
	case uiaOpInfo:
		node, err := uiaHelperInfo(req.ElementID)
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Node = &node
	case uiaOpClick:
		fallback, node, err := uiaHelperClick(req.ElementID)
		if err != nil {
			resp.Error = err.Error()
			return resp
		}
		resp.Fallback = fallback
		if node != nil {
			resp.Node = node
		}
	case uiaOpFocus:
		if err := uiaHelperFocus(req.ElementID); err != nil {
			resp.Error = err.Error()
		}
	case uiaOpSetValue:
		if err := uiaHelperSetValue(req.ElementID, req.Value); err != nil {
			resp.Error = err.Error()
		}
	default:
		resp.Error = fmt.Sprintf("unknown op %q", req.Op)
	}
	return resp
}
