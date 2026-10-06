package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/script"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/sporebridge"
)

// savedScript is one accumulated spore snippet. The agent curates these
// itself: save upserts by Name (edit is read → modify → save), delete
// removes. Each saved script is projected as its own LLM tool
// (script-<name>, typed schema from the run() signature) and listed as an
// index line in every turn's hot context.
type savedScript struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Script      string `json:"script"`
}

// savedScriptsDoc is the single JSON document persisted under the agent's
// own namespace (agent/<actorID>/scripts).
type savedScriptsDoc struct {
	Scripts map[string]savedScript `json:"scripts"`
}

const maxSavedScriptBytes = 16 * 1024
const maxSavedScripts = 64

// savedScriptsMu guards savedScripts: script_save/script_delete run on the
// owner lane, while buildSavedScriptsBlock reads from resolveHotContext
// (which pure-context callers assemble concurrently).
var savedScriptsMu sync.Mutex

// scriptsStore returns the agent persist backend handle for the scripts doc.
func (a *Actor) scriptsStore() persist.Persist {
	return persist.MustNew(config.PersistConfig("agent"))
}

func (a *Actor) scriptsDocName() string {
	return a.agentStoreKey() + "/scripts"
}

// loadSavedScripts restores the accumulated script set from the agent's
// persist namespace. A missing doc (first run) is not an error.
func (a *Actor) loadSavedScripts() error {
	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts != nil {
		return nil
	}
	a.savedScripts = map[string]savedScript{}
	var doc savedScriptsDoc
	if err := a.scriptsStore().Load(a.scriptsDocName(), &doc); err != nil {
		if err == persist.ErrNotExist {
			return nil
		}
		return fmt.Errorf("load saved scripts: %w", err)
	}
	for name, s := range doc.Scripts {
		a.savedScripts[name] = s
	}
	return nil
}

// persistSavedScripts writes the whole set back as one document. Called with
// savedScriptsMu held.
func (a *Actor) persistSavedScripts() error {
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	return a.scriptsStore().Save(a.scriptsDocName(), savedScriptsDoc{Scripts: a.savedScripts})
}

// handleScriptSave backs the agent-local script_save: upsert one spore
// snippet by Name. Stateful (owner lane): low-frequency mutate that writes
// actor state and persists — exactly the category the owner lane is for.
func (a *Actor) handleScriptSave(_ actor.Context, req domain.AgentScriptSaveReq) (domain.AgentScriptSaveResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Name is required")
	}
	if strings.TrimSpace(req.Script) == "" {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Script is required")
	}
	if len(req.Script) > maxSavedScriptBytes {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Script exceeds %d bytes", maxSavedScriptBytes)
	}

	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	updated := false
	if _, exists := a.savedScripts[name]; exists {
		updated = true
	} else if len(a.savedScripts) >= maxSavedScripts {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: script quota full (%d); delete one first", maxSavedScripts)
	}
	a.savedScripts[name] = savedScript{Name: name, Description: strings.TrimSpace(req.Description), Script: req.Script}
	if err := a.persistSavedScripts(); err != nil {
		// Roll back the in-memory copy so state and disk never diverge.
		if !updated {
			delete(a.savedScripts, name)
		}
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: %w", err)
	}
	// The projected script-<name> tool surface follows the saved set.
	a.pendingToolsRefresh.Store(true)
	return domain.AgentScriptSaveResp{Name: name, Updated: updated}, nil
}

// handleScriptDelete backs the agent-local script_delete: remove one saved
// snippet by Name. Unknown names are an explicit error. Stateful (owner
// lane).
func (a *Actor) handleScriptDelete(_ actor.Context, req domain.AgentScriptDeleteReq) (domain.AgentScriptDeleteResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: Name is required")
	}

	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	if _, exists := a.savedScripts[name]; !exists {
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: %q not found", name)
	}
	prev := a.savedScripts[name]
	delete(a.savedScripts, name)
	if err := a.persistSavedScripts(); err != nil {
		a.savedScripts[name] = prev
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: %w", err)
	}
	a.pendingToolsRefresh.Store(true)
	return domain.AgentScriptDeleteResp{Deleted: true}, nil
}

// handleScriptRead backs the agent-local script_read: fetch one saved
// snippet's full source by Name. With the hot-context block reduced to an
// index, read is the sole retrieval path for a body — the edit loop is
// read → modify → save (upsert overwrites). Unknown names are an explicit
// error. Stateless (PureContext).
func (a *Actor) handleScriptRead(_ actor.PureContext, req domain.AgentScriptReadReq) (domain.AgentScriptReadResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.AgentScriptReadResp{}, fmt.Errorf("script_read: Name is required")
	}
	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	s, ok := a.savedScripts[name]
	if !ok {
		return domain.AgentScriptReadResp{}, fmt.Errorf("script_read: %q not found", name)
	}
	return domain.AgentScriptReadResp{Name: s.Name, Description: s.Description, Script: s.Script}, nil
}

// scriptRunParams extracts the run() entry declaration via the official
// spore schema parser (script.ParseCallables) — no parallel hand-rolled
// parser to drift. ok is false when the source does not parse or declares
// no run(), which callers treat as the generic-schema fallback.
func scriptRunParams(src string) (desc *schema.CallableDesc, ok bool) {
	descs, err := script.ParseCallables(src)
	if err != nil {
		return nil, false
	}
	for i := range descs {
		if descs[i].Name == "run" {
			return &descs[i], true
		}
	}
	return nil, false
}

// scriptSignatureLine renders "run(n: int): int" for descriptions and the
// hot-context index. Uses the spore TypeDesc.String rendering.
func scriptSignatureLine(desc *schema.CallableDesc) string {
	parts := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		parts[i] = p.Name + ": " + p.Type.String()
	}
	sig := "run(" + strings.Join(parts, ", ") + ")"
	if len(desc.Returns) > 0 {
		sig += ": " + desc.Returns[0].String()
	}
	return sig
}

// coerceSporeArg narrows one JSON-normalized Go value to the Go kind the
// VM's typed-argument binding expects for the declared spore scalar: spore
// int binds Go int (long binds int64), so the JSON float64→int64 promotion
// that keeps `is int` true on `any` params must be re-narrowed for typed
// params. Non-scalar / any params pass through untouched.
func coerceSporeArg(t schema.TypeDesc, v any) any {
	if t.Kind != schema.TypeKindScalar {
		return v
	}
	n, isNum := v.(int64)
	if !isNum {
		return v
	}
	switch t.Name {
	case "int", "short", "ushort", "uint":
		return int(n)
	case "long", "ulong":
		return n
	case "float", "double":
		return float64(n)
	}
	return v
}

// sporeParamSchema maps one spore parameter type to a JSON-Schema property.
// `any` (and anything unmapped) yields no type constraint — the dynamic
// contract escape hatch.
func sporeParamSchema(t schema.TypeDesc) map[string]any {
	switch t.Kind {
	case schema.TypeKindScalar:
		switch t.Name {
		case "int":
			return map[string]any{"type": "integer"}
		case "float":
			return map[string]any{"type": "number"}
		case "string":
			return map[string]any{"type": "string"}
		case "bool":
			return map[string]any{"type": "boolean"}
		}
	case schema.TypeKindArray:
		return map[string]any{"type": "array"}
	case schema.TypeKindMap:
		return map[string]any{"type": "object"}
	}
	return map[string]any{}
}

// coerceSporeArg narrows one JSON-normalized Go value to the Go kind the

// savedScriptTools projects every saved script as one LLM-facing tool
// (script-<name>), mirroring the MCP dynamic-tool pattern: the tool list is
// synthesized per turn from live state, and the turn engine intercepts the
// synthetic "script.<name>" callable ID for execution (no cell registration
// — post-OnStart Register is not a supported path). The typed InputSchema is
// derived from the run() signature, so the contract sits exactly where the
// provider reads it while generating arguments; scripts whose signature
// cannot be parsed degrade to a generic Args object.
func (a *Actor) savedScriptTools() []domain.ToolSpec {
	savedScriptsMu.Lock()
	names := make([]string, 0, len(a.savedScripts))
	scripts := make(map[string]savedScript, len(a.savedScripts))
	for name, s := range a.savedScripts {
		names = append(names, name)
		scripts[name] = s
	}
	savedScriptsMu.Unlock()
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	used := make(map[string]bool, len(names))
	out := make([]domain.ToolSpec, 0, len(names))
	for _, name := range names {
		s := scripts[name]
		props := map[string]any{}
		var required []string
		var sig string
		sigDesc, ok := scriptRunParams(s.Script)
		if !ok {
			props["Args"] = map[string]any{"description": "script arguments (structure defined by the script; script_read shows the source)"}
			sig = "run(signature unavailable — script_read for the source)"
		} else {
			sig = scriptSignatureLine(sigDesc)
			for _, p := range sigDesc.Parameters {
				props[p.Name] = sporeParamSchema(p.Type)
				required = append(required, p.Name)
			}
		}
		schemaMap := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			schemaMap["required"] = required
		}
		schemaBytes, err := json.Marshal(schemaMap)
		if err != nil {
			continue
		}
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			desc = "saved spore script"
		}
		toolName := "script-" + sanitizeMCPSegment(s.Name)
		if used[toolName] {
			toolName = fmt.Sprintf("script-%s-%d", sanitizeMCPSegment(s.Name), len(out))
		}
		used[toolName] = true
		out = append(out, domain.ToolSpec{
			Name:        toolName,
			Description: desc + " — " + sig,
			InputSchema: string(schemaBytes),
			EffectKind:  string(domain.EffectNone),
			CallableID:  "script." + s.Name,
			ServiceName: "",
		})
	}
	return out
}

// executeSavedScriptCall backs the turn-engine interception of the synthetic
// "script.<name>" callable: it runs the stored body through the exact eval
// path (same budget, same host bridge carrying the caller role — reach, not
// permission), with the tool input's named fields bound positionally to the
// run() signature. Diagnostics return as tool errors, so a saved-but-broken
// script fails visibly with the read → edit → save loop as the fix path.
func (a *Actor) executeSavedScriptCall(ctx actor.PureContext, name string, input string) (string, bool) {
	savedScriptsMu.Lock()
	s, ok := a.savedScripts[name]
	savedScriptsMu.Unlock()
	if !ok {
		return fmt.Sprintf("script %q not found (deleted?)", name), true
	}

	var in map[string]any
	if trimmed := strings.TrimSpace(input); trimmed != "" && trimmed != "{}" {
		if err := json.Unmarshal([]byte(trimmed), &in); err != nil {
			return fmt.Sprintf("script %q: arguments must be a JSON object: %v", name, err), true
		}
	}
	args := make([]any, 0, 2)
	if sigDesc, ok := scriptRunParams(s.Script); ok {
		for _, p := range sigDesc.Parameters {
			args = append(args, coerceSporeArg(p.Type, normalizeSporeSkillJSON(in[p.Name])))
		}
	}

	bridge := sporebridge.NewHost(evalHostSeams{
		lookupService: ctx.LookupService,
		self:          ctx.Self(),
	}).WithRole(string(ctx.Identity().Role))
	value, err := evalSporeSource(ctx.Lifecycle(), s.Script, args, bridge.BindTo)
	if err != nil {
		return "saved script failed: " + err.Error(), true
	}
	normalized := normalizeSporeSkillJSON(value)
	encoded, merr := json.Marshal(normalized)
	if merr != nil {
		return "saved script result not serializable: " + merr.Error(), true
	}
	if len(encoded) > evalSporeBudget.MaxOutputBytes {
		return fmt.Sprintf("script output exceeds %d bytes (got %d)", evalSporeBudget.MaxOutputBytes, len(encoded)), true
	}
	return string(encoded), false
}

// buildSavedScriptsBlock renders the accumulated script set as a compact
// index: one line per script (name, signature, description). Bodies stay in
// storage — each script is directly callable as its own tool (script-<name>,
// typed schema), and script_read fetches a body for editing. Returns nil
// when nothing is saved.
func (a *Actor) buildSavedScriptsBlock() *domain.ContentBlock {
	savedScriptsMu.Lock()
	names := make([]string, 0, len(a.savedScripts))
	scripts := make(map[string]savedScript, len(a.savedScripts))
	for name, s := range a.savedScripts {
		names = append(names, name)
		scripts[name] = s
	}
	savedScriptsMu.Unlock()
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("## Saved Scripts\n\n")
	sb.WriteString("Your accumulated spore snippets, each callable directly as its own tool (script-<name>); edit via script_read then script_save; remove with script_delete.\n")
	for _, name := range names {
		s := scripts[name]
		sig := "run(?)"
		if sigDesc, ok := scriptRunParams(s.Script); ok {
			sig = scriptSignatureLine(sigDesc)
		}
		sb.WriteString("\n- **" + s.Name + "** `" + sig + "`")
		if s.Description != "" {
			sb.WriteString(" — " + s.Description)
		}
		sb.WriteString("\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}
