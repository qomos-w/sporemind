package sdk

import (
	"encoding/json"
	"fmt"
)

// CallableParam describes one parameter or field of a callable's request or
// response schema, as returned by ListCallables.
type CallableParam struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Description string `json:"Description,omitempty"`
	Required    bool   `json:"Required,omitempty"`
}

// CallableMeta is the metadata for one callable in the host registry. It is
// the decoded response item from the registry.query host call. Metadata only —
// no credential values (AuthToken etc.) ever appear here; calling a callable
// still requires its declared permission.
type CallableMeta struct {
	CallID       string          `json:"CallID"`
	Service      string          `json:"Service,omitempty"`
	Kind         string          `json:"Kind,omitempty"`
	Params       []CallableParam `json:"Params,omitempty"`
	FinalType    string          `json:"FinalType,omitempty"`
	FinalFields  []CallableParam `json:"FinalFields,omitempty"`
	Permission   string          `json:"Permission,omitempty"`
	EffectKind   string          `json:"EffectKind,omitempty"`
	Description  string          `json:"Description,omitempty"`
	ReqSchemaID  uint64          `json:"ReqSchemaId,omitempty"`
	RespSchemaID uint64          `json:"RespSchemaId,omitempty"`
}

// ListCallablesQuery is the request for the registry.query host call.
//
//   - Service filters by service name (case-insensitive substring).
//   - Callable filters by the full dotted callID and the description
//     (case-insensitive substring).
//   - Both empty returns all callables.
//   - Limit caps the page size; Cursor paginates.
// JSON tags use the lowercase wire keys pinned by the appbinding
// SDKCallCatalog contract — identical casing on both ends, no reliance on
// Go's case-insensitive field matching.
type ListCallablesQuery struct {
	Service  string `json:"service,omitempty"`
	Callable string `json:"callable,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

// ListCallablesResponse is the response from the registry.query host call.
type ListCallablesResponse struct {
	Items      []CallableMeta `json:"Items"`
	NextCursor string         `json:"NextCursor,omitempty"`
}

// ListCallables queries the host's callable registry via the registry.query
// host call. It returns metadata for callables matching the query filters,
// enabling a plugin to discover the full system's callable surface without
// hardcoding service names or callIDs.
//
// Requires the registry.read capability — declare sdk.PermRegistryRead in the
// appdef permissions block. The host enforces the declared-permission gate at
// invocation time; this function is metadata-only and never returns credential
// values.
func ListCallables(q ListCallablesQuery) (*ListCallablesResponse, error) {
	host := ActiveHost()
	data, err := host.Invoke("registry.query", q)
	if err != nil {
		return nil, fmt.Errorf("sdk.ListCallables: %w", err)
	}
	var resp ListCallablesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("sdk.ListCallables: decode response: %w", err)
	}
	return &resp, nil
}
