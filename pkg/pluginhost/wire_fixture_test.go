package pluginhost

import (
	"encoding/binary"
	"testing"
)

// TestWireFixtureBinaryCodecRoundTrip validates that the BinaryCodec frame
// encoding produced by EncodeInvokeFrame is byte-compatible with the decode
// path consumed by both the QuickApp and NativePlugin invoke routes. Both
// paths share the same length-prefixed framing (abi.go), so a frame authored
// on one side must round-trip cleanly on the other.
func TestWireFixtureBinaryCodecRoundTrip(t *testing.T) {
	// A representative JSON-style invoke payload, as a QuickApp or native
	// plugin would emit it.
	payload := []byte(`{"action":"test","value":42}`)

	frame := EncodeInvokeFrame(payload)
	decoded, err := DecodeInvokeFrame(frame)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if string(decoded) != string(payload) {
		t.Fatalf("round-trip mismatch: got %q want %q", decoded, payload)
	}

	// An empty payload must encode/decode without panicking.
	emptyFrame := EncodeInvokeFrame(nil)
	if got, err := DecodeInvokeFrame(emptyFrame); err != nil {
		t.Fatalf("empty payload decode failed: %v", err)
	} else if len(got) != 0 {
		t.Fatalf("empty payload should decode to empty, got %d bytes", len(got))
	}

	// A truncated frame (last byte removed) must be rejected rather than
	// silently yielding a partial payload.
	truncated := frame[:len(frame)-1]
	if _, err := DecodeInvokeFrame(truncated); err == nil {
		t.Fatal("expected truncated frame to be rejected")
	}
}

// TestDecodeInvokeFrame_RejectsIllegalPayloads exercises the frame validator
// with corrupted, oversized, and structurally invalid inputs.
func TestDecodeInvokeFrame_RejectsIllegalPayloads(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame []byte
	}{
		{
			name:  "empty frame (zero bytes)",
			frame: []byte{},
		},
		{
			name:  "partial header (1 byte)",
			frame: []byte{0x00},
		},
		{
			name:  "partial header (2 bytes)",
			frame: []byte{0x00, 0x00},
		},
		{
			name:  "partial header (3 bytes, just below minimum)",
			frame: []byte{0x00, 0x00, 0x00},
		},
		{
			name: "length exceeds actual payload",
			frame: func() []byte {
				f := make([]byte, 8)
				binary.BigEndian.PutUint32(f[:4], 100) // claims 100 bytes
				copy(f[4:], []byte("test"))            // only 4 bytes follow
				return f
			}(),
		},
		{
			name: "length is zero but payload has trailing bytes",
			frame: func() []byte {
				f := make([]byte, 8)
				binary.BigEndian.PutUint32(f[:4], 0) // claims 0 bytes
				copy(f[4:], []byte("extra"))          // 4 bytes follow
				return f
			}(),
		},
		{
			name: "max uint32 length (DoS guard)",
			frame: func() []byte {
				f := make([]byte, 8)
				binary.BigEndian.PutUint32(f[:4], 0xFFFFFFFF)
				copy(f[4:], []byte("tiny"))
				return f
			}(),
		},
		{
			name: "header only, zero-length payload",
			frame: func() []byte {
				f := make([]byte, 4)
				binary.BigEndian.PutUint32(f[:4], 0)
				return f
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The header-only zero-length case is actually valid.
			if tt.name == "header only, zero-length payload" {
				got, err := DecodeInvokeFrame(tt.frame)
				if err != nil {
					t.Fatalf("zero-length frame should be valid: %v", err)
				}
				if len(got) != 0 {
					t.Fatalf("expected empty payload, got %d bytes", len(got))
				}
				return
			}
			_, err := DecodeInvokeFrame(tt.frame)
			if err == nil {
				t.Fatal("expected error for illegal payload, got nil")
			}
		})
	}
}

// TestEncodeInvokeFrame_PreservesLargePayload verifies that a large payload
// (approaching the uint32 boundary in length-prefix space) round-trips
// correctly without corruption.
func TestEncodeInvokeFrame_PreservesLargePayload(t *testing.T) {
	// 64 KiB payload — well within uint32 range but exercises the copy path.
	payload := make([]byte, 64*1024)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	frame := EncodeInvokeFrame(payload)
	// Verify header encodes the correct length.
	declaredLen := binary.BigEndian.Uint32(frame[:4])
	if int(declaredLen) != len(payload) {
		t.Fatalf("header length = %d, want %d", declaredLen, len(payload))
	}
	decoded, err := DecodeInvokeFrame(frame)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(decoded) != len(payload) {
		t.Fatalf("decoded length = %d, want %d", len(decoded), len(payload))
	}
	for i, b := range decoded {
		if b != payload[i] {
			t.Fatalf("byte mismatch at %d: got %d want %d", i, b, payload[i])
		}
	}
}

// TestInvokeFramed_NilInvokeFunction verifies that a nil invoke function
// produces a clear error rather than a nil-deref panic.
func TestInvokeFramed_NilInvokeFunction(t *testing.T) {
	frame := EncodeInvokeFrame([]byte("test"))
	_, err := InvokeFramed(nil, frame, 0)
	if err == nil {
		t.Fatal("expected error when invoke is nil")
	}
}

// TestInvokeFramed_ResponseCapacityEnforced verifies that responses exceeding
// the declared capacity are rejected.
func TestInvokeFramed_ResponseCapacityEnforced(t *testing.T) {
	// invoke returns 10 bytes, capacity is 5 → must fail.
	invoke := func(payload []byte, capacity int) ([]byte, error) {
		return make([]byte, 10), nil
	}
	frame := EncodeInvokeFrame([]byte("test"))
	_, err := InvokeFramed(invoke, frame, 5)
	if err == nil {
		t.Fatal("expected capacity overflow error")
	}
}

// TestInvokeFramed_RoundTrip verifies a successful invoke round-trip through
// the length-prefixed frame protocol.
func TestInvokeFramed_RoundTrip(t *testing.T) {
	invoke := func(payload []byte, capacity int) ([]byte, error) {
		// Echo the payload back.
		return payload, nil
	}
	original := []byte(`{"hello":"world"}`)
	frame := EncodeInvokeFrame(original)
	resp, err := InvokeFramed(invoke, frame, 1024)
	if err != nil {
		t.Fatalf("InvokeFramed failed: %v", err)
	}
	// The response is a length-prefixed frame, so decode it.
	decoded, err := DecodeInvokeFrame(resp)
	if err != nil {
		t.Fatalf("decode response frame failed: %v", err)
	}
	if string(decoded) != string(original) {
		t.Fatalf("round-trip mismatch: got %q want %q", decoded, original)
	}
}
