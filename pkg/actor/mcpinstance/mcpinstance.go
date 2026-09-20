// Package mcpinstance runs a single MCP client session. One instance per
// persisted McpServerConfig held by mcpmanager.
//
// Lifecycle:
//   - Manager spawns NewActor(cfg) → constructor seeds a.cfg
//   - OnStart auto-connects unless cfg.Enabled == false
//   - connect (Internal) is the manager-owned entrypoint to establish the
//     session (stdio subprocess or Streamable HTTP, initialize handshake,
//     tools/list discovery + cache)
//   - disconnect (Internal) tears the session down
//   - call_tool (Internal) executes one tool through the live session
//   - configure (Internal) replaces cfg and reconciles connection state
//   - status (Public) returns the sanitized McpServerStatus
//
// Lane layout: connect/disconnect/call_tool/configure run on the dedicated
// ExecLoop lane (ModeStateful) because their MCP round trips are bounded by
// connectTimeout (45s handshake) and callTimeout (2min tools/call). status
// and tools stay on the owner loop, so connectivity and cached tool-list
// queries answer instantly even while a long call is in flight.
//
// Every connection state change emits the mcp.server_status event so the
// frontend and the manager can track connectivity without polling.
//
// Secrets: stdio env / http header values live in a.cfg (never exposed as a
// component); the Public status callable only reports Id/Connected/
// ToolCount/Error.
package mcpinstance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qomos-w/gospore/actor"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/util"
)

// Transport kinds (McpServerConfig.Transport).
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// EventKind is the status event emitted on every connection state change.
const EventKind = "mcp.server_status"

// ExecLoop is the dedicated execution lane for the MCP round-trip
// callables (connect/disconnect/call_tool/configure). The initialize
// handshake is bounded by connectTimeout (45s) and a single tools/call by
// callTimeout (2min); running them on the owner loop would park the whole
// instance — status/tools queries and every other callable — behind one slow
// server round trip. The lane is ModeStateful so its handlers keep full
// Context semantics (emitStatus, watcher plumbing) while serializing against
// each other: a disconnect never races a connect into a
// "reconnected-after-user-disconnect" state, and configure cannot swap cfg
// mid-handshake.
const ExecLoop = "mcp_exec"

// ClientName/ClientVersion identify this client in the MCP initialize
// handshake (Implementation). Reported to the server as clientInfo.
const (
	ClientName    = "sporemind"
	ClientVersion = "0.1.0"
)

// connectTimeout bounds the initialize handshake + tools/list discovery so a
// hung server cannot stall the manager queue forever. Public remote servers
// can take ~20s to answer the initialized notification on a cold start
// (observed: mcp.deepwiki.com), so 15s is too tight.
const connectTimeout = 45 * time.Second

// callTimeout bounds a single tools/call round trip.
const callTimeout = 2 * time.Minute

// Auto-reconnect schedule: after an unexpected drop the session watcher retries
// connect with exponential backoff. The backoff starts at reconnectInitialDelay
// and multiplies by reconnectBackoffFactor per attempt, capped at
// reconnectMaxDelay. reconnectMaxAttempts is the per-episode budget; a session
// that stays alive for reconnectStabilityWindow resets the budget.
const (
	reconnectInitialDelay    = 500 * time.Millisecond
	reconnectBackoffFactor   = 1.5
	reconnectMaxDelay        = 30 * time.Second
	reconnectMaxAttempts     = 10
	reconnectStabilityWindow = 60 * time.Second
)

// reconnectPolicy is the tunable auto-reconnect schedule. Production uses the
// package defaults (the zero value resolves via withDefaults); tests shrink
// the delays to milliseconds to exercise the loop quickly.
type reconnectPolicy struct {
	initialDelay time.Duration
	factor       float64
	maxDelay     time.Duration
	maxAttempts  int
	stability    time.Duration
}

func defaultReconnectPolicy() reconnectPolicy {
	return reconnectPolicy{
		initialDelay: reconnectInitialDelay,
		factor:       reconnectBackoffFactor,
		maxDelay:     reconnectMaxDelay,
		maxAttempts:  reconnectMaxAttempts,
		stability:    reconnectStabilityWindow,
	}
}

// withDefaults fills zero fields with the package defaults so a partial
// policy (e.g. tests tuning only the delays) still behaves sanely.
func (p reconnectPolicy) withDefaults() reconnectPolicy {
	if p.initialDelay <= 0 {
		p.initialDelay = reconnectInitialDelay
	}
	if p.factor <= 0 {
		p.factor = reconnectBackoffFactor
	}
	if p.maxDelay <= 0 {
		p.maxDelay = reconnectMaxDelay
	}
	if p.maxAttempts <= 0 {
		p.maxAttempts = reconnectMaxAttempts
	}
	if p.stability <= 0 {
		p.stability = reconnectStabilityWindow
	}
	return p
}

// nextReconnectDelay returns the backoff delay to wait before the given
// 1-based reconnect attempt: initial * factor^(attempt-1), capped at maxDelay.
func (p reconnectPolicy) nextReconnectDelay(attempt int) time.Duration {
	p = p.withDefaults()
	if attempt < 1 {
		attempt = 1
	}
	delay := float64(p.initialDelay)
	for i := 1; i < attempt && delay < float64(p.maxDelay); i++ {
		delay *= p.factor
	}
	if delay > float64(p.maxDelay) {
		delay = float64(p.maxDelay)
	}
	return time.Duration(delay)
}

// notifyTimeout bounds the fire-and-forget parent notification so a hung
// manager (or parent unreachable) does not block the instance goroutine.
const notifyTimeout = 5 * time.Second

// ToolInfo is one cached tools/list entry (name + description + input schema).
// InputSchema is the raw JSON-schema object the server advertised; callers
// pass it through verbatim (research decision: inputSchema 直接透传).
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Actor owns one MCP client session. cfg is seeded by NewActor and replaced
// by handleConfigure; nothing is persisted on disk — the manager holds the
// durable record. All connection state (session, tools cache) is runtime-only.
type Actor struct {
	actor.Host

	log actor.Logger // set in OnStart; used by background tool-refresh

	// statusNotify is fired after every connection-status change (emitStatus)
	// and every successful tool-list refresh (refreshTools) so the parent
	// mcpmanager can flag mounted agents' tool surfaces for refresh. Captured
	// in OnStart via ctx.Parent(); nil in unit tests that construct the actor
	// directly (no parent) or never call emitStatus.
	statusNotify func(ev domain.McpServerStatusEvent)

	// ServerViews is the read-safe projection of this instance published for
	// topology enrichment: identity + transport + live connection state only.
	// Env/header VALUES never leave the actor — the view carries no Stdio/Http
	// payloads at all, not even key names. Kept in sync with cfg+state by
	// refreshViewLocked; the cell publishes it whenever the projection store
	// has subscribers, so the actor graph renders mcp-server nodes without
	// blocking invokes.
	ServerViews []domain.McpServerView `gospore:"component,public"`

	mu        sync.Mutex
	cfg       domain.McpServerConfig
	session   *mcp.ClientSession
	connected bool
	lastErr   string
	tools     []ToolInfo

	// watcherDone is closed when the session-watcher goroutine exits. It lets
	// disconnectLocked wait for a clean teardown without racing the watcher.
	watcherDone chan struct{}

	// refreshGate serializes tool-list refreshes. ToolListChanged notifications
	// arrive on an SDK background goroutine; without a guard a burst of them
	// could run listAllTools concurrently and race each other's cache swap.
	refreshGate sync.Mutex

	// buildTransportFn is the transport-construction hook used by connect. Nil
	// routes to buildTransport (production stdio/http transports); tests replace
	// it to drive in-memory transports without subprocesses or HTTP servers.
	buildTransportFn func(ctx actor.Context, cfg domain.McpServerConfig) (mcp.Transport, error)

	// Auto-reconnect state. reconnect is the backoff schedule (zero value
	// resolves to the package defaults); reconnectDisabled distinguishes an
	// explicit user disconnect (never auto-reconnect) from an unexpected drop;
	// reconnectStop cancels the in-flight loop; reconnectAttempts is the rolling
	// budget; connectedSince lets the watcher detect stability-window resets.
	reconnect         reconnectPolicy
	reconnectDisabled bool
	reconnectStop     chan struct{}
	reconnectAttempts int
	connectedSince    time.Time
}

// NewActor returns a factory closure that captures the seed config.
func NewActor(cfg domain.McpServerConfig) func() actor.Actor {
	return func() actor.Actor {
		return &Actor{cfg: cfg, reconnect: defaultReconnectPolicy()}
	}
}

func (a *Actor) Type() string { return "mcpinstance" }

func (a *Actor) OnStart(ctx actor.Context) error {
	a.log = ctx.Logger()
	ctx.Logger().Info("mcpinstance: starting",
		"id", a.cfg.ID,
		"name", a.cfg.Name,
		"transport", a.cfg.Transport,
		"enabled", a.cfg.Enabled,
	)
	// Bridge every status/tool change to the parent manager so it can push a
	// tools_refresh to agents that mount mcp:<server-id>. Fire-and-forget:
	// the manager reconciles its own snapshot and fans out asynchronously.
	if parent := ctx.Parent(); parent != nil {
		parentRef := parent
		a.statusNotify = func(ev domain.McpServerStatusEvent) {
			notifyCtx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
			defer cancel()
			call := parentRef.Invoke(notifyCtx, "mcp.internal_server_status_changed", ev)
			if call != nil {
				call.Close()
			}
			if a.log != nil {
				a.log.Debug("mcpinstance: notified manager of status change",
					"id", ev.Status.ID, "connected", ev.Status.Connected)
			}
		}
	}
	// Exec lane first: Register validates that any WithLoop target is already
	// declared, so this must precede the connect/disconnect/call_tool/
	// configure registrations below.
	if err := ctx.RegisterLoop(ExecLoop, actor.ModeStateful); err != nil {
		return fmt.Errorf("mcpinstance: register exec loop: %w", err)
	}
	if err := ctx.Register("mcpinstance.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("mcpinstance: register status: %w", err)
	}
	if err := ctx.Register("mcpinstance.connect", a.handleConnect, actor.Internal(), actor.WithLoop(ExecLoop)); err != nil {
		return fmt.Errorf("mcpinstance: register connect: %w", err)
	}
	if err := ctx.Register("mcpinstance.disconnect", a.handleDisconnect, actor.Internal(), actor.WithLoop(ExecLoop)); err != nil {
		return fmt.Errorf("mcpinstance: register disconnect: %w", err)
	}
	if err := ctx.Register("mcpinstance.call_tool", a.handleCallTool, actor.Internal(), actor.WithLoop(ExecLoop)); err != nil {
		return fmt.Errorf("mcpinstance: register call_tool: %w", err)
	}
	if err := ctx.Register("mcpinstance.tools", a.handleTools, actor.Internal()); err != nil {
		return fmt.Errorf("mcpinstance: register tools: %w", err)
	}
	if err := ctx.Register("mcpinstance.configure", a.handleConfigure, actor.Internal(), actor.WithLoop(ExecLoop)); err != nil {
		return fmt.Errorf("mcpinstance: register configure: %w", err)
	}
	if err := ctx.RegisterEventKind(EventKind, domain.McpServerStatusEvent{}, actor.Public()); err != nil {
		return fmt.Errorf("mcpinstance: register event %s: %w", EventKind, err)
	}
	// Baseline projection so topology sees the instance even before the first
	// connect attempt (e.g. a disabled server) or when auto-connect fails.
	a.mu.Lock()
	a.refreshViewLocked()
	a.mu.Unlock()
	if !a.cfg.Enabled {
		return nil
	}
	if err := a.connect(ctx); err != nil {
		// The server may simply not be up yet (restart ordering); retry with
		// the standard reconnect budget instead of staying offline until a
		// manual connect.
		ctx.Logger().Error("mcpinstance: auto-connect failed", "id", a.cfg.ID, "error", err)
		a.startReconnectLoop(ctx)
	}
	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	a.disconnectLocked(ctx)
	return nil
}

// handleStatus is the Public observation point: reports connection state
// only — no config, no env/header values. Stateless (PureContext): a locked
// snapshot read that runs on the forked pure loop.
func (a *Actor) handleStatus(_ actor.PureContext) (domain.McpServerStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked(), nil
}

// handleConnect establishes the MCP session. Idempotent: connecting an
// already-connected instance returns the current status. Runs on the
// ExecLoop lane: the initialize handshake is bounded by connectTimeout
// (45s) and must not park the owner loop.
func (a *Actor) handleConnect(ctx actor.Context) (domain.McpServerStatus, error) {
	if err := a.connect(ctx); err != nil {
		return domain.McpServerStatus{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.statusLocked(), nil
}

func (a *Actor) handleDisconnect(ctx actor.Context) (domain.McpServerStatus, error) {
	a.disconnectLocked(ctx)
	a.mu.Lock()
	status := a.statusLocked()
	a.mu.Unlock()
	a.emitStatus(ctx)
	return status, nil
}

// handleCallTool executes one tool through the live session. Returns a
// protocol error when the instance is not connected or the session is gone.
// Runs on the ExecLoop lane (see ExecLoop): the 2min tools/call round trip
// must not park the owner loop, where status/tools queries answer from cache.
func (a *Actor) handleCallTool(ctx actor.Context, req domain.McpCallToolReq) (domain.McpCallToolResp, error) {
	a.mu.Lock()
	session := a.session
	cfgID := a.cfg.ID
	a.mu.Unlock()
	if session == nil {
		return domain.McpCallToolResp{}, fmt.Errorf("mcpinstance.call_tool: server %q is not connected", cfgID)
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), callTimeout)
	defer cancel()
	result, err := session.CallTool(callCtx, &mcp.CallToolParams{
		Name:      req.Tool,
		Arguments: req.Arguments,
	})
	if err != nil {
		// The connection may have died mid-call; surface that to the next
		// status poll/event rather than leaving stale "connected" state.
		a.markFailed(err)
		return domain.McpCallToolResp{}, fmt.Errorf("mcpinstance.call_tool: %w", err)
	}
	resp := domain.McpCallToolResp{
		Content: contentToDomain(result.Content),
		IsError: result.IsError,
	}
	if result.IsError {
		resp.Error = summarizeContent(result.Content)
	}
	return resp, nil
}

// handleTools returns the cached tools/list snapshot as the wire shape
// (name/description/inputSchema serialized to a JSON string — passed through
// verbatim). Internal: only the manager consumes it for agent tool injection
// (mcp.discover_tools). Stateless (PureContext): a locked snapshot read that
// runs on the forked pure loop.
func (a *Actor) handleTools(_ actor.PureContext) (domain.McpServerTools, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := domain.McpServerTools{ID: a.cfg.ID, Name: a.cfg.Name}
	for _, t := range a.tools {
		schema := ""
		if len(t.InputSchema) > 0 {
			if b, err := json.Marshal(t.InputSchema); err == nil {
				schema = string(b)
			}
		}
		out.Tools = append(out.Tools, domain.McpToolView{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out, nil
}

// handleConfigure replaces the in-memory cfg and reconciles the session:
// if Enabled it (re)connects with the new transport; if disabled it
// disconnects. Visibility is Internal — the manager is the only caller.
// The request type is the write-side McpServerConfig (registered schema
// struct); no dedicated Req wrapper exists in the mcp schema.
func (a *Actor) handleConfigure(ctx actor.Context, req domain.McpServerConfig) (domain.McpServerStatus, error) {
	if err := ValidateConfig(req); err != nil {
		return domain.McpServerStatus{}, fmt.Errorf("mcpinstance.configure: %w", err)
	}
	a.mu.Lock()
	// A changed transport/endpoint invalidates any live session. disconnectLocked
	// must not run while a.mu is held (it reaps the watcher, which needs a.mu to
	// finish), so release the lock first and re-acquire for the config swap.
	if a.connected && !sameEndpoint(a.cfg, req) {
		a.mu.Unlock()
		a.disconnectLocked(ctx)
		a.mu.Lock()
	}
	a.cfg = req
	a.refreshViewLocked()
	a.mu.Unlock()
	if !req.Enabled {
		return a.handleDisconnect(ctx)
	}
	return a.handleConnect(ctx)
}

// connect establishes the session. Caller may hold a.mu or not: connect
// acquires it for state mutation but performs I/O outside the lock.
func (a *Actor) connect(ctx actor.Context) error {
	a.mu.Lock()
	if a.connected {
		a.mu.Unlock()
		return nil
	}
	cfg := a.cfg
	a.mu.Unlock()

	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	connectCtx, cancel := context.WithTimeout(ctx.Lifecycle(), connectTimeout)
	defer cancel()

	build := a.buildTransportFn
	if build == nil {
		build = a.buildTransport
	}
	transport, err := build(ctx, cfg)
	if err != nil {
		return err
	}

	session, tools, err := a.establishSession(connectCtx, cfg, transport)
	if err != nil {
		return err
	}

	watcherDone := make(chan struct{})
	a.mu.Lock()
	if a.connected || a.session != nil {
		// Another connect won the race; discard the duplicate session so it
		// does not leak.
		a.mu.Unlock()
		_ = session.Close()
		return nil
	}
	a.session = session
	a.connected = true
	a.lastErr = ""
	a.tools = tools
	a.watcherDone = watcherDone
	a.reconnectDisabled = false
	a.connectedSince = time.Now()
	a.refreshViewLocked()
	cfgID := a.cfg.ID
	a.mu.Unlock()

	ctx.Logger().Info("mcpinstance: connected",
		"id", cfgID, "name", cfg.Name, "transport", cfg.Transport, "tools", len(tools))

	go a.watchSession(ctx, session, watcherDone)
	a.emitStatus(ctx)
	return nil
}

// establishSession performs the MCP initialize handshake and tools/list
// discovery over the given transport, returning the live session and the
// cached tool list. Extracted from connect so tests can drive a real
// handshake over an in-memory transport without a subprocess or HTTP server.
// The caller passes the cfg snapshot it captured under a.mu so the error
// messages cannot race a concurrent configure swap.
func (a *Actor) establishSession(connectCtx context.Context, cfg domain.McpServerConfig, transport mcp.Transport) (*mcp.ClientSession, []ToolInfo, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: ClientName, Version: ClientVersion}, &mcp.ClientOptions{
		ToolListChangedHandler: func(_ context.Context, _ *mcp.ToolListChangedRequest) {
			// Server-side tools changed: re-discover so the cache stays fresh.
			// Fires on an SDK background goroutine; uses the logger captured
			// in OnStart because no actor.Context is available here.
			a.refreshTools()
		},
	})
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("initialize %s server %q: %w", cfg.Transport, cfg.Name, err)
	}
	tools, err := listAllTools(connectCtx, session)
	if err != nil {
		_ = session.Close()
		return nil, nil, fmt.Errorf("tools/list on %q: %w", cfg.Name, err)
	}
	return session, tools, nil
}

// watchSession blocks until the connection closes (server-initiated, transport
// error, or our own disconnect) and reconciles state. An unexpected close hands
// off to startReconnectLoop so the session auto-reconnects with exponential
// backoff; an explicit disconnect (disconnectLocked cleared the session first)
// exits without a loop. It ignores sessions that have already been
// replaced/cleaned by a newer lifecycle step.
func (a *Actor) watchSession(ctx actor.Context, session *mcp.ClientSession, done chan struct{}) {
	defer close(done)
	err := session.Wait()

	policy := a.reconnect.withDefaults()
	a.mu.Lock()
	if a.session != session {
		a.mu.Unlock()
		return // superseded by reconnect or cleaned up by disconnect
	}
	a.connected = false
	a.session = nil
	a.watcherDone = nil
	if err != nil {
		a.lastErr = err.Error()
	}
	// A session that survived past the stability window earns a fresh
	// reconnect budget for the next episode.
	if !a.connectedSince.IsZero() && time.Since(a.connectedSince) >= policy.stability {
		a.reconnectAttempts = 0
	}
	a.connectedSince = time.Time{}
	autoReconnect := !a.reconnectDisabled && a.cfg.Enabled
	a.refreshViewLocked()
	a.mu.Unlock()

	if err != nil {
		ctx.Logger().Warn("mcpinstance: connection lost", "id", a.cfg.ID, "error", err)
	}
	a.emitStatus(ctx)

	if autoReconnect {
		a.startReconnectLoop(ctx)
	}
}

// startReconnectLoop launches the auto-reconnect goroutine for the current
// disconnect episode. At most one loop runs at a time — a second unexpected
// drop while a loop is already in flight is covered by that loop.
func (a *Actor) startReconnectLoop(ctx actor.Context) {
	a.mu.Lock()
	if a.reconnectStop != nil {
		a.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	a.reconnectStop = stop
	a.mu.Unlock()
	go a.reconnectLoop(ctx, stop)
}

// reconnectLoop retries connect with exponential backoff until the budget is
// exhausted (final failure status event, stale tools cache cleared) or a
// reconnect succeeds. It exits early when the actor is explicitly disconnected
// or stopped (stop channel closed) or cfg went disabled meanwhile.
func (a *Actor) reconnectLoop(ctx actor.Context, stop chan struct{}) {
	policy := a.reconnect.withDefaults()
	defer func() {
		a.mu.Lock()
		if a.reconnectStop == stop {
			a.reconnectStop = nil
		}
		a.mu.Unlock()
	}()

	a.mu.Lock()
	cfgID := a.cfg.ID
	a.mu.Unlock()

	for {
		a.mu.Lock()
		attempt := a.reconnectAttempts + 1
		disabled := a.reconnectDisabled || !a.cfg.Enabled
		a.mu.Unlock()
		if disabled {
			return
		}
		if attempt > policy.maxAttempts {
			break
		}
		if !sleepOrStopped(ctx, stop, policy.nextReconnectDelay(attempt)) {
			return // explicit disconnect or shutdown
		}
		a.mu.Lock()
		disabled = a.reconnectDisabled || !a.cfg.Enabled
		a.mu.Unlock()
		if disabled {
			return
		}
		if err := a.connect(ctx); err != nil {
			msg := truncateError(err)
			a.mu.Lock()
			a.reconnectAttempts = attempt
			a.lastErr = fmt.Sprintf("reconnect attempt %d/%d failed: %s", attempt, policy.maxAttempts, msg)
			a.refreshViewLocked()
			a.mu.Unlock()
			a.emitStatus(ctx)
			ctx.Logger().Warn("mcpinstance: reconnect attempt failed",
				"id", cfgID, "attempt", attempt, "max", policy.maxAttempts, "error", msg)
			continue
		}
		return // reconnected; the new session watcher owns the lifecycle
	}

	// Budget exhausted: surface a final failure and drop the stale tools cache
	// (the server is unreachable, so its tools must not stay injected).
	a.mu.Lock()
	a.reconnectAttempts = policy.maxAttempts + 1
	a.tools = nil
	a.lastErr = fmt.Sprintf("reconnect failed after %d attempts", policy.maxAttempts)
	a.refreshViewLocked()
	a.mu.Unlock()
	a.emitStatus(ctx)
	ctx.Logger().Error("mcpinstance: giving up on reconnect",
		"id", cfgID, "attempts", policy.maxAttempts)
}

// sleepOrStopped waits out the backoff delay, returning false early when the
// reconnect loop is cancelled (explicit disconnect) or the actor is shutting
// down.
func sleepOrStopped(ctx actor.Context, stop chan struct{}, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	case <-ctx.Lifecycle().Done():
		return false
	}
}

// stopReconnectLocked marks the actor as user-disconnected and cancels any
// in-flight reconnect loop. Caller must hold a.mu.
func (a *Actor) stopReconnectLocked() {
	a.reconnectDisabled = true
	if a.reconnectStop != nil {
		close(a.reconnectStop)
		a.reconnectStop = nil
	}
}

// truncateError bounds an error message that may embed upstream payloads:
// per project convention no unbounded error detail enters logs or events.
func truncateError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 512 {
		s = s[:512] + "...(truncated)"
	}
	return s
}

// disconnectLocked tears down the live session. Caller must NOT hold a.mu:
// the state reset runs under a.mu, but the session close and the watcher reap
// happen without the lock, so a wedged transport can neither stall the manager
// queue nor deadlock the watcher (it needs a.mu to finish reconciliation).
// Always transitions state to disconnected and clears the tools cache; the
// status event is emitted by the caller (handleDisconnect) or by the watcher
// when the closure was unexpected.
func (a *Actor) disconnectLocked(ctx actor.Context) {
	a.mu.Lock()
	a.stopReconnectLocked()
	if a.session == nil && !a.connected {
		a.mu.Unlock()
		return
	}
	session := a.session
	watcherDone := a.watcherDone
	a.connected = false
	a.session = nil
	a.tools = nil
	a.watcherDone = nil
	a.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
	// Give the watcher a bounded moment to observe the close; never block the
	// manager queue on a wedged transport.
	if watcherDone != nil {
		select {
		case <-watcherDone:
		case <-time.After(5 * time.Second):
			ctx.Logger().Warn("mcpinstance: session watcher did not exit in time", "id", a.cfg.ID)
		}
	}
	a.mu.Lock()
	a.refreshViewLocked()
	a.mu.Unlock()
	ctx.Logger().Info("mcpinstance: disconnected", "id", a.cfg.ID, "name", a.cfg.Name)
}

// markFailed records an async failure discovered from a callable path (e.g. a
// dead session mid-call). The watcher owns the state transition; this only
// records the error so the next status poll reports it.
func (a *Actor) markFailed(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil {
		return // watcher already handled it
	}
	a.lastErr = err.Error()
	a.refreshViewLocked()
}

// statusLocked returns the current status. Caller must hold a.mu.
func (a *Actor) statusLocked() domain.McpServerStatus {
	status := domain.McpServerStatus{
		ID:        a.cfg.ID,
		Connected: a.connected,
		ToolCount: int32(len(a.tools)),
		Error:     a.lastErr,
	}
	return status
}

// refreshViewLocked rewrites the ServerViews projection from cfg + state.
// Caller must hold a.mu. The cell publishes the component after the next
// invoke (or a RefreshProjection), so topology enrichment reads a snapshot
// that is eventually consistent with the live session — never a blocking
// child invoke. Env/header values are never copied into the view.
func (a *Actor) refreshViewLocked() {
	a.ServerViews = []domain.McpServerView{{
		ID:        a.cfg.ID,
		Name:      a.cfg.Name,
		Transport: a.cfg.Transport,
		Enabled:   a.cfg.Enabled,
		Status:    a.statusLocked(),
	}}
}

// emitStatus broadcasts the current status to mcp.server_status subscribers
// and fires the parent notification so mounted agents' tool surfaces are
// flagged for refresh (mcpmanager→agent tools_refresh_notify chain).
func (a *Actor) emitStatus(ctx actor.Context) {
	a.mu.Lock()
	status := a.statusLocked()
	a.mu.Unlock()
	ev := domain.McpServerStatusEvent{Status: status}
	if err := ctx.EmitEvent(EventKind, ev); err != nil {
		ctx.Logger().Warn("mcpinstance: emit status event failed", "id", a.cfg.ID, "error", err)
	}
	if a.statusNotify != nil {
		a.statusNotify(ev)
	}
}

// refreshTools re-runs tools/list on a live session and swaps the cache. Used
// by the ToolListChangedHandler (SDK background goroutine) and safe to call
// with no live session (no-op). The swap is atomic and rollback-safe: the
// fetch happens into a local, and a.tools is replaced under a.mu only on
// success — a failed tools/list leaves the previous cache intact.
//
// Concurrent notifications are serialized by refreshGate: a call that arrives
// while another refresh is in flight waits for it to finish before re-reading,
// so the cache always reflects the last completed fetch rather than racing
// partial results. The swap also re-checks that the session is still the one
// the fetch ran against, so a disconnect that raced the refresh cannot be
// masked by a stale tool list.
func (a *Actor) refreshTools() {
	a.refreshGate.Lock()
	defer a.refreshGate.Unlock()

	a.mu.Lock()
	session := a.session
	cfgID := a.cfg.ID
	a.mu.Unlock()
	if session == nil {
		return
	}
	discCtx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()
	tools, err := listAllTools(discCtx, session)
	if err != nil {
		if a.log != nil {
			a.log.Warn("mcpinstance: tools/list refresh failed", "id", cfgID, "error", err)
		}
		return
	}
	a.mu.Lock()
	if a.session == session {
		a.tools = tools
	}
	a.mu.Unlock()
	// The tool surface changed: notify the manager so mounted agents' active
	// turn engines re-resolve their MCP tool list at the next safe judgment
	// window (the next turn would pick the change up anyway, but an active
	// turn should see tools appear/disappear mid-flight).
	if a.statusNotify != nil {
		a.statusNotify(domain.McpServerStatusEvent{Status: domain.McpServerStatus{ID: cfgID, Connected: true}})
	}
}

// buildTransport constructs the SDK transport for cfg. The stdio variant
// merges a credential-scrubbed copy of the process environment with the
// configured env overrides (user-configured env wins); the http variant
// injects configured headers via a per-request RoundTripper.
func (a *Actor) buildTransport(ctx actor.Context, cfg domain.McpServerConfig) (mcp.Transport, error) {
	switch cfg.Transport {
	case TransportStdio:
		if cfg.Stdio == nil {
			return nil, fmt.Errorf("stdio transport requires Stdio config")
		}
		cmd := util.Command(cfg.Stdio.Command, cfg.Stdio.Args...)
		// Never leak host credentials into the MCP server process: scrub the
		// parent environment first, then let user-configured env override it.
		cmd.Env = mergeEnv(scrubEnv(os.Environ()), cfg.Stdio.Env)
		cmd.Stderr = &stderrBridge{logger: ctx.Logger(), id: cfg.ID}
		return &mcp.CommandTransport{Command: cmd}, nil
	case TransportHTTP:
		if cfg.Http == nil {
			return nil, fmt.Errorf("http transport requires Http config")
		}
		if _, err := url.Parse(cfg.Http.URL); err != nil {
			return nil, fmt.Errorf("invalid http url %q: %w", cfg.Http.URL, err)
		}
		base := http.DefaultTransport.(*http.Transport).Clone()
		if proxy := cfg.Http.Proxy; proxy != "" {
			proxyURL, err := url.Parse(proxy)
			if err != nil {
				return nil, fmt.Errorf("invalid http proxy %q: %w", proxy, err)
			}
			base.Proxy = http.ProxyURL(proxyURL)
		}
		client := &http.Client{Transport: headerInjectingTransport(base, cfg.Http.Headers)}
		return &mcp.StreamableClientTransport{
			Endpoint:   cfg.Http.URL,
			HTTPClient: client,
			MaxRetries: 1,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", cfg.Transport)
	}
}

// ValidateConfig is exported so the manager can validate before persisting.
func ValidateConfig(cfg domain.McpServerConfig) error {
	if cfg.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch cfg.Transport {
	case TransportStdio:
		if cfg.Stdio == nil {
			return fmt.Errorf("stdio transport requires Stdio config")
		}
		if cfg.Stdio.Command == "" {
			return fmt.Errorf("stdio.command is required")
		}
	case TransportHTTP:
		if cfg.Http == nil {
			return fmt.Errorf("http transport requires Http config")
		}
		u, err := url.Parse(cfg.Http.URL)
		if err != nil {
			return fmt.Errorf("invalid http url %q: %w", cfg.Http.URL, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("http url must use http/https scheme, got %q", u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("http url must include a host")
		}
		if proxy := cfg.Http.Proxy; proxy != "" {
			p, err := url.Parse(proxy)
			if err != nil {
				return fmt.Errorf("invalid http proxy %q: %w", proxy, err)
			}
			if p.Host == "" {
				return fmt.Errorf("http proxy must include a host")
			}
		}
	default:
		return fmt.Errorf("transport must be %q or %q, got %q", TransportStdio, TransportHTTP, cfg.Transport)
	}
	return nil
}

// sameEndpoint reports whether two configs share the same transport endpoint
// AND the same secrets (env/headers). The stdio transport captures env at
// process start and the http transport captures headers at build time, so a
// configure push that changes either must tear down the session to take
// effect.
func sameEndpoint(a, b domain.McpServerConfig) bool {
	if a.Transport != b.Transport {
		return false
	}
	switch a.Transport {
	case TransportStdio:
		if a.Stdio == nil || b.Stdio == nil {
			return a.Stdio == b.Stdio
		}
		if a.Stdio.Command != b.Stdio.Command || !equalStrings(a.Stdio.Args, b.Stdio.Args) {
			return false
		}
		return equalStringMaps(a.Stdio.Env, b.Stdio.Env)
	case TransportHTTP:
		if a.Http == nil || b.Http == nil {
			return a.Http == b.Http
		}
		if a.Http.URL != b.Http.URL {
			return false
		}
		if a.Http.Proxy != b.Http.Proxy {
			return false
		}
		return equalStringMaps(a.Http.Headers, b.Http.Headers)
	default:
		return false
	}
}

// listAllTools paginates tools/list and flattens the results into ToolInfo.
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]ToolInfo, error) {
	var out []ToolInfo
	seen := make(map[string]bool)
	cursor := ""
	for {
		result, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		for _, t := range result.Tools {
			if t == nil || t.Name == "" || seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			info := ToolInfo{Name: t.Name, Description: t.Description}
			if t.InputSchema != nil {
				if schema, ok := schemaToMap(t.InputSchema); ok {
					info.InputSchema = schema
				}
			}
			out = append(out, info)
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// schemaToMap normalizes a Tool.InputSchema value (any JSON-marshalable shape)
// into map[string]any so ToolInfo carries a plain Go map.
func schemaToMap(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, true
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, false
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, false
		}
		return m, true
	}
}

// contentToDomain flattens an MCP CallToolResult.Content slice into the wire
// shape. Text blocks map directly; image blocks surface their base64 payload
// via Data/MimeType so the turn engine can pass it to the LLM; audio and
// embedded-resource blocks degrade to a compact diagnostic text placeholder
// since they cannot be surfaced as images. Everything else falls back to a
// JSON dump.
func contentToDomain(content []mcp.Content) []domain.McpToolContent {
	out := make([]domain.McpToolContent, 0, len(content))
	for _, c := range content {
		if c == nil {
			continue
		}
		switch cc := c.(type) {
		case *mcp.TextContent:
			if cc != nil {
				out = append(out, domain.McpToolContent{Type: "text", Text: cc.Text})
			}
		case *mcp.ImageContent:
			if cc != nil {
				out = append(out, domain.McpToolContent{
					Type:     "image",
					Data:     base64.StdEncoding.EncodeToString(cc.Data),
					MimeType: cc.MIMEType,
				})
			}
		case *mcp.AudioContent:
			if cc != nil {
				out = append(out, domain.McpToolContent{
					Type: "text",
					Text: fmt.Sprintf("[audio content block: type=%s, %d bytes]", cc.MIMEType, len(cc.Data)),
				})
			}
		case *mcp.EmbeddedResource:
			out = append(out, embeddedResourceToDomain(cc))
		default:
			data, err := json.Marshal(c)
			if err != nil {
				out = append(out, domain.McpToolContent{Type: "unknown", Text: fmt.Sprintf("%v", c)})
				continue
			}
			out = append(out, domain.McpToolContent{Type: "other", Text: string(data)})
		}
	}
	return out
}

// embeddedResourceToDomain degrades an MCP embedded-resource block to a
// diagnostic text placeholder. The resource cannot be surfaced to the LLM as
// an image through the wire shape, so we report its identity and size instead
// of dropping it silently.
func embeddedResourceToDomain(er *mcp.EmbeddedResource) domain.McpToolContent {
	if er == nil || er.Resource == nil {
		return domain.McpToolContent{Type: "text", Text: "[resource block: uri=<nil>]"}
	}
	rc := er.Resource
	var b strings.Builder
	b.WriteString("[resource block: uri=")
	b.WriteString(rc.URI)
	if rc.MIMEType != "" {
		b.WriteString(", type=")
		b.WriteString(rc.MIMEType)
	}
	if len(rc.Blob) > 0 {
		fmt.Fprintf(&b, ", %d bytes", len(rc.Blob))
	} else if rc.Text != "" {
		fmt.Fprintf(&b, ", %d chars text", len(rc.Text))
	}
	b.WriteString("]")
	return domain.McpToolContent{Type: "text", Text: b.String()}
}

// summarizeContent joins the text of a result for the Error field.
func summarizeContent(content []mcp.Content) string {
	var b strings.Builder
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok && tc != nil && tc.Text != "" {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// credentialEnvPatterns are substrings that mark an environment variable name
// as credential-shaped (matched case-insensitively). Mirrors the
// deepseek-harness client scrub (`KEY|PASSWORD|SECRET|TOKEN`) extended with
// CREDENTIAL and APIKEY per the mcp-fixes plan. APIKEY is redundant with KEY
// but kept explicit for readability.
var credentialEnvPatterns = []string{"KEY", "PASSWORD", "SECRET", "TOKEN", "CREDENTIAL", "APIKEY"}

// projectEnvPrefixes are project-internal namespaces never handed to an MCP
// stdio subprocess (matched case-insensitively; env names are case-insensitive
// on Windows).
var projectEnvPrefixes = []string{"SPOREMIND_", "DSH_"}

// scrubEnv filters credential-shaped and project-internal variables out of an
// environment slice (entries of the form "NAME=value"). Entries without an
// '=' pass through untouched; matching is case-insensitive on the name.
func scrubEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 && isSensitiveEnvName(kv[:i]) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// isSensitiveEnvName reports whether name looks like a credential (contains a
// credentialEnvPatterns substring) or lives in a projectEnvPrefixes
// namespace. Matching is case-insensitive.
func isSensitiveEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, pat := range credentialEnvPatterns {
		if strings.Contains(upper, pat) {
			return true
		}
	}
	for _, prefix := range projectEnvPrefixes {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

// mergeEnv returns base with the configured overrides applied (set or
// replaced by key; an empty-string value clears the variable).
func mergeEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for _, kv := range base {
		if i := strings.IndexByte(kv, '='); i > 0 {
			merged[kv[:i]] = kv[i+1:]
		}
	}
	for k, v := range overrides {
		if v == "" {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// headerInjectingTransport wraps an http.RoundTripper to add configured
// headers to every MCP request (e.g. Authorization for remote servers).
func headerInjectingTransport(base http.RoundTripper, headers map[string]string) http.RoundTripper {
	return &headerInjectRoundTripper{base: base, headers: headers}
}

type headerInjectRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerInjectRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// stderrBridge forwards a stdio server's stderr to the actor logger.
type stderrBridge struct {
	logger actor.Logger
	id     string
}

func (b *stderrBridge) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	if line != "" {
		b.logger.Debug("mcpinstance: stdio server stderr", "id", b.id, "line", line)
	}
	return len(p), nil
}
