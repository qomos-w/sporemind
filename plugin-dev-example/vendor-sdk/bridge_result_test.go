package sdk

import (
	"strings"
	"testing"
)

// TestBridgeResultConvertsHostErrorEnvelope guards the error half of the host
// bridge wire contract: dispatch failures arrive as {"__host_error__": "..."}
// with a positive byte count. bridgeResult must surface them as Go errors —
// without this check state.set failures were silently swallowed.
func TestBridgeResultConvertsHostErrorEnvelope(t *testing.T) {
	if _, err := bridgeResult("state.set", []byte(`{"__host_error__":"app-state dispatch not configured"}`)); err == nil {
		t.Fatal("bridgeResult should convert __host_error__ envelope into an error")
	} else if want := "app-state dispatch not configured"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want substring %q", err, want)
	}
}

// TestBridgeResultPassesNormalResponse guards that legitimate responses (even
// ones carrying an error-ish field) pass through untouched.
func TestBridgeResultPassesNormalResponse(t *testing.T) {
	body := []byte(`{"Value":null,"Found":false,"error_detail":"upstream"}`)
	out, err := bridgeResult("state.get", body)
	if err != nil {
		t.Fatalf("normal response must not error: %v", err)
	}
	if string(out) != string(body) {
		t.Fatalf("response mutated: %q", out)
	}
}
