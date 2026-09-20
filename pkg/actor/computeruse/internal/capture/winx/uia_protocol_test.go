package winx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"io"
	"testing"

	"github.com/qomos-w/sporemind/pkg/actor/computeruse/internal/capture"
)

func TestWriteReadFrameRoundTrip(t *testing.T) {
	req := uiaRequest{
		ID: 7, Op: uiaOpList, WindowID: 123, MaxDepth: 3,
		NameContains: "OK", ControlType: "button",
	}
	var buf bytes.Buffer
	if err := writeFrame(&buf, &req); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	got, err := readFrame(bufio.NewReader(&buf), maxFrameSize)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	var decoded uiaRequest
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != req {
		t.Fatalf("round trip mismatch: got %+v want %+v", decoded, req)
	}
}

func TestWriteReadFrameElementNode(t *testing.T) {
	resp := uiaResponse{
		ID:    11,
		Nodes: []capture.ElementNode{{ID: "e1", Name: "x", ControlType: "edit", Bounds: image.Rect(10, 20, 30, 40)}},
	}
	var buf bytes.Buffer
	if err := writeFrame(&buf, &resp); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	line, err := readFrame(bufio.NewReader(&buf), maxFrameSize)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	var decoded uiaResponse
	if err := json.Unmarshal(line, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Nodes) != 1 || decoded.Nodes[0].ID != "e1" || decoded.Nodes[0].Bounds != image.Rect(10, 20, 30, 40) {
		t.Fatalf("element round trip mismatch: %+v", decoded.Nodes)
	}
}

func TestReadFrameSizeLimit(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"id":1,"op":"list","name_contains":"`)
	for i := 0; i < 100; i++ {
		buf.WriteString("0123456789abcdef")
	}
	buf.WriteString(`"}` + "\n")
	_, err := readFrame(bufio.NewReader(&buf), 64)
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("expected errFrameTooLarge, got %v", err)
	}
}

func TestReadFrameEOF(t *testing.T) {
	_, err := readFrame(bufio.NewReader(bytes.NewReader(nil)), maxFrameSize)
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestReadFrameTruncated(t *testing.T) {
	_, err := readFrame(bufio.NewReader(bytes.NewBufferString("no newline here")), maxFrameSize)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("expected io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestWriteFrameTooLarge(t *testing.T) {
	big := uiaRequest{NameContains: string(make([]byte, maxFrameSize+1))}
	err := writeFrame(&bytes.Buffer{}, &big)
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("expected errFrameTooLarge, got %v", err)
	}
}
