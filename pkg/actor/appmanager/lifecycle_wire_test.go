package appmanager

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/spore/transport"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestDecodeSporeAppInvokeRespJSONEnvelope(t *testing.T) {
	want := []byte(`TBC\x02payload`)
	body, err := json.Marshal(gen.SporeAppInvokeResp{Payload: want})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	got, err := decodeSporeAppInvokeResp(body)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestDecodeSporeAppInvokeRespBinaryEnvelope(t *testing.T) {
	view, err := (&transport.BinaryCodec{}).Encode(sporeAppInvokeRespDesc, identity.CanonicalID{}, gen.SporeAppInvokeResp{Payload: []byte(`TBC\x02payload`)})
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	got, err := decodeSporeAppInvokeResp(view.Data)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if string(got) != `TBC\x02payload` {
		t.Fatalf("payload = %q, want %q", got, `TBC\x02payload`)
	}
}
