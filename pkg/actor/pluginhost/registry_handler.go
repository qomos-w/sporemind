package pluginhost

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// The registry.query wire types (RegistryQueryReq / RegistryQueryResp /
// RegistryCallableMeta) live in pkg/appbinding as part of the SDKCallCatalog
// wire contract — the catalog is the single source of truth shared with the
// SDK vendoring, the hostproto codegen extraction, and the conformance tests.
// This handler consumes them; it does not restate them.

const registryQueryDefaultLimit = 200

// handleRegistryQuery serves the SDK registry.query callID locally. It
// flattens the topology snapshot, filters by case-insensitive substring
// (Service → ServiceName, Callable → dotted CallID + Description), applies
// cursor pagination, and returns {Items, NextCursor}.
//
// Metadata carries no credential values: CallableInterface fields describe
// types and permissions, never secrets. Invocation still requires declared
// permissions; this call is discovery-only.
func (a *Actor) handleRegistryQuery(req []byte) ([]byte, error) {
	var r appbinding.RegistryQueryReq
	if err := json.Unmarshal(req, &r); err != nil {
		return nil, fmt.Errorf("pluginhost: registry.query decode: %w", err)
	}

	// Gather all callables from the topology snapshot.
	var all []domain.CallableInterface
	if a.topo != nil {
		for _, node := range a.topo.Snapshot() {
			all = append(all, node.Callables...)
		}
	}

	// Sort by CallID for a stable order (cursor pagination requires it).
	sort.Slice(all, func(i, j int) bool {
		return all[i].Name < all[j].Name
	})

	// Apply case-insensitive substring filters.
	serviceQ := strings.ToLower(r.Service)
	callableQ := strings.ToLower(r.Callable)
	var filtered []domain.CallableInterface
	for _, ci := range all {
		if serviceQ != "" {
			if !strings.Contains(strings.ToLower(ci.ServiceName), serviceQ) {
				continue
			}
		}
		if callableQ != "" {
			haystack := strings.ToLower(ci.Name + " " + ci.Description)
			if !strings.Contains(haystack, callableQ) {
				continue
			}
		}
		filtered = append(filtered, ci)
	}

	// Apply limit (default 200; negative clamped to 0).
	limit := r.Limit
	if limit <= 0 {
		limit = registryQueryDefaultLimit
	}

	// Apply cursor (offset-based: the cursor is the string offset into
	// the filtered list).
	offset := 0
	if r.Cursor != "" {
		if n, err := strconv.Atoi(r.Cursor); err == nil && n >= 0 {
			offset = n
		}
	}

	// Clamp offset into range.
	if offset > len(filtered) {
		offset = len(filtered)
	}

	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}

	var items []appbinding.RegistryCallableMeta
	if offset < len(filtered) {
		window := filtered[offset:end]
		items = make([]appbinding.RegistryCallableMeta, 0, len(window))
		for _, ci := range window {
			items = append(items, ciToMeta(ci))
		}
	}

	nextCursor := ""
	if end < len(filtered) {
		nextCursor = strconv.Itoa(end)
	}

	return json.Marshal(appbinding.RegistryQueryResp{
		Items:      items,
		NextCursor: nextCursor,
	})
}

// ciToMeta maps a CallableInterface to the wire-facing CallableMeta shape.
func ciToMeta(ci domain.CallableInterface) appbinding.RegistryCallableMeta {
	return appbinding.RegistryCallableMeta{
		CallID:       ci.Name,
		Service:      ci.ServiceName,
		Kind:         ci.Kind,
		Params:       ci.Params,
		FinalType:    ci.FinalType,
		FinalFields:  ci.FinalFields,
		Permission:   ci.Permission,
		EffectKind:   ci.EffectKind,
		Description:  ci.Description,
		ReqSchemaId:  ci.ReqSchemaID,
		RespSchemaId: ci.FinalSchemaID,
	}
}
