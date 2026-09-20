package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// ToolSpecsFromCallables builds ToolSpec values from a callables map,
// filtered to only the allowed callable IDs. The LLM-facing Name is the
// callable ID with dots folded to hyphens (e.g. "project.read" →
// "project-read"): providers restrict tool names to [a-zA-Z0-9_-], so
// the dotted callable ID cannot be sent verbatim. The dotted form still
// appears verbatim in prompt text and is accepted as a routing alias.
func ToolSpecsFromCallables(callables map[string]domain.CallableInterface, allowed []string) []domain.ToolSpec {
	out := make([]domain.ToolSpec, 0, len(allowed))
	for _, callableID := range allowed {
		ci, ok := callables[callableID]
		if !ok {
			continue
		}
		desc := ci.Description
		if desc == "" {
			desc = callableID
		}

		// ToolName: declared value; dot-fold is the mechanical default when
		// empty (LLM providers restrict tool names to [a-zA-Z0-9_-], so the
		// dotted callable ID cannot be sent verbatim).
		llmName := ci.ToolName
		if llmName == "" {
			llmName = strings.ReplaceAll(callableID, ".", "-")
		}

		// EffectKind: declared value only. Empty means EffectNone (read-
		// only) — the conservative default that the old suffix heuristic's
		// fallback also produced for unknown suffixes. All production
		// callables declare their effect at registration; this default only
		// applies to test stubs and unregistered metadata shells.
		effectKind := ci.EffectKind
		if effectKind == "" {
			effectKind = string(domain.EffectNone)
		}

		// ServiceName: value recorded by the cell at registration —
		// derived from the callID's first segment when it matches a
		// declared domain (actor.ServiceNameFor). Empty means the routing
		// layer falls back to ctx.Self() (agent-local callable).
		serviceName := ci.ServiceName

		out = append(out, domain.ToolSpec{
			Name:        llmName,
			Description: desc,
			InputSchema: hostToolInputSchema(ci),
			EffectKind:  effectKind,
			CallableID:  callableID,
			ServiceName: serviceName,
			Stream:      ci.Stream,
		})
	}
	return out
}

// maxDeepToolSchemaBytes bounds the deep field-level input schema a tool may
// carry: oversized projections fall back to the shallow form so tool surfaces
// cannot balloon prompts.
const maxDeepToolSchemaBytes = 16 * 1024

// hostToolInputSchema resolves the callable's field-level request layout and
// projects it to a deep JSON Schema; unresolved or oversized layouts fall back
// to the shallow interface-derived schema.
func hostToolInputSchema(ci domain.CallableInterface) string {
	layout, err := protocol.ResolveRequestLayout(ci)
	if err != nil {
		return CallableToJSONSchema(ci)
	}
	schema := layout.JSONSchema()
	if schema == "" || len(schema) > maxDeepToolSchemaBytes {
		return CallableToJSONSchema(ci)
	}
	return schema
}

func toolBareName(callableID string) string {
	if idx := strings.LastIndex(callableID, "."); idx >= 0 {
		return callableID[idx+1:]
	}
	return callableID
}

// bareNameCounts tallies how many tools share each bare (last-segment) name.
// A bare name is only usable as a routing alias when exactly one tool has it.
func bareNameCounts(tools []domain.ToolSpec) map[string]int {
	counts := make(map[string]int, len(tools))
	for _, t := range tools {
		counts[strings.ToLower(toolBareName(t.CallableID))]++
	}
	return counts
}

// toolAliasKeys returns the lowercase lookup keys under which a tool must be
// reachable from an LLM-emitted tool name. Because prompt prose uses the
// dotted form while the provider-facing name uses hyphens, all of these
// spellings must resolve to the same tool:
//   - the declared Name (hyphen form, e.g. "project-read")
//   - the dotted callable ID (prompt spelling, e.g. "project.read")
//   - the underscore form (legacy, e.g. "project_read")
//   - the bare last segment, only when unique across the whole tool set
func toolAliasKeys(t domain.ToolSpec, bareCounts map[string]int) []string {
	keys := []string{
		strings.ToLower(t.Name),
		strings.ToLower(t.CallableID),
		strings.ToLower(strings.ReplaceAll(t.CallableID, ".", "_")),
	}
	bare := strings.ToLower(toolBareName(t.CallableID))
	if bareCounts[bare] == 1 {
		keys = append(keys, bare)
	}
	return keys
}

// mcpToolSpecsFromCatalog converts the mcpmanager's connected-server tool
// catalog into LLM-facing ToolSpecs. The LLM-facing Name is hyphen-delimited
// ("mcp-<server>-<tool>") since dots are not allowed in provider tool names;
// the CallableID stays dot-delimited ("mcp.<serverID>.<tool>") so the turn
// engine can route the invocation back to mcpmanager.call_tool. The server
// NAME is user-editable, so each segment is sanitized to [a-zA-Z0-9_-] and a
// sanitization collision falls back to the stable server ID.
func mcpToolSpecsFromCatalog(resp domain.McpDiscoverToolsResp) []domain.ToolSpec {
	var out []domain.ToolSpec
	used := make(map[string]bool, len(resp.Servers)*2)
	for _, srv := range resp.Servers {
		for _, t := range srv.Tools {
			name := "mcp-" + sanitizeMCPSegment(srv.Name) + "-" + sanitizeMCPSegment(t.Name)
			if used[name] {
				name = "mcp-" + srv.ID + "-" + sanitizeMCPSegment(t.Name)
			}
			used[name] = true
			out = append(out, domain.ToolSpec{
				Name:        name,
				Description: t.Description,
				InputSchema: t.InputSchema,
				EffectKind:  string(domain.EffectNone),
				CallableID:  "mcp." + srv.ID + "." + t.Name,
				ServiceName: "mcp",
			})
		}
	}
	return out
}

// appToolSpecsFromCatalog converts the appmanager's registered app callable
// catalog into LLM-facing ToolSpecs. The LLM-facing Name is hyphen-delimited
// ("app-<appID>-<callable>") since dots are not allowed in provider tool names;
// the CallableID stays dot-delimited ("app.<appID>.<callable>") so the turn
// engine can route the invocation back to appmanager.invoke. The app ID is
// sanitized to [a-zA-Z0-9_-] for the LLM-facing name.
//
// allowedByApp gates the exposed surface per running app: only callables whose
// routing CallableID is in that app's set are emitted. An app missing from the
// map, an empty set, and a nil map all mean the app contributes no tools — app
// tools are visible only through the app-bundle cards the agent has mounted.
func appToolSpecsFromCatalog(resp gen.AppManagerListResp, allowedByApp map[string]map[string]bool) []domain.ToolSpec {
	var out []domain.ToolSpec
	used := make(map[string]bool, len(resp.Items)*2)
	for _, app := range resp.Items {
		// Only expose callables from running apps/services.
		if app.State != "running" {
			continue
		}
		allowed := allowedByApp[app.ID]
		for _, c := range app.Callables {
			// expose: "frontend" callables are panel-facing only — the agent
			// tool registry must not project them (the appdef expose field;
			// empty/both/agent all remain agent-reachable). The symmetric
			// frontend surface (client.gen.ts / server.gen.go) skips
			// expose: "agent" callables at codegen time.
			if c.Expose == "frontend" {
				continue
			}
			callableID := "app." + app.ID + "." + c.ID
			if app.Runtime == "service" {
				// Service callables route directly to the owning service, not
				// through appmanager.invoke.
				callableID = app.ID + "." + c.ID
			}
			if !allowed[callableID] {
				continue
			}
			effectKind := c.Effect
			if effectKind == "" {
				effectKind = string(domain.EffectNone)
			}

			// Build input schema from the callable's request schema. The
			// default must be a valid object schema because OpenAI rejects
			// schemas without "type": "object" (treats missing type as null).
			inputSchema := appCallableInputSchema(app, c)

			var spec domain.ToolSpec
			if app.Runtime == "service" {
				llmName := "service-" + sanitizeMCPSegment(app.ID) + "-" + sanitizeMCPSegment(c.ID)
				for used[llmName] {
					llmName = llmName + "_"
				}
				used[llmName] = true
				spec = domain.ToolSpec{
					Name:        llmName,
					Description: fmt.Sprintf("Invoke service callable %s on %s", c.ID, app.ID),
					InputSchema: inputSchema,
					EffectKind:  effectKind,
					CallableID:  callableID,
					ServiceName: app.ID,
				}
				if c.Service != "" {
					spec.ServiceName = c.Service
				}
			} else {
				llmName := "app-" + sanitizeMCPSegment(app.ID) + "-" + sanitizeMCPSegment(c.ID)
				if used[llmName] {
					llmName = "app-" + app.ID + "-" + sanitizeMCPSegment(c.ID)
				}
				used[llmName] = true
				spec = domain.ToolSpec{
					Name:        llmName,
					Description: fmt.Sprintf("Invoke callable %s on app %s", c.ID, app.ID),
					InputSchema: inputSchema,
					EffectKind:  effectKind,
					CallableID:  callableID,
					ServiceName: "appmanager",
				}
			}
			if c.ToolName != "" {
				spec.Name = c.ToolName
			}
			if c.Streaming {
				spec.Stream = true
			}
			out = append(out, spec)
		}
	}
	return out
}

const emptyObjectInputSchema = `{"type":"object","properties":{}}`

// appCallableInputSchema resolves a callable's RequestSchema into a JSON
// Schema string for the LLM tool spec. The field carries either an inline
// JSON Schema (hand-registered apps) or a struct name that resolves against
// the app's registered schema descriptors (.appdef apps), projected deep via
// the protocol layout. Anything unresolvable falls back to an empty object
// schema.
func appCallableInputSchema(app gen.AppStatus, c gen.AppCallableDescriptor) string {
	trimmed := strings.TrimSpace(c.RequestSchema)
	if trimmed == "" {
		return emptyObjectInputSchema
	}
	if strings.HasPrefix(trimmed, "{") {
		return trimmed
	}
	layout, err := protocol.LayoutFromAppObjects(trimmed, app.SchemaDescriptors)
	if err != nil {
		return emptyObjectInputSchema
	}
	schema := layout.JSONSchema()
	if schema == "" || len(schema) > maxDeepToolSchemaBytes {
		return emptyObjectInputSchema
	}
	return schema
}

// sanitizeMCPSegment keeps LLM-facing tool name segments provider-safe:
// providers restrict tool names to [a-zA-Z0-9_-], so spaces and punctuation
// are folded to underscores.
func sanitizeMCPSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "server"
	}
	return b.String()
}

// jsonSchemaType maps callable param types to JSON Schema types. Unknown
// types (custom struct names like PlanPolicy, PlanTaskRef) default to
// "object" since the LLM API rejects type names that are not part of the
// JSON Schema vocabulary.
func jsonSchemaType(t string) string {
	switch t {
	case "string":
		return "string"
	case "int", "long":
		return "integer"
	case "bool":
		return "boolean"
	case "double":
		return "number"
	case "any":
		return "object"
	default:
		if strings.HasSuffix(t, "[]") {
			return "array"
		}
		return "object"
	}
}

// CallableToJSONSchema generates a JSON Schema (object form) from enriched
// callable metadata. The output is a compact JSON string suitable for LLM
// tool input_schema fields.
func CallableToJSONSchema(ci domain.CallableInterface) string {
	props := make(map[string]any)
	var required []string

	for _, p := range ci.Params {
		prop := map[string]any{"type": jsonSchemaType(p.Type)}
		if p.Description != "" {
			prop["description"] = p.Description
		}
		if strings.HasSuffix(p.Type, "[]") {
			elemType := strings.TrimSuffix(p.Type, "[]")
			prop["items"] = map[string]any{"type": jsonSchemaType(elemType)}
		}
		props[p.Name] = prop
		if p.Required {
			required = append(required, p.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	b, err := json.Marshal(schema)
	if err != nil {
		return fmt.Sprintf(`{"type":"object","properties":{},"error":"schema marshal: %s"}`, err)
	}
	return string(b)
}
