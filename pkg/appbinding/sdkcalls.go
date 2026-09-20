package appbinding

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// SDKCall describes one SDK-facing host callID whose wire contract is NOT the
// schema of a backing gospore callable. These are exactly the callIDs where
// the toolchain must not extract types from the host manifest: llm.* payloads
// are adapted (adaptLLMPayload) and the terminal aggregated (llmAggregator),
// provider.get is a pluginhost-side filter over the provider list, and
// config.*/state.* are served locally by the pluginhost. The catalog is the
// single source of truth for the app-facing wire shapes of these calls;
// pass-through callIDs (project.*, provider.list, aggregator.*, shell.*,
// sshmanager.*) keep their manifest-extracted schemas, which already match the
// wire (Go's encoding/json decodes field names case-insensitively, so the
// lowercase SDK keys reach the capitalized host fields).
type SDKCall struct {
	// CallID is the SDK-facing identifier passed to Host.Invoke.
	CallID string
	// ReqType is the request payload type (nil = no payload). Its json tags
	// are the wire contract.
	ReqType reflect.Type
	// RespType is the terminal response type (nil = opaque response; the
	// generated caller returns the raw bytes).
	RespType reflect.Type
	// Note documents host-served (local) calls in the host_protocol query.
	Note string
	// StreamChunkKind names the wire chunk vocabulary this callID's stream
	// uses: "LLMChunk" decodes each envelope into sdk.LLMChunk via
	// sdk.ForwardLLMChunks (what the generated StreamLLM* callers wire);
	// empty means raw envelope bytes — the app decodes its own domain chunk
	// from the {"kind","data"} data field.
	//
	// Streaming-ness itself is NOT stored here: a catalog callID streams iff
	// it has a registered appbinding stream route (LookupStreamRoute), which
	// in turn mirrors the backing actor's actor.Streaming[T]() declaration —
	// the same chain the manifest extraction uses. Use IsSDKCallStreaming.
	StreamChunkKind string
}

// IsSDKCallStreaming reports whether an SDK host callID has chunked delivery:
// a stream route is registered for it. This is the catalog-side counterpart
// of the manifest-derived HostCallSchema.Streaming and must be used instead
// of storing a second Streaming flag that could drift.
func IsSDKCallStreaming(callID string) bool {
	_, ok := LookupStreamRoute(callID)
	return ok
}

// SDKCallCatalog enumerates every SDK host callID whose wire contract must be
// declared here rather than extracted from the backing callable schema. It
// starts empty and is populated at package init by RegisterHostCall calls
// (see capabilities_registered.go and cap_*.go). A catalog entry implicitly
// requires the capability HostCallCapability(CallID) maps to — the capability
// is derived, never stored, so the runtime gate stays the single
// authorization source.
var SDKCallCatalog = map[string]SDKCall{}

// LookupSDKCall returns the catalog entry for callID.
func LookupSDKCall(callID string) (SDKCall, bool) {
	c, ok := SDKCallCatalog[callID]
	return c, ok
}

// IsSDKHostCallAlias reports whether callID is one of the fixed SDK-facing
// aliases (project.read_file, provider.list, ...) whose wire contract IS the
// backing callable's schema. Together with the SDKCallCatalog keys this is
// the static declarable set; callIDs from the open prefix families
// (shell.*, sshmanager.*) validate against the gospore manifest surface the
// appmanager provides at dev_generate time.
func IsSDKHostCallAlias(callID string) bool {
	_, ok := HostCallAliases[callID]
	return ok
}

// DerivedCapabilities translates declared .appdef permissions into the
// manifest permission set. Host callIDs (llm.complete, state.get,
// project.read_file, ...) map to their sorted, deduped host capabilities —
// the runtime authorization chain (manifest validation, consent dialog,
// pluginhost bridge gate) speaks capabilities. Entries that map to no
// capability prefix are app-internal permission labels (e.g. the `public`
// token referenced by `event { permission: }`) and pass through verbatim.
func DerivedCapabilities(callIDs []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, id := range callIDs {
		v := HostCallCapability(id)
		if v == "" || strings.HasPrefix(id, "plugin.") {
			// plugin.* bundle callIDs stay verbatim: they are their own
			// per-callID authorization gates at the host bridge (granting
			// plugin.X.translate must not imply plugin.Y.other even though
			// both map to the bundle.invoke capability).
			v = id
		}
		if seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// --- SDK wire-contract types ---
//
// The json tags are the wire contract consumed by the host adapters
// (llm_stream.go's adaptLLMPayload, the pluginhost local handlers); the
// conformance tests pin that alignment. They are emitted verbatim into apps
// by the dev_generate host-protocol extraction.

// LLMMessage is one conversation message in LLMReq.Messages. Content is
// either a plain string or an array of typed blocks ({"type","text"} plus
// tool blocks: assistant {"type":"tool_use","tool_use_id","tool_name","input"}
// and user {"type":"tool_result","tool_use_id","text","is_error"} — the loop
// shape a tools-carrying request produces).
type LLMMessage struct {
	Role    string `json:"role,omitempty"`
	Content any    `json:"content,omitempty"`
}

// LLMToolSpec is one function tool offered to the model. InputSchema is the
// JSON Schema object for the tool arguments; omitting it yields an empty
// object schema on the wire.
type LLMToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// LLMToolCall is one completed tool call the model emitted. Arguments is the
// raw JSON the model produced as tool input. The plugin executes the call
// itself — the host never dispatches plugin tools.
type LLMToolCall struct {
	ID        string          `json:"Id,omitempty"`
	Name      string          `json:"Name"`
	Arguments json.RawMessage `json:"Arguments,omitempty"`
}

// LLMReq is the SDK-facing llm.complete / llm.chat request. Prompt and
// Messages are mutually exclusive; Model+Provider together pin a ModelUnit
// (a naked Model is rejected host-side). Tools offers function tools to the
// model; completed calls come back in LLMResp.ToolCalls. ToolChoice controls
// whether the model must call one: "auto" (default), "none", "required" (any
// tool), a bare tool name (forces that exact tool), or the OpenAI JSON object
// form {"type":"function","function":{"name":"…"}}.
type LLMReq struct {
	Prompt           string        `json:"prompt,omitempty"`
	Messages         []LLMMessage  `json:"messages,omitempty"`
	System           string        `json:"system,omitempty"`
	Model            string        `json:"model,omitempty"`
	Provider         string        `json:"provider,omitempty"`
	Temperature      *float64      `json:"temperature,omitempty"`
	TopP             *float64      `json:"top_p,omitempty"`
	FrequencyPenalty *float64      `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64      `json:"presence_penalty,omitempty"`
	ReasoningEffort  string        `json:"reasoning_effort,omitempty"`
	ThinkingBudget   *int32        `json:"thinking_budget,omitempty"`
	Tools            []LLMToolSpec `json:"tools,omitempty"`
	ToolChoice       string        `json:"tool_choice,omitempty"`
}

// LLMResp is the llm.complete / llm.chat terminal (the llmAggregator output
// shape); Usage is the final usage object and ToolCalls the completed tool
// calls when the model ended with tool use instead of text.
type LLMResp struct {
	Text      string          `json:"Text"`
	Reasoning string          `json:"Reasoning,omitempty"`
	Usage     json.RawMessage `json:"Usage,omitempty"`
	ToolCalls []LLMToolCall   `json:"ToolCalls,omitempty"`
}

// ProviderGetReq is the provider.get request.
type ProviderGetReq struct {
	ID string `json:"id"`
}

// ConfigGetReq is the config.get request (scope "host" only).
type ConfigGetReq struct {
	Scope string `json:"scope"`
	Key   string `json:"key"`
}

// DbProfileDialReq is the db.profile_dial request.
type DbProfileDialReq struct {
	ID string `json:"id"`
}

// DbProfileDialResp is the db.profile_dial response: dial-ready parameters
// for a dbmanager connection profile. The SSH tunnel (if any) is already
// resolved to a loopback DialAddr and the decision-point-4 TLS default is
// applied, so the plugin dials DialAddr directly. Raw secrets cross this
// surface — it is gated by the db.dial capability (RiskHigh).
type DbProfileDialResp struct {
	Backend       string `json:"backend"`
	Endpoint      string `json:"endpoint"`
	Database      string `json:"database,omitempty"`
	DialAddr      string `json:"dial_addr"`
	TLS           bool   `json:"tls"`
	TLSServerName string `json:"tls_server_name,omitempty"`
	AuthSource    string `json:"auth_source,omitempty"`
	Username      string `json:"username,omitempty"`
	Password      string `json:"password,omitempty"`
	AccessKey     string `json:"access_key,omitempty"`
	Secret        string `json:"secret,omitempty"`
	Token         string `json:"token,omitempty"`
}

// StateKeyReq is the state.get / state.delete request.
type StateKeyReq struct {
	Key string `json:"key"`
}

// StateSetReq is the state.set request; Value marshals as base64 text
// (encoding/json []byte semantics), which the host decodes back to bytes.
type StateSetReq struct {
	Key   string `json:"key"`
	Value []byte `json:"value"`
}

// StateGetResp is the state.get response.
type StateGetResp struct {
	Value []byte `json:"Value"`
	Found bool   `json:"Found,omitempty"`
}

// StateDeleteResp is the state.delete response.
type StateDeleteResp struct {
	Removed bool `json:"Removed"`
}

// AppEmitReq is the app.emit request: publish one app-declared event. The
// pluginhost injects the calling plugin's identity; the appmanager validates
// the Event against the app manifest's declared Events before broadcasting.
type AppEmitReq struct {
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// AppEmitResp is the app.emit response.
type AppEmitResp struct {
	Accepted bool `json:"Accepted"`
}

// RegistryQueryReq is the registry.query request (capability registry.read).
// A plugin declares the callID and invokes it to discover callable metadata
// across the running actor tree. JSON tags mirror the pluginhost-side
// handler exactly — the lowercase wire keys are the SDK-facing convention;
// the host handler decodes the same keys.
type RegistryQueryReq struct {
	Service  string `json:"service,omitempty"`
	Callable string `json:"callable,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

// RegistryQueryResp is the registry.query response.
type RegistryQueryResp struct {
	Items      []RegistryCallableMeta `json:"Items"`
	NextCursor string                `json:"NextCursor,omitempty"`
}

// RegistryCallableMeta describes one callable's public metadata. It
// intentionally carries no credential values — query ≠ invocation.
// JSON tags mirror the pluginhost-side handler exactly.
type RegistryCallableMeta struct {
	CallID       string                 `json:"CallID"`
	Service      string                 `json:"Service,omitempty"`
	Kind         string                 `json:"Kind,omitempty"`
	Params       []domain.CallableParam `json:"Params,omitempty"`
	FinalType    string                 `json:"FinalType,omitempty"`
	FinalFields  []domain.CallableParam `json:"FinalFields,omitempty"`
	Permission   string                 `json:"Permission,omitempty"`
	EffectKind   string                 `json:"EffectKind,omitempty"`
	Description  string                 `json:"Description,omitempty"`
	ReqSchemaId  int32                  `json:"ReqSchemaId,omitempty"`
	RespSchemaId int32                  `json:"RespSchemaId,omitempty"`
}
