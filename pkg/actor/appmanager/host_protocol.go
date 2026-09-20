package appmanager

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/schema"
	hostgen "github.com/qomos-w/sporemind/gen"
	"github.com/qomos-w/sporemind/pkg/appbinding"
	"github.com/qomos-w/sporemind/pkg/codegen"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Local host callIDs (config.get, state.*, app.emit, registry.query,
// provider.get, dialog.openFile/dialog.openFolder) are served by the
// pluginhost itself rather than
// by a backing gospore callable. They may be absent from the gospore
// manifest, so ResolveHostCalls injects them into the resolved surface via
// appbinding.LocalHostCallIDs() to make the query list them. Their wire
// contracts live in the appbinding registration tables — the HostCallDef,
// not any hand-maintained list here, owns typing, notes, and locality.

// ResolveHostCalls turns the embedded gospore manifest (gen/gmanifest.json)
// into the SDK-facing host callID -> wire protocol mapping consumed by the
// protocol query and the dev_generate extraction. Pure function of the
// manifest bytes so it is unit-testable; the actor caches the result.
//
// Enumeration rule: every manifest callable becomes a candidate callID
// (Service.Name). Alias targets are reported under each SDK-facing alias
// (llm.complete and llm.chat both resolve aiaggregator.dispatch); callIDs
// that already gate to a capability (shell.exec, sshmanager.*) are reported
// under their own name. Everything else is host-internal and dropped.
func ResolveHostCalls(manifestJSON []byte) (map[string]codegen.HostCallSchema, error) {
	var gm schema.GosporeManifest
	if err := json.Unmarshal(manifestJSON, &gm); err != nil {
		return nil, fmt.Errorf("parse gospore manifest: %w", err)
	}

	// Reverse alias index: target callID -> sorted SDK-facing callIDs.
	reverse := make(map[string][]string)
	for sdk, target := range appbinding.HostCallAliases {
		reverse[target] = append(reverse[target], sdk)
	}
	for _, list := range reverse {
		sort.Strings(list)
	}

	calls := make(map[string]codegen.HostCallSchema)
	for _, c := range gm.Callables {
		// RegisterDomain exposure scope: only callables whose domain was
		// EXPOSED as a service (manifest Service != "" — set by
		// DomainHandle.Expose/ExposeChildren at registration) are reachable
		// by the host bridge, which resolves targets via LookupService. A
		// callable whose domain was merely declared (RegisterDomain without
		// Expose) carries Service == "" with Namespace set: it is
		// actor-scoped and requires a concrete ActorID to invoke — there is
		// no service to route to, so it must NOT be promoted to a fake
		// service callID here (the old c.Namespace fallback did exactly
		// that and produced un-routable generated callers).
		if c.Service == "" || c.Name == "" {
			continue
		}
		service := c.Service
		callID := service + "." + c.Name
		streaming := c.Mode == "streaming"

		emit := func(sdkCallID string) {
			if appbinding.HostCallCapability(sdkCallID) == "" {
				return
			}
			calls[sdkCallID] = codegen.HostCallSchema{
				TargetCallID:  callID,
				Service:       service,
				Streaming:     streaming,
				ReqSchemaID:   c.ReqSchemaID,
				FinalSchemaID: c.FinalSchemaID,
				ChunkSchemaID: c.ChunkSchemaID,
			}
		}

		if aliases := reverse[callID]; len(aliases) > 0 {
			for _, alias := range aliases {
				emit(alias)
			}
			continue
		}
		emit(callID)
	}

	for _, callID := range appbinding.LocalHostCallIDs() {
		if _, exists := calls[callID]; !exists {
			calls[callID] = codegen.HostCallSchema{TargetCallID: callID}
		}
	}
	return calls, nil
}

// hostCalls returns the cached resolved host protocol. The manifest is
// embedded at compile time, so the parse result is immutable for the
// process lifetime; the cache is derived read-only data, not actor state.
func (a *Actor) hostCalls() (map[string]codegen.HostCallSchema, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hostCallsCache != nil {
		return a.hostCallsCache, nil
	}
	calls, err := ResolveHostCalls(hostgen.ManifestJSON())
	if err != nil {
		return nil, err
	}
	a.hostCallsCache = calls
	return calls, nil
}

// handleHostProtocol serves appmanager.host_protocol: the plugin-reachable
// host capability surface with, per capability, the SDK host callIDs and the
// request/response type names + schema IDs behind them. Read-only discovery
// for the query → declare → dev_generate extraction flow.
func (a *Actor) handleHostProtocol(_ actor.PureContext, req gen.AppManagerHostProtocolReq) (gen.AppManagerHostProtocolResp, error) {
	calls, err := a.hostCalls()
	if err != nil {
		return gen.AppManagerHostProtocolResp{}, fmt.Errorf("resolve host protocol: %w", err)
	}

	query := strings.ToLower(req.Query)
	wantCapability := strings.TrimSpace(req.Capability)

	capIDs := make([]string, 0, len(appbinding.CapabilityCatalog))
	for id := range appbinding.CapabilityCatalog {
		capIDs = append(capIDs, id)
	}
	sort.Strings(capIDs)

	var out []gen.AppManagerHostCapabilityInfo
	for _, capID := range capIDs {
		if wantCapability != "" && capID != wantCapability {
			continue
		}
		meta := appbinding.CapabilityCatalog[capID]

		var callIDs []string
		for callID := range calls {
			if appbinding.HostCallCapability(callID) == capID {
				callIDs = append(callIDs, callID)
			}
		}
		sort.Strings(callIDs)

		// Query filter matches capability, title, description or any callID.
		if query != "" {
			hay := strings.ToLower(capID + " " + meta.Title + " " + meta.Description + " " + strings.Join(callIDs, " "))
			if !strings.Contains(hay, query) {
				continue
			}
		}

		infos := make([]gen.AppManagerHostCallInfo, 0, len(callIDs))
		for _, callID := range callIDs {
			c := calls[callID]
			info := gen.AppManagerHostCallInfo{
				CallID:       callID,
				TargetCallID: c.TargetCallID,
				Service:      c.Service,
				Streaming:    c.Streaming,
			}
			// Catalog entries own the SDK wire contract: the manifest schema
			// behind an aliased/backed callID is NOT what an app sends (llm.*
			// payloads are adapted, state.*/config.get are local), so type
			// names and streaming come from the catalog, not the manifest.
			if sc, ok := appbinding.LookupSDKCall(callID); ok {
				info.Streaming = appbinding.IsSDKCallStreaming(callID)
				if sc.ReqType != nil {
					info.RequestType = sc.ReqType.Name()
				}
				if sc.RespType != nil {
					info.ResponseType = sc.RespType.Name()
				} else {
					info.ResponseType = ""
				}
				if sc.Note != "" {
					info.Note = sc.Note
				}
			} else if c.ReqSchemaID != 0 || c.FinalSchemaID != 0 || c.ChunkSchemaID != 0 {
				if c.ReqSchemaID != 0 {
					info.RequestSchemaID = c.ReqSchemaID
					info.RequestType = gen.SchemaIDs[c.ReqSchemaID]
				}
				if c.FinalSchemaID != 0 {
					info.ResponseSchemaID = c.FinalSchemaID
					info.ResponseType = gen.SchemaIDs[c.FinalSchemaID]
				}
				if c.ChunkSchemaID != 0 && info.ResponseType == "" {
					info.ResponseSchemaID = c.ChunkSchemaID
					info.ResponseType = gen.SchemaIDs[c.ChunkSchemaID]
				}
			}
			infos = append(infos, info)
		}

		out = append(out, gen.AppManagerHostCapabilityInfo{
			Capability:  capID,
			Title:       meta.Title,
			Description: meta.Description,
			RiskLevel:   meta.RiskLevel,
			Calls:       infos,
		})
	}
	if out == nil {
		out = []gen.AppManagerHostCapabilityInfo{}
	}
	return gen.AppManagerHostProtocolResp{Capabilities: out}, nil
}
