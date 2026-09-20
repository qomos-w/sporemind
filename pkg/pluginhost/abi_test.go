package pluginhost

import "testing"

func TestInvokeFramed(t *testing.T) {
	frame, err := InvokeFramed(func(request []byte, capacity int) ([]byte, error) { return append(request, '!'), nil }, EncodeInvokeFrame([]byte("ok")), 8)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := DecodeInvokeFrame(frame)
	if err != nil || string(payload) != "ok!" {
		t.Fatalf("payload=%q err=%v", payload, err)
	}
}

func TestInvokeFramedRejectsResponseCapacity(t *testing.T) {
	_, err := InvokeFramed(func([]byte, int) ([]byte, error) { return []byte("long"), nil }, EncodeInvokeFrame([]byte("x")), 2)
	if err == nil {
		t.Fatal("expected capacity error")
	}
}
