package appbinding

import (
	"reflect"
	"sort"
	"strings"
	"time"
)

// CapabilityDef declares one host capability: its authorization identifier,
// presentation metadata, and the SDK-facing callIDs that gate to it. Together
// with HostCallDef this is the single registration point for the host
// capability surface — adding a capability is one RegisterCapability call plus
// one RegisterHostCall call per callID, all colocated in this file (or a
// cap_*.go file for new subsystems).
type CapabilityDef struct {
	ID                 string
	Title              string
	Description        string
	RiskLevel          string
	I18nTitleKey       string
	I18nDescriptionKey string
	Locales            map[string]CapabilityLocale

	// CallPrefixes are prefix-match families: any callID with this prefix
	// gates to the capability (e.g. "llm." matches llm.complete and any
	// future llm.* callID). Exact callID→capability mappings are derived
	// automatically from RegisterHostCall's Capability field — do not
	// duplicate them here.
	CallPrefixes []string
}

// HostCallDef declares one SDK-facing host callID: the capability it requires,
// its routing (alias target or pluginhost-local), and its adapted wire
// contract when that differs from the backing callable schema.
type HostCallDef struct {
	CallID     string
	Capability string

	// Target is the backing callable's callID when it differs from CallID
	// (an SDK-facing alias). Empty means CallID routes to itself.
	Target string

	// Local marks callIDs served by the pluginhost itself (no backing
	// gospore callable): state.*, config.get, app.emit, registry.query,
	// provider.get, db.profile_list, db.profile_dial. The host bridge
	// routes these to its local dispatch.
	Local bool

	// Adapted wire contract — set only when the SDK-facing wire shape differs
	// from the backing callable schema (llm.* payloads are adapted,
	// state.*/config.get are local). Pass-through callIDs omit these and
	// keep their manifest-extracted schemas.
	ReqType         reflect.Type
	RespType        reflect.Type
	Note            string
	StreamChunkKind string

	// Budget bounds a NON-streaming (unary) host call whose latency is
	// legitimately long — image/video generation runs minutes. Stream routes
	// carry their budget on StreamRoute instead; a Budget here lifts the
	// generic 30s reverse-call cap for this callID on every transport path
	// (framed and HTTP-data). Zero means the generic cap applies.
	Budget time.Duration
}

var (
	// registeredCaps holds every registered CapabilityDef by ID. It backs
	// IsKnownHostCapability and CapabilityCatalog.
	registeredCaps = map[string]CapabilityDef{}
	// callIDToCapability is the exact-match callID → capability map.
	callIDToCapability = map[string]string{}
	// callPrefixToCapability maps prefix families → capability; matched
	// longest-prefix-first by HostCallCapability.
	callPrefixToCapability = map[string]string{}
	// registeredHostCalls holds every registered HostCallDef by SDK callID.
	// It backs LocalHostCallIDs and the alias catalog below.
	registeredHostCalls = map[string]HostCallDef{}
	// hostCallBudgets holds the unary per-callID latency budgets declared on
	// HostCallDef.Budget. It backs HostCallBudget, which the pluginhost
	// reverse-bridge consults to lift the generic 30s cap for long unary
	// host calls (image/video generation).
	hostCallBudgets = map[string]time.Duration{}
)

// RegisterCapability installs one capability definition. It populates the
// known-capability set, the CapabilityCatalog presentation map, and the
// callID→capability lookup tables. Calls belong in init() alongside the
// matching RegisterHostCall blocks.
func RegisterCapability(def CapabilityDef) {
	registeredCaps[def.ID] = def
	for _, prefix := range def.CallPrefixes {
		callPrefixToCapability[prefix] = def.ID
	}
	CapabilityCatalog[def.ID] = Capability{
		ID:                 def.ID,
		Title:              def.Title,
		Description:        def.Description,
		RiskLevel:          def.RiskLevel,
		I18nTitleKey:       def.I18nTitleKey,
		I18nDescriptionKey: def.I18nDescriptionKey,
		Locales:            def.Locales,
	}
}

// RegisterHostCall installs one SDK-facing host callID: its capability
// requirement, routing, and optional adapted wire contract. It populates the
// exact callID→capability mapping (the authorization gate), the alias catalog
// (when Target differs), and the SDKCallCatalog (when an adapted wire
// contract is present).
func RegisterHostCall(def HostCallDef) {
	callIDToCapability[def.CallID] = def.Capability
	registeredHostCalls[def.CallID] = def
	if def.Target != "" && def.Target != def.CallID {
		HostCallAliases[def.CallID] = def.Target
	}
	if def.ReqType != nil || def.RespType != nil || def.Note != "" || def.StreamChunkKind != "" {
		SDKCallCatalog[def.CallID] = SDKCall{
			CallID:          def.CallID,
			ReqType:         def.ReqType,
			RespType:        def.RespType,
			Note:            def.Note,
			StreamChunkKind: def.StreamChunkKind,
		}
	}
	if def.Budget > 0 {
		hostCallBudgets[def.CallID] = def.Budget
	}
}

// HostCallBudget returns the unary latency budget a callID registered via
// HostCallDef.Budget. Zero means the generic host-bridge cap applies.
func HostCallBudget(callID string) time.Duration { return hostCallBudgets[callID] }

// HostCallCapability maps a host-bridge callID (the SDK-facing identifier a
// plugin passes to Host.Invoke) to the host capability it requires. It is the
// single source of truth shared by the pluginhost bridge (authorization) and
// the appmanager protocol query/extraction (protocol release). Exact
// registrations win over prefix families; within prefix families the longest
// match wins. CallIDs that match neither return "" and are always denied.
func HostCallCapability(callID string) string {
	if cap, ok := callIDToCapability[callID]; ok {
		return cap
	}
	bestPrefix := ""
	bestCap := ""
	for prefix, cap := range callPrefixToCapability {
		if strings.HasPrefix(callID, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix = prefix
			bestCap = cap
		}
	}
	return bestCap
}

// CapabilityHasCallGate reports whether any registered host callID or prefix
// family gates to the capability. Load-time grants (app.data: authorized by
// manifest declaration, delivered through the OnLoad config rather than a
// host call) return false and are exempt from the callID-coverage invariant.
func CapabilityHasCallGate(id string) bool {
	for _, cap := range callIDToCapability {
		if cap == id {
			return true
		}
	}
	for _, cap := range callPrefixToCapability {
		if cap == id {
			return true
		}
	}
	return false
}

// LocalHostCallIDs returns the sorted callIDs served by the pluginhost itself
// (no backing gospore callable). It replaces the hand-maintained
// localHostCalls list in the appmanager host_protocol surface.
func LocalHostCallIDs() []string {
	var ids []string
	for cid, def := range registeredHostCalls {
		if def.Local {
			ids = append(ids, cid)
		}
	}
	sort.Strings(ids)
	return ids
}

// RegisteredHostCall returns the HostCallDef for an SDK-facing callID.
func RegisteredHostCall(callID string) (HostCallDef, bool) {
	def, ok := registeredHostCalls[callID]
	return def, ok
}
