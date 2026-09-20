package sdk

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestListCallablesCoversWireAndDecode pins the registry.query contract: the
// query rides the wire as the ListCallablesQuery struct, the response decodes
// into CallableMeta items with all contract fields, and host errors surface.
func TestListCallablesCoversWireAndDecode(t *testing.T) {
	type invokeCapture struct {
		callID  string
		payload ListCallablesQuery
	}
	var captured invokeCapture
	host := &captureHost{invoke: func(callID string, payload any) ([]byte, error) {
		req, ok := payload.(ListCallablesQuery)
		if !ok {
			t.Fatalf("registry.query payload type %T, want ListCallablesQuery", payload)
		}
		captured = invokeCapture{callID: callID, payload: req}
		resp := ListCallablesResponse{
			Items: []CallableMeta{
				{
					CallID:      "project.read_file",
					Service:     "project",
					Kind:        "unary",
					Description: "Read a project file",
					Params: []CallableParam{
						{Name: "Path", Type: "string", Required: true},
					},
					FinalType:    "ProjectReadFileResp",
					FinalFields:  []CallableParam{{Name: "Content", Type: "string"}},
					Permission:   "fs.read",
					EffectKind:   "none",
					ReqSchemaID:  1234,
					RespSchemaID: 1235,
				},
			},
			NextCursor: "cursor-2",
		}
		data, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return data, nil
	}}
	SetHost(host)
	defer SetHost(nil)

	q := ListCallablesQuery{Service: "project", Limit: 10, Cursor: "cursor-1"}
	result, err := ListCallables(q)
	if err != nil {
		t.Fatalf("ListCallables: %v", err)
	}

	if captured.callID != "registry.query" {
		t.Errorf("host callID = %q, want registry.query", captured.callID)
	}
	if captured.payload.Service != "project" || captured.payload.Limit != 10 || captured.payload.Cursor != "cursor-1" {
		t.Errorf("wire query = %+v, want {Service:project Limit:10 Cursor:cursor-1}", captured.payload)
	}

	if len(result.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.CallID != "project.read_file" || item.Service != "project" {
		t.Errorf("item identity = %+v", item)
	}
	if item.ReqSchemaID != 1234 || item.RespSchemaID != 1235 {
		t.Errorf("schema IDs = %d/%d, want 1234/1235", item.ReqSchemaID, item.RespSchemaID)
	}
	if len(item.Params) != 1 || item.Params[0].Name != "Path" || !item.Params[0].Required {
		t.Errorf("params = %+v", item.Params)
	}
	if result.NextCursor != "cursor-2" {
		t.Errorf("NextCursor = %q, want cursor-2", result.NextCursor)
	}
}

// TestListCallablesQueryWireCasing pins the exact JSON wire casing of the
// request: lowercase keys, matching the appbinding SDKCallCatalog contract
// (TestSDKCallCatalogRegistryQueryWire pins the same golden on the host side).
// A casing drift here would still decode via Go's case-insensitive matching,
// so only an explicit golden catches it.
func TestListCallablesQueryWireCasing(t *testing.T) {
	data, err := json.Marshal(ListCallablesQuery{Service: "project", Callable: "read", Limit: 10, Cursor: "42"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"service":"project","callable":"read","limit":10,"cursor":"42"}`
	if string(data) != want {
		t.Fatalf("wire JSON = %s, want %s", data, want)
	}
}

// TestListCallablesHostError verifies that a host-side error is surfaced.
func TestListCallablesHostError(t *testing.T) {
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return nil, errTestHostFailure
	}})
	defer SetHost(nil)

	_, err := ListCallables(ListCallablesQuery{})
	if err == nil || !strings.Contains(err.Error(), "host failure") {
		t.Errorf("host error not surfaced: %v", err)
	}
}

// TestListCallablesDecodeError verifies that a malformed response is surfaced.
func TestListCallablesDecodeError(t *testing.T) {
	SetHost(&captureHost{invoke: func(string, any) ([]byte, error) {
		return []byte(`not json`), nil
	}})
	defer SetHost(nil)

	_, err := ListCallables(ListCallablesQuery{})
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Errorf("decode error not surfaced: %v", err)
	}
}

// TestListCallablesEmptyQuery verifies the all-callables path (both filters empty).
func TestListCallablesEmptyQuery(t *testing.T) {
	SetHost(&captureHost{invoke: func(callID string, payload any) ([]byte, error) {
		req, ok := payload.(ListCallablesQuery)
		if !ok {
			t.Fatalf("payload type %T, want ListCallablesQuery", payload)
		}
		if req.Service != "" || req.Callable != "" {
			t.Errorf("empty query must have empty Service/Callable, got %+v", req)
		}
		return []byte(`{"Items":[]}`), nil
	}})
	defer SetHost(nil)

	result, err := ListCallables(ListCallablesQuery{})
	if err != nil {
		t.Fatalf("ListCallables: %v", err)
	}
	if len(result.Items) != 0 {
		t.Errorf("items = %d, want 0", len(result.Items))
	}
}
