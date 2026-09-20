package appdef

import (
	"fmt"
	"strings"

	schema "github.com/qomos-w/spore/schema"
)

// isValidTypeRef checks whether a type reference (possibly composite like
// array<T> or map<K,V>) only references declared structs, type aliases, or
// built-in scalars.
func isValidTypeRef(typeStr string, structNames map[string]bool, aliases map[string]bool) bool {
	for _, ref := range ExtractTypeReferences(typeStr) {
		if !structNames[ref] && !aliases[ref] && !scalars[ref] {
			return false
		}
	}
	return true
}

// extractTypeTokens splits a type string (possibly composite) into its
// constituent word tokens, including lowercase scalars. Unlike
// ExtractTypeReferences, which only yields capitalized names, this returns
// all word tokens so unknown lowercase scalars (e.g. float64) are caught.
func extractTypeTokens(typeStr string) []string {
	cleaned := typeStr
	cleaned = strings.ReplaceAll(cleaned, "array<", "")
	cleaned = strings.ReplaceAll(cleaned, "map<", "")
	cleaned = strings.ReplaceAll(cleaned, ">", "")
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.ReplaceAll(cleaned, "optional", "")
	var tokens []string
	for _, w := range strings.Fields(cleaned) {
		w = strings.TrimSpace(w)
		if w != "" {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

// validateFieldTypes recursively checks a TypeDesc: scalars against the
// allowlist, struct references against declared structs/aliases, and
// element/key/value of composite types. Unknown scalars produce a diagnostic
// naming the field path so the failure points at the appdef cause.
func validateFieldTypes(t schema.TypeDesc, path string, structNames, aliases map[string]bool, diags *[]Diagnostic) {
	switch t.Kind {
	case schema.TypeKindScalar:
		if t.Name != "" && t.Name != "void" && !scalars[t.Name] {
			*diags = append(*diags, Diagnostic{
				Line:    0,
				Message: fmt.Sprintf("field %s: unknown scalar %q — supported scalars are string, bool, int, int32, int64, long, float (Go float32), double (Go float64), bytes, any, void; use double for 64-bit floats", path, t.Name),
			})
		}
	case schema.TypeKindStruct:
		if t.Name != "" && !structNames[t.Name] && !aliases[t.Name] {
			*diags = append(*diags, Diagnostic{
				Line:    0,
				Message: fmt.Sprintf("field %s: unknown struct or alias %q — declare it or fix the reference", path, t.Name),
			})
		}
	case schema.TypeKindArray:
		if t.Element != nil {
			validateFieldTypes(*t.Element, path+"[]", structNames, aliases, diags)
		}
	case schema.TypeKindMap:
		if t.Key != nil {
			validateFieldTypes(*t.Key, path+"[key]", structNames, aliases, diags)
		}
		if t.Value != nil {
			validateFieldTypes(*t.Value, path+"[val]", structNames, aliases, diags)
		}
	}
}

// Validate checks an AppDef AST for semantic errors:
// - Dangling struct references in callable request/response and event payload
// - Dangling callable references in bundle tools
// - Duplicate IDs across callables, events, entrypoints, and bundles
// - Invalid toolName patterns on callables
// Returns all diagnostics found.
func Validate(app *AppDef) []Diagnostic {
	var diags []Diagnostic

	// App-level meta fields are required by the downstream manifest contract
	// (native_build refuses an empty id). The block label (app Foo) is a
	// display name only — it is NOT adopted as the id, so a missing id:
	// field must fail here, at dev_generate, instead of surfacing later as
	// an opaque build-gate failure.
	if app.AppMeta.ID == "" {
		diags = append(diags, Diagnostic{
			Line:    1,
			Message: "app block: missing id — add `id: \"app.yourapp\"` as the first field; the block label (app Foo) is a display name only and is NOT adopted as the id",
		})
	}
	if app.AppMeta.Name == "" {
		diags = append(diags, Diagnostic{Line: 1, Message: "app block: missing name field"})
	}
	if app.AppMeta.Version == "" {
		diags = append(diags, Diagnostic{Line: 1, Message: "app block: missing version field (e.g. version: \"0.1.0\")"})
	}
	if app.AppMeta.Namespace == "" {
		diags = append(diags, Diagnostic{Line: 1, Message: "app block: missing namespace field (short lowercase slug, e.g. namespace: \"totp\")"})
	}

	// Collect declared struct and type alias names.
	structNames := make(map[string]bool, len(app.Structs)+len(app.TypeAliases))
	for _, s := range app.Structs {
		structNames[s.Name] = true
	}
	aliasNames := make(map[string]bool, len(app.TypeAliases))
	for _, a := range app.TypeAliases {
		aliasNames[a.Name] = true
	}

	// Struct field types and alias targets must resolve to a known scalar or
	// a declared struct/alias. Previously only callable/event top-level refs
	// were checked: a field typed `float64` passed validation and codegen
	// silently degraded it to interface{} — the drift surfaced only after the
	// handler was written against the wrong generated type.
	for _, s := range app.Structs {
		for _, f := range s.Fields {
			validateFieldTypes(f.Type, fmt.Sprintf("%s.%s", s.Name, f.Name), structNames, aliasNames, &diags)
		}
	}
	for _, a := range app.TypeAliases {
		for _, tok := range extractTypeTokens(a.Target) {
			if !structNames[tok] && !aliasNames[tok] && !scalars[tok] {
				diags = append(diags, Diagnostic{
					Line:    a.Line,
					Message: fmt.Sprintf("type alias %q target %q is not a known scalar, declared struct, or alias — supported scalars: string, bool, int, int32, int64, long, float, double, bytes, any, void", a.Name, tok),
				})
			}
		}
	}

	// Collect declared callable IDs.
	callableIDs := make(map[string]int, len(app.Callables)) // id -> declaration line
	for _, c := range app.Callables {
		if prev, ok := callableIDs[c.ID]; ok {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("duplicate callable id %q (first declared at line %d)", c.ID, prev),
			})
		} else {
			callableIDs[c.ID] = c.Line
		}
	}

	// Collect declared event IDs.
	eventIDs := make(map[string]int, len(app.Events))
	for _, e := range app.Events {
		if prev, ok := eventIDs[e.ID]; ok {
			diags = append(diags, Diagnostic{
				Line:    e.Line,
				Message: fmt.Sprintf("duplicate event id %q (first declared at line %d)", e.ID, prev),
			})
		} else {
			eventIDs[e.ID] = e.Line
		}
	}

	// Collect declared entrypoint IDs.
	entrypointIDs := make(map[string]int, len(app.Entrypoints))
	for _, ep := range app.Entrypoints {
		if prev, ok := entrypointIDs[ep.ID]; ok {
			diags = append(diags, Diagnostic{
				Line:    ep.Line,
				Message: fmt.Sprintf("duplicate entrypoint id %q (first declared at line %d)", ep.ID, prev),
			})
		} else {
			entrypointIDs[ep.ID] = ep.Line
		}
	}

	// Validate callables.
	for _, c := range app.Callables {
		if c.ID == "" {
			diags = append(diags, Diagnostic{Line: c.Line, Message: "callable: missing id"})
			continue
		}

		// Check toolName pattern.
		if c.ToolName != "" && !toolNameRe.MatchString(c.ToolName) {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: invalid toolName %q (must match [a-zA-Z0-9_-]+)", c.ID, c.ToolName),
			})
		}

		// Check effect vocabulary.
		if c.Effect != "" && !validEffects[c.Effect] {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: invalid effect %q (allowed: none, reversible, irreversible, read, write, mutate)", c.ID, c.Effect),
			})
		}

		// Check timeout range if declared.
		if c.TimeoutMs != 0 {
			if c.TimeoutMs < 30000 {
				diags = append(diags, Diagnostic{
					Line:    c.Line,
					Message: fmt.Sprintf("callable %q: timeout %dms is below minimum 30s", c.ID, c.TimeoutMs),
				})
			} else if c.TimeoutMs > 900000 {
				diags = append(diags, Diagnostic{
					Line:    c.Line,
					Message: fmt.Sprintf("callable %q: timeout %dms exceeds maximum 15min", c.ID, c.TimeoutMs),
				})
			}
		}

		// Check request struct reference.
		if c.Request != "" && c.Request != "void" && !isValidTypeRef(c.Request, structNames, aliasNames) {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: request references undefined struct %q", c.ID, c.Request),
			})
		}

		// The pluginhost pushes host events into the plugin under the
		// reserved dispatch name `__event__:<kind>`; callable IDs must not
		// squat that namespace (the colon itself is already impossible via
		// callableIDRe — this guard documents intent and future-proofs it).
		if strings.HasPrefix(c.ID, "__event__") {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: id prefix __event__ is reserved for host event delivery", c.ID),
			})
		}

		// Callable ids become Go handler names (handle<ID>) via
		// CallableIDToHandlerName, which only understands underscores.
		// Dots and hyphens pass parsing and generate uncompilable code that
		// only fails at the build gate with a misleading compile error.
		if !callableIDRe.MatchString(c.ID) {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: id must be a snake_case identifier matching [A-Za-z_][A-Za-z0-9_]* — dots/hyphens generate uncompilable Go handler names", c.ID),
			})
		}

		// Check response struct reference.
		if c.Response != "" && c.Response != "void" && !isValidTypeRef(c.Response, structNames, aliasNames) {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: response references undefined struct %q", c.ID, c.Response),
			})
		}

		// Streaming callables emit Response-typed chunks (SDK HandlerStream:
		// each chunk is a partial of the terminal), so a streaming declaration
		// without a response type has nothing to type the chunks with.
		if c.Streaming && (c.Response == "" || c.Response == "void") {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: streaming requires a response type (chunks are Response-typed)", c.ID),
			})
		}

		// Expose must be one of the closed vocabulary targets. An empty
		// value is the default (both) and needs no check.
		if c.Expose != "" && !validExposeTargets[c.Expose] {
			diags = append(diags, Diagnostic{
				Line:    c.Line,
				Message: fmt.Sprintf("callable %q: invalid expose %q (allowed: frontend, agent, both)", c.ID, c.Expose),
			})
		}

		// Watch entries must reference events declared in the same .appdef —
		// they name events whose emission invalidates this callable's cached
		// results, so an unknown event id is a dangling reference.
		for _, ev := range c.Watch {
			if _, ok := eventIDs[ev]; !ok {
				diags = append(diags, Diagnostic{
					Line:    c.Line,
					Message: fmt.Sprintf("callable %q: watch references undefined event %q", c.ID, ev),
				})
			}
		}
	}

	// Validate entrypoints.
	for _, ep := range app.Entrypoints {
		if ep.ID == "" {
			diags = append(diags, Diagnostic{Line: ep.Line, Message: "entrypoint: missing id"})
			continue
		}
		if ep.Kind == "" {
			diags = append(diags, Diagnostic{Line: ep.Line, Message: fmt.Sprintf("entrypoint %q: missing kind", ep.ID)})
			continue
		}
		if !validEntrypointKinds[ep.Kind] {
			diags = append(diags, Diagnostic{
				Line:    ep.Line,
				Message: fmt.Sprintf("entrypoint %q: invalid kind %q (allowed: view, panel, command)", ep.ID, ep.Kind),
			})
		}
	}

	// Validate events.
	for _, e := range app.Events {
		if e.ID == "" {
			diags = append(diags, Diagnostic{Line: e.Line, Message: "event: missing id"})
			continue
		}
		if e.Payload != "" && e.Payload != "void" && !isValidTypeRef(e.Payload, structNames, aliasNames) {
			diags = append(diags, Diagnostic{
				Line:    e.Line,
				Message: fmt.Sprintf("event %q: payload references undefined struct %q", e.ID, e.Payload),
			})
		}
	}

	// Collect declared listen kinds (inbound host event subscriptions).
	// Kind membership in the appbinding EventCatalog is enforced by codegen
	// (dev_generate) to keep the catalog single-sourced; here we validate
	// shape and duplicates only.
	listenKinds := make(map[string]int, len(app.Listens))
	for _, l := range app.Listens {
		if l.Kind == "" {
			diags = append(diags, Diagnostic{Line: l.Line, Message: "listen: missing event kind"})
			continue
		}
		if prev, ok := listenKinds[l.Kind]; ok {
			diags = append(diags, Diagnostic{
				Line:    l.Line,
				Message: fmt.Sprintf("duplicate listen kind %q (first declared at line %d)", l.Kind, prev),
			})
		} else {
			listenKinds[l.Kind] = l.Line
		}
	}

	// Validate bundles.
	for _, b := range app.Bundles {
		for _, tool := range b.Tools {
			if _, ok := callableIDs[tool]; !ok {
				diags = append(diags, Diagnostic{
					Line:    b.Line,
					Message: fmt.Sprintf("bundle: tool %q references undefined callable", tool),
				})
			}
		}
	}

	// Validate free_agent (standalone: no agent_binding required).
	// No structural constraints on the free_agent block itself beyond parsing.

	// Validate plugin_agent blocks: slot name pattern + uniqueness, per-block
	// system_prompt size cap, and non-empty bundle IDs. Bundle card ID
	// existence is checked at mount time (appdef has no builtin-card
	// catalog), but empty/duplicate entries are authoring mistakes caught
	// here. Each block is validated independently — limits are per slot.
	seenSlots := make(map[string]int, len(app.PluginAgents))
	for _, pa := range app.PluginAgents {
		if !pluginAgentSlotRe.MatchString(pa.Name) {
			diags = append(diags, Diagnostic{
				Line:    pa.Line,
				Message: fmt.Sprintf("plugin_agent: invalid slot name %q (want [a-z][a-z0-9-]*)", pa.Name),
			})
		} else if prev, ok := seenSlots[pa.Name]; ok {
			diags = append(diags, Diagnostic{
				Line:    pa.Line,
				Message: fmt.Sprintf("plugin_agent: duplicate slot %q (first declared at line %d)", pa.Name, prev),
			})
		} else {
			seenSlots[pa.Name] = pa.Line
		}
		if len(pa.SystemPrompt) > maxPluginAgentSystemPromptBytes {
			diags = append(diags, Diagnostic{
				Line:    pa.Line,
				Message: fmt.Sprintf("plugin_agent %q: system_prompt exceeds %d bytes (got %d)", pa.Name, maxPluginAgentSystemPromptBytes, len(pa.SystemPrompt)),
			})
		}
		seenBundles := make(map[string]bool, len(pa.Bundles))
		for _, b := range pa.Bundles {
			if b == "" {
				diags = append(diags, Diagnostic{
					Line:    pa.Line,
					Message: fmt.Sprintf("plugin_agent %q: bundles contains an empty entry", pa.Name),
				})
				continue
			}
			if seenBundles[b] {
				diags = append(diags, Diagnostic{
					Line:    pa.Line,
					Message: fmt.Sprintf("plugin_agent %q: duplicate bundle %q", pa.Name, b),
				})
			}
			seenBundles[b] = true
		}
		// Model unit binding: optional "Provider|Model" string. Empty keeps the
		// host default; non-empty must split into exactly two non-empty parts
		// with no embedded extra "|" (avoids ambiguity with model names).
		if pa.Model != "" {
			provider, model, hasPipe := strings.Cut(pa.Model, "|")
			if !hasPipe || provider == "" || model == "" || strings.Contains(model, "|") {
				diags = append(diags, Diagnostic{
					Line:    pa.Line,
					Message: fmt.Sprintf("plugin_agent %q: model %q is not a valid \"Provider|Model\" unit", pa.Name, pa.Model),
				})
			}
		}
	}

	// Check for duplicate bundle titles (not strictly required, but useful).
	bundleTitles := make(map[string]int)
	for _, b := range app.Bundles {
		if b.Title != "" {
			if prev, ok := bundleTitles[b.Title]; ok {
				diags = append(diags, Diagnostic{
					Line:    b.Line,
					Message: fmt.Sprintf("duplicate bundle title %q (first declared at line %d)", b.Title, prev),
				})
			} else {
				bundleTitles[b.Title] = b.Line
			}
		}
	}

	// Validate response=void convention: if response is empty string, it's valid (void).
	// This is handled by the caller not setting Response; no diagnostic needed.

	return diags
}

// ValidateToolName checks if a toolName matches the allowed pattern.
func ValidateToolName(name string) bool {
	return toolNameRe.MatchString(name)
}

// StructTypeNames returns the set of declared struct type names from the AppDef.
func StructTypeNames(app *AppDef) map[string]bool {
	names := make(map[string]bool, len(app.Structs))
	for _, s := range app.Structs {
		names[s.Name] = true
	}
	return names
}

// CallableIDSet returns the set of declared callable IDs from the AppDef.
func CallableIDSet(app *AppDef) map[string]bool {
	ids := make(map[string]bool, len(app.Callables))
	for _, c := range app.Callables {
		ids[c.ID] = true
	}
	return ids
}

// ExtractTypeReferences extracts all referenced type names from a TypeDesc tree.
// This is useful for validating cross-file type references.
func ExtractTypeReferences(typeStr string) []string {
	// Simple extraction: find capitalized words that look like type names.
	var refs []string
	seen := make(map[string]bool)

	// Handle array<T>, map<K,V>, optional T.
	cleaned := typeStr
	cleaned = strings.ReplaceAll(cleaned, "array<", "")
	cleaned = strings.ReplaceAll(cleaned, "map<", "")
	cleaned = strings.ReplaceAll(cleaned, ">", "")
	cleaned = strings.ReplaceAll(cleaned, ",", "")
	cleaned = strings.ReplaceAll(cleaned, "optional ", "")

	for _, word := range strings.Fields(cleaned) {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		// Type names start with uppercase.
		if word[0] >= 'A' && word[0] <= 'Z' {
			if !seen[word] {
				seen[word] = true
				refs = append(refs, word)
			}
		}
	}
	return refs
}
