// Package mcpmanager is the CRUD manager for embedded MCP client instances.
//
// Topology:
//
//	/mcpmanager                # this actor; holds []McpServerConfig
//	├── /mcpmanager/srv-1      # one MCP client session per persisted config
//	├── /mcpmanager/srv-2
//	└── ...
//
// Instances are spawned/respawned by this actor; the durable record lives
// here, the runtime connection state lives on each mcpinstance child.
//
// Lane layout: the CRUD mutation handlers (add/update/remove_server) run on
// the owner loop — they mutate Servers + persisted state. The routing
// handlers that wait on children (connect/disconnect/call_tool/
// discover_tools) run as stateless (PureContext) handlers on forked
// goroutines, where each child invoke is independently bounded: a slow
// handshake (45s), tools/call (2min), or hung tools list (childInvokeTimeout)
// never parks the manager queue. discover_tools fans out one goroutine per
// server and aggregates, so a single slow child does not delay the other
// servers' tool collection.
//
// Security contract (from schemas/mcp._2600.spore):
//   - McpServerConfig (with full stdio env / http header VALUES) is
//     WRITE-ONLY: it appears in add/update requests and persisted state,
//     never in any callable response.
//   - There is NO gospore component projection of McpServerConfig: the
//     Servers field carries no `gospore:"component"` tag, so the projection
//     store/manifest never see env/header values. The server list is served
//     exclusively by the safe mcp.list_servers view.
//   - All responses use the safe views (McpServerView), which expose only
//     env/header KEY NAMES plus a Configured flag — never values.
//   - mcp.* callables are AdminOnly and require an admin or internal system
//     role at runtime (requireAgentOrHuman): they may carry secrets and must
//     not be reachable by the anonymous role. mcp.list_servers serves the
//     safe view (key names only), so internal system actors — e.g. the
//     project actor's McpExternalCardProvider — may read it; mcp.add_server
//     additionally accepts agent-originated registration (the bundle-use
//     flow: an agent adds a server on the user's behalf) and mcp.reconnect
//     accepts agent-initiated self-healing (a mounted agent re-establishing
//     a dropped server's session); the remaining CRUD
//     and lifecycle callables (update/remove/connect/disconnect) stay
//     strictly admin-gated via requireAdmin.
package mcpmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"

	"github.com/qomos-w/sporemind/pkg/actor/mcpinstance"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// childInvokeTimeout bounds how long the manager waits on a child callable
// before falling back to a "child unreachable" response. Keep small:
// refreshListSnapshot fans out one invoke per persisted server, so a single
// hung child must not stall the whole list.
const childInvokeTimeout = domain.DefaultInvokeTimeout

// childConnectInvokeTimeout is the relaxed budget for connect/disconnect
// invokes. The child's connectTimeout budget is 45s (slow public servers:
// DeepWiki answers the initialized notification after ~17s on cold start);
// the manager must outlive the child handshake or the invoke stream is torn
// down mid-handshake and surfaces to callers as a bare "EOF".
const childConnectInvokeTimeout = 60 * time.Second

// Actor manages a list of McpServerConfig and spawns one mcpinstance child
// per entry. Servers is deliberately NOT a gospore component: each config may
// carry stdio env / http header secret VALUES, so no projection may ever see
// it. All read paths go through the safe views (mcp.list_servers /
// McpServerView), which expose key names only.
type Actor struct {
	actor.Host
	store   persist.Persist
	Servers []domain.McpServerConfig

	actorID       string
	nextID        int64
	mu            sync.Mutex
	childActorIDs map[string]string // server ID -> actor ID

	// listSnapshot caches the result of handleListServers so the PureContext
	// handler can answer without the owner queue or the actor tree. Stateful
	// mutation handlers (and OnStart) rebuild it via refreshListSnapshot. The
	// cached Status reflects each child's state at the last mutation, not
	// live; mcpinstance emits mcp.server_status events on every change, so
	// the frontend gets live updates even though the snapshot is stale until
	// the next mutation.
	listSnapshot atomic.Pointer[mcpListSnapshot]
}

// mcpListSnapshot is the immutable handleListServers result cached for
// PureContext reads.
type mcpListSnapshot struct {
	Items []domain.McpServerView
}

var _ persist.Persistent = (*Actor)(nil)

type managerSnapshot struct {
	Servers []domain.McpServerConfig `json:"servers"`
	NextID  int64                    `json:"nextId"`
}

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("mcpmanager"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.childActorIDs = make(map[string]string)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("mcpmanager: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) Type() string { return "mcpmanager" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("mcpmanager: starting", "id", a.actorID, "servers", len(a.Servers))

	if err := ctx.Register("mcp.list_servers", a.handleListServers, actor.AdminOnly(),
		actor.WithDescription("List every MCP server configured in the system, with id, name, transport, enabled flag, and live status (connected, toolCount, error). Safe view: env/header key names only, never values. Each server is mountable on an agent as the bundle card mcp:<server-id>."),
	); err != nil {
		return fmt.Errorf("mcpmanager: register list_servers: %w", err)
	}
	if err := ctx.Register("mcp.add_server", a.handleAddServer, actor.AdminOnly(),
		actor.WithEffect(string(domain.EffectReversible)),
		actor.WithDescription("Register a new MCP server in the system registry. The server auto-connects when Enabled is true, after which its tools become discoverable and it appears as the mountable bundle card mcp:<server-id>. Secrets passed in Stdio.Env / Http.Headers are write-only: accepted here, never returned by any callable. Only add a server when the user asks for it."),
	); err != nil {
		return fmt.Errorf("mcpmanager: register add_server: %w", err)
	}
	if err := ctx.Register("mcp.update_server", a.handleUpdateServer, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register update_server: %w", err)
	}
	if err := ctx.Register("mcp.remove_server", a.handleRemoveServer, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register remove_server: %w", err)
	}
	if err := ctx.Register("mcp.connect", a.handleConnect, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register connect: %w", err)
	}
	if err := ctx.Register("mcp.disconnect", a.handleDisconnect, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register disconnect: %w", err)
	}
	if err := ctx.Register("mcp.reconnect", a.handleReconnect, actor.AdminOnly(),
		actor.WithDescription("Force-reconnect an MCP server: tear down any live session, reset the auto-reconnect budget, and establish a fresh connection (a plain connect is a no-op while the instance still reports connected). Agent-facing so a mounted agent can self-heal a dropped server; returns the live status."),
	); err != nil {
		return fmt.Errorf("mcpmanager: register reconnect: %w", err)
	}
	if err := ctx.Register("mcp.call_tool", a.handleCallTool, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register call_tool: %w", err)
	}
	if err := ctx.Register("mcp.discover_tools", a.handleDiscoverTools, actor.AdminOnly()); err != nil {
		return fmt.Errorf("mcpmanager: register discover_tools: %w", err)
	}
	if err := ctx.Register("mcp.internal_server_status_changed", a.handleServerStatusChanged, actor.Internal()); err != nil {
		return fmt.Errorf("mcpmanager: register internal_server_status_changed: %w", err)
	}

	if err := ctx.RegisterDomain("mcp").Expose(); err != nil {
		return fmt.Errorf("mcpmanager: expose: %w", err)
	}

	// Register the status event kind here too so the manifest always carries
	// it even when no server has been added yet (children only exist once a
	// config is persisted). Each actor has its own event registry, so the
	// duplicate registration on mcpinstance is independent and harmless.
	if err := ctx.RegisterEventKind(mcpinstance.EventKind, domain.McpServerStatusEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("mcpmanager: register event %s: %w", mcpinstance.EventKind, err)
	}

	// Re-spawn persisted children so the tree survives process restart.
	for _, cfg := range a.Servers {
		props := actor.PropsFromFunc(mcpinstance.NewActor(cfg)).WithAsyncStart()
		if spawned, err := ctx.Spawn(props, cfg.ID); err != nil {
			ctx.Logger().Error("mcpmanager: re-spawn mcpinstance failed", "id", cfg.ID, "error", err)
		} else {
			a.mu.Lock()
			a.childActorIDs[cfg.ID] = spawned.ID().String()
			a.mu.Unlock()
			ctx.Logger().Info("mcpmanager: re-spawned mcpinstance", "id", cfg.ID, "name", cfg.Name)
		}
	}
	a.refreshListSnapshot(ctx)
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	return a.Save()
}

// Save snapshots Servers + nextID. Caller may hold a.mu (Save acquires it
// internally too, but only briefly to copy the slice).
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("mcpmanager"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	snapshot := managerSnapshot{
		Servers: append([]domain.McpServerConfig(nil), a.Servers...),
		NextID:  a.nextID,
	}
	a.mu.Unlock()
	return a.store.Save(a.actorID, snapshot)
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("mcpmanager"))
		if err != nil {
			return err
		}
	}
	var snapshot managerSnapshot
	if err := persist.LoadOrZero(a.store, a.actorID, &snapshot); err != nil {
		return err
	}
	a.Servers = snapshot.Servers
	a.nextID = snapshot.NextID

	// Defensive: rehydrate nextID from max existing ID in case the counter
	// got out of sync with the list (e.g. mid-write crash).
	var maxN int64 = -1
	for _, c := range a.Servers {
		var n int64
		if _, err := fmt.Sscanf(c.ID, "srv-%d", &n); err == nil && n > maxN {
			maxN = n
		}
	}
	if a.nextID <= maxN {
		a.nextID = maxN + 1
	}
	return nil
}

func (a *Actor) saveOrLog(ctx actor.Context) {
	if err := a.Save(); err != nil {
		ctx.Logger().Error("mcpmanager: save state failed", "error", err)
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleListServers serves the safe server view snapshot (McpServerView:
// env/header key names only, never values). The gate mirrors
// requireAgentOrHuman (same as mcp.discover_tools): user-facing web roles and
// internal system actors (project external cards) may read the list; the
// explicit anonymous web role is denied.
func (a *Actor) handleListServers(ctx actor.PureContext) (domain.McpListServersResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.McpListServersResp{}, err
	}
	if s := a.listSnapshot.Load(); s != nil {
		return domain.McpListServersResp{Items: s.Items}, nil
	}
	// Defensive fallback before the first stateful refresh (OnStart always
	// refreshes, so this only covers an un-started actor).
	return domain.McpListServersResp{}, nil
}

// handleAddServer registers a new MCP server config and spawns its child.
// The gate is requireAgentOrHuman (not requireAdmin): agent-initiated
// registration is a supported flow — an agent with the bundle-use bundle
// mounts mcp.add_server and adds a server on the user's behalf, and internal
// actor-to-actor calls arrive with a zero/system identity. The security
// contract is unchanged where it matters: the anonymous web role is still
// denied, and McpServerConfig stays write-only (values never appear in any
// response).
func (a *Actor) handleAddServer(ctx actor.Context, req domain.McpAddServerReq) (domain.McpAddServerResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.McpAddServerResp{}, err
	}
	cfg := req.Config
	if cfg.Name == "" {
		return domain.McpAddServerResp{}, fmt.Errorf("mcp.add_server: name is required")
	}
	if err := mcpinstance.ValidateConfig(cfg); err != nil {
		return domain.McpAddServerResp{}, fmt.Errorf("mcp.add_server: %w", err)
	}

	a.mu.Lock()
	for _, existing := range a.Servers {
		if existing.Name == cfg.Name {
			a.mu.Unlock()
			return domain.McpAddServerResp{}, fmt.Errorf("mcp.add_server: name %q already exists", cfg.Name)
		}
	}
	cfg.ID = fmt.Sprintf("srv-%d", a.nextID)
	a.Servers = append(a.Servers, cfg)
	a.nextID++
	a.mu.Unlock()
	a.saveOrLog(ctx)

	props := actor.PropsFromFunc(mcpinstance.NewActor(cfg)).WithAsyncStart()
	spawned, err := ctx.Spawn(props, cfg.ID)
	if err != nil {
		// Spawn failed: roll back the persisted record so we don't leave an
		// orphan entry that will fail again on every restart.
		a.mu.Lock()
		for i, c := range a.Servers {
			if c.ID == cfg.ID {
				a.Servers = append(a.Servers[:i], a.Servers[i+1:]...)
				break
			}
		}
		a.mu.Unlock()
		a.saveOrLog(ctx)
		return domain.McpAddServerResp{}, fmt.Errorf("mcp.add_server: spawn mcpinstance: %w", err)
	}
	a.mu.Lock()
	a.childActorIDs[cfg.ID] = spawned.ID().String()
	a.mu.Unlock()
	ctx.Logger().Info("mcp.add_server", "id", cfg.ID, "name", cfg.Name, "transport", cfg.Transport)
	a.refreshListSnapshot(ctx)
	return domain.McpAddServerResp{Server: a.composeView(ctx, cfg)}, nil
}

func (a *Actor) handleRemoveServer(ctx actor.Context, req domain.McpRemoveServerReq) (domain.McpRemoveServerResp, error) {
	if err := requireAdmin(ctx.Identity().Role); err != nil {
		return domain.McpRemoveServerResp{}, err
	}
	a.mu.Lock()
	var removed domain.McpServerConfig
	var found bool
	for i, c := range a.Servers {
		if c.ID == req.ID {
			removed = c
			a.Servers = append(a.Servers[:i], a.Servers[i+1:]...)
			found = true
			break
		}
	}
	a.mu.Unlock()
	if !found {
		return domain.McpRemoveServerResp{}, fmt.Errorf("mcp.remove_server: server %q not found", req.ID)
	}
	a.saveOrLog(ctx)

	// Stop the spawned child if it exists.
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[removed.ID]
	a.mu.Unlock()
	if ok {
		canonical, err := identity.ParseCanonicalID(actorIDStr)
		if err == nil {
			if childRef, ok := ctx.LookupID(id.From(canonical)); ok && childRef != nil {
				_ = ctx.Destroy(childRef)
			}
		}
		a.mu.Lock()
		delete(a.childActorIDs, removed.ID)
		a.mu.Unlock()
	}
	ctx.Logger().Info("mcp.remove_server", "id", removed.ID, "name", removed.Name)
	a.refreshListSnapshot(ctx)
	return domain.McpRemoveServerResp{}, nil
}

// handleUpdateServer replaces a persisted McpServerConfig and pushes the new
// config to the running child via the Internal mcpinstance.configure
// callable.
//
// Secret preservation rule: the Public views strip env/header values, so the
// edit UI literally cannot resubmit them. An env/header key that is absent
// from the request (or present with an empty value) therefore keeps its
// previously stored value — mirrors the frpmanager token rule. Removing a key
// requires remove + re-add.
func (a *Actor) handleUpdateServer(ctx actor.Context, req domain.McpUpdateServerReq) (domain.McpUpdateServerResp, error) {
	if err := requireAdmin(ctx.Identity().Role); err != nil {
		return domain.McpUpdateServerResp{}, err
	}
	a.mu.Lock()
	idx := -1
	for i, c := range a.Servers {
		if c.ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.McpUpdateServerResp{}, fmt.Errorf("mcp.update_server: server %q not found", req.ID)
	}
	existing := a.Servers[idx]

	cfg := req.Config
	cfg.ID = existing.ID
	if cfg.Name == "" {
		a.mu.Unlock()
		return domain.McpUpdateServerResp{}, fmt.Errorf("mcp.update_server: name is required")
	}
	for i, other := range a.Servers {
		if i != idx && other.Name == cfg.Name {
			a.mu.Unlock()
			return domain.McpUpdateServerResp{}, fmt.Errorf("mcp.update_server: name %q already exists", cfg.Name)
		}
	}
	preserveSecrets(&cfg, existing)
	if err := mcpinstance.ValidateConfig(cfg); err != nil {
		a.mu.Unlock()
		return domain.McpUpdateServerResp{}, fmt.Errorf("mcp.update_server: %w", err)
	}
	a.Servers[idx] = cfg
	a.mu.Unlock()
	a.saveOrLog(ctx)

	a.pushConfigure(ctx, cfg)
	ctx.Logger().Info("mcp.update_server", "id", cfg.ID, "name", cfg.Name)
	a.refreshListSnapshot(ctx)
	return domain.McpUpdateServerResp{Server: a.composeView(ctx, cfg)}, nil
}

// handleConnect routes to the child's Internal connect callable.
//
// Stateless (PureContext) routing handler: the manager mutates no owner-lane
// state here — findServer is an a.mu-guarded read, the list snapshot is an
// atomic store — so the 60s connect budget runs on a forked goroutine and
// cannot park the owner queue behind one slow handshake.
func (a *Actor) handleConnect(ctx actor.PureContext, req domain.McpConnectReq) (domain.McpConnectResp, error) {
	if err := requireAdmin(ctx.Identity().Role); err != nil {
		return domain.McpConnectResp{}, err
	}
	cfg, ok := a.findServer(req.ID)
	if !ok {
		return domain.McpConnectResp{}, fmt.Errorf("mcp.connect: server %q not found", req.ID)
	}
	status, err := a.invokeChildStatus(ctx, cfg, "mcpinstance.connect", nil)
	if err != nil {
		return domain.McpConnectResp{}, fmt.Errorf("mcp.connect: %w", err)
	}
	ctx.Logger().Info("mcp.connect", "id", cfg.ID, "name", cfg.Name)
	a.refreshListSnapshot(ctx)
	return domain.McpConnectResp{Status: status}, nil
}

// handleDisconnect routes to the child's Internal disconnect callable.
// Stateless (PureContext) for the same reason as handleConnect.
func (a *Actor) handleDisconnect(ctx actor.PureContext, req domain.McpDisconnectReq) (domain.McpDisconnectResp, error) {
	if err := requireAdmin(ctx.Identity().Role); err != nil {
		return domain.McpDisconnectResp{}, err
	}
	cfg, ok := a.findServer(req.ID)
	if !ok {
		return domain.McpDisconnectResp{}, fmt.Errorf("mcp.disconnect: server %q not found", req.ID)
	}
	status, err := a.invokeChildStatus(ctx, cfg, "mcpinstance.disconnect", nil)
	if err != nil {
		return domain.McpDisconnectResp{}, fmt.Errorf("mcp.disconnect: %w", err)
	}
	ctx.Logger().Info("mcp.disconnect", "id", cfg.ID, "name", cfg.Name)
	a.refreshListSnapshot(ctx)
	return domain.McpDisconnectResp{Status: status}, nil
}

// handleReconnect routes to the child's Internal reconnect callable.
// Agent-facing (requireAgentOrHuman): the hot-context MCP status block tells
// every mounted agent which mcp:<server-id> cards it carries and instructs it
// to self-heal a dropped server with this callable; the anonymous web role is
// still denied. Stateless (PureContext) for the same reason as
// handleConnect: the teardown + handshake budget runs on a forked goroutine.
func (a *Actor) handleReconnect(ctx actor.PureContext, req domain.McpReconnectReq) (domain.McpReconnectResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.McpReconnectResp{}, err
	}
	cfg, ok := a.findServer(req.ID)
	if !ok {
		return domain.McpReconnectResp{}, fmt.Errorf("mcp.reconnect: server %q not found", req.ID)
	}
	status, err := a.invokeChildStatus(ctx, cfg, "mcpinstance.reconnect", nil)
	if err != nil {
		return domain.McpReconnectResp{}, fmt.Errorf("mcp.reconnect: %w", err)
	}
	ctx.Logger().Info("mcp.reconnect", "id", cfg.ID, "name", cfg.Name, "connected", status.Connected)
	a.refreshListSnapshot(ctx)
	return domain.McpReconnectResp{Status: status}, nil
}

// handleCallTool routes to the child's Internal call_tool callable.
// Agent-facing: the relaxed requireAgentOrHuman gate lets the agent turn
// engine (an internal actor with zero identity) execute mcp.<server>.<tool>
// tools; the anonymous web role is still denied.
//
// Stateless (PureContext) routing handler: a 2min tools/call must not park
// the owner queue, where CRUD and status fan-outs answer.
func (a *Actor) handleCallTool(ctx actor.PureContext, req domain.McpCallToolReq) (domain.McpCallToolResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.McpCallToolResp{}, err
	}
	cfg, ok := a.findServer(req.ID)
	if !ok {
		return domain.McpCallToolResp{}, fmt.Errorf("mcp.call_tool: server %q not found", req.ID)
	}
	resp, err := a.invokeChildCallTool(ctx, cfg, req)
	if err != nil {
		return domain.McpCallToolResp{}, fmt.Errorf("mcp.call_tool: %w", err)
	}
	return resp, nil
}

// handleDiscoverTools returns each server's cached tool list for agent tool
// injection (resolveTools synthesizes mcp.<server>.<tool> ToolSpecs from it).
// Only servers whose child is reachable with a cached tool list contribute;
// unconnected/unreachable servers are skipped. Agent-facing: internal actor
// callers (zero identity) plus human roles; the anonymous web role is denied.
//
// Stateless (PureContext) handler: fans out one goroutine per server so a
// single slow/hung child (up to childInvokeTimeout) does not delay the whole
// catalog or block the owner queue.
func (a *Actor) handleDiscoverTools(ctx actor.PureContext) (domain.McpDiscoverToolsResp, error) {
	if err := requireAgentOrHuman(ctx.Identity().Role); err != nil {
		return domain.McpDiscoverToolsResp{}, err
	}
	a.mu.Lock()
	snap := append([]domain.McpServerConfig(nil), a.Servers...)
	a.mu.Unlock()

	if len(snap) == 0 {
		return domain.McpDiscoverToolsResp{}, nil
	}

	results := make([]domain.McpServerTools, len(snap))
	var wg sync.WaitGroup
	for i, cfg := range snap {
		wg.Add(1)
		go func(i int, cfg domain.McpServerConfig) {
			defer wg.Done()
			tools, err := a.invokeChildTools(ctx, cfg)
			if err != nil {
				return // not connected / child unreachable: skip this server
			}
			results[i] = tools
		}(i, cfg)
	}
	wg.Wait()

	out := make([]domain.McpServerTools, 0, len(snap))
	for _, r := range results {
		if r.ID != "" {
			out = append(out, r)
		}
	}
	return domain.McpDiscoverToolsResp{Servers: out}, nil
}

// handleServerStatusChanged receives the child→parent status notification
// fired by mcpinstance on every connection-state or tool-list change and
// pushes a tools_refresh_notify to every loaded agent that mounts the
// affected mcp:<server-id> bundle card. Stateless (PureContext): the only
// mutation is an atomic cache store (refreshListSnapshot) and the agent
// fan-out already runs on a detached goroutine, so this notification runs on
// the forked pure loop and never parks the manager's owner lane.
func (a *Actor) handleServerStatusChanged(ctx actor.PureContext, ev domain.McpServerStatusEvent) {
	serverID := ev.Status.ID
	// Debug level: reconnect loops emit a status event per attempt, so Info
	// would flood the log during a server outage.
	ctx.Logger().Debug("mcpmanager: server status changed",
		"id", serverID, "connected", ev.Status.Connected, "tools", ev.Status.ToolCount)
	a.refreshListSnapshot(ctx)
	if serverID == "" {
		return
	}
	wsRef, ok := ctx.LookupService("workspace")
	if !ok || wsRef == nil {
		return
	}
	lookupID := ctx.LookupID
	lifecycle := ctx.Lifecycle()
	go a.notifyMountedAgents(lifecycle, wsRef, lookupID, serverID)
}

// notifyMountedAgents finds every loaded agent that mounts mcp:<server-id>
// and calls tools_refresh_notify on it, flagging the active turn engine to
// re-resolve its tool surface at the next safe judgment window. Agents that
// are not loaded have no live actor and cannot be running a turn; their next
// turn resolves MCP tools fresh anyway (resolveMCPTools queries discover_tools
// per turn), so they are skipped rather than lazy-loaded.
func (a *Actor) notifyMountedAgents(lifecycle context.Context, wsRef ref.Ref, lookupID func(id.ActorID) (ref.Ref, bool), serverID string) {
	listCtx, cancel := context.WithTimeout(lifecycle, childInvokeTimeout)
	defer cancel()
	call := wsRef.Invoke(listCtx, "workspace.list_agents", domain.WorkspaceListAgentsReq{})
	if call == nil {
		return
	}
	value, err := call.Final(listCtx)
	call.Close()
	if err != nil {
		return
	}
	var list domain.AgentRefListResp
	switch v := value.(type) {
	case domain.AgentRefListResp:
		list = v
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &list)
	}
	for _, ag := range list.Items {
		if ag.ActorID == "" {
			continue
		}
		if ag.LoadState != "" && ag.LoadState != "loaded" {
			continue
		}
		cid, err := identity.ParseCanonicalID(ag.ActorID)
		if err != nil {
			continue
		}
		agentRef, ok := lookupID(id.From(cid))
		if !ok || agentRef == nil {
			continue
		}
		if !a.agentMountsMCPServer(lifecycle, agentRef, serverID) {
			continue
		}
		a.notifyAgentToolsRefresh(lifecycle, agentRef, serverID)
	}
}

// agentMountsMCPServer reports whether the agent currently has an enabled
// mcp:<server-id> component mount, checked via the agent's public
// component_list callable. Mirrors the agent-side mountedMCPServerIDs /
// mcpCardServerID semantics so only agents that would actually see the
// server's tools are refreshed.
func (a *Actor) agentMountsMCPServer(lifecycle context.Context, agentRef ref.Ref, serverID string) bool {
	callCtx, cancel := context.WithTimeout(lifecycle, childInvokeTimeout)
	defer cancel()
	call := agentRef.Invoke(callCtx, "component_list", domain.AgentComponentListReq{})
	if call == nil {
		return false
	}
	value, err := call.Final(callCtx)
	call.Close()
	if err != nil {
		return false
	}
	var resp domain.AgentComponentListResp
	switch v := value.(type) {
	case domain.AgentComponentListResp:
		resp = v
	default:
		body, _ := json.Marshal(v)
		_ = json.Unmarshal(body, &resp)
	}
	for _, m := range resp.Items {
		if !m.Enabled {
			continue
		}
		if strings.HasPrefix(m.CardID, "mcp:") && strings.TrimPrefix(m.CardID, "mcp:") == serverID {
			return true
		}
	}
	return false
}

// notifyAgentToolsRefresh calls the agent's AdminOnly tools_refresh_notify
// callable (the same chain appmanager uses) so the agent stages a tool
// surface re-resolution. The payload JSON shape mirrors
// agent.AgentToolsRefreshNotifyReq (AppId/Reason); the typed type lives in
// the agent package, so mcpmanager sends a structural twin to avoid importing
// the whole agent package.
func (a *Actor) notifyAgentToolsRefresh(lifecycle context.Context, agentRef ref.Ref, serverID string) {
	notifyCtx, cancel := context.WithTimeout(lifecycle, childInvokeTimeout)
	defer cancel()
	payload := struct {
		AppID  string `json:"AppId"`
		Reason string `json:"Reason,omitempty"`
	}{AppID: serverID, Reason: "mcp.status_changed"}
	call := agentRef.Invoke(notifyCtx, "tools_refresh_notify", payload)
	if call != nil {
		call.Close()
	}
}

// ---------------------------------------------------------------------------
// Child routing
// ---------------------------------------------------------------------------

func (a *Actor) findServer(id string) (domain.McpServerConfig, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.Servers {
		if c.ID == id {
			return c, true
		}
	}
	return domain.McpServerConfig{}, false
}

// childRef resolves the spawned child actor ref for a server ID. Read of the
// childActorIDs map is a.mu-guarded: the stateless routing handlers read it
// from forked goroutines while owner-lane CRUD mutates it.
func (a *Actor) childRef(ctx actor.PureContext, serverID string) (ref.Ref, bool) {
	a.mu.Lock()
	actorIDStr, ok := a.childActorIDs[serverID]
	a.mu.Unlock()
	if !ok {
		return nil, false
	}
	canonical, err := identity.ParseCanonicalID(actorIDStr)
	if err != nil {
		return nil, false
	}
	childRef, ok := ctx.LookupID(id.From(canonical))
	if !ok || childRef == nil {
		return nil, false
	}
	return childRef, true
}

// invokeChildCall runs one unary callable on the child and returns the raw
// decoded value. Bounded by childInvokeTimeout so a hung child cannot stall
// the manager queue; connect/disconnect get the relaxed handshake budget.
// Takes a PureContext (the minimal surface it needs — Lifecycle + LookupID)
// so both stateful owner-lane handlers and stateless fan-out goroutines share
// this path; actor.Context is assignable to actor.PureContext.
func (a *Actor) invokeChildCall(ctx actor.PureContext, cfg domain.McpServerConfig, callID string, payload any) (any, error) {
	childRef, ok := a.childRef(ctx, cfg.ID)
	if !ok {
		return nil, fmt.Errorf("child for server %q is not running", cfg.ID)
	}
	budget := childInvokeTimeout
	if callID == "mcpinstance.connect" || callID == "mcpinstance.disconnect" || callID == "mcpinstance.reconnect" {
		budget = childConnectInvokeTimeout
	}
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), budget)
	defer cancel()
	call := childRef.Invoke(invokeCtx, callID, payload)
	if call == nil {
		return nil, fmt.Errorf("invoke %s on server %q returned no call", callID, cfg.ID)
	}
	value, err := call.Final(invokeCtx)
	if err != nil {
		return nil, fmt.Errorf("invoke %s on server %q: %w", callID, cfg.ID, err)
	}
	if value == nil {
		return nil, fmt.Errorf("invoke %s on server %q returned no value", callID, cfg.ID)
	}
	return value, nil
}

func (a *Actor) invokeChildStatus(ctx actor.PureContext, cfg domain.McpServerConfig, callID string, payload any) (domain.McpServerStatus, error) {
	value, err := a.invokeChildCall(ctx, cfg, callID, payload)
	if err != nil {
		return domain.McpServerStatus{}, err
	}
	status, ok := decodeStatus(value)
	if !ok {
		return domain.McpServerStatus{}, fmt.Errorf("invoke %s on server %q returned unexpected payload", callID, cfg.ID)
	}
	return status, nil
}

func (a *Actor) invokeChildCallTool(ctx actor.PureContext, cfg domain.McpServerConfig, req domain.McpCallToolReq) (domain.McpCallToolResp, error) {
	value, err := a.invokeChildCall(ctx, cfg, "mcpinstance.call_tool", req)
	if err != nil {
		return domain.McpCallToolResp{}, err
	}
	resp, ok := decodeCallToolResp(value)
	if !ok {
		return domain.McpCallToolResp{}, fmt.Errorf("invoke mcpinstance.call_tool on server %q returned unexpected payload", cfg.ID)
	}
	return resp, nil
}

// invokeChildTools fetches the child's cached tool list for agent tool
// injection. Bounded by childInvokeTimeout like every other child invoke.
func (a *Actor) invokeChildTools(ctx actor.PureContext, cfg domain.McpServerConfig) (domain.McpServerTools, error) {
	value, err := a.invokeChildCall(ctx, cfg, "mcpinstance.tools", nil)
	if err != nil {
		return domain.McpServerTools{}, err
	}
	tools, ok := decodeServerTools(value)
	if !ok {
		return domain.McpServerTools{}, fmt.Errorf("invoke mcpinstance.tools on server %q returned unexpected payload", cfg.ID)
	}
	return tools, nil
}

// pushConfigure fires the Internal mcpinstance.configure callable at the
// child for cfg.ID. If the child isn't reachable, the manager logs a warning
// and returns: the durable record on disk is the source of truth, but a
// missed configure means runtime state has drifted from the persisted policy
// until the child is re-spawned (e.g. on restart) or this is retried.
func (a *Actor) pushConfigure(ctx actor.Context, cfg domain.McpServerConfig) {
	childRef, ok := a.childRef(ctx, cfg.ID)
	if !ok {
		ctx.Logger().Warn("mcpmanager: configure push skipped, child unreachable — runtime may diverge from persisted state",
			"id", cfg.ID)
		return
	}
	invokeCtx, cancel := context.WithTimeout(ctx.Lifecycle(), childInvokeTimeout)
	defer cancel()
	call := childRef.Invoke(invokeCtx, "mcpinstance.configure", cfg)
	if call != nil {
		_ = call.Close()
	}
}

// ---------------------------------------------------------------------------
// Snapshot / view composition
// ---------------------------------------------------------------------------

// refreshListSnapshot rebuilds the cached list result by fanning out one
// status invoke per child and stores it atomically. Called from stateful
// mutation handlers, the stateless routing handlers, and OnStart so the
// PureContext handleListServers can return a consistent snapshot without
// the owner queue or the actor tree. The per-child composeView fan-out is
// parallel (indexed writes, WaitGroup): one wedged child bounds the rebuild
// by its own childInvokeTimeout instead of stalling the caller for the sum
// of N timeouts.
func (a *Actor) refreshListSnapshot(ctx actor.PureContext) {
	a.mu.Lock()
	snap := append([]domain.McpServerConfig(nil), a.Servers...)
	a.mu.Unlock()

	out := make([]domain.McpServerView, len(snap))
	var wg sync.WaitGroup
	for i, cfg := range snap {
		wg.Add(1)
		go func(i int, cfg domain.McpServerConfig) {
			defer wg.Done()
			out[i] = a.composeView(ctx, cfg)
		}(i, cfg)
	}
	wg.Wait()
	a.listSnapshot.Store(&mcpListSnapshot{Items: out})
}

// composeView assembles the wire-level McpServerView for a persisted cfg. It
// looks up the child actor and asks it for live status; if the child isn't
// reachable (e.g. unit test, or child still booting), it falls back to a
// "not connected" status derived from the persisted cfg. In all paths the
// env/header values are stripped — the view carries key names only.
func (a *Actor) composeView(ctx actor.PureContext, cfg domain.McpServerConfig) domain.McpServerView {
	view := viewOf(cfg)
	view.Status = domain.McpServerStatus{ID: cfg.ID}

	status, err := a.invokeChildStatus(ctx, cfg, "mcpinstance.status", nil)
	if err != nil {
		return view // fallback: not connected
	}
	view.Status = status
	return view
}

// viewOf converts the write-side config into the read-safe view: env and
// header maps become key-name entries with a Configured flag; values are
// never copied.
func viewOf(cfg domain.McpServerConfig) domain.McpServerView {
	view := domain.McpServerView{
		ID:        cfg.ID,
		Name:      cfg.Name,
		Transport: cfg.Transport,
		Enabled:   cfg.Enabled,
	}
	if cfg.Stdio != nil {
		view.Stdio = &domain.McpStdioTransportView{
			Command: cfg.Stdio.Command,
			Args:    append([]string(nil), cfg.Stdio.Args...),
			Env:     envEntries(cfg.Stdio.Env),
		}
	}
	if cfg.Http != nil {
		view.Http = &domain.McpHttpTransportView{
			URL:     cfg.Http.URL,
			Headers: envEntries(cfg.Http.Headers),
			Proxy:   cfg.Http.Proxy,
		}
	}
	return view
}

func envEntries(m map[string]string) []domain.McpEnvVarEntry {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]domain.McpEnvVarEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, domain.McpEnvVarEntry{Key: k, Configured: true})
	}
	return out
}

// preserveSecrets carries env/header values from the prior persisted record
// into cfg for keys the request omitted or left blank. See handleUpdateServer
// for the rationale (Public views strip values, so the UI cannot resubmit
// them).
func preserveSecrets(cfg *domain.McpServerConfig, prior domain.McpServerConfig) {
	if cfg.Stdio != nil && prior.Stdio != nil {
		cfg.Stdio.Env = mergeKeep(prior.Stdio.Env, cfg.Stdio.Env)
	}
	if cfg.Http != nil && prior.Http != nil {
		cfg.Http.Headers = mergeKeep(prior.Http.Headers, cfg.Http.Headers)
	}
}

// mergeKeep returns the new map with values for keys that existed in old but
// are absent (or empty) in new filled back from old.
func mergeKeep(old, new map[string]string) map[string]string {
	if len(old) == 0 {
		return new
	}
	out := make(map[string]string, len(old)+len(new))
	for k, v := range old {
		out[k] = v
	}
	for k, v := range new {
		if v == "" {
			if _, had := old[k]; had {
				continue // blank in request → keep stored value
			}
		}
		out[k] = v
	}
	if out == nil {
		out = map[string]string{}
	}
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// requireAdmin enforces the runtime half of the schema contract: mcp.*
// callables must not be reachable by the anonymous role. Takes the caller's
// role so both stateful (actor.Context) and stateless (actor.PureContext)
// handlers can use it.
func requireAdmin(role id.Role) error {
	return policy.RequireAdmin(role)
}

// requireAgentOrHuman allows the agent-facing callables to be reached by:
//   - admin/developer web roles (frontgate explicit caller_role header)
//   - internal system actors (role "system" / zero identity) — the agent
//     turn engine routes mcp.<server>.<tool> calls through the mcpmanager
//     service ref, which injects the manager's own "system" role onto every
//     internal call (appRef default caller role).
//
// The explicit anonymous web role ("anonymous") is still denied. Applies to
// the agent-facing callables (mcp.discover_tools, mcp.call_tool,
// mcp.list_servers, mcp.reconnect) and to mcp.add_server, which accepts
// agent-originated registration (the bundle-use flow: an agent mounts
// mcp.add_server and adds a server on the user's behalf). The remaining CRUD
// and lifecycle callables (update/remove/connect/disconnect) stay strictly
// admin-gated via requireAdmin.
func requireAgentOrHuman(role id.Role) error {
	if role == "" {
		return nil
	}
	return policy.RequireAgentOrHuman(role)
}

func decodeStatus(v any) (domain.McpServerStatus, bool) {
	switch x := v.(type) {
	case domain.McpServerStatus:
		return x, true
	case *domain.McpServerStatus:
		if x != nil {
			return *x, true
		}
		return domain.McpServerStatus{}, false
	case []byte:
		if len(x) == 0 {
			return domain.McpServerStatus{}, false
		}
		var s domain.McpServerStatus
		if err := json.Unmarshal(x, &s); err != nil {
			return domain.McpServerStatus{}, false
		}
		return s, true
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.McpServerStatus{}, false
		}
		var s domain.McpServerStatus
		if err := json.Unmarshal(body, &s); err != nil {
			return domain.McpServerStatus{}, false
		}
		return s, true
	}
}

// decodeServerTools decodes the raw result of an mcpinstance.tools invoke
// into the typed McpServerTools shape.
func decodeServerTools(v any) (domain.McpServerTools, bool) {
	switch x := v.(type) {
	case domain.McpServerTools:
		return x, true
	case *domain.McpServerTools:
		if x != nil {
			return *x, true
		}
		return domain.McpServerTools{}, false
	case []byte:
		if len(x) == 0 {
			return domain.McpServerTools{}, false
		}
		var s domain.McpServerTools
		if err := json.Unmarshal(x, &s); err != nil {
			return domain.McpServerTools{}, false
		}
		return s, true
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.McpServerTools{}, false
		}
		var s domain.McpServerTools
		if err := json.Unmarshal(body, &s); err != nil {
			return domain.McpServerTools{}, false
		}
		return s, true
	}
}

func decodeCallToolResp(v any) (domain.McpCallToolResp, bool) {
	switch x := v.(type) {
	case domain.McpCallToolResp:
		return x, true
	case *domain.McpCallToolResp:
		if x != nil {
			return *x, true
		}
		return domain.McpCallToolResp{}, false
	case []byte:
		if len(x) == 0 {
			return domain.McpCallToolResp{}, false
		}
		var r domain.McpCallToolResp
		if err := json.Unmarshal(x, &r); err != nil {
			return domain.McpCallToolResp{}, false
		}
		return r, true
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.McpCallToolResp{}, false
		}
		var r domain.McpCallToolResp
		if err := json.Unmarshal(body, &r); err != nil {
			return domain.McpCallToolResp{}, false
		}
		return r, true
	}
}
