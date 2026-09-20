package appdef

import "regexp"

// --- validation regexes ---

// maxPluginAgentSystemPromptBytes caps the per-block plugin_agent
// system_prompt so a manifest cannot smuggle an unbounded prompt into the
// host. Each declared block gets its own budget.
const maxPluginAgentSystemPromptBytes = 8192

// pluginAgentSlotRe defines the allowed pattern for plugin_agent binding
// slot names (the optional name header; "default" when omitted). Mirrors the
// PluginAgentBinding.Name schema comment: [a-z][a-z0-9-]*.
var pluginAgentSlotRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// toolNameRe defines the allowed pattern for callable toolName values.
var toolNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// callableIDRe defines the allowed pattern for callable ids: snake_case Go
// identifiers only, because CallableIDToHandlerName turns the id into a Go
// function name (dots/hyphens produce uncompilable handlers).
var callableIDRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// typeAliasRe matches type alias declarations in raw source.
var typeAliasRe = regexp.MustCompile(`^type\s+(\w+)\s*=\s*(.+)$`)

// --- closed vocabularies ---

// validEffects is the closed vocabulary for callable effect declarations.
// none/reversible/irreversible mirror the host EffectKind classification
// (domain EffectNone/EffectReversible/EffectIrreversible); read/write/mutate
// are legacy appdef spellings kept for backward compatibility. Anything else
// fails validation rather than silently degrading to an unknown EffectKind in
// the tool audit pipeline.
var validEffects = map[string]bool{
	"none": true, "reversible": true, "irreversible": true,
	"read": true, "write": true, "mutate": true,
}

// validEntrypointKinds is the closed vocabulary for entrypoint Kind values.
// view/panel are mountable surfaces (session creation accepts only these);
// command is a non-view action entrypoint. Other spellings (e.g. page,
// settings) have no host consumer and would fail routing at runtime.
var validEntrypointKinds = map[string]bool{
	"view": true, "panel": true, "command": true,
}

// validExposeTargets is the closed vocabulary for callable expose values.
// A callable is exposed to the app's frontend panel, to the LLM agent as a
// tool, or to both. An empty value means the default "both" and is consumed
// as-is (the manifest omits it rather than normalizing); anything outside
// this set fails validation rather than silently degrading at the routing
// boundary.
var validExposeTargets = map[string]bool{
	"frontend": true, "agent": true, "both": true,
}

// --- scalar allowlist ---

var scalars = map[string]bool{
	"string": true, "bool": true, "int": true, "int32": true,
	"int64": true, "long": true, "float": true, "double": true,
	"any": true, "bytes": true, "void": true,
}

// IsKnownScalar reports whether name is a built-in scalar. Exported for
// codegen, which must hard-fail on anything outside this set instead of
// silently degrading the generated Go field to interface{} (a field typed
// `float64` once compiled as interface{} and the drift surfaced only after
// the handler was written).
func IsKnownScalar(name string) bool {
	return scalars[name]
}
