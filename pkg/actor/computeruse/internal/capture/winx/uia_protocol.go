// UIA helper wire protocol, shared by the main-process client and the
// helper subprocess. Requests and responses are single-line JSON objects
// exchanged over the helper's stdin/stdout.
package winx

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

// maxFrameSize bounds a single protocol message so a wedged or hostile
// helper cannot exhaust memory.
const maxFrameSize = 8 << 20 // 8 MiB

// uiaOp identifies one helper operation.
type uiaOp string

const (
	uiaOpList     uiaOp = "list"
	uiaOpInfo     uiaOp = "info"
	uiaOpClick    uiaOp = "click"
	uiaOpFocus    uiaOp = "focus"
	uiaOpSetValue uiaOp = "set_value"
)

// uiaRequest is a single helper request. Fields are op-specific; unused
// fields are omitted on the wire.
type uiaRequest struct {
	ID           uint64 `json:"id"`
	Op           uiaOp  `json:"op"`
	WindowID     int    `json:"window_id,omitempty"`
	MaxDepth     int    `json:"max_depth,omitempty"`
	MaxResults   int    `json:"max_results,omitempty"`
	NameContains string `json:"name_contains,omitempty"`
	ControlType  string `json:"control_type,omitempty"`
	ElementID    string `json:"element_id,omitempty"`
	Button       string `json:"button,omitempty"`
	Value        string `json:"value,omitempty"`
}

// uiaResponse is the helper's answer to a request. Error is set for any
// failure (COM error, invalid element id, unsupported op, ...); the host
// treats a non-empty Error exactly like a Go error and never lets a
// helper-side failure terminate the process.
type uiaResponse struct {
	ID    uint64 `json:"id"`
	Error string `json:"error,omitempty"`

	// Nodes carries list results.
	Nodes []capture.ElementNode `json:"nodes,omitempty"`
	// Node carries info results and the click-fallback snapshot.
	Node *capture.ElementNode `json:"node,omitempty"`
	// Fallback asks the host to perform a coordinate click on Node.Bounds
	// because the element exposes no invokable pattern.
	Fallback bool `json:"fallback,omitempty"`
}

var errFrameTooLarge = errors.New("uia frame exceeds size limit")

// writeFrame encodes v as one newline-terminated JSON line.
func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > maxFrameSize {
		return errFrameTooLarge
	}
	b = append(b, '\n')
	n, err := w.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return io.ErrShortWrite
	}
	return nil
}

// readFrame reads one newline-terminated line, capped at max bytes. A
// trailing EOF with buffered data yields io.ErrUnexpectedEOF; a clean EOF
// with no data yields io.EOF.
func readFrame(r *bufio.Reader, max int) ([]byte, error) {
	var buf []byte
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF && len(buf) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if b == '\n' {
			return buf, nil
		}
		buf = append(buf, b)
		if len(buf) > max {
			return nil, fmt.Errorf("%w: %d bytes", errFrameTooLarge, len(buf))
		}
	}
}
