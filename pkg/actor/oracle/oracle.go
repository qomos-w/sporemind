package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/diagcrash"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const maxDiagnostics = 200
const maxRawDataBytes = 32 * 1024
const defaultDiagnosticsLimit = 50
const maxDiagnosticsLimit = 200

// defaultKindAllowedTTL bounds how long the oracle serves an agent kind's
// allowed-callable set from cache before re-fetching the kind config from
// workspace (OwnerLane blocking audit 2026-08-27 P1: the previous
// unconditional Await on workspace.get_agent_kind_config held the oracle
// owner lane on every capability_discover call). Config edits propagate
// within this window; oracle.refresh_capability_cache forces an immediate
// invalidation.
const defaultKindAllowedTTL = 60 * time.Second

// defaultServiceListTTL bounds how long search_services serves the cached
// runtime service list before re-querying runtime.list_services. Service
// topology changes are rare; the short window keeps the debug surface fresh
// without a cross-actor Await per query.
const defaultServiceListTTL = 10 * time.Second

// kindAllowedEntry is one agent kind's cached allowed-callable set.
type kindAllowedEntry struct {
	allowed   []string
	fetchedAt time.Time
}

// Actor is the global capability discovery and diagnostics hub.
type Actor struct {
	actor.Host
	store       persist.Persist
	diagnostics []domain.Diagnostic
	diagMu      sync.RWMutex // protects diagnostics
	actorID     string
	// lastStoreDrops remembers the last-seen events-store fan-out drop count
	// per subscriber actor (gospore.events.stats storeSubs). Written only on
	// the diag_ops lane by the events-stats tick; nil until first poll.
	lastStoreDrops map[string]uint64

	// kindAllowed caches the bundle-derived allowed-callable sets per agent
	// kind (kindAllowedTTL). Guarded by kindAllowedMu; consulted from
	// PureContext handlers on forked goroutines.
	kindAllowedMu  sync.Mutex
	kindAllowed    map[string]kindAllowedEntry
	kindAllowedTTL time.Duration // 0 → defaultKindAllowedTTL; tests may override

	// svcList caches the runtime service list for search_services
	// (svcListTTL). Guarded by svcListMu.
	svcListMu      sync.Mutex
	svcList        []domain.ServiceInfo
	svcListFetched time.Time
	svcListTTL     time.Duration // 0 → defaultServiceListTTL; tests may override
}

var _ persist.Persistent = (*Actor)(nil)

func (a *Actor) Type() string { return "oracle" }

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("oracle"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if err := a.Load(); err != nil {
		ctx.Logger().Error("oracle: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("oracle: starting", "id", ctx.Self().ID().String())
	// diag_ops is the dedicated stateful lane for report_diagnostic: it is a
	// fire-and-forget sink (called by failover, panicprobe, filesystem grep,
	// process-state reporting) that appends to the diagnostic ring buffer and
	// writes it to disk, so it cannot be stateless; isolating it keeps that
	// disk write off the owner lane.
	if err := ctx.RegisterLoop("diag_ops", actor.ModeStateful); err != nil {
		return fmt.Errorf("oracle: register diag_ops loop: %w", err)
	}
	if err := ctx.Register("oracle.capability_discover", a.handleCapabilityDiscover, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register capability.discover: %w", err)
	}
	if err := ctx.Register("oracle.capability_explain", a.handleCapabilityExplain, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register capability.explain: %w", err)
	}
	if err := ctx.Register("oracle.report_diagnostic", a.handleReportDiagnostic, actor.Public(), actor.WithLoop("diag_ops")); err != nil {
		return fmt.Errorf("oracle: register report_diagnostic: %w", err)
	}
	if err := ctx.Register(callableEventsStatsTick, a.handleEventsStatsTick, actor.Public(), actor.WithLoop("diag_ops")); err != nil {
		return fmt.Errorf("oracle: register events stats tick: %w", err)
	}
	a.scheduleEventsStatsTick(ctx)
	if err := ctx.Register("oracle.list_diagnostics", a.handleListDiagnostics, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register list_diagnostics: %w", err)
	}
	if err := ctx.Register("oracle.get_diagnostic", a.handleGetDiagnostic, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register get_diagnostic: %w", err)
	}
	if err := ctx.Register("oracle.search_services", a.handleSearchServices, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register search_services: %w", err)
	}
	if err := ctx.Register("oracle.refresh_capability_cache", a.handleRefreshCapabilityCache, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register refresh_capability_cache: %w", err)
	}
	if err := ctx.RegisterEventKind("diagnostic", domain.Diagnostic{}, actor.Public()); err != nil {
		return fmt.Errorf("oracle: register event kind diagnostic: %w", err)
	}
	if err := ctx.RegisterDomain("oracle").Expose(); err != nil {
		return fmt.Errorf("oracle: expose service: %w", err)
	}
	a.ingestCrashReports(ctx)
	return nil
}

// ingestCrashReports scans <DataDir>/logs/crash-*.log for crash files left
// behind by a previous dying process and replays each as a Diagnostic so
// the failure surfaces through /debug/problems instead of being lost.
//
// Each replayed file is moved to crash-replayed/ so subsequent startups do
// not double-report. Failures during scan or move are logged but never
// fatal — losing one crash replay is preferable to wedging the oracle.
func (a *Actor) ingestCrashReports(ctx actor.Context) {
	pending, err := diagcrash.Pending()
	if err != nil {
		ctx.Logger().Error("oracle: scan crash files failed", "error", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	ctx.Logger().Info("oracle: replaying crash reports", "count", len(pending))
	for _, path := range pending {
		a.replayCrashFile(ctx, path)
	}
}

func (a *Actor) replayCrashFile(ctx actor.Context, path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		ctx.Logger().Error("oracle: read crash file failed", "file", path, "error", err)
		return
	}
	body := string(content)
	header, raw := splitCrashBody(body)
	diag := domain.Diagnostic{
		ID:         ctx.NewID().String(),
		Severity:   "error",
		Source:     "process.crash",
		Message:    header,
		Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
		CallableID: "process.crash",
		RawData:    truncateRaw(raw),
	}
	a.diagMu.Lock()
	a.diagnostics = append(a.diagnostics, diag)
	if len(a.diagnostics) > maxDiagnostics {
		a.diagnostics = a.diagnostics[len(a.diagnostics)-maxDiagnostics:]
	}
	a.diagMu.Unlock()
	_ = ctx.EmitEvent("diagnostic", diag)
	if err := diagcrash.MarkReplayed(path); err != nil {
		ctx.Logger().Error("oracle: mark crash file replayed failed", "file", path, "error", err)
	}
	ctx.Logger().Warn("oracle: replayed crash report", "file", filepath.Base(path), "header", header)
}

// splitCrashBody returns (header, fullBody) where header is the first line
// of the crash report (used as the Diagnostic.Message) and fullBody is the
// raw text placed under Diagnostic.RawData.
func splitCrashBody(body string) (string, string) {
	idx := strings.IndexByte(body, '\n')
	header := body
	if idx >= 0 {
		header = body[:idx]
	}
	return strings.TrimSpace(header), body
}

func truncateRaw(s string) string {
	if len(s) > maxRawDataBytes {
		return s[:maxRawDataBytes] + "\n... [truncated]"
	}
	return s
}

func (a *Actor) OnStop(ctx actor.Context) error {
	return a.Save()
}

func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("oracle"))
		if err != nil {
			return err
		}
	}
	// Snapshot under diagMu: report_diagnostic runs on its own stateful lane,
	// so Save (diag_ops) and OnStop (owner lane) can overlap with each other;
	// the lock keeps the persisted slice consistent.
	a.diagMu.RLock()
	snapshot := append([]domain.Diagnostic(nil), a.diagnostics...)
	a.diagMu.RUnlock()
	return a.store.Save(a.actorID, map[string]any{
		"diagnostics": snapshot,
	})
}

func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("oracle"))
		if err != nil {
			return err
		}
	}
	var snapshot struct {
		Diagnostics []domain.Diagnostic `json:"diagnostics"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &snapshot); err != nil {
		return err
	}
	a.diagnostics = snapshot.Diagnostics
	return nil
}

func (a *Actor) saveOrLog(ctx actor.Context) {
	if err := a.Save(); err != nil {
		ctx.Logger().Error("oracle: save state failed", "error", err)
	}
}

// handleCapabilityDiscover is a stateless (PureContext) handler: it runs on a
// forked goroutine with no mailbox serialization, so even a cache-miss fetch
// of the kind config never blocks the oracle owner lane. The allowed set is
// served from the TTL cache in the steady state.
func (a *Actor) handleCapabilityDiscover(ctx actor.PureContext, req domain.OracleCapabilityDiscoverReq) (domain.OracleCapabilityDiscoverResp, error) {
	if req.AgentKind == "" {
		return domain.OracleCapabilityDiscoverResp{}, fmt.Errorf("oracle.capability_discover: agentKind is required")
	}

	allowed := a.fetchAllowedCallables(ctx, req.AgentKind)
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}

	candidates := make([]domain.CapabilityCandidate, 0)
	seen := make(map[string]struct{})

	// Filter availableToolNames by allowed callables.
	for _, name := range req.AvailableToolNames {
		if _, ok := seen[name]; ok {
			continue
		}
		if _, permitted := allowedSet[name]; !permitted {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, domain.CapabilityCandidate{
			Kind:           "tool",
			ID:             name,
			Name:           callableBareName(name),
			RelevanceScore: 1.0,
			Rationale:      fmt.Sprintf("Allowed for %s", req.AgentKind),
		})
	}

	// Cap by limit.
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if int32(len(candidates)) > limit {
		candidates = candidates[:limit]
	}

	return domain.OracleCapabilityDiscoverResp{Items: candidates}, nil
}

func (a *Actor) handleCapabilityExplain(_ actor.PureContext, req domain.OracleCapabilityExplainReq) (domain.OracleCapabilityExplainResp, error) {
	if req.ID == "" {
		return domain.OracleCapabilityExplainResp{}, fmt.Errorf("oracle.capability_explain: id is required")
	}
	return domain.OracleCapabilityExplainResp{
		Summary:    fmt.Sprintf("%s capability", req.ID),
		ExpandMode: defaultExpandMode(req.ID),
		WhenToUse:  "Use this capability when the task matches its responsibility.",
		Examples:   "",
		RelatedIds: nil,
	}, nil
}

// handleReportDiagnostic records a diagnostic in the ring buffer and persists
// it. State-writing: it mutates a.diagnostics and writes the snapshot to
// disk, so it runs on the dedicated "diag_ops" stateful lane (see OnStart)
// rather than the owner lane.
func (a *Actor) handleReportDiagnostic(ctx actor.Context, req domain.OracleReportDiagnosticReq) error {
	rawData := req.RawData
	if len(rawData) > maxRawDataBytes {
		rawData = rawData[:maxRawDataBytes] + "\n... [truncated]"
	}
	a.appendDiagnostic(ctx, domain.Diagnostic{
		Severity:      req.Severity,
		Source:        req.Source,
		Message:       req.Message,
		AgentID:       req.AgentID,
		TurnID:        req.TurnID,
		StepID:        req.StepID,
		CallableID:    req.CallableID,
		TargetService: req.TargetService,
		ToolUseID:     req.ToolUseID,
		Input:         req.Input,
		Output:        req.Output,
		Unit:          req.Unit,
		HttpStatus:    req.HttpStatus,
		RawData:       rawData,
	})
	return nil
}

// appendDiagnostic stamps ID/timestamp, appends to the ring buffer, emits the
// diagnostic event and persists. Caller supplies the domain fields. Serialized
// by the diag_ops lane (or diagMu for concurrent readers of the ring).
func (a *Actor) appendDiagnostic(ctx actor.Context, diag domain.Diagnostic) {
	diag.ID = ctx.NewID().String()
	diag.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)
	a.diagMu.Lock()
	a.diagnostics = append(a.diagnostics, diag)
	if len(a.diagnostics) > maxDiagnostics {
		a.diagnostics = a.diagnostics[len(a.diagnostics)-maxDiagnostics:]
	}
	a.diagMu.Unlock()
	_ = ctx.EmitEvent("diagnostic", diag)
	a.saveOrLog(ctx)
}

// ── events-store drop observability (gospore.events.stats storeSubs) ──

const (
	callableEventsStatsTick = "oracle.events_stats_tick"
	eventsStatsPollInterval = 60 * time.Second
)

// eventsStatsResult mirrors the storeSubs block of gospore.events.stats.
// Defined locally (not codegen) because the callable returns a `any`-shaped
// JSON envelope.
type eventsStatsResult struct {
	StoreSubs []struct {
		ActorID   string   `json:"actorId"`
		Kinds     []string `json:"kinds"`
		Dropped   uint64   `json:"dropped"`
		BufferCap int      `json:"bufferCap"`
	} `json:"storeSubs"`
}

// scheduleEventsStatsTick re-arms the self-scheduling poll chain.
func (a *Actor) scheduleEventsStatsTick(ctx actor.PureContext) {
	if err := ctx.After(eventsStatsPollInterval, callableEventsStatsTick, nil); err != nil {
		ctx.Logger().Error("oracle: schedule events stats tick failed", "err", err)
	}
}

// handleEventsStatsTick polls the gospore events store's per-subscriber drop
// counters and records a diagnostic whenever a subscriber's cumulative drop
// count increases — a slow consumer losing frames in the Go-side fan-out.
// Together with the frontend's wails-raw gapStats reports this distinguishes
// Go-side fan-out loss from Wails-channel loss. Runs on the diag_ops lane so
// the cross-actor Await never holds the owner lane.
func (a *Actor) handleEventsStatsTick(ctx actor.Context) error {
	// Re-arm first so the chain survives any fetch/decode failure below.
	a.scheduleEventsStatsTick(ctx)

	planner := ctx.Planner()
	root := ctx.Root()
	if planner == nil || root == nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	raw, err := planner.Call(callCtx, root, "gospore.events.stats", map[string]any{}).Await()
	if err != nil {
		return nil // transient; next tick retries
	}
	var stats eventsStatsResult
	if err := decodeJSONEnvelope(raw, &stats); err != nil {
		ctx.Logger().Warn("oracle: decode events stats failed", "err", err)
		return nil
	}

	next := make(map[string]uint64, len(stats.StoreSubs))
	for _, s := range stats.StoreSubs {
		next[s.ActorID] = s.Dropped
		prev, seen := a.lastStoreDrops[s.ActorID]
		if s.Dropped > prev || (!seen && s.Dropped > 0) {
			delta := s.Dropped - prev
			a.appendDiagnostic(ctx, domain.Diagnostic{
				Severity: "warning",
				Source:   "events-store",
				Message: fmt.Sprintf("subscriber fan-out dropped frames: actor=%s kinds=%v +%d (total %d, bufferCap %d)",
					s.ActorID, s.Kinds, delta, s.Dropped, s.BufferCap),
			})
		}
	}
	a.lastStoreDrops = next
	return nil
}

// decodeJSONEnvelope decodes a planner.Call raw result ([]byte / string /
// JSON-marshalable) into out, mirroring agent's decodeCellStatsResult.
func decodeJSONEnvelope(raw any, out any) error {
	var b []byte
	switch v := raw.(type) {
	case nil:
		return nil
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		var err error
		b, err = json.Marshal(v)
		if err != nil {
			return fmt.Errorf("re-encode stats envelope: %w", err)
		}
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func (a *Actor) handleListDiagnostics(_ actor.PureContext, req domain.OracleListDiagnosticsReq) (domain.OracleListDiagnosticsResp, error) {
	var since time.Time
	if req.Since != "" {
		if t, err := time.Parse(time.RFC3339Nano, req.Since); err == nil {
			since = t
		}
	}

	a.diagMu.RLock()
	snapshot := make([]domain.Diagnostic, len(a.diagnostics))
	copy(snapshot, a.diagnostics)
	a.diagMu.RUnlock()

	filtered := make([]domain.DiagnosticSummary, 0)
	for i := len(snapshot) - 1; i >= 0; i-- {
		d := snapshot[i]
		if req.Severity != "" && !strings.EqualFold(d.Severity, req.Severity) {
			continue
		}
		if req.Source != "" && !strings.Contains(strings.ToLower(d.Source), strings.ToLower(req.Source)) {
			continue
		}
		if req.AgentID != "" && d.AgentID != req.AgentID {
			continue
		}
		if req.TurnID != "" && d.TurnID != req.TurnID {
			continue
		}
		if !since.IsZero() {
			if ts, err := time.Parse(time.RFC3339Nano, d.Timestamp); err == nil && ts.Before(since) {
				continue
			}
		}
		filtered = append(filtered, diagnosticSummary(d))
	}

	limit := req.Limit
	if limit <= 0 {
		limit = defaultDiagnosticsLimit
	}
	if limit > maxDiagnosticsLimit {
		limit = maxDiagnosticsLimit
	}
	offset := req.Offset
	if int(offset) > len(filtered) {
		offset = int32(len(filtered))
	}
	end := int(offset) + int(limit)
	if end > len(filtered) {
		end = len(filtered)
	}

	return domain.OracleListDiagnosticsResp{Items: filtered[offset:end]}, nil
}

func (a *Actor) handleGetDiagnostic(_ actor.PureContext, req domain.OracleGetDiagnosticReq) (domain.Diagnostic, error) {
	a.diagMu.RLock()
	defer a.diagMu.RUnlock()
	for i := len(a.diagnostics) - 1; i >= 0; i-- {
		if a.diagnostics[i].ID == req.ID {
			return a.diagnostics[i], nil
		}
	}
	return domain.Diagnostic{}, fmt.Errorf("oracle.get_diagnostic: diagnostic %q not found", req.ID)
}

// handleSearchServices is a stateless (PureContext) handler: the
// runtime.list_services round-trip runs on a forked goroutine (owner lane
// never held), and the service list is served from a short TTL cache in the
// steady state.
func (a *Actor) handleSearchServices(ctx actor.PureContext, req domain.OracleSearchServicesReq) (domain.OracleSearchServicesResp, error) {
	list, err := a.fetchServiceList(ctx)
	if err != nil {
		return domain.OracleSearchServicesResp{}, err
	}

	query := strings.ToLower(req.Query)
	filtered := make([]domain.ServiceInfo, 0, len(list.Items))
	seen := make(map[string]struct{})
	for _, svc := range list.Items {
		if _, ok := seen[svc.Name]; ok {
			continue
		}
		seen[svc.Name] = struct{}{}
		if query != "" && !strings.Contains(strings.ToLower(svc.Name), query) {
			continue
		}
		filtered = append(filtered, svc)
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if int(limit) > len(filtered) {
		limit = int32(len(filtered))
	}
	if limit < 0 {
		limit = 0
	}

	return domain.OracleSearchServicesResp{Items: filtered[:limit]}, nil
}

// fetchServiceList returns the runtime service list, cached for
// svcListTTL. Runs on the calling (pure, forked) goroutine so the owner
// lane is never held; only successful fetches are cached.
func (a *Actor) fetchServiceList(ctx actor.PureContext) (domain.RuntimeListServicesResp, error) {
	ttl := a.svcListTTL
	if ttl <= 0 {
		ttl = defaultServiceListTTL
	}
	a.svcListMu.Lock()
	if a.svcList != nil && time.Since(a.svcListFetched) < ttl {
		cached := domain.RuntimeListServicesResp{Items: append([]domain.ServiceInfo(nil), a.svcList...)}
		a.svcListMu.Unlock()
		return cached, nil
	}
	a.svcListMu.Unlock()

	planner := ctx.Planner()
	if planner == nil {
		return domain.RuntimeListServicesResp{}, fmt.Errorf("oracle.search_services: planner not available")
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()

	result, err := planner.Call(callCtx, ctx.Root(), "runtime.list_services", domain.RuntimeListServicesReq{}).Await()
	if err != nil {
		return domain.RuntimeListServicesResp{}, fmt.Errorf("oracle.search_services: list services failed: %w", err)
	}

	var list domain.RuntimeListServicesResp
	switch v := result.(type) {
	case []byte:
		if err := json.Unmarshal(v, &list); err != nil {
			return domain.RuntimeListServicesResp{}, fmt.Errorf("oracle.search_services: decode list services: %w", err)
		}
	case domain.RuntimeListServicesResp:
		list = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &list)
	default:
		return domain.RuntimeListServicesResp{}, fmt.Errorf("oracle.search_services: unexpected list services type %T", result)
	}

	a.svcListMu.Lock()
	a.svcList = append([]domain.ServiceInfo(nil), list.Items...)
	a.svcListFetched = time.Now()
	a.svcListMu.Unlock()
	return list, nil
}

// handleRefreshCapabilityCache drops the kind-config-derived capability
// cache so the next capability_discover re-fetches fresh kind configs from
// workspace. Called after a workspace.save_agent_kind_config to make config
// edits visible immediately instead of waiting out the TTL.
func (a *Actor) handleRefreshCapabilityCache(_ actor.PureContext) error {
	a.kindAllowedMu.Lock()
	a.kindAllowed = nil
	a.kindAllowedMu.Unlock()
	return nil
}

// cachedKindAllowed returns the cached allowed-callable set for agentKind
// when it exists and is within the TTL.
func (a *Actor) cachedKindAllowed(agentKind string) ([]string, bool) {
	ttl := a.kindAllowedTTL
	if ttl <= 0 {
		ttl = defaultKindAllowedTTL
	}
	a.kindAllowedMu.Lock()
	defer a.kindAllowedMu.Unlock()
	entry, ok := a.kindAllowed[agentKind]
	if !ok || time.Since(entry.fetchedAt) >= ttl {
		return nil, false
	}
	return append([]string(nil), entry.allowed...), true
}

// storeKindAllowed caches the allowed-callable set for agentKind.
func (a *Actor) storeKindAllowed(agentKind string, allowed []string) {
	a.kindAllowedMu.Lock()
	if a.kindAllowed == nil {
		a.kindAllowed = make(map[string]kindAllowedEntry)
	}
	a.kindAllowed[agentKind] = kindAllowedEntry{
		allowed:   append([]string(nil), allowed...),
		fetchedAt: time.Now(),
	}
	a.kindAllowedMu.Unlock()
}

// fetchAllowedCallables returns the callable IDs allowed for agentKind,
// derived from the kind config's DefaultBundleIDs and cached for
// kindAllowedTTL. Cache misses fetch from workspace; this runs on the
// calling (pure, forked) goroutine so the owner lane is never held. Falls
// back to empty slice on any error.
func (a *Actor) fetchAllowedCallables(ctx actor.PureContext, agentKind string) []string {
	if allowed, ok := a.cachedKindAllowed(agentKind); ok {
		return allowed
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil
	}
	wsRef, ok := ctx.LookupService("workspace")
	if !ok {
		return nil
	}
	payload, _ := json.Marshal(domain.WorkspaceGetAgentKindConfigReq{Kind: agentKind})
	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), domain.DefaultInvokeTimeout)
	defer cancel()
	result, err := planner.Call(callCtx, wsRef, "workspace.get_agent_kind_config", payload).Await()
	if err != nil {
		return nil
	}
	var cfg domain.AgentKindConfig
	switch v := result.(type) {
	case []byte:
		if err := json.Unmarshal(v, &cfg); err != nil {
			return nil
		}
	case domain.AgentKindConfig:
		cfg = v
	case map[string]interface{}:
		b, _ := json.Marshal(v)
		_ = json.Unmarshal(b, &cfg)
	default:
		return nil
	}
	allowed := agentkit.CallableIDsForBundles(cfg.DefaultBundleIDs)
	a.storeKindAllowed(agentKind, allowed)
	return allowed
}

func callableBareName(callableID string) string {
	idx := strings.LastIndex(callableID, ".")
	if idx < 0 {
		return callableID
	}
	return callableID[idx+1:]
}

func defaultExpandMode(callableID string) string {
	switch {
	case strings.HasPrefix(callableID, "fork_"):
		return "fork_child"
	case callableID == "project.write" || callableID == "project.edit" || callableID == "project.rm" || callableID == "project.shell_exec":
		return "confirm"
	default:
		return "inline"
	}
}

func diagnosticSummary(d domain.Diagnostic) domain.DiagnosticSummary {
	return domain.DiagnosticSummary{
		ID:            d.ID,
		Severity:      d.Severity,
		Source:        d.Source,
		Message:       d.Message,
		Timestamp:     d.Timestamp,
		AgentID:       d.AgentID,
		TurnID:        d.TurnID,
		StepID:        d.StepID,
		CallableID:    d.CallableID,
		TargetService: d.TargetService,
		ToolUseID:     d.ToolUseID,
		Input:         d.Input,
		Output:        d.Output,
		Unit:          d.Unit,
		HttpStatus:    d.HttpStatus,
		RawData:       d.RawData,
	}
}
