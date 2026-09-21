package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/util"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (a *Actor) callablesMap() map[string]domain.CallableInterface {
	if a.topo == nil {
		return nil
	}
	out := make(map[string]domain.CallableInterface)
	for _, node := range a.topo.Snapshot() {
		for _, ci := range node.Callables {
			if _, exists := out[ci.Name]; exists {
				continue
			}
			out[ci.Name] = ci
		}
	}
	return out
}

// ensureLocalInteractionCallables makes agent-owned callables available during
// the short topology propagation window after actor startup. These callables
// are registered via ctx.Register (handler-only) but are not declared as
// @callable in the schema manifest, so the topology snapshot never carries
// their CallableInterface metadata. This fallback supplies discovery metadata
// so ToolSpecsFromCallables can emit them; execution is intercepted by the
// turn engine (goal/turn) or handled directly (memory).
func ensureLocalInteractionCallables(callables map[string]domain.CallableInterface) map[string]domain.CallableInterface {
	if callables == nil {
		callables = make(map[string]domain.CallableInterface)
	}
	if _, ok := callables["goal_submit"]; !ok {
		callables["goal_submit"] = domain.CallableInterface{
			Name:        "goal_submit",
			Description: "Submit an interpretation of the active goal for user confirmation.",
			Params: []domain.CallableParam{{
				Name: "InterpretedGoal", Type: "string", Required: true,
				Description: "The target end-state and acceptance criteria, not implementation steps.",
			}},
		}
	}
	if _, ok := callables["goal_card_submit"]; !ok {
		callables["goal_card_submit"] = domain.CallableInterface{
			Name:        "goal_card_submit",
			Description: "Submit an existing task-card binding for user confirmation. On approval the current agent claims that card and starts executing its card body as a bound goal.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Exact ID of the existing matching task card."},
				{Name: "InterpretedGoal", Type: "string", Description: "Optional refined interpretation of the task card."},
			},
		}
	}
	if _, ok := callables["turn_assess"]; !ok {
		callables["turn_assess"] = domain.CallableInterface{
			Name:        "turn_assess",
			Description: "Record a self-assessment of the current turn.",
			Params: []domain.CallableParam{
				{Name: "Decision", Type: "string", Required: true, Description: "Must be \"complete_candidate\" if the goal may be complete, or \"ready_for_review\" if the work is done and awaiting external review before completion. Do NOT call this tool while the goal still needs work — end the turn and the system continues automatically. An agent with a parent reviewer whose goal is bound to a task card must use \"ready_for_review\"; an agent without a parent agent completes directly via \"complete_candidate\" (\"ready_for_review\" is rejected — no reviewer exists to resume it)."},
				{Name: "Reason", Type: "string", Description: "Reason for the assessment."},
				{Name: "Evidence", Type: "string[]", Description: "Evidence supporting the assessment. On \"ready_for_review\" these are stamped onto the bound task card's data.review_evidence so the reviewer sees them on the card — list the concrete file paths you touched (and test commands you ran)."},
				{Name: "Outputs", Type: "object", Description: "Typed JSON produced when declaring ready_for_review. Each key must satisfy the bound task card's data.outputs contract; the review path validates this before approving. Omit when the task card declares no outputs contract."},
			},
		}
	}
	if _, ok := callables["memory_save"]; !ok {
		callables["memory_save"] = domain.CallableInterface{
			Name:        "memory_save",
			Description: "Save a short memory note to the session layer.",
			Params: []domain.CallableParam{
				{Name: "Content", Type: "string", Required: true, Description: "The memory content to persist."},
				{Name: "Layer", Type: "string", Description: "Memory layer; defaults to session. Only session is allowed for direct saves."},
			},
		}
	}
	if _, ok := callables["memory_recall"]; !ok {
		callables["memory_recall"] = domain.CallableInterface{
			Name:        "memory_recall",
			Description: "Recall memory nodes by exact ID, content substring, or layer.",
			Params: []domain.CallableParam{
				{Name: "Query", Type: "string", Description: "Node ID for exact recall, or content substring to search."},
				{Name: "Layer", Type: "string", Description: "Filter by layer (session, experience, ontology)."},
				{Name: "Limit", Type: "int", Description: "Maximum number of nodes to return."},
			},
		}
	}
	if _, ok := callables["memory_dream"]; !ok {
		callables["memory_dream"] = domain.CallableInterface{
			Name:        "memory_dream",
			Description: "Trigger a memory consolidation cycle (merge, promote, connect). Runs asynchronously.",
		}
	}
	planSubmit, ok := callables["plan_submit"]
	if !ok {
		planSubmit = domain.CallableInterface{Name: "plan_submit"}
	}
	// The topology may already contain a stale callable descriptor from an
	// actor that registered plan.submit before the generated schema metadata was
	// available. Never let that descriptor hide the required Title parameter.
	if !hasCallableParam(planSubmit.Params, "Title") {
		planSubmit.Description = "Submit an implementation plan with required Title and Body. Always include a non-empty Title and the complete markdown Body."
		planSubmit.Params = []domain.CallableParam{
			{Name: "Title", Type: "string", Required: true, Description: "Short title used for the plan card and identifier."},
			{Name: "Body", Type: "string", Required: true, Description: "Complete markdown plan body."},
			{Name: "Tasks", Type: "PlanTaskRef[]", Description: "Optional plan tasks created after approval."},
			{Name: "Policy", Type: "PlanPolicy", Description: "Optional plan approval policy."},
		}
	}
	callables["plan_submit"] = planSubmit

	// Agent-local, project component, and MCP manager callables declared by
	// the bundle-use bundle. They reach the topology via the runtime manifest
	// export, but agent-owned registrations carry no WithDescription/
	// WithParams and cross-actor shells may lag topology propagation, so the
	// manifest yields metadata shells (empty description, no params) that
	// materialize as description-less, schema-less tools. Fill missing
	// entries and enrich existing shells so ToolSpecsFromCallables emits
	// usable specs for any agent kind that mounts the contributing bundle.
	localToolCallables := map[string]domain.CallableInterface{
		"component_mount": {
			Name:        "component_mount",
			Description: "Mount a component card (bundle, skill, prompt, mode) on this agent by cardId. Declared dependencies are auto-mounted transitively.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Full card ID, e.g. builtin:bundle:file-tools."},
				{Name: "Enabled", Type: "bool", Description: "Whether the mount is enabled (default true)."},
				{Name: "Order", Type: "int", Description: "Mount order for sorting."},
				{Name: "Scope", Type: "string", Description: "Mount scope: user (default), builtin, or dependency."},
			},
		},
		"component_unmount": {
			Name:        "component_unmount",
			Description: "Unmount a component card by cardId. Protected cards with builtin scope cannot be unmounted.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Full card ID of the mounted component to remove."},
			},
		},
		"component_set_enabled": {
			Name:        "component_set_enabled",
			Description: "Toggle a mounted component's enabled state without removing the mount. A disabled card stays mounted but contributes no tools or prompts.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Full card ID of the mounted component."},
				{Name: "Enabled", Type: "bool", Required: true, Description: "New enabled state."},
			},
		},
		"component_list": {
			Name:        "component_list",
			Description: "List all component cards currently mounted on this agent, including their enabled state, scope, and kind.",
		},
		"component_snapshot": {
			Name:        "component_snapshot",
			Description: "Get the fully resolved component snapshot: mounted cards plus the prompts and tools they contribute after dependency resolution and deduplication.",
		},
		"project.component_list": {
			Name:        "project.component_list",
			Description: "List every component descriptor available in the project catalog: builtin cards, persisted project cards, and external provider cards. Each descriptor includes its ref (cardId, kind), title, icon, tools, declared dependencies, and ShadowedById (non-empty when a persisted card overrides an external/builtin card of the same ID).",
			Kind:        "unary",
			Permission:  "public",
		},
		"project.component_get": {
			Name:        "project.component_get",
			Description: "Get a single component descriptor by cardId. Use it to inspect a specific bundle's tools and dependencies before deciding to mount it.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Full card ID of the component to inspect."},
			},
			Kind:       "unary",
			Permission: "public",
		},
		"appmanager.component_list": {
			Name:        "appmanager.component_list",
			Description: "List every virtual app-bundle component currently projected by the running app registry. Each descriptor includes the cardId, title, icon, declared tools, and dependencies. Use this to discover which bundles each loaded app exposes for mounting. Read-only.",
			Kind:        "unary",
			Permission:  "public",
			ServiceName: "appmanager",
		},
		"appmanager.component_get": {
			Name:        "appmanager.component_get",
			Description: "Get one virtual app-bundle component descriptor by cardId. Use it to inspect a specific app bundle's tools and dependencies before deciding to mount it.",
			Params: []domain.CallableParam{
				{Name: "CardId", Type: "string", Required: true, Description: "Full card ID of the virtual app-bundle component to inspect."},
			},
			Kind:        "unary",
			Permission:  "public",
			ServiceName: "appmanager",
		},
		"mcp.list_servers": {
			Name:        "mcp.list_servers",
			Description: "List every MCP server configured in the system, with id, name, transport, enabled flag, and live status (connected, toolCount, error). Safe view: env/header key names only, never values. Each server is mountable on this agent as the bundle card mcp:<server-id>.",
			ServiceName: "mcp",
		},
		"mcp.add_server": {
			Name:        "mcp.add_server",
			Description: "Register a new MCP server in the system registry. The server auto-connects when Enabled is true, after which its tools become discoverable and it appears as the mountable bundle card mcp:<server-id>. Secrets passed in Stdio.Env / Http.Headers are write-only: accepted here, never returned by any callable. Only add a server when the user asks for it.",
			Params: []domain.CallableParam{
				{Name: "Config", Type: "McpServerConfig", Required: true, Description: `Server config object: Name (unique, required), Transport ("stdio" or "http"), Stdio {Command, Args, Env} for stdio servers, Http {Url, Headers} for http servers, Enabled (true = auto-connect).`},
			},
			ServiceName: "mcp",
		},
		"mcp.reconnect": {
			Name:        "mcp.reconnect",
			Description: "Force-reconnect an MCP server: tear down any live (possibly wedged) session, reset the auto-reconnect budget, and establish a fresh connection. Use it when a mounted MCP server shows DISCONNECTED in your hot-context \"Mounted MCP Servers\" block or its tools stopped responding; on failure the background reconnect loop keeps retrying with backoff. Returns the live status (connected, toolCount, error).",
			Params: []domain.CallableParam{
				{Name: "Id", Type: "string", Required: true, Description: "MCP server ID as shown in the Mounted MCP Servers hot-context block (the mcp:<server-id> card without the mcp: prefix)."},
			},
			ServiceName: "mcp",
		},
		"open_global_browser": {
			Name:        "open_global_browser",
			Description: "Open a URL or local file in the sporemind desktop's shared global browser (the right-panel global browser tab). Supports http://, https://, file:// URLs and project-relative paths. Only available in the desktop client; in headless mode an open_global event is emitted but no browser window will appear.",
			Params: []domain.CallableParam{
				{Name: "Url", Type: "string", Required: true, Description: "URL or local file path to open. Supports http://, https://, file:// and project-relative paths (resolved against the project root)."},
			},
		},
		"show_page_thumbnail": {
			Name:        "show_page_thumbnail",
			Description: "Show a thumbnail card of a web page or local file in the conversation timeline. The tool only renders the preview; the page opens in the right-panel global browser tab when the user clicks the card. Supports http://, https://, file:// URLs and project-relative paths.",
			Params: []domain.CallableParam{
				{Name: "Url", Type: "string", Required: true, Description: "URL or local file path to preview. Supports http://, https://, file:// and project-relative paths (resolved against the project root)."},
				{Name: "Title", Type: "string", Description: "Optional display title for the thumbnail card."},
			},
		},
		"open_ssh_session": {
			Name:        "open_ssh_session",
			Description: "Open an interactive SSH shell session on a remote host. Before opening, check sshmanager.session_list for an existing session on the same host and reuse its sessionId with sshmanager.shell_run — do not open duplicate sessions. The desktop right panel opens a terminal tab bound to the session. Returns the session ID for shell_input/shell_stream calls. Only available in the desktop client; in headless mode the session is created but no terminal tab appears.",
			Params: []domain.CallableParam{
				{Name: "HostId", Type: "string", Required: true, Description: "ID of the saved SSH host to connect to (from sshmanager.host_list)"},
			},
		},
	}
	for id, fb := range localToolCallables {
		existing, ok := callables[id]
		switch {
		case !ok:
			// Not in manifest at all → insert the full shell.
			callables[id] = fb
		case existing.Description == "" && len(existing.Params) == 0:
			// Manifest has a metadata-only shell; enrich display fields while
			// preserving any runtime declarations (EffectKind, ServiceName,
			// ToolName, Stream) that the actor registration already set.
			existing.Description = fb.Description
			existing.Params = fb.Params
			if fb.Kind != "" {
				existing.Kind = fb.Kind
			}
			if fb.Permission != "" {
				existing.Permission = fb.Permission
			}
			existing.Name = fb.Name
			callables[id] = existing
		default:
			// Already a fully-populated entry → leave it alone.
		}
	}

	// agent.skill.use is always provided by appendAutonomousTools via
	// SkillUseTool() with the full JSON schema (enums, required fields).
	// The topology registers it with only a description and no params, so
	// if it leaks into ToolSpecsFromCallables it produces an empty-schema
	// duplicate that collides with SkillUseTool() in dedupToolsByName.
	delete(callables, "skill_use")
	return callables
}

func ensureWorktreeCallables(callables map[string]domain.CallableInterface) map[string]domain.CallableInterface {
	if callables == nil {
		callables = make(map[string]domain.CallableInterface)
	}
	fallbacks := map[string]domain.CallableInterface{
		"project.worktree_enter": worktreeCallable("project.worktree_enter", "Enter and bind an isolated worktree for the current agent.", []domain.CallableParam{
			{Name: "Name", Type: "string", Description: "Worktree name and branch; omitted to generate one."},
			{Name: "BaseRef", Type: "string", Description: "Commit-ish to start from; empty uses HEAD."},
		}),
		"project.worktree_exit": worktreeCallable("project.worktree_exit", "Leave the current worktree using merge or discard.", []domain.CallableParam{
			{Name: "Mode", Type: "string", Required: true, Description: "merge or discard."},
			{Name: "Force", Type: "bool", Description: "Discard only; allow uncommitted changes."},
			{Name: "BaseBranch", Type: "string", Description: "Merge only; target branch in the main repository."},
		}),
		"project.worktree_list":           worktreeCallable("project.worktree_list", "List project worktrees.", nil),
		"project.worktree_get":            worktreeCallable("project.worktree_get", "Get a project worktree by ID.", []domain.CallableParam{{Name: "WorktreeID", Type: "string", Required: true}}),
		"project.worktree_agent_bindings": worktreeCallable("project.worktree_agent_bindings", "List agent to worktree bindings.", nil),
	}
	for id, fallback := range fallbacks {
		if _, ok := callables[id]; !ok {
			callables[id] = fallback
		}
	}
	return callables
}

func worktreeCallable(name, description string, params []domain.CallableParam) domain.CallableInterface {
	return domain.CallableInterface{Name: name, Kind: "unary", Description: description, Params: params, Permission: "public"}
}

func hasCallableParam(params []domain.CallableParam, name string) bool {
	for _, param := range params {
		if param.Name == name {
			return true
		}
	}
	return false
}

func appendUnique(allowed []string, ids ...string) []string {
	seen := make(map[string]struct{}, len(allowed)+len(ids))
	for _, id := range allowed {
		seen[id] = struct{}{}
	}
	for _, id := range ids {
		if _, ok := seen[id]; !ok {
			allowed = append(allowed, id)
			seen[id] = struct{}{}
		}
	}
	return allowed
}

// resolveTools computes the turn's tool surface. The agent kind's allowed
// callables are derived locally from the (per-turn cached) kind config via
// resolveBundleCallableIDs, so the chat/startTurn path performs no synchronous
// cross-actor discovery chain (OwnerLane blocking audit 2026-08-27 P1: the
// previous agent→oracle→workspace Await chain held both owner lanes on every
// chat submit). Builtin bundles resolve from embedded assets (matching the
// oracle's agentkit.CallableIDsForBundles computation for capability_discover,
// which is intentionally left builtin-only); project app-bundle cards resolve
// via a single project.component_get lookup, the same additive cross-actor
// surface resolveMCPTools/resolveAppTools already use in this function.
// ToolSpecsFromCallables intersects the allowed set with the callable map,
// reproducing the oracle's filtering locally; config edits are picked up per
// turn via resetTurnSnapshot's kindConfig invalidation. oracle.capability_discover
// remains available for direct (frontend) consumers.
func (a *Actor) resolveTools(ctx actor.Context, cfg domain.AgentKindConfig, callables map[string]domain.CallableInterface) []domain.ToolSpec {
	if cfg.Kind == "" {
		return nil
	}
	// The topology provider is eventually consistent with OnStart registration.
	// Goal mode is mounted immediately by /goal, so the first turn can otherwise
	// see the goal tool contribution while the callable map still lacks it. Add
	// the two local interaction callables from their schema metadata as a
	// readiness fallback; execution remains intercepted by turn_engine.
	callables = ensureLocalInteractionCallables(callables)
	callables = ensureWorktreeCallables(callables)
	componentSnapshot := a.resolveComponentSnapshot(ctx)
	componentCallables := componentSnapshotToolIDs(componentSnapshot)

	allowed := a.resolveBundleCallableIDs(ctx, cfg.DefaultBundleIDs)
	allowed = appendUnique(allowed, componentCallables...)
	tools := applyComponentToolMetadata(ToolSpecsFromCallables(callables, allowed), componentSnapshot)
	// Append MCP tools (external provider surface).
	tools = append(tools, a.resolveMCPTools(ctx)...)
	// Append plugin tools discovered via the global appmanager.list query.
	tools = append(tools, a.resolveAppTools(ctx)...)
	return tools
}

// resolveMCPTools queries the mcpmanager for connected servers' tools and
// synthesizes LLM-facing ToolSpecs (mcp.<server>.<tool>, inputSchema passed
// through verbatim), but only for servers referenced by mounted
// mcp:<server-id> component cards. With no MCP card mounted, no MCP tools are
// injected (explicit constraint); discover_tools is still consulted per turn
// so the tool list stays fresh. Returns nil when the manager is unreachable
// or no mounted server's tools are discoverable — MCP tools are an additive
// surface, never a hard dependency.
func (a *Actor) resolveMCPTools(ctx actor.Context) []domain.ToolSpec {
	serverIDs := mountedMCPServerIDs(a.resolveComponentSnapshot(ctx).Mounts)
	if len(serverIDs) == 0 {
		return nil
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil
	}
	mcpRef, ok := ctx.LookupService("mcp")
	if !ok {
		return nil
	}
	payload, _ := json.Marshal(domain.McpDiscoverToolsReq{})
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(invokeCtx, mcpRef, "mcp.discover_tools", payload).Await()
	if err != nil || result == nil {
		return nil
	}
	var resp domain.McpDiscoverToolsResp
	switch v := result.(type) {
	case []byte:
		if err := json.Unmarshal(v, &resp); err != nil {
			return nil
		}
	case domain.McpDiscoverToolsResp:
		resp = v
	case *domain.McpDiscoverToolsResp:
		if v == nil {
			return nil
		}
		resp = *v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &resp)
	default:
		return nil
	}
	return mcpToolSpecsFromCatalog(filterMCPServers(resp, serverIDs))
}

// mountedMCPServerIDs extracts the server ID of every enabled mcp:<server-id>
// component mount. Returns nil when no MCP card is mounted, so callers can
// short-circuit before touching the mcpmanager.
func mountedMCPServerIDs(mounts []domain.AgentComponentMount) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, m := range mounts {
		if !m.Enabled {
			continue
		}
		if id := mcpCardServerID(m.CardID); id != "" {
			ids[id] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// mcpCardServerID returns the MCP server ID referenced by a card ID of the
// form mcp:<server-id>, or "" for any other card.
func mcpCardServerID(cardID string) string {
	if !strings.HasPrefix(cardID, "mcp:") {
		return ""
	}
	return strings.TrimPrefix(cardID, "mcp:")
}

// filterMCPServers retains only the servers whose ID is in the mounted set,
// so the catalog synthesizes tools solely for mounted MCP servers.
func filterMCPServers(resp domain.McpDiscoverToolsResp, serverIDs map[string]struct{}) domain.McpDiscoverToolsResp {
	if len(serverIDs) == 0 {
		resp.Servers = nil
		return resp
	}
	filtered := make([]domain.McpServerTools, 0, len(serverIDs))
	for _, srv := range resp.Servers {
		if _, ok := serverIDs[srv.ID]; ok {
			filtered = append(filtered, srv)
		}
	}
	resp.Servers = filtered
	return resp
}

// resolveAppTools queries the appmanager for registered apps' callables and
// synthesizes LLM-facing ToolSpecs (app.<appID>.<callable>, routing to
// appmanager.invoke via the appmanager service). Returns nil when the
// appmanager is unreachable or no running app declares callables — app tools
// are an additive surface, never a hard dependency. Mirrors resolveMCPTools.
func (a *Actor) resolveAppTools(ctx actor.Context) []domain.ToolSpec {
	planner := ctx.Planner()
	if planner == nil {
		return nil
	}
	appMgrRef, ok := ctx.LookupService("appmanager")
	if !ok {
		return nil
	}
	payload, _ := json.Marshal(gen.AppManagerListReq{})
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(invokeCtx, appMgrRef, "appmanager.list", payload).Await()
	if err != nil || result == nil {
		return nil
	}
	var resp gen.AppManagerListResp
	switch v := result.(type) {
	case []byte:
		if err := json.Unmarshal(v, &resp); err != nil {
			return nil
		}
	case gen.AppManagerListResp:
		resp = v
	case *gen.AppManagerListResp:
		if v == nil {
			return nil
		}
		resp = *v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &resp)
	default:
		return nil
	}
	return appToolSpecsFromCatalog(resp, a.appToolsAllowedByBundleMount(ctx, resp))
}

// mountedAppBundleCardIDs returns the card IDs of app-bundle:{appID}: cards
// this agent has mounted and enabled, read from a.cardRefs (falling back to
// canonical ComponentMounts when cardRefs is nil, mirroring cardRefEnabled).
func (a *Actor) mountedAppBundleCardIDs(appID string) []string {
	refs := a.cardRefs
	if refs == nil {
		refs = canonicalCardRefsFromMounts(a.ComponentMounts)
	}
	prefix := "app-bundle:" + appID + ":"
	var ids []string
	for _, ref := range refs {
		if ref.Disabled || !strings.HasPrefix(ref.ID, prefix) {
			continue
		}
		ids = append(ids, ref.ID)
	}
	return ids
}

// appToolsAllowedByBundleMount computes the per-app allowed-callable filter
// for appToolSpecsFromCatalog: an app exposes only the union of tools from
// the app-bundle:{appID}: cards this agent has mounted and enabled. With no
// bundle mounted — including apps whose manifest declares no bundles at all —
// the app contributes an empty set, i.e. zero tools. Fail-closed: bundle
// resolution that comes back empty (project unreachable, stale mount) yields
// zero tools instead of a full passthrough.
func (a *Actor) appToolsAllowedByBundleMount(ctx actor.Context, resp gen.AppManagerListResp) map[string]map[string]bool {
	allowed := make(map[string]map[string]bool, len(resp.Items))
	for _, app := range resp.Items {
		if app.State != "running" {
			continue
		}
		callables := a.resolveBundleCallableIDs(ctx, a.mountedAppBundleCardIDs(app.ID))
		set := make(map[string]bool, len(callables))
		for _, callable := range callables {
			set[callable] = true
		}
		allowed[app.ID] = set
	}
	return allowed
}

func (a *Actor) buildPromptArtifactStateful(ctx actor.Context) domain.PromptArtifact {
	cfg := a.fetchAgentKindConfig(ctx)
	inst := a.resolveInstructions(ctx)
	a.appendMemoryBase(inst)
	a.appendMemoryExperience(inst)
	callables := a.callablesMap()
	tools := a.resolveTools(ctx, cfg, callables)
	tools = nativeToolRegistry.AppendWebSearch(tools, a.status.Unit.Model)
	tools = a.appendAutonomousTools(tools, cfg)
	if a.child.Mode {
		filtered := tools[:0]
		for _, t := range tools {
			if t.CallableID != "workspace.agent_spawn_by_type" {
				filtered = append(filtered, t)
			}
		}
		tools = filtered
	}
	compiled := a.compileMessagesWithSource(false)

	var fragments []domain.PromptFragment
	var segments []domain.PromptContextSegment

	add := func(name, kind, content, sourceRange string) {
		if content == "" {
			return
		}
		id := fmt.Sprintf("frag-%d", len(fragments))
		fragments = append(fragments, domain.PromptFragment{
			ID:          id,
			Name:        name,
			Kind:        kind,
			Content:     content,
			Source:      "compiled",
			SourceRange: sourceRange,
		})
		segments = append(segments, domain.PromptContextSegment{
			ID:         "seg-" + id,
			FragmentID: id,
			Chars:      int32(len([]rune(content))),
		})
	}

	// Config segment.
	var cfgB strings.Builder
	if a.status.Unit.Model != "" {
		fmt.Fprintf(&cfgB, "Model: %s", a.status.Unit.Model)
		if a.status.Unit.Provider != "" {
			fmt.Fprintf(&cfgB, " (%s)", a.status.Unit.Provider)
		}
	}
	add("Config", "config", cfgB.String(), "")

	// System blocks: instructions + hot context (appended to system prompt
	// in the actual dispatch; shown here directly after instructions).
	var lastSystemSegName string
	if inst != nil {
		// Show Base (role + component prompts + tool guidance) and Resolved
		// as separate labeled groups. Memory blocks injected into
		// Base/Resolved are tagged with their own kind for visual distinction.
		for i, p := range inst.Base {
			if p == "" {
				continue
			}
			mKind, mLabel := memorySegmentKind(p)
			label := "System Block"
			kind := "system"
			if mKind != "" {
				kind = mKind
				label = mLabel
			}
			if mKind == "" && len(inst.Base) > 1 {
				label = fmt.Sprintf("System Block #%d", i+1)
			}
			add(label, kind, p, "")
			if mKind == "" {
				lastSystemSegName = label
			}
		}
		for i, p := range inst.Resolved {
			if p == "" {
				continue
			}
			mKind, mLabel := memorySegmentKind(p)
			label := "Resolved"
			kind := "system"
			if mKind != "" {
				kind = mKind
				label = mLabel
			}
			if mKind == "" && len(inst.Resolved) > 1 {
				label = fmt.Sprintf("Resolved #%d", i+1)
			}
			add(label, kind, p, "")
			if mKind == "" {
				lastSystemSegName = label
			}
		}
	}
	// Cache breakpoint annotation on the last instruction segment.
	if lastSystemSegName != "" {
		for i := range segments {
			if fragments[i].Name == lastSystemSegName {
				fragments[i].Content += "\n[cache_control: ephemeral]"
				break
			}
		}
	}

	// Resolve hot context blocks once. Session memory blocks (post-hot)
	// are tagged with their own kind for visual distinction.
	hotCtx := a.resolveFullHotContext(ctx)
	for j, block := range hotCtx {
		if block.Text == "" {
			continue
		}
		mKind, mLabel := memorySegmentKind(block.Text)
		label := "Environment Context"
		kind := "hotcontext"
		if mKind != "" {
			kind = mKind
			label = mLabel
		}
		if mKind == "" && len(hotCtx) > 1 {
			label = fmt.Sprintf("Environment Context #%d", j+1)
		}
		add(label, kind, block.Text, "")
	}

	// Tools: one segment per ToolSpec.
	for _, tool := range tools {
		var tb strings.Builder
		fmt.Fprintf(&tb, "%s", tool.Name)
		if tool.Description != "" {
			fmt.Fprintf(&tb, "\n%s", tool.Description)
		}
		if tool.InputSchema != "" {
			fmt.Fprintf(&tb, "\nSchema:\n%s", tool.InputSchema)
		}
		add(tool.Name, "tool", tb.String(), "")
	}

	// Messages: one segment per ChatMessage.
	for i, c := range compiled {
		msg := c.msg
		var mb strings.Builder
		fmt.Fprintf(&mb, "[%s]", msg.Role)
		for _, cb := range msg.Content {
			switch cb.Type {
			case domain.ContentBlockText:
				if cb.Text != "" {
					fmt.Fprintf(&mb, "\n%s", cb.Text)
				}
			case domain.ContentBlockToolUse:
				fmt.Fprintf(&mb, "\n<tool_use id=%q name=%q>\n%s\n</tool_use>", cb.ToolUseID, cb.ToolName, cb.Input)
			case domain.ContentBlockToolResult:
				prefix := ""
				if cb.IsError {
					prefix = " [ERROR]"
				}
				fmt.Fprintf(&mb, "\n<tool_result id=%q%s>\n%s\n</tool_result>", cb.ToolUseID, prefix, cb.Text)
			}
		}
		label := fmt.Sprintf("%s #%d", msg.Role, i+1)
		add(label, "message", mb.String(), fmt.Sprintf("%d", c.sourceIdx))
	}

	artifact := domain.PromptArtifact{
		Fragments:       fragments,
		ContextSegments: segments,
	}
	if len(a.RawSession.SummarySegments) > 0 {
		segs := make([]domain.SummarySegment, len(a.RawSession.SummarySegments))
		copy(segs, a.RawSession.SummarySegments)
		artifact.SummarySegments = segs
	}
	if a.status.ContextBudget != nil {
		artifact.ContextWindowSize = a.status.ContextBudget.ContextWindowSize
		artifact.TokenBudget = a.status.ContextBudget.TokenBudget
	}
	return artifact
}

// handlePromptArtifact is the pure handler for agent.prompt_artifact.
// It returns the most recently built prompt artifact from refreshPromptCaches
// without entering the owner queue.
func (a *Actor) handlePromptArtifact(actor.PureContext) (domain.PromptArtifact, error) {
	p := a.cachedPromptArtifact.Load()
	if p == nil {
		return domain.PromptArtifact{}, nil
	}
	return *p, nil
}

// buildCompiledPromptStateful builds the actual LLM-bound message list, decomposed
// into one segment per protocol-compliant ChatMessage. This is the inspector's
// "Compiled Prompt" tab: the messages that are (or would be) sent to the LLM
// after compaction. Called on the cell goroutine by refreshPromptCaches.

func (a *Actor) buildCompiledPromptStateful(ctx actor.Context) domain.PromptArtifact {
	var fragments []domain.PromptFragment
	var segments []domain.PromptContextSegment

	add := func(name, kind, content, sourceRange string) {
		if content == "" {
			return
		}
		id := fmt.Sprintf("frag-%d", len(fragments))
		fragments = append(fragments, domain.PromptFragment{
			ID:          id,
			Name:        name,
			Kind:        kind,
			Content:     content,
			Source:      "compiled",
			SourceRange: sourceRange,
		})
		segments = append(segments, domain.PromptContextSegment{
			ID:         "seg-" + id,
			FragmentID: id,
			Chars:      int32(len([]rune(content))),
		})
	}

	// System instructions are sent as the LLM's system prompt, separate from messages.
	// Hot context is appended to the system prompt, so it is shown here directly after instructions.
	// Memory blocks injected into Base/Resolved/hot-context are emitted as
	// separate fragments with their own kind for visual distinction.
	if ctx != nil {
		inst := a.resolveInstructions(ctx)
		a.appendMemoryBase(inst)
		a.appendMemoryExperience(inst)
		if inst != nil {
			// Split Base into memory vs non-memory fragments.
			var nonMemBase []string
			for _, p := range inst.Base {
				if p == "" {
					continue
				}
				mKind, mLabel := memorySegmentKind(p)
				if mKind != "" {
					add(mLabel, mKind, p, "")
				} else {
					nonMemBase = append(nonMemBase, p)
				}
			}
			if len(nonMemBase) > 0 {
				add("System instructions", "system", strings.Join(nonMemBase, "\n\n"), "")
			}
			var nonMemResolved []string
			for _, p := range inst.Resolved {
				if p == "" {
					continue
				}
				mKind, mLabel := memorySegmentKind(p)
				if mKind != "" {
					add(mLabel, mKind, p, "")
				} else {
					nonMemResolved = append(nonMemResolved, p)
				}
			}
			if len(nonMemResolved) > 0 {
				add("Resolved instructions", "system", strings.Join(nonMemResolved, "\n\n"), "")
			}
		}
	}

	// Hot context is collected here and appended after summaries so the inspector
	// segment order matches the visual hierarchy requested by the UI.
	var hotBlocks []domain.ContentBlock
	if ctx != nil {
		hot := a.resolveFullHotContext(ctx)
		for _, b := range hot {
			if b.Text != "" {
				hotBlocks = append(hotBlocks, b)
			}
		}
	}

	// Summary replaces compacted assistant/tool messages in the actual dispatch.
	for _, seg := range a.RawSession.SummarySegments {
		if seg.Text == "" {
			continue
		}
		label := fmt.Sprintf("Summary L%d [%d-%d]", seg.Level, seg.SourceStartIndex, seg.SourceEndIndex)
		add(label, "summary", seg.Text, fmt.Sprintf("%d-%d", seg.SourceStartIndex, seg.SourceEndIndex))
	}

	for j, b := range hotBlocks {
		mKind, mLabel := memorySegmentKind(b.Text)
		label := "Hot context"
		kind := "hotcontext"
		if mKind != "" {
			kind = mKind
			label = mLabel
		}
		if mKind == "" && len(hotBlocks) > 1 {
			label = fmt.Sprintf("Hot context #%d", j+1)
		}
		add(label, kind, b.Text, "")
	}

	// Remaining conversation messages after compaction.
	compiled := a.compileMessagesWithSource(true)

	// Messages: one segment per ChatMessage.
	for i, c := range compiled {
		msg := c.msg

		var mb strings.Builder
		fmt.Fprintf(&mb, "[%s]", msg.Role)
		for _, cb := range msg.Content {
			switch cb.Type {
			case domain.ContentBlockText:
				if cb.Text != "" {
					fmt.Fprintf(&mb, "\n%s", cb.Text)
				}
			case domain.ContentBlockToolUse:
				fmt.Fprintf(&mb, "\n<tool_use id=%q name=%q>\n%s\n</tool_use>", cb.ToolUseID, cb.ToolName, cb.Input)
			case domain.ContentBlockToolResult:
				prefix := ""
				if cb.IsError {
					prefix = " [ERROR]"
				}
				fmt.Fprintf(&mb, "\n<tool_result id=%q%s>\n%s\n</tool_result>", cb.ToolUseID, prefix, cb.Text)
			}
		}
		label := fmt.Sprintf("%s #%d", msg.Role, i+1)
		add(label, "message", mb.String(), fmt.Sprintf("%d", c.sourceIdx))
	}

	return domain.PromptArtifact{
		Fragments:       fragments,
		ContextSegments: segments,
	}
}

// handleCompiledPrompt is the pure handler for agent.compiled_prompt.
// It returns the most recently built compiled prompt from refreshPromptCaches
// without entering the owner queue.
func (a *Actor) handleCompiledPrompt(actor.PureContext) (domain.PromptArtifact, error) {
	p := a.cachedCompiledPrompt.Load()
	if p == nil {
		return domain.PromptArtifact{}, nil
	}
	return *p, nil
}

// fetchAgentKindConfig queries workspace for the current agent kind config.
// Returns zero value if workspace is unavailable or the config is not found.
// The result is cached after the first successful fetch within a turn to avoid
// repeated synchronous invokes; the cache is invalidated at the start of each
// turn (see resetTurnSnapshot) so live edits to the kind config are picked up.

func (a *Actor) fetchAgentKindConfig(ctx actor.Context) domain.AgentKindConfig {
	if cached := a.kindConfig.Load(); cached != nil {
		return *cached
	}
	planner := ctx.Planner()
	if planner == nil {
		return domain.AgentKindConfig{}
	}
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return domain.AgentKindConfig{}
	}
	cfgPayload, _ := json.Marshal(domain.WorkspaceGetAgentKindConfigReq{Kind: a.agentKind})
	cfgCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	cfgResult, err := planner.Call(cfgCtx, wsRef, "workspace.get_agent_kind_config", cfgPayload).Await()
	if err != nil {
		return domain.AgentKindConfig{}
	}
	var cfg domain.AgentKindConfig
	switch v := cfgResult.(type) {
	case []byte:
		if err := json.Unmarshal(v, &cfg); err != nil {
			return domain.AgentKindConfig{}
		}
	case domain.AgentKindConfig:
		cfg = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &cfg)
	default:
		return domain.AgentKindConfig{}
	}
	a.kindConfig.Store(&cfg)
	return cfg
}

// parentProjectID returns the actor ID of the agent's parent project actor,
// used for project-scoped approvals and workspace.git_* ProjectId fallback.
func parentProjectID(ctx actor.Context) string {
	if p := ctx.Parent(); p != nil {
		return p.ID().String()
	}
	return ""
}

// projectGuideFiles lists the project guide files auto-mounted into the agent
// environment, in priority order. AGENTS.md overrides CLAUDE.md: when both
// exist, only AGENTS.md is mounted.
var projectGuideFiles = []string{"AGENTS.md", "CLAUDE.md"}

// maxGuideFileChars caps the guide file content mounted into the environment
// prompt; longer files are truncated with a marker.
const maxGuideFileChars = 32768

// fetchProjectGuideFile reads the project guide file (AGENTS.md, falling back
// to CLAUDE.md) from the caller's resolved root via the parent project actor's
// project.read callable. Relative paths resolve per caller binding, so
// worktree-bound agents read their own checkout's guide. Returns the file name
// and (possibly truncated) content, or empty strings when neither file exists.
func fetchProjectGuideFile(ctx actor.Context) (string, string) {
	parent := ctx.Parent()
	if parent == nil {
		return "", ""
	}
	planner := ctx.Planner()
	if planner == nil {
		return "", ""
	}
	for _, name := range projectGuideFiles {
		payload, _ := json.Marshal(domain.FileSystemReadReq{Path: name})
		infoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
		result, err := planner.Call(infoCtx, parent, "project.read", payload).Await()
		cancel()
		if err != nil || result == nil {
			continue
		}
		content := strings.TrimSpace(decodeFileReadContent(result))
		if content == "" {
			continue
		}
		if len(content) > maxGuideFileChars {
			cut := maxGuideFileChars
			for cut > 0 && !utf8.RuneStart(content[cut]) {
				cut--
			}
			content = content[:cut] + "\n...(truncated)"
		}
		return name, content
	}
	return "", ""
}

// decodeFileReadContent extracts the Content field from a project.read RPC
// result across the transport shapes the planner may return.
func decodeFileReadContent(result any) string {
	switch v := result.(type) {
	case []byte:
		var resp domain.FileSystemReadResp
		_ = json.Unmarshal(v, &resp)
		return resp.Content
	case domain.FileSystemReadResp:
		return v.Content
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		var resp domain.FileSystemReadResp
		_ = json.Unmarshal(b, &resp)
		return resp.Content
	}
	return ""
}

// resolveProjectRoot queries the parent project actor for the primary root path.

func (a *Actor) resolveProjectRoot(ctx actor.Context) string {
	if cached := a.cachedProjectRoot.Load(); cached != nil {
		return *cached
	}
	parent := ctx.Parent()
	if parent == nil {
		return ""
	}
	planner := ctx.Planner()
	if planner == nil {
		return ""
	}
	infoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(infoCtx, parent, "project.info", nil).Await()
	if err != nil || result == nil {
		return ""
	}
	info := decodeProjectInfo(result)
	root := ""
	if len(info.Roots) > 0 {
		root = info.Roots[0].Path
	}
	a.cachedProjectRoot.Store(&root)
	return root
}

// resolveStaticEnvironment returns static environment info (declared env config
// + project roots + path rules) as a single string, suitable for inclusion in
// the system block. This content rarely changes within a session and benefits
// from prompt caching.
func (a *Actor) resolveStaticEnvironment(ctx actor.Context) string {
	// Not cached: embeds time.Now() for "Current date" which must stay fresh.
	cfg := a.fetchAgentKindConfig(ctx)
	var parts []string

	// 1. Declared environment context from agent kind config + current date
	var envLines []string
	for k, v := range cfg.EnvironmentContext {
		envLines = append(envLines, fmt.Sprintf("%s: %s", k, v))
	}
	envLines = append(envLines, fmt.Sprintf("Current date: %s", time.Now().Format("2006-01-02")))
	sort.Strings(envLines)
	parts = append(parts, "Environment:\n"+strings.Join(envLines, "\n"))

	// 2. Project roots (working directory) from parent project actor
	if parent := ctx.Parent(); parent != nil {
		planner := ctx.Planner()
		if planner != nil {
			infoCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
			defer cancel()
			infoResult, infoErr := planner.Call(infoCtx, parent, "project.info", nil).Await()
			if infoErr == nil && infoResult != nil {
				info := decodeProjectInfo(infoResult)
				if len(info.Roots) > 0 {
					var lines []string
					lines = append(lines, "Project roots:")
					for i, r := range info.Roots {
						if i == 0 {
							lines = append(lines, fmt.Sprintf("  - %s (primary, default): %s", r.Name, util.NormalizePath(r.Path)))
						} else {
							lines = append(lines, fmt.Sprintf("  - %s: %s", r.Name, util.NormalizePath(r.Path)))
						}
					}
					lines = append(lines, "")
					lines = append(lines, "Execution environment:")
					if a.worktreeStatus != "" && a.worktreeStatus != "none" && a.worktreePath != "" {
						// Worktree-bound agent: the execution root is the worktree.
						// Stamping the main-repo path here misleads the model into
						// cross-root absolute paths that confinement rejects.
						lines = append(lines, fmt.Sprintf("  - Agent shell/working directory: %s", util.NormalizePath(a.worktreePath)))
						lines = append(lines, fmt.Sprintf("  - Bound to worktree %q: the main-repo roots above are off-limits.", a.worktreeName))
						lines = append(lines, "")
						lines = append(lines, "Path rules:")
						lines = append(lines, "  - Relative paths resolve against your worktree root.")
						lines = append(lines, "  - Absolute paths outside your worktree and `..` escapes are rejected (worktree isolation).")
					} else {
						lines = append(lines, fmt.Sprintf("  - Agent shell/working directory: %s", util.NormalizePath(info.Roots[0].Path)))
						lines = append(lines, "")
						lines = append(lines, "Path rules:")
						lines = append(lines, "  - Relative paths resolve against the primary root.")
						lines = append(lines, "  - Absolute paths can access any root.")
					}
					parts = append(parts, strings.Join(lines, "\n"))
				}
			}

			// 3. Project guide file (AGENTS.md, falling back to CLAUDE.md)
			// auto-mounted from the caller's resolved project root.
			if name, guide := fetchProjectGuideFile(ctx); guide != "" {
				parts = append(parts, fmt.Sprintf("Project Guide (%s):\n%s", name, guide))
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// resolveHotContext assembles the "hot" context blocks that change between
// turns and are appended to the system prompt. Agent owns the composition
// strategy; prompt contributions are read from the project card snapshot.
//
// Goal is injected here via buildGoalBlock so the full formatted block
// (## Active Goal) appears in the hot context section of the prompt,
// rather than being duplicated in the instructions Resolved section.
//
// The active plan is deliberately not injected into the prompt at all; plan
// state lives on the agent and in the persisted plan card, and the model can
// consult it via tools when needed.
//
// Git status is deliberately excluded from hot context because it is not part
// of the per-turn instruction surface and should not influence the model's
// reasoning. The active task board (buildTaskBoardBlock) is included here so
// the LLM sees its own pending/in-progress tasks every turn. Mounted MCP
// servers (buildMCPStatusBlock) are included so the agent knows which
// external tool servers it carries and can self-heal a dropped one via
// mcp.reconnect.
func (a *Actor) resolveHotContext(ctx actor.Context) []domain.ContentBlock {
	var blocks []domain.ContentBlock
	if block := a.buildGoalBlock(ctx); block != nil {
		blocks = append(blocks, *block)
	}
	if block := a.buildWorkflowBlock(ctx); block != nil {
		blocks = append(blocks, *block)
	}
	if block := a.buildTaskBoardBlock(); block != nil {
		blocks = append(blocks, *block)
	}
	if block := a.buildConversableBlock(ctx); block != nil {
		blocks = append(blocks, *block)
	}
	if block := a.buildMCPStatusBlock(ctx); block != nil {
		blocks = append(blocks, *block)
	}
	return blocks
}

// buildParentContext extracts recent conversation context from the parent's
// raw session messages so the explore child has situational awareness of what
// the parent LLM was discussing. It skips tool result blobs and the explore
// call itself (the last assistant message with a tool_use block).

func (a *Actor) buildParentContext() string {
	steps := a.steps
	if len(steps) == 0 {
		return ""
	}

	var parts []string
	toolCallsSkipped := false

	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		// Skip tool result messages — too large, not useful as context.
		if step.Role == domain.ChatRoleTool {
			continue
		}
		// Skip the last assistant message if it contains a tool_use (the
		// explore call that spawned this child). Only skip the very first
		// tool-using assistant message encountered (the most recent one).
		if step.Role == domain.ChatRoleAssistant && !toolCallsSkipped {
			hasToolUse := false
			for _, cb := range step.Content {
				if cb.Type == domain.ContentBlockToolUse {
					hasToolUse = true
					break
				}
			}
			if hasToolUse {
				toolCallsSkipped = true
				continue
			}
		}
		// Collect text content from user and assistant messages.
		var text string
		for _, cb := range step.Content {
			if cb.Type == domain.ContentBlockText && cb.Text != "" {
				if text != "" {
					text += "\n"
				}
				text += cb.Text
			}
		}
		if text != "" {
			label := step.Role
			parts = append(parts, fmt.Sprintf("[%s]: %s", label, text))
		}
		// Stop once we have enough context.
		if len(parts) >= 3 {
			break
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}

	return "\n\n<parent_context>\n" + strings.Join(parts, "\n\n") + "\n</parent_context>"
}

// resolveInstructions queries workspace for the current agent kind config and
// compiles prompt contributions from the project card snapshot.
//
// It delegates to compileTurnInstructions, the single orchestration entry
// point shared by the dispatch path and the preview paths (Prompt Context
// Snapshot, Compiled Prompt). This ensures the preview views see the same
// instruction blocks — including worktree/plan dynamic cards — as the real
// dispatch. Goal is injected via HotContext (buildGoalBlock), not here.

func (a *Actor) resolveInstructions(ctx actor.Context) *domain.CompiledInstructions {
	accountScoped := a.agentKind == domain.AgentKindCoordinator && ctx.Identity().Subject != "" && ctx.Identity().Role != "anonymous"
	if !accountScoped {
		if cached := a.resolvedInstructions.Load(); cached != nil {
			// Return a deep copy — callers (Snapshot/Compiled builders,
			// startTurnWithName) may append to .Base/.Resolved, which must not
			// mutate the cache. Both slices are copied so reassignment mutations
			// on the returned value cannot corrupt the cached backing arrays.
			return &domain.CompiledInstructions{
				Base:     append([]string(nil), cached.Base...),
				Resolved: append([]string(nil), cached.Resolved...),
			}
		}
	}

	cfg := a.fetchAgentKindConfig(ctx)
	if cfg.Kind == "" {
		return &domain.CompiledInstructions{}
	}

	snapshot := a.resolveComponentSnapshot(ctx)
	instructions := a.compileTurnInstructions(ctx, cfg, snapshot)
	if a.agentKind == domain.AgentKindCoordinator && ctx.Identity().Subject != "" && ctx.Identity().Role != "anonymous" {
		a.guidanceMu.RLock()
		state, err := a.loadGuidance()
		a.guidanceMu.RUnlock()
		if err == nil {
			if profile, ok := state.Profiles[ctx.Identity().Subject]; ok {
				if block := coordinatorGuidancePrompt(&profile); block != "" {
					instructions.Base = append(instructions.Base, block)
				}
			}
		}
	}

	if !accountScoped {
		a.resolvedInstructions.Store(instructions)
	}
	// Return an independent copy; the cached value must stay pristine so
	// callers appending memory blocks (appendMemoryBase/appendMemoryExperience)
	// do not pollute the cache — otherwise the next resolve (e.g. Compiled
	// Prompt built after the Snapshot) would see ontology/experience twice.
	return &domain.CompiledInstructions{
		Base:     append([]string(nil), instructions.Base...),
		Resolved: append([]string(nil), instructions.Resolved...),
	}
}

// rebuildSteps reconstructs the steps slice from Session.Turns.
// Called on actor load (OnStart / OnRestore).

// compiledMessage pairs a protocol message with the source step index it came from.
type compiledMessage struct {
	msg       domain.ChatMessage
	sourceIdx int32
}

// compileMessagesWithSource rebuilds the []ChatMessage slice for the next LLM
// dispatch from persisted turn steps and in-flight steps, and records each
// message's original step index so inspector views can show where compiled
// fragments came from.
func (a *Actor) compileMessagesWithSource(skipCompacted bool) []compiledMessage {
	head := int(a.Session.ActiveHead)
	if head < 0 || head >= len(a.Session.Turns) {
		head = len(a.Session.Turns) - 1
	}
	if head < 0 {
		return nil
	}

	coveredEnd := a.lastCompactedIdx()

	out := make([]compiledMessage, 0)

	appendFromStep := func(step domain.Step, arrayIdx int) {
		idx := int32(arrayIdx)
		if step.Discarded {
			slog.Debug("compileMessages: skip discarded step", "stepIdx", idx, "role", step.Role, "id", step.ID)
			return
		}
		if skipCompacted && coveredEnd >= 0 && idx <= coveredEnd {
			slog.Debug("compileMessages: skip compacted step", "stepIdx", idx, "role", step.Role, "id", step.ID)
			return
		}
		msg := stepToChatMessage(step)
		// user_inject steps are rendered in the assistant timeline for UI, but
		// they are semantically user messages and must be sent to the LLM as user.
		if step.Type == "user_inject" || msg.Role == "agent" {
			msg.Role = "user"
		}
		// Goal-condition user message: while the goal is not yet submitted,
		// prefix its first text block so the LLM recognizes it as the
		// goal condition and waits for goal_submit before working.
		if step.Meta == "goal_submit" && a.RawSession.Goal != nil && !a.RawSession.Goal.Confirmed {
			applyGoalSubmitPrefix(&msg)
		}
		// Workflow-establish user messages, including injected follow-ups: while
		// no workflow is active, prefix their first text block so the LLM
		// recognizes the workflow intent and calls workflow_start before
		// orchestrating. Engine-generated screenshot observations (Meta
		// "screenshot") are not user intent and stay unprefixed.
		if step.Meta != "screenshot" && !a.workflowActive() && (step.Meta == "workflow_submit" || (step.Type == "user_inject" && a.cardRefEnabled("builtin:mode:workflow"))) {
			applyWorkflowStartPrefix(&msg)
		}
		// Peer agent messages (Meta=agent|<id>|<name>) and human injects
		// (Meta=user|<id>|<name>): annotate the sender so the LLM can tell
		// them apart from the operator's own input. Runs after the role
		// normalization above so user_inject steps are covered too.
		if msg.Role == "user" {
			if kind, sid, sname, ok := parseSenderMeta(step.Meta); ok {
				applyPeerSenderPrefix(&msg, kind, sid, sname)
			}
		}
		filterLLMContent(&msg)
		if !messageHasContent(msg) {
			slog.Debug("compileMessages: skip empty step", "stepIdx", idx, "role", msg.Role, "id", msg.ID, "blocks", len(msg.Content))
			return
		}
		// Split assistant steps that contain tool_result blocks into separate
		// assistant + tool messages to satisfy the LLM protocol.
		if msg.Role == "assistant" && hasToolResult(msg) {
			split := splitToolStep(msg)
			slog.Debug("compileMessages: split step", "stepIdx", idx, "role", step.Role, "id", step.ID, "splitInto", len(split))
			for _, m := range split {
				out = append(out, compiledMessage{msg: m, sourceIdx: idx})
			}
			return
		}
		switch msg.Role {
		case "user", "system", "assistant", "tool":
			out = append(out, compiledMessage{msg: msg, sourceIdx: idx})
		default:
			slog.Debug("compileMessages: skip unknown role", "stepIdx", idx, "role", step.Role, "id", step.ID)
		}
	}

	// Build the set of in-scope turn IDs: persisted turns up to ActiveHead,
	// plus the active in-flight assistant turn (a.status.TurnID). The latter
	// is set by resetTurnSnapshot at startTurn, but the turn is only appended
	// to Session.Turns at completion — so steps synthesized for the active
	// turn (e.g. slash-path agent_skill_use) would otherwise be invisible
	// to compileMessages and the LLM would re-invoke the skill.
	inScope := make(map[string]struct{}, head+2)
	for i := 0; i <= head && i < len(a.Session.Turns); i++ {
		inScope[a.Session.Turns[i].ID] = struct{}{}
	}
	if a.getActiveTurnRef() != "" && a.status.TurnID != "" {
		inScope[a.status.TurnID] = struct{}{}
	}
	for i, step := range a.steps {
		if _, ok := inScope[step.TurnID]; !ok {
			continue
		}
		appendFromStep(step, i)
	}

	return mergeConsecutiveReasoning(out)
}

// mergeConsecutiveReasoning merges consecutive assistant messages where the
// first carries ReasoningContent without Content, into the following assistant
// message that has Content. This avoids sending two consecutive assistant
// messages (protocol violation) and ensures reasoning reaches the LLM.
func mergeConsecutiveReasoning(msgs []compiledMessage) []compiledMessage {
	if len(msgs) < 2 {
		return msgs
	}
	out := make([]compiledMessage, 0, len(msgs))
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.msg.Role == domain.ChatRoleAssistant &&
			m.msg.ReasoningContent != "" &&
			len(m.msg.Content) == 0 &&
			i+1 < len(msgs) &&
			msgs[i+1].msg.Role == domain.ChatRoleAssistant &&
			len(msgs[i+1].msg.Content) > 0 {
			next := msgs[i+1]
			next.msg.ReasoningContent = m.msg.ReasoningContent
			next.sourceIdx = m.sourceIdx
			out = append(out, next)
			i++
			continue
		}
		out = append(out, m)
	}
	return out
}

// compileMessages rebuilds the []ChatMessage slice for the next LLM dispatch
// from persisted turn steps and in-flight steps.
//
// Critical fix: tool_call steps store both tool_use and tool_result blocks under
// Role="assistant". The LLM protocol requires them to be split into separate
// messages: assistant (tool_use only) + tool (tool_result only). This function
// performs that split so the reconstructed history is protocol-compliant.
func (a *Actor) compileMessages(skipCompacted bool) []domain.ChatMessage {
	compiled := a.compileMessagesWithSource(skipCompacted)
	messages := make([]domain.ChatMessage, len(compiled))
	for i, c := range compiled {
		messages[i] = c.msg
	}

	// The summary is intentionally not appended here. It is injected as a
	// user/assistant pair by buildDispatchHistory so the actual LLM request
	// contains exactly one summary representation, and the inspector handlers
	// render it as a dedicated summary segment.

	// Validate: log warning if any tool message lacks a preceding assistant with tool_use.
	for i, msg := range messages {
		if msg.Role != "tool" {
			continue
		}
		var ids []string
		for _, b := range msg.Content {
			if b.Type == domain.ContentBlockToolResult {
				ids = append(ids, b.ToolUseID)
			}
		}
		if len(ids) == 0 {
			slog.Warn("compileMessages: tool message has no tool_result blocks", "idx", i)
			continue
		}
		found := false
		for j := i - 1; j >= 0; j-- {
			if messages[j].Role == "assistant" {
				for _, b := range messages[j].Content {
					if b.Type == domain.ContentBlockToolUse {
						for _, id := range ids {
							if b.ToolUseID == id {
								found = true
							}
						}
					}
				}
				break
			}
		}
		if !found {
			slog.Warn("compileMessages: orphaned tool message — no preceding assistant with matching tool_use",
				"idx", i, "toolUseIds", ids)
		}
	}

	return messages
}

// pastTurnImagePlaceholder replaces an image block from a previous turn in the
// dispatch context. A text block (rather than dropping it) keeps the message
// structurally non-empty and preserves the fact that an image was part of the
// conversation at that position.
const pastTurnImagePlaceholder = "[image from a previous turn omitted]"

// currentImageTurnIDs returns the turn IDs whose steps may keep image blocks in
// the dispatch context: the latest user turn (the current turn's input; while a
// turn starts, ActiveHead points at it) and the active assistant turn (mid-turn
// user_inject steps are anchored to it).
func (a *Actor) currentImageTurnIDs() map[string]bool {
	ids := make(map[string]bool, 2)
	head := int(a.Session.ActiveHead)
	if head < 0 || head >= len(a.Session.Turns) {
		head = len(a.Session.Turns) - 1
	}
	for i := head; i >= 0; i-- {
		if a.Session.Turns[i].Role == "user" {
			ids[a.Session.Turns[i].ID] = true
			break
		}
	}
	if ref := a.getActiveTurnRef(); ref != "" {
		ids[ref] = true
	}
	return ids
}

// compileDispatchMessages builds the LLM dispatch context (turn-start seed and
// post-compaction rebuild). It is compileMessages plus the past-turn image
// rule: images are only meaningful in the turn that carries them; replaying
// them in later turns bloats the request and hard-fails (HTTP 400) when the
// aggregator routes to a text-only model. Past-turn image blocks are replaced
// by a text placeholder; the current turn's images are compiled as-is.
func (a *Actor) compileDispatchMessages() []domain.ChatMessage {
	msgs := a.compileMessages(true)
	if len(msgs) == 0 {
		return msgs
	}
	keepTurns := a.currentImageTurnIDs()
	keepSteps := make(map[string]bool)
	for _, s := range a.steps {
		if keepTurns[s.TurnID] {
			keepSteps[s.ID] = true
		}
	}
	// Past-turn images are dropped from the wire — but recognition results
	// survive: a successfully recognized image keeps its description text
	// (cheap, already paid for) plus the msg:<id> handle for targeted
	// re-recognition; failures and unrecognized images degrade to the
	// placeholder, still carrying the handle.
	for i := range msgs {
		if keepSteps[msgs[i].ID] {
			continue
		}
		hasImage := false
		for _, b := range msgs[i].Content {
			if b.Type == domain.ContentBlockImage {
				hasImage = true
				break
			}
		}
		if !hasImage {
			continue
		}
		blocks := make([]domain.ContentBlock, len(msgs[i].Content))
		copy(blocks, msgs[i].Content)
		for j := range blocks {
			if blocks[j].Type != domain.ContentBlockImage {
				continue
			}
			if b := blocks[j]; b.Recognized && b.RecognitionText != "" {
				// Success carries the [image recognized] marker; a stored
				// failure keeps its explicit marker — both end with the ref.
				blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: recognizedImageWireText(b.RecognitionText, msgs[i].ID, j)}
				continue
			}
			ref := ""
			if msgs[i].ID != "" {
				ref = "\n[image ref: msg:" + msgs[i].ID + ":" + strconv.Itoa(j) + "]"
			}
			blocks[j] = domain.ContentBlock{Type: domain.ContentBlockText, Text: pastTurnImagePlaceholder + ref}
		}
		msgs[i].Content = blocks
	}
	return msgs
}

// hasToolResult returns true if the message contains any tool_result block.
func hasToolResult(msg domain.ChatMessage) bool {
	for _, b := range msg.Content {
		if b.Type == domain.ContentBlockToolResult {
			return true
		}
	}
	return false
}

// splitToolStep splits an assistant message that contains both tool_use and
// tool_result blocks into protocol-compliant separate messages:
//  1. assistant message with non-tool_result blocks
//  2. one "tool" message per tool_result block
func splitToolStep(msg domain.ChatMessage) []domain.ChatMessage {
	var asstBlocks []domain.ContentBlock
	var resultBlocks []domain.ContentBlock
	for _, b := range msg.Content {
		if b.Type == domain.ContentBlockToolResult {
			resultBlocks = append(resultBlocks, b)
		} else {
			asstBlocks = append(asstBlocks, b)
		}
	}
	out := make([]domain.ChatMessage, 0, 1+len(resultBlocks))
	if len(asstBlocks) > 0 {
		out = append(out, domain.ChatMessage{
			ID:               msg.ID,
			Role:             msg.Role,
			Content:          asstBlocks,
			Timestamp:        msg.Timestamp,
			ReasoningContent: msg.ReasoningContent,
		})
	}
	for _, b := range resultBlocks {
		out = append(out, domain.ChatMessage{
			Role:    "tool",
			Content: []domain.ContentBlock{b},
		})
	}
	return out
}

// ── Built-in autonomous tools ──

func (a *Actor) appendAutonomousTools(tools []domain.ToolSpec, cfg domain.AgentKindConfig) []domain.ToolSpec {
	tools = append(tools, agentkit.AutonomousToolsForKind(cfg)...)
	forkSpecs := agentkit.ForkToolSpecsFromBundles(cfg.DefaultBundleIDs)
	if len(forkSpecs) > 0 {
		// Drop the generic "fork_agent" spec that ToolSpecsFromCallables may
		// produce from the bundle's legacy data.tools=fork_agent entry. Also drop
		// "resolve_child_slot" which is an internal callable for workspace's fork
		// spawn and must not appear in child tool lists. Only the intent-revealing
		// aliases (fork_explore/fork_review/fork_general) should reach the LLM.
		filtered := make([]domain.ToolSpec, 0, len(tools)+len(forkSpecs))
		for _, t := range tools {
			if t.CallableID == "fork_agent" || t.CallableID == "resolve_child_slot" {
				continue
			}
			filtered = append(filtered, t)
		}
		tools = filtered
	}
	tools = append(tools, forkSpecs...)
	// The debug/introspection specs are gated on whether the debug bundle is
	// present. The bundle may be mounted at runtime (component_mount) or carried
	// over by the builtin:prompt:debug → builtin:bundle:debug rename migration,
	// neither of which is reflected in DefaultBundleIDs — so union in the
	// agent's actually-mounted cards before checking, making the mount the
	// single source of truth for these specs.
	mountedBundles := append([]string{}, cfg.DefaultBundleIDs...)
	for _, ref := range a.cardRefs {
		mountedBundles = append(mountedBundles, ref.ID)
	}
	// recognize_image is mounted for every primary: text-only models need it
	// to re-recognize with a task-specific prompt (the automatic pre-dispatch
	// pass only produces a context-free baseline), while vision models use it
	// as the escape hatch to inspect an image on demand (local file / MCP
	// marker / generated asset) they otherwise cannot load into their own
	// context. Slots without a pinned unit stay undetermined — the
	// aggregator-side substitution still covers them.
	if !sliceContains(mountedBundles, agentkit.ImageRecognitionBundleID) {
		mountedBundles = append(mountedBundles, agentkit.ImageRecognitionBundleID)
	}
	tools = append(tools, agentkit.DebugToolSpecsFromBundles(mountedBundles)...)
	tools = append(tools, agentkit.MediaToolSpecsFromBundles(mountedBundles)...)
	return tools
}

func sliceContains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// dedupToolsByName removes tools that resolve to the same LLM-facing Name,
// keeping the first occurrence. A repeated entry with an identical CallableID is
// a harmless duplicate; two distinct CallableIDs collapsing to the same Name is
// a real collision and is logged as a warning so misconfiguration surfaces
// without breaking the turn (most LLM providers reject duplicate tool names).
func (a *Actor) dedupToolsByName(ctx actor.Context, tools []domain.ToolSpec) []domain.ToolSpec {
	seen := make(map[string]int, len(tools))
	out := make([]domain.ToolSpec, 0, len(tools))
	for _, t := range tools {
		if idx, ok := seen[t.Name]; ok {
			if out[idx].CallableID != t.CallableID && ctx != nil {
				ctx.Logger().Warn("tool name collision: dropping duplicate tool",
					"name", t.Name,
					"kept", out[idx].CallableID,
					"dropped", t.CallableID)
			}
			continue
		}
		seen[t.Name] = len(out)
		out = append(out, t)
	}
	return out
}

func (a *Actor) handleConfigure(ctx actor.Context, req domain.AgentConfigureReq) error {
	// Invalidate all cached cross-actor data so the next turn start re-fetches
	// fresh values (slot change may affect model unit / window size).
	a.kindConfig.Store(nil)
	a.resolvedInstructions.Store(nil)
	a.componentSnapshot.Store(nil)
	a.cachedProjectRoot.Store(nil)

	newPrimary := derefSlot(req.Primary)
	newFast := derefSlot(req.Fast)
	newExecution := derefSlot(req.Execution)
	newReview := derefSlot(req.Review)
	newSummary := derefSlot(req.Summary)

	// If a turn is currently running and the primary slot's concrete unit
	// changed, stage the unit so the engine picks it up at the next safe
	// judgment window instead of waiting for the next turn.
	if a.activeTurnEngine() != nil {
		oldUnit := slotFirstUnit(a.primary)
		newUnit := slotFirstUnit(newPrimary)
		if !modelUnitEqual(oldUnit, newUnit) && newUnit.Model != "" {
			unit := newUnit
			a.pendingChangeUnit.Store(&unit)
		}
	}

	a.slotMu.Lock()
	a.primary = newPrimary
	a.fast = newFast
	a.execution = newExecution
	a.review = newReview
	a.summary = newSummary
	a.slotMu.Unlock()

	// Explicit user-driven title edit forwarded from workspace.update_agent.
	// Unlike agent.title.set (LLM inference, set-once), this overwrites an
	// existing title and re-persists + notifies so the UI reflects the edit.
	if req.Title != "" {
		title := truncateTitle(req.Title)
		if a.title != title {
			a.title = title
			a.Title = title
			a.saveMailbox(ctx)
			a.notifyWorkspaceStatus(ctx)
		}
	}
	ctx.Logger().Info("agent: reconfigured slots",
		"primaryCands", len(a.primary.Candidates), "fastCands", len(a.fast.Candidates))
	// Re-discover aggregator refs in case the configuration now references
	// newly-spawned named aggregators.
	a.refreshAggRefs(ctx)
	return nil
}

// derefSlot returns the pointed-to slot, or the zero value when nil.
func derefSlot(s *domain.ModelSlot) domain.ModelSlot {
	if s == nil {
		return domain.ModelSlot{}
	}
	return *s
}

func (a *Actor) handleThinkingGetRegistry(_ actor.PureContext) (gen.GetThinkingRegistryResp, error) {
	entries := make([]gen.ThinkingRegistryEntry, 0, len(a.loadThinkingRegistry()))
	for k, v := range a.loadThinkingRegistry() {
		entries = append(entries, gen.ThinkingRegistryEntry{
			Unit:  gen.ModelUnit{Model: k.Model, Provider: k.Provider},
			Level: v,
		})
	}
	return gen.GetThinkingRegistryResp{Entries: entries}, nil
}

func (a *Actor) handleThinkingSetLevel(ctx actor.Context, req gen.SetThinkingLevelReq) (gen.ThinkingLevel, error) {
	key := thinkingUnitKey{Model: req.Unit.Model, Provider: req.Unit.Provider}
	a.storeThinkingLevel(key, req.Level)
	a.saveMailbox(ctx)
	primaryUnit := a.slotFirstUnitResolved(ctx, a.primary)
	if (req.Unit.Model == a.status.Unit.Model && req.Unit.Provider == a.status.Unit.Provider) ||
		(a.status.Unit.Model == "" && req.Unit.Model == primaryUnit.Model && req.Unit.Provider == primaryUnit.Provider) {
		a.activeThinkLevel = req.Level.Mode
		a.notifyWorkspaceStatus(ctx)
	}
	_ = ctx.EmitEvent("thinking_level", map[string]any{
		"Unit":  req.Unit,
		"Level": req.Level,
	})
	return req.Level, nil
}
