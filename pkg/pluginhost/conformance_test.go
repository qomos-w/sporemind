package pluginhost

import "testing"

func TestBinaryCodecFrameConformance(t *testing.T) {
	cases := [][]byte{nil, {}, {0}, []byte("hello"), make([]byte, 1024)}
	for _, input := range cases {
		frame := EncodeInvokeFrame(input)
		got, err := DecodeInvokeFrame(frame)
		if err != nil {
			t.Fatalf("input len %d: %v", len(input), err)
		}
		if string(got) != string(input) {
			t.Fatalf("input len %d: round trip mismatch", len(input))
		}
	}
}

func TestBinaryCodecRejectsTruncatedAndTrailingFrames(t *testing.T) {
	for _, frame := range [][]byte{{}, {0}, {0, 0, 0}, {0, 0, 0, 1}, {0, 0, 0, 0, 1}} {
		if _, err := DecodeInvokeFrame(frame); err == nil {
			t.Fatalf("expected rejection for frame %v", frame)
		}
	}
}
