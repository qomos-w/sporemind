package aimanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/aiaggregator"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

const autoAggregatorID = "system"

// disableWindowReason is the projected health reason for a provider currently
// inside one of its daily disable windows. It is the only aimanager-local
// health reason; every other reason originates from the llmclient classifier
// (see pkg/llmclient/health.go). Cooldown tuning (1.5× backoff, 15m cap, 429
// floor/ceiling, quota duration) is owned by aimanager and injected into the
// llmclient health layer via cooldownPolicy/SetCooldownPolicy (方式 A).
const disableWindowReason = "disable_window"

// Recovery-mode strings projected alongside HealthState. cooling_down units
// recover when the cooldown expires; disabled units require explicit recovery
// (manual reset or config refresh).
const (
	recoveryCooldown        = "cooldown"
	recoveryManualOrBalance = "manual_or_balance_refresh"
)

// recoveryModeForState maps an llmclient health state to its projected recovery
// mode.
func recoveryModeForState(state string) string {
	if state == llmclient.HealthStateDisabled {
		return recoveryManualOrBalance
	}
	return recoveryCooldown
}

// Actor manages LLM provider and named aggregator configurations.
type Actor struct {
	actor.Host
	store persist.Persist
	// stateLoaded is set once Load() has completed (first start included).
	// Save refuses to run before that: gospore's ForceCleanup (45e5a37)
	// guarantees OnStop runs even when the actor is aborted mid-OnInit, so
	// a Save firing before Load would persist zero-value providers/
	// aggregators over the real record — a data wipe, not just a leak.
	stateLoaded     atomic.Bool
	Providers       []domain.Provider `gospore:"component,public"`
	AggregatorNames map[string]string `gospore:"component,public"` // actor ID → configured name
	configVersion   int64
	aggregators     map[string]domain.AIManagerAggregatorGetResp // key = config ID (stable)
	aggRefs         map[string]ref.Ref                           // key = config ID (stable)
	aggActorIDs     map[string]string                            // key = config ID → actor ID (stable across restarts)
	// persistedHealth is the durable disabled overlay synced from the llmclient
	// health snapshot (see buildPersistedHealth). It is the only health state
	// that survives restarts and the source of LastFailureAt projections; live
	// cooldown/disabled state lives in llmclient.DefaultProviderHealth.
	persistedHealth map[string]persistedHealthEntry // key = provider name
	// providerLastHealth is a runtime-only cache of the last known health
	// state per provider ("" = healthy/no record). It is used by
	// recordProviderFailureLocked to detect the selectable→unhealthy
	// transition without before/after snapshot comparison (which was broken
	// by the double-RecordFailure fix). Not persisted — rebuilt from the
	// llmclient snapshot + persistedHealth on restart.
	providerLastHealth map[string]string // key = provider name → health state
	// assignments tracks which provider each (agent, slot) is currently
	// assigned to. Keyed by "agentID|slotKind". In-memory only (not persisted)
	// — it re-establishes on first dispatch after restart. Updated fire-and-
	// forget by aiaggregator after each selection; read by all aggregators
	// via provider_assignments for load-aware new/fallback picks.
	assignments  map[string]string // key = agentID|slotKind → providerName
	mu           sync.RWMutex
	actorID      string
	lifecycleCtx context.Context // cancelled when the actor stops
	// modelDefaults stores the user-edited global model default overrides.
	// The built-in baseline is merged on read via effectiveModelDefaults().
	modelDefaults []domain.ModelDefault
	// prunedOnLoad records aggregator config IDs whose unit lists were healed
	// (dangling references removed) by the last Load. Runtime-only: consumed by
	// OnInit to log and persist the healed state, then left stale.
	prunedOnLoad []string
}

type persistState struct {
	Providers     []domain.Provider                            `json:"providers"`
	Aggregators   map[string]domain.AIManagerAggregatorGetResp `json:"aggregators"`
	AggActorIDs   map[string]string                            `json:"aggActorIDs"`      // configID → actorID
	Health        map[string]persistedHealthEntry              `json:"health,omitempty"` // providerName → disabled health (persisted reasons only)
	ModelDefaults []domain.ModelDefault                        `json:"modelDefaults,omitempty"`
}

// persistedHealthEntry is the restart-surviving subset of provider health. It
// intentionally omits messages, tokens and response bodies; only non-sensitive
// classification fields are persisted.
type persistedHealthEntry struct {
	State         string    `json:"state"`
	Reason        string    `json:"reason"`
	LastFailureAt time.Time `json:"lastFailureAt"`
	CooldownUntil time.Time `json:"cooldownUntil,omitempty"`
	RecoveryMode  string    `json:"recoveryMode,omitempty"`
	LastErrorCode string    `json:"lastErrorCode,omitempty"`
	CredentialID  string    `json:"credentialId,omitempty"`
}

// reportProviderFailureReq is the structured failure report sent by
// aiaggregator after a provider request fails. StatusCode/Code/RetryAfter come
// from the normalized llmclient.UpstreamError. It is JSON-compatible with the
// legacy {providerName} payload: absent fields decode to zero values, which the
// classifier treats as a transient availability failure.
type reportProviderFailureReq struct {
	ProviderName string `json:"providerName"`
	Model        string `json:"model,omitempty"`
	StatusCode   int32  `json:"statusCode,omitempty"`
	Code         string `json:"code,omitempty"`
	Type         string `json:"type,omitempty"`
	RetryAfter   int32  `json:"retryAfter,omitempty"` // seconds
	Message      string `json:"message,omitempty"`
}

// reportProviderFailureResp acknowledges the report and returns the cooldown
// deadline (Unix seconds, 0 = none) when the provider entered cooling_down.
type reportProviderFailureResp struct {
	Ok            bool  `json:"ok"`
	CooldownUntil int64 `json:"cooldownUntil,omitempty"`
}

var _ persist.Persistent = (*Actor)(nil)

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("aimanager"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	a.aggregators = make(map[string]domain.AIManagerAggregatorGetResp)
	a.aggRefs = make(map[string]ref.Ref)
	a.aggActorIDs = make(map[string]string)
	a.persistedHealth = make(map[string]persistedHealthEntry)
	a.providerLastHealth = make(map[string]string)
	a.assignments = make(map[string]string)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("aimanager: load state failed", "error", err)
	}
	// Replay the durable disabled overlay into the llmclient health layer so
	// selection and projection see the same disabled state that survived the
	// restart, before any aggregator spawns.
	a.mu.Lock()
	// Persisted state may carry aggregator references to aggregators that no
	// longer exist (deleted by an older build or hand-edited JSON). Heal the
	// unit lists in memory and persist the result so the state stays clean.
	if len(a.prunedOnLoad) > 0 {
		ctx.Logger().Warn("aimanager: pruned dangling aggregator references from persisted state", "affected", strings.Join(a.prunedOnLoad, ","))
		if err := a.Save(); err != nil {
			ctx.Logger().Error("aimanager: persist pruned aggregator state failed", "error", err)
		}
	}
	a.backfillPersistedHealth()
	// Seed providerLastHealth from the post-replay llmclient snapshot so the
	// first failure report after a restart has an accurate baseline for the
	// becameUnhealthy transition. Providers not in the snapshot (healthy) are
	// implicitly "" — no entry needed.
	for name := range a.persistedHealth {
		now := time.Now()
		state, _, _, _ := providerHealthProjection(llmclient.HealthSnapshot(), name, now)
		if state != "" {
			a.providerLastHealth[name] = state
		}
	}
	a.mu.Unlock()
	a.registerProviderGates()
	return nil
}

func (a *Actor) Type() string { return "aimanager" }

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("aimanager: starting", "id", a.actorID)
	a.lifecycleCtx = ctx.Lifecycle()

	// Inject the cooldown policy into the process-wide llmclient health layer.
	// aimanager owns the policy; llmclient applies it to new failure
	// judgements. Re-injected on every provider-config change below.
	a.applyCooldownPolicy()

	if err := ctx.RegisterDomain("aimanager").Expose(); err != nil {
		return fmt.Errorf("aimanager: expose service: %w", err)
	}

	if err := ctx.Register("aimanager.config_version", a.handleConfigVersion, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register config.version: %w", err)
	}
	if err := ctx.Register("aimanager.provider_list", a.handleProviderList, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register provider.list: %w", err)
	}
	if err := ctx.Register("aimanager.provider_full_list", a.handleProviderFullList, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.full_list: %w", err)
	}
	if err := ctx.Register("aimanager.provider_resolve_token", a.handleProviderResolveToken, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.resolve_token: %w", err)
	}
	if err := ctx.Register("aimanager.provider_resolve_model", a.handleProviderResolveModel, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.resolve_model: %w", err)
	}
	if err := ctx.Register("aimanager.provider_report_failure", a.handleProviderReportFailure, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.report_failure: %w", err)
	}
	if err := ctx.Register("aimanager.provider_report_success", a.handleProviderReportSuccess, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.report_success: %w", err)
	}
	if err := ctx.Register("aimanager.provider_assign", a.handleProviderAssign, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.assign: %w", err)
	}
	if err := ctx.Register("aimanager.provider_assignments", a.handleProviderAssignments, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register provider.assignments: %w", err)
	}
	if err := ctx.Register("aimanager.provider_configure", a.handleProviderConfigure, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register provider.configure: %w", err)
	}
	if err := ctx.Register("aimanager.provider_set_token_plan", a.handleProviderSetTokenPlan, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register provider.set_token_plan: %w", err)
	}
	if err := ctx.Register("aimanager.provider_set_disabled", a.handleProviderSetDisabled, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register provider.set_disabled: %w", err)
	}
	if err := ctx.Register("aimanager.provider_reset_health", a.handleProviderResetHealth, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register provider.reset_health: %w", err)
	}
	if err := ctx.Register("aimanager.provider_record_probe", a.handleProviderRecordProbe, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register provider.record_probe: %w", err)
	}
	if err := ctx.Register("aimanager.provider_fetch_models", a.handleProviderFetchModels, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register provider.fetch_models: %w", err)
	}
	if err := ctx.Register("aimanager.model_list", a.handleModelList, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register model.list: %w", err)
	}
	if err := ctx.Register("aimanager.model_defaults_get", a.handleModelDefaultsGet, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register model_defaults_get: %w", err)
	}
	if err := ctx.Register("aimanager.model_defaults_set", a.handleModelDefaultsSet, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register model_defaults_set: %w", err)
	}
	if err := ctx.Register("aimanager.fetch_openrouter_models", a.handleFetchOpenRouterModels, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register fetch_openrouter_models: %w", err)
	}
	if err := ctx.Register("aimanager.aggregator_list", a.handleAggregatorList, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register aggregator.list: %w", err)
	}
	if err := ctx.Register("aimanager.aggregator_configure", a.handleAggregatorConfigure, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register aggregator.configure: %w", err)
	}
	if err := ctx.Register("aimanager.aggregator_set_disabled", a.handleAggregatorSetDisabled, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register aggregator.set_disabled: %w", err)
	}
	if err := ctx.Register("aimanager.aggregator_get", a.handleAggregatorGet, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register aggregator.get: %w", err)
	}
	if err := ctx.Register("aimanager.aggregator_resolve", a.handleAggregatorResolve, actor.Internal()); err != nil {
		return fmt.Errorf("aimanager: register aggregator.resolve: %w", err)
	}
	if err := ctx.Register("aimanager.config_export", a.handleConfigExport, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register config.export: %w", err)
	}
	if err := ctx.Register("aimanager.config_import", a.handleConfigImport, actor.AdminOnly()); err != nil {
		return fmt.Errorf("aimanager: register config.import: %w", err)
	}
	if err := ctx.Register("aimanager.unit_health_list", a.handleUnitHealthList, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register unit_health_list: %w", err)
	}
	if err := ctx.Register("aimanager.list_units", a.handleListUnits, actor.Public()); err != nil {
		return fmt.Errorf("aimanager: register list_units: %w", err)
	}

	// Spawn aiaggregator children from persisted config.
	//
	// All mutations of a.aggregators / a.aggRefs / a.aggActorIDs hold a.mu:
	// stateless handlers (aggregator_list, provider_list, …) are invocable the
	// moment their Register call above returns, and they read these maps under
	// a.mu.RLock — the transport dispatches stateless calls on forked
	// goroutines without waiting for OnStart to finish. notifyAggregator is
	// called outside the critical section because it takes the lock itself.
	oldAggregators := a.aggregators
	a.mu.Lock()
	a.aggregators = make(map[string]domain.AIManagerAggregatorGetResp, len(oldAggregators))
	a.mu.Unlock()
	for configID, cfg := range oldAggregators {
		a.mu.Lock()
		aggRef := a.spawnAggregator(ctx, configID, a.aggActorIDs[configID])
		if aggRef != nil {
			a.aggregators[configID] = cfg
		}
		a.mu.Unlock()
		if aggRef != nil {
			a.notifyAggregator(configID)
		}
	}

	// Spawn the system-level auto aggregator that dynamically collects all
	// models from all registered providers, reusing a persisted actor ID so
	// agents bound to it remain valid across restarts.
	autoUnits := 0
	autoStrategy := "smart"
	a.mu.Lock()
	autoRef := a.spawnAggregator(ctx, autoAggregatorID, a.aggActorIDs[autoAggregatorID])
	if autoRef != nil {
		units := a.buildAutoUnits()
		prev, hadPrev := oldAggregators[autoAggregatorID]
		if hadPrev && prev.Strategy != "" {
			autoStrategy = normalizeStrategy(prev.Strategy)
		}
		a.aggregators[autoAggregatorID] = domain.AIManagerAggregatorGetResp{
			ID:       autoAggregatorID,
			Name:     "Auto (All Providers)",
			Strategy: autoStrategy,
			Disabled: prev.Disabled,
			Units:    units,
		}
		autoUnits = len(units)
	}
	a.mu.Unlock()
	if autoRef != nil {
		a.notifyAggregator(autoAggregatorID)
		ctx.Logger().Info("aimanager: auto aggregator spawned", "units", autoUnits, "strategy", autoStrategy)
	}

	a.mu.Lock()
	a.rebuildAggregatorNames()
	a.mu.Unlock()

	return nil
}

func (a *Actor) OnStop(ctx actor.Context) error {
	for _, r := range a.aggRefs {
		_ = ctx.Stop(r)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.stateLoaded.Load() {
		// gospore's ForceCleanup (45e5a37) guarantees OnStop runs even when
		// the actor is aborted mid-OnInit. Saving then would persist the
		// zero-value providers/aggregators over the real record — a data
		// wipe, not just a leak. Nothing was mutated; skip the save.
		return nil
	}
	return a.Save()
}

// spawnAggregator creates an aiaggregator child with the given configID as its
// spawn name. If one already exists for this configID it returns the cached ref.
//
// ID assignment policy (CLAUDE.md §存档重建):
//   - preferredActorID == "": fresh spawn, runtime assigns new ID.
//   - preferredActorID != "": recovery from persisted state. The ID MUST be
//     reused as-is. A malformed persisted ID or a collision with an existing
//     actor is treated as data corruption — we refuse to spawn rather than
//     silently auto-genning and orphaning every agent that references this
//     aggregator via AggregatorActorID.
func (a *Actor) spawnAggregator(ctx actor.PureContext, configID string, preferredActorID string) ref.Ref {
	if r, ok := a.aggRefs[configID]; ok {
		if _, stillLive := ctx.LookupID(r.ID()); stillLive {
			return r
		}
		// Dead ref — reuse its actor ID if no preferred ID is given so
		// external references (agents) stay valid.
		if preferredActorID == "" {
			preferredActorID = r.ID().String()
		}
		delete(a.aggRefs, configID)
	}

	props := actor.PropsFromFunc(aiaggregator.NewActor()).WithPlanner()
	if preferredActorID != "" {
		cid, err := identity.ParseCanonicalID(preferredActorID)
		if err != nil {
			ctx.Logger().Error("aimanager: refused to spawn aggregator: persisted ActorID is malformed",
				"configId", configID, "actorID", preferredActorID, "error", err)
			return nil
		}
		idObj := id.From(cid)
		if _, occupied := ctx.LookupID(idObj); occupied {
			ctx.Logger().Error("aimanager: refused to spawn aggregator: persisted ActorID collides with an existing actor",
				"configId", configID, "actorID", preferredActorID)
			return nil
		}
		props = props.WithID(idObj)
	}

	aggRef, err := ctx.Spawn(props, configID)
	if err != nil {
		ctx.Logger().Error("aimanager: spawn aggregator failed", "configId", configID, "error", err)
		return nil
	}
	if aggRef == nil {
		return nil
	}
	a.aggRefs[configID] = aggRef
	if a.aggActorIDs == nil {
		a.aggActorIDs = make(map[string]string)
	}
	a.aggActorIDs[configID] = aggRef.ID().String()
	return aggRef
}

// Save persists provider/aggregator config plus the durable disabled health
// overlay synced from the llmclient health snapshot (see buildPersistedHealth).
// Caller must hold a.mu (write).
func (a *Actor) Save() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("aimanager"))
		if err != nil {
			return err
		}
	}
	aggCopy := make(map[string]domain.AIManagerAggregatorGetResp, len(a.aggregators))
	for cid, cfg := range a.aggregators {
		if cid == autoAggregatorID {
			continue
		}
		cfg.Strategy = normalizeStrategy(cfg.Strategy)
		aggCopy[cid] = cfg
	}
	// AggActorIDs is the durable configID → actorID identity map. Keep entries
	// for aggregators that still exist (plus the auto aggregator, whose config
	// is never persisted but whose actor ID must survive), overlaid by live
	// refs. Without this, a Save between Load and spawn (e.g. the dangling-ref
	// heal in OnInit) would wipe every persisted actor ID.
	aggActorIDs := make(map[string]string, len(a.aggRefs)+len(a.aggActorIDs))
	for cid, actorID := range a.aggActorIDs {
		if cid == autoAggregatorID {
			aggActorIDs[cid] = actorID
			continue
		}
		if _, alive := a.aggregators[cid]; alive {
			aggActorIDs[cid] = actorID
		}
	}
	for cid, r := range a.aggRefs {
		aggActorIDs[cid] = r.ID().String()
	}

	state := persistState{
		Providers:     a.Providers,
		Aggregators:   aggCopy,
		AggActorIDs:   aggActorIDs,
		Health:        a.buildPersistedHealth(),
		ModelDefaults: a.modelDefaults,
	}
	return a.store.Save(a.actorID, state)
}

func (a *Actor) Load() (err error) {
	// Mark state as load-complete only after a successful pass over every
	// success path (first start included) — Save's wipe guard depends on it.
	defer func() {
		if err == nil {
			a.stateLoaded.Store(true)
		}
	}()
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("aimanager"))
		if err != nil {
			return err
		}
	}
	// Probe raw JSON to detect format. ErrNotExist means first start —
	// legitimate empty state, leave a.Providers at zero value.
	var raw map[string]any
	if err := persist.LoadOrZero(a.store, a.actorID, &raw); err != nil {
		return err
	}
	if raw == nil {
		return nil
	}

	// Migrate legacy provider state where Models was array<string> (pre-refactor)
	// into array<ProviderModel>. Rewrites the in-memory JSON in-place; the next
	// Save() call persists the new format.
	migrateLegacyProviderModels(raw)

	// Re-encode the (possibly migrated) raw map and decode into the target
	// struct in one pass. This avoids store.Load re-reading the file (which
	// would still contain the legacy format until the next Save).
	rawBytes, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("aimanager: re-encode migrated state: %w", err)
	}

	// Old format has "aggregatorSettings".
	if _, isOld := raw["aggregatorSettings"]; isOld {
		var oldState struct {
			Providers          []domain.Provider                            `json:"providers"`
			AggregatorSettings map[string]domain.AIManagerAggregatorGetResp `json:"aggregatorSettings"`
		}
		if err := json.Unmarshal(rawBytes, &oldState); err == nil {
			a.Providers = oldState.Providers
			if oldState.AggregatorSettings != nil {
				a.aggregators = make(map[string]domain.AIManagerAggregatorGetResp)
				for kind, settings := range oldState.AggregatorSettings {
					name := kind
					if kind == "anthropic" {
						name = "Anthropic"
					} else if kind == "openai" {
						name = "OpenAI"
					}
					units := settings.Units
					if units == nil {
						units = []domain.ManualCallableUnit{}
					}
					a.aggregators[kind] = domain.AIManagerAggregatorGetResp{
						ID:    kind,
						Name:  name,
						Units: units,
					}
				}
			}
			a.prunedOnLoad = pruneDanglingAggregatorRefs(a.aggregators)
			return nil
		}
	}

	// New format — keys may be config IDs or (legacy) actor IDs.
	var state persistState
	if err := json.Unmarshal(rawBytes, &state); err == nil {
		a.Providers = state.Providers
		if state.Aggregators != nil {
			a.aggregators = make(map[string]domain.AIManagerAggregatorGetResp, len(state.Aggregators))
			for k, v := range state.Aggregators {
				if v.Units == nil {
					v.Units = []domain.ManualCallableUnit{}
				}
				v.Strategy = normalizeStrategy(v.Strategy)
				// Prefer the config ID inside the value; fall back to the map key.
				key := v.ID
				if key == "" {
					key = k
				}
				a.aggregators[key] = v
			}
		}
		if state.AggActorIDs != nil {
			a.aggActorIDs = state.AggActorIDs
		}
		if state.Health != nil {
			// Keep only the durable disabled entries in the overlay. Legacy
			// cooling_down entries are dropped (cooldowns are transient and
			// expire naturally in llmclient; only disabled survives restart).
			// The actual llmclient replay happens in backfillPersistedHealth
			// (called from OnInit after Load).
			if a.persistedHealth == nil {
				a.persistedHealth = make(map[string]persistedHealthEntry)
			}
			for name, e := range state.Health {
				if e.State != llmclient.HealthStateDisabled {
					continue
				}
				a.persistedHealth[name] = e
			}
		}
		a.modelDefaults = state.ModelDefaults
		a.prunedOnLoad = pruneDanglingAggregatorRefs(a.aggregators)
		return nil
	}

	// Fallback: very old format (providers only).
	return json.Unmarshal(rawBytes, &a.Providers)
}

// migrateLegacyProviderModels detects providers persisted with the pre-refactor
// Models field as array<string> and rewrites each entry to array<ProviderModel>
// in-place. Idempotent: providers already in the new format are untouched.
func migrateLegacyProviderModels(raw map[string]any) {
	providers, ok := raw["providers"].([]any)
	if !ok {
		return
	}
	for i, p := range providers {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		models, ok := pm["Models"].([]any)
		if !ok {
			continue
		}
		needsMigration := false
		for _, m := range models {
			if _, isStr := m.(string); isStr {
				needsMigration = true
				break
			}
		}
		if !needsMigration {
			continue
		}
		converted := make([]any, 0, len(models))
		for _, m := range models {
			if s, ok := m.(string); ok {
				converted = append(converted, map[string]any{"Name": s})
			} else {
				converted = append(converted, m)
			}
		}
		pm["Models"] = converted
		providers[i] = pm
	}
	raw["providers"] = providers
}

// parseDisableWindow parses a "HH:MM" time string into minutes since midnight.
// Returns -1 for invalid format.
func parseDisableWindow(s string) int {
	if len(s) != 5 || s[2] != ':' || s[0] < '0' || s[0] > '9' || s[1] < '0' || s[1] > '9' || s[3] < '0' || s[3] > '9' || s[4] < '0' || s[4] > '9' {
		return -1
	}
	h := int(s[0]-'0')*10 + int(s[1]-'0')
	m := int(s[3]-'0')*10 + int(s[4]-'0')
	if h > 23 || m > 59 {
		return -1
	}
	return h*60 + m
}

func validateDisableWindows(windows []domain.ProviderDisableWindow) error {
	for i, w := range windows {
		if parseDisableWindow(w.Start) < 0 || parseDisableWindow(w.End) < 0 {
			return fmt.Errorf("disableWindows[%d] must use HH:MM values", i)
		}
	}
	return nil
}

// isoWeekday converts time.Weekday to ISO weekday (1=Monday .. 7=Sunday).
func isoWeekday(t time.Time) int {
	wd := t.Weekday()
	if wd == time.Sunday {
		return 7
	}
	return int(wd)
}

// weekdayAllowed reports whether the ISO weekday of t is present in days.
// An empty/absent days list means every day is allowed.
func weekdayAllowed(days []int32, t time.Time) bool {
	if len(days) == 0 {
		return true
	}
	wd := isoWeekday(t)
	for _, d := range days {
		if int(d) == wd {
			return true
		}
	}
	return false
}

// disableWindowsEqual compares two disable-window lists. Field order is
// significant (it is the user's display order in the settings editor).
func disableWindowsEqual(a, b []domain.ProviderDisableWindow) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Start != b[i].Start ||
			a[i].End != b[i].End ||
			!slices.Equal(a[i].Days, b[i].Days) {
			return false
		}
	}
	return true
}

// isProviderInDisableWindow reports whether the current local time falls inside
// any of the provider's daily disable windows. Each window is a [Start, End)
// interval in local time; when Start > End the interval wraps past midnight.
// Start == End disables the provider for the entire day.
func isProviderInDisableWindow(windows []domain.ProviderDisableWindow, now time.Time) bool {
	return providerDisableDeadline(windows, now) > 0
}

// providerDisableDeadline returns the Unix timestamp when the union of all
// active disable windows ends (i.e. when the provider becomes available again),
// or 0 if the provider is not currently inside any disable window.
//
// The union is computed as a per-minute coverage array over today and tomorrow.
// Projecting each recurring window onto both days merges overlapping and
// cross-midnight windows without relying on their input order.
func providerDisableDeadline(windows []domain.ProviderDisableWindow, now time.Time) int64 {
	if len(windows) == 0 {
		return 0
	}
	curMin := now.Hour()*60 + now.Minute()
	loc := now.Location()
	var coverage [2880]bool
	allDay := false
	for _, w := range windows {
		s := parseDisableWindow(w.Start)
		e := parseDisableWindow(w.End)
		if s < 0 || e < 0 {
			continue
		}
		if s == e {
			if len(w.Days) == 0 {
				allDay = true
				for i := range coverage {
					coverage[i] = true
				}
				break
			}
			// Start == End with an explicit day filter means "the entire day
			// on the allowed weekdays". Evaluate today and tomorrow separately.
			for day := 0; day < 2; day++ {
				base := day * 1440
				t := time.Date(now.Year(), now.Month(), now.Day()+day, 12, 0, 0, 0, loc)
				if weekdayAllowed(w.Days, t) {
					for i := base; i < base+1440; i++ {
						coverage[i] = true
					}
				}
			}
			continue
		}
		if s < e {
			// Non-wrapping window: each occurrence is confined to a single
			// calendar day and is active when that day's weekday is allowed.
			for day := 0; day < 2; day++ {
				base := day * 1440
				t := time.Date(now.Year(), now.Month(), now.Day()+day, 12, 0, 0, 0, loc)
				if weekdayAllowed(w.Days, t) {
					for i := base + s; i < base+e; i++ {
						coverage[i] = true
					}
				}
			}
		} else {
			// Wrapping window: split at midnight. Evaluate it on the three
			// calendar days that can overlap the two-day coverage (yesterday,
			// today, tomorrow). Each half is checked against the weekday of the
			// calendar day it falls on.
			for wrapDay := -1; wrapDay < 2; wrapDay++ {
				wbase := wrapDay * 1440
				t1 := time.Date(now.Year(), now.Month(), now.Day()+wrapDay, 12, 0, 0, 0, loc)
				if weekdayAllowed(w.Days, t1) {
					start := wbase + s
					if start < 0 {
						start = 0
					}
					end := wbase + 1440
					if end > len(coverage) {
						end = len(coverage)
					}
					for i := start; i < end; i++ {
						coverage[i] = true
					}
				}
				t2 := time.Date(now.Year(), now.Month(), now.Day()+wrapDay+1, 12, 0, 0, 0, loc)
				if weekdayAllowed(w.Days, t2) {
					start := wbase + 1440
					if start < 0 {
						start = 0
					}
					end := wbase + 1440 + e
					if end > len(coverage) {
						end = len(coverage)
					}
					for i := start; i < end; i++ {
						coverage[i] = true
					}
				}
			}
		}
	}
	if !coverage[curMin] {
		return 0
	}
	if allDay {
		end := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
		return end.Unix()
	}
	for i := curMin; i < len(coverage); i++ {
		if !coverage[i] {
			dayOffset := i / 1440
			minOfDay := i % 1440
			t := time.Date(now.Year(), now.Month(), now.Day()+dayOffset, minOfDay/60, minOfDay%60, 0, 0, loc)
			return t.Unix()
		}
	}
	// The configured daily windows cover every minute of both projected days.
	// The next poll recomputes the recurring schedule before this deadline.
	end := time.Date(now.Year(), now.Month(), now.Day()+2, 0, 0, 0, 0, loc)
	return end.Unix()
}

// cooldownPolicy is the process-wide cooldown tuning owned by aimanager and
// injected into the llmclient health layer via SetCooldownPolicy (方式 A).
// Values consolidated from the pre-refactor per-layer constants: 1.5× backoff,
// 15m cap, 429 floor 60s / ceiling 15m, quota 10m, availability threshold 5.
func cooldownPolicy() llmclient.CooldownPolicy {
	return llmclient.DefaultCooldownPolicy()
}

// applyCooldownPolicy injects the aimanager-owned policy into the process-wide
// llmclient health layer. It is called on startup and on every provider-config
// change. Policy changes only affect new failure judgements.
func (a *Actor) applyCooldownPolicy() {
	llmclient.SetCooldownPolicy(cooldownPolicy())
}

// unitKey mirrors llmclient's canonical "provider::model" lookup key.
func unitKey(provider, model string) string { return provider + "::" + model }

// unitHealthProjection maps one unit's llmclient snapshot entry onto the
// ManualCallableUnit health fields (state/reason/recoveryMode/cooldownUntil).
// Expired cooldowns project as healthy (the unit is selectable again);
// disabled units carry manual recovery. Returns empty values when the unit has
// no recorded health.
func unitHealthProjection(snap map[string]llmclient.UnitHealthSnapshot, provider, model string, now time.Time) (state, reason, recoveryMode string, cooldownUntil int64) {
	s, ok := snap[unitKey(provider, model)]
	if !ok || s.State == llmclient.HealthStateHealthy {
		return "", "", "", 0
	}
	switch s.State {
	case llmclient.HealthStateDisabled:
		return s.State, s.Reason, recoveryModeForState(s.State), 0
	case llmclient.HealthStateCoolingDown:
		if !now.Before(s.CooldownUntil) {
			return "", "", "", 0
		}
		return s.State, s.Reason, recoveryModeForState(s.State), s.CooldownUntil.Unix()
	}
	return "", "", "", 0
}

// providerHealthProjection aggregates per-unit llmclient snapshots into the
// provider-level health fields used by ProviderListResp projections. The worst
// unit state wins: disabled > cooling_down > healthy. For cooling_down the
// furthest-future deadline is projected so the badge persists until every unit
// recovers. Returns empty values when the provider has no non-healthy unit.
func providerHealthProjection(snap map[string]llmclient.UnitHealthSnapshot, provider string, now time.Time) (state, reason, recoveryMode string, cooldownUntil int64) {
	worst := ""
	var worstReason string
	var maxCD time.Time
	for _, s := range snap {
		if s.Provider != provider {
			continue
		}
		if s.State == llmclient.HealthStateDisabled {
			return s.State, s.Reason, recoveryModeForState(s.State), 0
		}
		if s.State == llmclient.HealthStateCoolingDown && now.Before(s.CooldownUntil) {
			if worst == "" {
				worst = llmclient.HealthStateCoolingDown
				worstReason = s.Reason
			}
			if s.CooldownUntil.After(maxCD) {
				maxCD = s.CooldownUntil
			}
		}
	}
	if worst != "" {
		return worst, worstReason, recoveryModeForState(worst), maxCD.Unix()
	}
	return "", "", "", 0
}

// buildPersistedHealth syncs the durable disabled overlay from the llmclient
// health snapshot. Only disabled units (authentication/authorization) survive
// restarts; cooling_down is transient and expires naturally, so it is not
// persisted. LastFailureAt is preserved from the previous overlay when present
// so the projected timestamp stays stable across saves. The overlay is stored
// back into a.persistedHealth (callers then persist it via Save). Caller must
// hold a.mu.
func (a *Actor) buildPersistedHealth() map[string]persistedHealthEntry {
	snap := llmclient.HealthSnapshot()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]persistedHealthEntry)
	now := time.Now()
	for _, k := range keys {
		s := snap[k]
		if s.State != llmclient.HealthStateDisabled {
			continue
		}
		prev, hadPrev := a.persistedHealth[s.Provider]
		lastFailureAt := now
		if hadPrev && !prev.LastFailureAt.IsZero() {
			lastFailureAt = prev.LastFailureAt
		}
		out[s.Provider] = persistedHealthEntry{
			State:         s.State,
			Reason:        s.Reason,
			LastFailureAt: lastFailureAt,
			RecoveryMode:  recoveryModeForState(s.State),
			CredentialID:  prev.CredentialID,
		}
	}
	a.persistedHealth = out
	return out
}

// backfillPersistedHealth replays the durable disabled overlay into the
// llmclient health layer after a restart, so selection and projection see the
// same disabled state that survived the restart. Authentication/authorization
// entries are re-recorded via RecordFailure against every model of the
// provider. Legacy quota_exhausted-disabled entries are replayed as a quota
// cooldown (self-healing instead of permanently stuck); configuration_error
// entries cannot be reproduced by the llmclient classifier (ClassStop) and are
// dropped. Caller must hold a.mu.
func (a *Actor) backfillPersistedHealth() {
	for name, e := range a.persistedHealth {
		if e.State != llmclient.HealthStateDisabled {
			continue
		}
		switch e.Reason {
		case llmclient.HealthReasonAuthenticationFailed, llmclient.HealthReasonAuthorizationFailed:
			status := 401
			if e.Reason == llmclient.HealthReasonAuthorizationFailed {
				status = 403
			}
			a.recordFailureForProvider(name, &llmclient.UpstreamError{StatusCode: status})
		case llmclient.HealthReasonQuotaExhausted:
			// Legacy migration: quota_exhausted was persisted as disabled.
			// Replay as a quota cooldown so the provider self-heals.
			a.recordFailureForProvider(name, &llmclient.UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})
		default:
			// configuration_error cannot be reproduced (ClassStop) — drop.
			delete(a.persistedHealth, name)
		}
	}
}

// recordFailureForProvider records a failure against every model of the
// provider, so a provider-level failure report propagates to all units (the
// provider-scoped health semantics preserved at unit granularity). Providers
// without models have nothing to cool. Caller must hold a.mu.
func (a *Actor) recordFailureForProvider(name string, err error) {
	p := a.lookupProvider(name)
	if p == nil {
		return
	}
	for _, m := range p.Models {
		llmclient.RecordFailure(name, m.Name, err)
	}
}

// recordSuccessForProvider clears the failure counter for every model of the
// provider. Caller must hold a.mu.
func (a *Actor) recordSuccessForProvider(name string) {
	p := a.lookupProvider(name)
	if p == nil {
		return
	}
	for _, m := range p.Models {
		llmclient.RecordSuccess(name, m.Name)
	}
}

// clearProviderHealthLocked removes every unit of the given provider from the
// llmclient health layer and drops its persisted overlay entry. Other
// providers' units (disabled and cooling_down alike) are untouched — the clear
// is per-provider via llmclient.ClearProvider, so no var-swap of the shared
// health instance and no race window with in-flight dispatches. Caller must
// hold a.mu.
func (a *Actor) clearProviderHealthLocked(name string) {
	llmclient.ClearProvider(name)
	delete(a.persistedHealth, name)
	delete(a.providerLastHealth, name)
}

// providerCooldownUntilLocked returns the furthest-future active cooldown
// deadline (Unix seconds) across the provider's units, or 0 when the provider
// is not currently cooling down. Caller must hold a.mu.
func (a *Actor) providerCooldownUntilLocked(name string) int64 {
	_, _, _, cd := providerHealthProjection(llmclient.HealthSnapshot(), name, time.Now())
	return cd
}

// recordProviderFailureLocked records a structured failure report into the
// llmclient health layer and reports whether the provider transitioned from
// selectable (healthy/no record) into cooling_down or disabled. A report
// without a model applies to every model of the provider. Caller must hold
// a.mu.
func (a *Actor) recordProviderFailureLocked(req reportProviderFailureReq, err error) (becameUnhealthy bool) {
	if req.Model == "" {
		// Provider-level failure not already recorded by aiaggregator;
		// broadcast to every model of the provider.
		a.recordFailureForProvider(req.ProviderName, err)
	}
	// When req.Model != "", aiaggregator.applyStreamOpenFailure already
	// called llmclient.RecordFailure synchronously before sending this
	// report. Re-recording would double-count consecutiveFailures.

	now := time.Now()
	currentState, _, _, _ := providerHealthProjection(llmclient.HealthSnapshot(), req.ProviderName, now)
	lastState := a.providerLastHealth[req.ProviderName]
	wasSelectable := lastState == "" || lastState == llmclient.HealthStateHealthy
	isUnhealthy := currentState == llmclient.HealthStateCoolingDown || currentState == llmclient.HealthStateDisabled
	becameUnhealthy = wasSelectable && isUnhealthy

	if currentState != lastState {
		if a.providerLastHealth == nil {
			a.providerLastHealth = make(map[string]string)
		}
		a.providerLastHealth[req.ProviderName] = currentState
	}
	return becameUnhealthy
}

// aggregatorIDsUsingProviderLocked returns the config IDs of aggregators whose
// unit list references the given provider. Caller must hold a.mu (read).
func (a *Actor) aggregatorIDsUsingProviderLocked(name string) []string {
	var out []string
	for configID, cfg := range a.aggregators {
		for _, u := range cfg.Units {
			if u.ProviderName == name {
				out = append(out, configID)
				break
			}
		}
	}
	return out
}

// aggregatorIDsUsingAggregatorLocked returns the config ids of every aggregator
// whose pool embeds a nested reference to the given child aggregator config id.
// Toggling a child's Disabled flag changes how these parents must project the
// entry (resolveUnitsWithDisabledRefs), so they all need a config push.
// Caller must hold a.mu.
func (a *Actor) aggregatorIDsUsingAggregatorLocked(childID string) []string {
	var out []string
	for configID, cfg := range a.aggregators {
		if configID == childID {
			continue
		}
		for _, u := range cfg.Units {
			if u.AggregatorID == childID {
				out = append(out, configID)
				break
			}
		}
	}
	return out
}

// handleProviderReportFailure receives a structured failure report from
// aiaggregator, records it into the llmclient health layer (the authority for
// cooldown/disabled state), syncs the durable disabled overlay, and notifies
// affected aggregators when the provider becomes unhealthy. The response shape
// is unchanged: Ok + the cooldown deadline when the provider is cooling down.
func (a *Actor) handleProviderReportFailure(ctx actor.PureContext, req reportProviderFailureReq) (reportProviderFailureResp, error) {
	if req.ProviderName == "" {
		return reportProviderFailureResp{Ok: true}, nil
	}

	upstreamErr := &llmclient.UpstreamError{
		StatusCode: int(req.StatusCode),
		Code:       req.Code,
		Type:       req.Type,
		Message:    req.Message,
		RetryAfter: time.Duration(req.RetryAfter) * time.Second,
	}

	a.mu.Lock()
	becameUnhealthy := a.recordProviderFailureLocked(req, upstreamErr)
	cooldownUntil := a.providerCooldownUntilLocked(req.ProviderName)

	// Collect aggregator config IDs that reference this provider so we can
	// push a fresh config (with the provider filtered out) outside the lock.
	var affectedAggregatorIDs []string
	if becameUnhealthy {
		affectedAggregatorIDs = a.aggregatorIDsUsingProviderLocked(req.ProviderName)
	}

	if err := a.Save(); err != nil {
		ctx.Logger().Error("aimanager: persist provider health failed",
			"provider", req.ProviderName, "error", err)
	}
	a.mu.Unlock()

	if becameUnhealthy {
		ctx.Logger().Warn("aimanager: provider became unhealthy",
			"provider", req.ProviderName,
			"affectedAggregators", len(affectedAggregatorIDs))
		for _, configID := range affectedAggregatorIDs {
			a.notifyAggregator(configID)
		}
		a.notifyAutoAggregator()
	}

	return reportProviderFailureResp{Ok: true, CooldownUntil: cooldownUntil}, nil
}

// handleProviderReportSuccess is called by aiaggregator after a provider
// request succeeds. Under the llmclient health semantics a success resets the
// unit's consecutive-failure counter; it does NOT shorten an active cooldown or
// clear a disabled state (cooldowns expire naturally, disabled units require
// explicit recovery). The persisted overlay is re-synced so a provider that
// recovered no longer carries stale disabled state.
func (a *Actor) handleProviderReportSuccess(ctx actor.PureContext, req reportProviderFailureReq) (reportProviderFailureResp, error) {
	if req.ProviderName == "" {
		return reportProviderFailureResp{Ok: true}, nil
	}

	a.mu.Lock()
	if req.Model != "" {
		llmclient.RecordSuccess(req.ProviderName, req.Model)
	} else {
		a.recordSuccessForProvider(req.ProviderName)
	}
	// Update providerLastHealth so the next failure report has an accurate
	// baseline. RecordSuccess resets the failure counter but does not clear
	// an active cooldown or disabled state — project the real current state.
	now := time.Now()
	currentState, _, _, _ := providerHealthProjection(llmclient.HealthSnapshot(), req.ProviderName, now)
	if a.providerLastHealth == nil {
		a.providerLastHealth = make(map[string]string)
	}
	if currentState == "" {
		// Provider is healthy/selectable — clear the entry so the next
		// failure detects the selectable→unhealthy transition.
		delete(a.providerLastHealth, req.ProviderName)
	} else if currentState != a.providerLastHealth[req.ProviderName] {
		a.providerLastHealth[req.ProviderName] = currentState
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("aimanager: persist provider health clear failed",
			"provider", req.ProviderName, "error", err)
	}
	a.mu.Unlock()

	return reportProviderFailureResp{Ok: true}, nil
}

// providerHealthResetReq is the fire-and-forget reset broadcast sent by
// aimanager to every aggregator after a manual health reset, so unit-level
// local state (disabledReason / cooldown) is cleared too.
type providerHealthResetReq struct {
	ProviderName string `json:"providerName"`
}

// handleProviderResetHealth is the manual recovery entry (UI "recover"
// action). It clears the provider's llmclient unit health (cooldown or
// disabled), drops its persisted overlay entry, persists the clearing,
// broadcasts aiaggregator.provider_health_reset to every aggregator so unit-level
// local state is cleared too, and pushes fresh configs so health annotations
// disappear from status projections.
func (a *Actor) handleProviderResetHealth(ctx actor.PureContext, req domain.AIManagerProviderResetHealthReq) (domain.AIManagerProviderResetHealthResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerProviderResetHealthResp{}, err
	}
	if req.ProviderName == "" {
		return domain.AIManagerProviderResetHealthResp{Ok: false, Error: "providerName is required"}, nil
	}

	a.mu.Lock()
	a.clearProviderHealthLocked(req.ProviderName)
	refs := make([]ref.Ref, 0, len(a.aggRefs))
	for _, r := range a.aggRefs {
		refs = append(refs, r)
	}
	configIDs := make([]string, 0, len(a.aggregators))
	for configID := range a.aggregators {
		configIDs = append(configIDs, configID)
	}
	if err := a.Save(); err != nil {
		ctx.Logger().Error("aimanager: persist provider health reset failed",
			"provider", req.ProviderName, "error", err)
	}
	a.mu.Unlock()

	for _, r := range refs {
		if r == nil {
			continue
		}
		call := r.Invoke(a.lifecycleCtx, "aiaggregator.provider_health_reset", providerHealthResetReq{ProviderName: req.ProviderName})
		_ = call.Close()
	}
	for _, configID := range configIDs {
		a.notifyAggregator(configID)
	}
	ctx.Logger().Info("aimanager: provider health manually reset",
		"provider", req.ProviderName)

	return domain.AIManagerProviderResetHealthResp{Ok: true}, nil
}

// handleProviderRecordProbe persists the outcome of a UI availability probe on
// a single model unit (see ProbeState on ProviderModel). It is informational
// state only: no health-record mutation, no aggregator propagation, no config
// version bump — dispatch keeps using the failure-driven health record.
func (a *Actor) handleProviderRecordProbe(ctx actor.PureContext, req domain.AIManagerProviderRecordProbeReq) (domain.AIManagerProviderRecordProbeResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerProviderRecordProbeResp{}, err
	}
	if req.ProviderName == "" || req.Model == "" {
		return domain.AIManagerProviderRecordProbeResp{Ok: false, Error: "providerName and model are required"}, nil
	}

	errLine := ""
	if !req.Ok && req.Error != "" {
		errLine = strings.SplitN(req.Error, "\n", 2)[0]
		if len(errLine) > 200 {
			errLine = errLine[:200]
		}
	}

	a.mu.Lock()
	for i, p := range a.Providers {
		if p.Name != req.ProviderName {
			continue
		}
		for j, m := range p.Models {
			if m.Name != req.Model {
				continue
			}
			m.ProbeState = ""
			if req.Ok {
				m.ProbeState = "ok"
				m.ProbeLatencyMs = req.LatencyMs
				m.ProbeError = ""
			} else {
				m.ProbeState = "failed"
				m.ProbeLatencyMs = 0
				m.ProbeError = errLine
			}
			m.ProbeAt = time.Now().Unix()
			p.Models[j] = m
			a.Providers[i] = p
			if err := a.Save(); err != nil {
				a.mu.Unlock()
				ctx.Logger().Error("aimanager: persist probe result failed",
					"provider", req.ProviderName, "model", req.Model, "error", err)
				return domain.AIManagerProviderRecordProbeResp{Ok: false, Error: "persist failed"}, nil
			}
			a.mu.Unlock()
			return domain.AIManagerProviderRecordProbeResp{Ok: true}, nil
		}
		a.mu.Unlock()
		return domain.AIManagerProviderRecordProbeResp{Ok: false, Error: "model not found"}, nil
	}
	a.mu.Unlock()
	return domain.AIManagerProviderRecordProbeResp{Ok: false, Error: "provider not found"}, nil
}

// providerAssignReq is the fire-and-forget assignment report sent by
// aiaggregator after it selects a unit. It records which provider a given
// (agent, slot) is currently using so all aggregators can load-balance
// new/fallback picks via provider_assignments.
type providerAssignReq struct {
	AgentID      string `json:"agentId"`
	SlotKind     string `json:"slotKind"`
	ProviderName string `json:"providerName"`
}

// handleProviderAssign records the provider assignment for a (agent, slot).
// Called fire-and-forget by aiaggregator after each selection.
func (a *Actor) handleProviderAssign(_ actor.PureContext, req providerAssignReq) (reportProviderFailureResp, error) {
	if req.AgentID == "" || req.ProviderName == "" {
		return reportProviderFailureResp{Ok: true}, nil
	}
	key := req.AgentID + "|" + req.SlotKind
	a.mu.Lock()
	a.assignments[key] = req.ProviderName
	a.mu.Unlock()
	return reportProviderFailureResp{Ok: true}, nil
}

// providerAssignmentsResp is the snapshot of provider → agent-slot count,
// derived from the assignments map. Used by sticky strategies to pick the
// least-loaded provider on new/fallback selections.
type providerAssignmentsResp struct {
	Counts map[string]int `json:"counts"`
}

// handleProviderAssignments returns the current provider→assignment count
// snapshot. Counts how many distinct (agent, slot) keys map to each provider.
func (a *Actor) handleProviderAssignments(_ actor.PureContext) (providerAssignmentsResp, error) {
	a.mu.RLock()
	counts := make(map[string]int, len(a.assignments))
	for _, provider := range a.assignments {
		counts[provider]++
	}
	a.mu.RUnlock()
	return providerAssignmentsResp{Counts: counts}, nil
}

// inferModality classifies a model as "image", "video", or "chat" based on its
// name. An explicit override (from ProviderModel.Modality) takes precedence.
// Image patterns cover the image-output families advertised by OpenRouter's
// catalog (gemini-*-image / "Nano Banana", gpt-*-image, grok-imagine-image)
// plus relay aliases (nano-banana, cogview, image-01).
func inferModality(modelName, explicit string) string {
	if explicit != "" {
		return explicit
	}
	n := strings.ToLower(modelName)
	// OpenRouter variant suffixes (":free", ":batch", …) must not break
	// suffix/contains matching, e.g. "google/gemini-2.5-flash-image:free".
	if i := strings.Index(n, ":"); i >= 0 {
		n = n[:i]
	}
	if strings.Contains(n, "gpt-image") ||
		strings.Contains(n, "nano-banana") ||
		strings.Contains(n, "cogview") ||
		strings.HasPrefix(n, "image-") ||
		strings.HasPrefix(n, "dall-e") ||
		strings.HasPrefix(n, "imagen") ||
		strings.Contains(n, "seedream") ||
		strings.Contains(n, "-image-") ||
		strings.HasSuffix(n, "-image") {
		return "image"
	}
	if strings.HasPrefix(n, "veo") ||
		strings.Contains(n, "seedance") ||
		strings.Contains(n, "cogvideo") ||
		strings.Contains(n, "kling") ||
		strings.HasPrefix(n, "sora") ||
		strings.HasPrefix(n, "wan") ||
		strings.Contains(n, "-video-") ||
		strings.HasSuffix(n, "-video") {
		return "video"
	}
	return "chat"
}

// isSupportedProtocol reports whether p names a protocol with a client impl.
func isSupportedProtocol(p string) bool { return supportedProtocols[p] }

// inferProtocol resolves the wire protocol for a model. Resolution order:
//  1. explicit — a non-empty ProviderModel.Protocol (schema field) wins.
//  2. defaultProtocol — the owning Provider.Kind.
//  3. "" — unknown; callers must validate via Supported before building a unit.
//
// Model-name prefix heuristics (claude-/gpt-/gemini-) were removed: protocol is
// now an explicit schema-level fact. This prevents synthesising an unimplemented
// protocol (e.g. "gemini") from a model name and failing deep inside dispatch.
func inferProtocol(modelName, explicit, defaultProtocol string) string {
	_ = modelName // retained in signature for callers; no longer inspected.
	if explicit != "" {
		return explicit
	}
	return defaultProtocol
}

// registerProviderGates registers each provider's MaxConcurrency with the
// global per-provider concurrency gate. This must be called whenever the
// provider list changes (OnStart, handleProviderConfigure, handleConfigImport).
// Caller need NOT hold a.mu — the gate is a process-global singleton.
func (a *Actor) registerProviderGates() {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.Providers {
		llmclient.DefaultProviderGate.Register(p.Name, int(p.MaxConcurrency))
	}
}

// buildAutoUnits generates ManualCallableUnit entries from all registered
// providers. ALL providers are included regardless of health state — disabled
// and cooling_down units are visible in the dropdown so the user can manually
// select them. The aggregator's selectUnit/chatUnits skips unhealthy units
// during auto-strategy selection; user-pinned units bypass that via the
// on-demand resolve path.
//
// Caller must hold a.mu.
func (a *Actor) buildAutoUnits() []domain.ManualCallableUnit {
	if len(a.Providers) == 0 {
		return []domain.ManualCallableUnit{}
	}
	units := make([]domain.ManualCallableUnit, 0, len(a.Providers)*2)
	snap := llmclient.HealthSnapshot()
	now := time.Now()
	for _, p := range a.Providers {
		pState, _, _, pCD := providerHealthProjection(snap, p.Name, now)
		var cooldownUntil int64
		if pState == llmclient.HealthStateCoolingDown && pCD > 0 {
			cooldownUntil = pCD
		}
		var disableUntil int64
		if dd := providerDisableDeadline(p.DisableWindows, now); dd > 0 {
			disableUntil = dd
		}
		for _, m := range p.Models {
			proto := inferProtocol(m.Name, m.Protocol, p.Kind)
			modality := inferModality(m.Name, m.Modality)
			if modality == "chat" && !isSupportedProtocol(proto) {
				continue
			}
			units = append(units, domain.ManualCallableUnit{
				Model:                 m.Name,
				Endpoint:              p.Endpoint,
				ProviderName:          p.Name,
				Protocol:              proto,
				Modality:              modality,
				UserAgent:             p.UserAgent,
				Proxy:                 p.Proxy,
				MaxConcurrency:        p.MaxConcurrency,
				MaxContextLength:      m.MaxContextLength,
				CooldownUntil:         cooldownUntil,
				DisableUntil:          disableUntil,
				Disabled:              p.Disabled || m.Disabled,
				IsTokenPlan:           p.IsTokenPlan,
				TokenPlanExpiresAt:    p.TokenPlanExpiresAt,
				TokenPlanRemainingPct: p.TokenPlanRemainingPct,
				TokenPlanWindowMs:     p.TokenPlanWindowMs,
			})
		}
	}
	return units
}

func (a *Actor) saveOrLog(ctx actor.PureContext) {
	t0 := time.Now()
	if err := a.Save(); err != nil {
		ctx.Logger().Error("aimanager: save state failed", "error", err, "dur", time.Since(t0))
	} else {
		ctx.Logger().Info("aimanager: save state done", "dur", time.Since(t0))
	}
}

func (a *Actor) handleConfigVersion(_ actor.PureContext) (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.configVersion, nil
}

// handleProviderList returns sanitized providers for public callers.
// AuthToken is replaced with a placeholder when set, so the frontend can
// show a masked indicator without exposing the actual secret.
func (a *Actor) handleProviderList(_ actor.PureContext) (domain.ProviderListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]domain.Provider, len(a.Providers))
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	for i, p := range a.Providers {
		if p.AuthToken != "" {
			p.HasAuthToken = true
			p.AuthToken = "********"
		}
		if dd := providerDisableDeadline(p.DisableWindows, now); dd > 0 {
			p.DisableUntil = dd
		}
		state, reason, recovery, cooldownUntil := providerHealthProjection(snap, p.Name, now)
		if state != "" {
			p.HealthState = state
			p.HealthReason = reason
			p.RecoveryMode = recovery
			if entry, ok := snap[unitKey(p.Name, firstModelName(p))]; ok && !entry.CooldownUntil.IsZero() {
				p.LastFailureAt = entry.CooldownUntil.Unix()
			}
			if cooldownUntil > 0 {
				p.CooldownUntil = cooldownUntil
			}
		}
		if p.DisableUntil > now.Unix() && p.HealthState != llmclient.HealthStateDisabled {
			p.HealthState = llmclient.HealthStateCoolingDown
			p.HealthReason = disableWindowReason
			p.RecoveryMode = recoveryCooldown
		}
		// Project the runtime-inferred modality into empty Model.Modality
		// fields so public consumers (provider settings badges, media page
		// model detection) see the same classification the aggregator units
		// use, without duplicating the heuristic client-side. Models is
		// copied first — the slice backing array is shared with persisted
		// state and must not be mutated.
		if len(p.Models) > 0 {
			models := make([]domain.ProviderModel, len(p.Models))
			copy(models, p.Models)
			for j := range models {
				if models[j].Modality == "" {
					models[j].Modality = inferModality(models[j].Name, "")
				}
			}
			p.Models = models
		}
		out[i] = p
	}
	return domain.ProviderListResp{Items: out}, nil
}

// handleProviderFullList returns full providers including AuthToken for internal callers (aggregators).
// lookupProviderAuthToken finds the auth token for a provider by name.
// Caller must hold a.mu (at least RLock).
func (a *Actor) lookupProviderAuthToken(name string) string {
	for _, p := range a.Providers {
		if p.Name == name {
			return p.AuthToken
		}
	}
	return ""
}

func (a *Actor) handleProviderFullList(_ actor.PureContext) (domain.ProviderListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]domain.Provider, len(a.Providers))
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	for i, p := range a.Providers {
		p.DisableUntil = providerDisableDeadline(p.DisableWindows, now)
		state, reason, recovery, cooldownUntil := providerHealthProjection(snap, p.Name, now)
		if state != "" {
			p.HealthState = state
			p.HealthReason = reason
			p.RecoveryMode = recovery
			if cooldownUntil > 0 {
				p.CooldownUntil = cooldownUntil
			}
		}
		out[i] = p
	}
	return domain.ProviderListResp{Items: out}, nil
}

// handleProviderResolveToken returns the current AuthToken for a provider.
// Called by aiaggregator at dispatch time so token rotation in the provider
// config is picked up without needing to re-configure dependent aggregators.
// Internal-only — never expose to anonymous roles; tokens are secrets.
func (a *Actor) handleProviderResolveToken(_ actor.PureContext, req domain.AIManagerProviderResolveTokenReq) (domain.AIManagerProviderResolveTokenResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return domain.AIManagerProviderResolveTokenResp{
		AuthToken: a.lookupProviderAuthToken(req.Name),
	}, nil
}

// handleProviderResolveModel returns model metadata (context window, max
// output tokens) for one (provider, model) pair. Agents use this to size
// context budgets when the bound unit was supplied ad-hoc (e.g. via
// TurnInput.Unit) and the aggregator has not already attached the metadata.
// Internal-only — it's a lookup over provider config, not a public API.
func (a *Actor) handleProviderResolveModel(_ actor.PureContext, req domain.AIManagerProviderResolveModelReq) (domain.AIManagerProviderResolveModelResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.Providers {
		if p.Name != req.Name {
			continue
		}
		for _, m := range p.Models {
			if m.Name == req.Model {
				proto := inferProtocol(m.Name, m.Protocol, p.Kind)
				var disableUntil int64
				if dd := providerDisableDeadline(p.DisableWindows, time.Now()); dd > 0 {
					disableUntil = dd
				}
				return domain.AIManagerProviderResolveModelResp{
					MaxContextLength:      m.MaxContextLength,
					MaxTokens:             m.MaxTokens,
					Endpoint:              p.Endpoint,
					Protocol:              proto,
					Modality:              inferModality(m.Name, m.Modality),
					MaxConcurrency:        p.MaxConcurrency,
					UserAgent:             p.UserAgent,
					Proxy:                 p.Proxy,
					DisableUntil:          disableUntil,
					Disabled:              p.Disabled || m.Disabled,
					IsTokenPlan:           p.IsTokenPlan,
					TokenPlanExpiresAt:    p.TokenPlanExpiresAt,
					TokenPlanRemainingPct: p.TokenPlanRemainingPct,
					TokenPlanWindowMs:     p.TokenPlanWindowMs,
				}, nil
			}
		}
	}
	return domain.AIManagerProviderResolveModelResp{}, nil
}

// lookupProvider returns the provider config by name, or nil. Caller must hold
// a.mu (at least RLock).
func (a *Actor) lookupProvider(name string) *domain.Provider {
	for i := range a.Providers {
		if a.Providers[i].Name == name {
			return &a.Providers[i]
		}
	}
	return nil
}

func (a *Actor) lookupProviderModel(providerName, modelName string) domain.ProviderModel {
	for _, p := range a.Providers {
		if p.Name != providerName {
			continue
		}
		for _, m := range p.Models {
			if m.Name == modelName {
				return m
			}
		}
	}
	return domain.ProviderModel{}
}

// firstModelName returns the name of the first model for a provider, or the
// empty string when the provider has no models. Used for LastFailureAt
// projection in handleProviderList.
func firstModelName(p domain.Provider) string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0].Name
}

func (a *Actor) handleProviderConfigure(ctx actor.PureContext, req domain.AIManagerProviderConfigureReq) (_ domain.AIManagerProviderConfigureResp, err error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerProviderConfigureResp{}, err
	}

	t0 := time.Now()
	ctx.Logger().Info("aimanager: provider.configure start", "name", req.Name, "kind", req.Kind)

	if err := validateDisableWindows(req.DisableWindows); err != nil {
		return domain.AIManagerProviderConfigureResp{}, fmt.Errorf("aimanager: provider.configure: %w", err)
	}

	a.mu.Lock()
	// locked tracks whether this handler still holds the write lock so the
	// panic-recover below only releases it when actually held. As a stateless
	// handler it runs concurrently with peer handlers: an unconditional
	// Unlock on a post-unlock panic would strip the lock from another
	// handler's in-flight critical section.
	locked := true
	ctx.Logger().Info("aimanager: provider.configure lock acquired", "dur", time.Since(t0))

	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("aimanager: provider.configure panic", "panic", r)
			if locked {
				a.mu.Unlock()
			}
			err = fmt.Errorf("aimanager: provider.configure panic: %v", r)
		}
	}()

	// Rename: PreviousName (when set and different from Name) identifies the
	// existing provider to update; Name carries the new display name. Without
	// this, renaming would miss the name-keyed lookup below and append a
	// duplicate entry instead of updating the original.
	lookupName := req.Name
	if req.PreviousName != "" && req.PreviousName != req.Name {
		for _, p := range a.Providers {
			if p.Name == req.Name {
				a.mu.Unlock()
				locked = false
				return domain.AIManagerProviderConfigureResp{}, fmt.Errorf("aimanager: provider.configure: rename target %q already exists", req.Name)
			}
		}
		lookupName = req.PreviousName
	}

	matched := false
	for i, p := range a.Providers {
		if p.Name != lookupName {
			continue
		}
		matched = true

		// Delete signal: all configuration fields empty. Preserve an explicit
		// empty DisableWindows as an update when any other provider setting is
		// populated, so the editor can clear the schedule.
		if req.Kind == "" && req.Endpoint == "" && len(req.Models) == 0 && req.AuthToken == "" && req.MaxConcurrency == 0 && req.UserAgent == "" && req.Proxy == "" && !req.IsTokenPlan && req.TokenPlanExpiresAt == "" && req.TokenPlanRemainingPct == 0 && req.TokenPlanWindowMs == 0 && len(req.DisableWindows) == 0 {
			a.Providers = append(a.Providers[:i], a.Providers[i+1:]...)

			var toNotify []string
			var toStop []ref.Ref

			for configID, cfg := range a.aggregators {
				filtered := make([]domain.ManualCallableUnit, 0, len(cfg.Units))
				for _, u := range cfg.Units {
					if u.ProviderName != lookupName {
						filtered = append(filtered, u)
					}
				}
				if len(filtered) == len(cfg.Units) {
					continue
				}
				cfg.Units = filtered
				a.aggregators[configID] = cfg
				if len(filtered) == 0 && cfg.Name == "" {
					if r, ok := a.aggRefs[configID]; ok {
						toStop = append(toStop, r)
						delete(a.aggRefs, configID)
					}
					delete(a.aggActorIDs, configID)
					delete(a.aggregators, configID)
				} else {
					toNotify = append(toNotify, configID)
				}
			}

			// Aggregators deleted above may have been referenced by others; prune
			// the stranded references so persisted state stays consistent.
			pruneN, pruneS := a.pruneAggregatorRefsLocked()
			toNotify = append(toNotify, pruneN...)
			toStop = append(toStop, pruneS...)

			// Clean up llmclient health and persisted overlay for the deleted provider.
			a.clearProviderHealthLocked(lookupName)

			a.configVersion++
			a.saveOrLog(ctx)
			ctx.Logger().Info("aimanager: provider.configure unlock", "dur", time.Since(t0))
			locked = false
			a.mu.Unlock()

			for _, r := range toStop {
				go func(r ref.Ref) { _ = ctx.Stop(r) }(r)
			}
			for _, id := range toNotify {
				a.notifyAggregator(id)
			}
			a.notifyAutoAggregator()
			ctx.Logger().Info("aimanager: provider.configure done", "dur", time.Since(t0), "action", "delete")
			return domain.AIManagerProviderConfigureResp{}, nil
		}

		updated := domain.Provider{
			Name:                  req.Name,
			Kind:                  req.Kind,
			Endpoint:              req.Endpoint,
			Models:                req.Models,
			AuthToken:             req.AuthToken,
			MaxConcurrency:        req.MaxConcurrency,
			UserAgent:             req.UserAgent,
			Proxy:                 req.Proxy,
			DisableWindows:        req.DisableWindows,
			IsTokenPlan:           req.IsTokenPlan,
			TokenPlanExpiresAt:    req.TokenPlanExpiresAt,
			TokenPlanRemainingPct: req.TokenPlanRemainingPct,
			TokenPlanWindowMs:     req.TokenPlanWindowMs,
		}
		// Preserve existing AuthToken when the update omits it.
		if updated.AuthToken == "" {
			updated.AuthToken = p.AuthToken
		}
		a.Providers[i] = updated
		// Config change resets llmclient health for the provider so it gets a
		// fresh chance; re-inject policy in case tuning constants changed.
		a.clearProviderHealthLocked(lookupName)
		a.applyCooldownPolicy()

		toNotify, toStop := a.propagateProviderUpdate(lookupName, updated, !disableWindowsEqual(p.DisableWindows, updated.DisableWindows))

		a.configVersion++
		a.saveOrLog(ctx)
		ctx.Logger().Info("aimanager: provider.configure unlock", "dur", time.Since(t0))
		locked = false
		a.mu.Unlock()

		for _, r := range toStop {
			go func(r ref.Ref) { _ = ctx.Stop(r) }(r)
		}
		for _, id := range toNotify {
			a.notifyAggregator(id)
		}
		a.notifyAutoAggregator()
		a.registerProviderGates()
		ctx.Logger().Info("aimanager: provider.configure done", "dur", time.Since(t0), "action", "update", "notified", len(toNotify), "stopped", len(toStop))
		return domain.AIManagerProviderConfigureResp{}, nil
	}

	if req.PreviousName != "" && !matched {
		a.mu.Unlock()
		locked = false
		return domain.AIManagerProviderConfigureResp{}, fmt.Errorf("aimanager: provider.configure: provider %q not found for rename", req.PreviousName)
	}

	a.Providers = append(a.Providers, domain.Provider{
		Name:                  req.Name,
		Kind:                  req.Kind,
		Endpoint:              req.Endpoint,
		Models:                req.Models,
		AuthToken:             req.AuthToken,
		MaxConcurrency:        req.MaxConcurrency,
		UserAgent:             req.UserAgent,
		Proxy:                 req.Proxy,
		DisableWindows:        req.DisableWindows,
		IsTokenPlan:           req.IsTokenPlan,
		TokenPlanExpiresAt:    req.TokenPlanExpiresAt,
		TokenPlanRemainingPct: req.TokenPlanRemainingPct,
		TokenPlanWindowMs:     req.TokenPlanWindowMs,
	})
	a.configVersion++
	a.saveOrLog(ctx)
	ctx.Logger().Info("aimanager: provider.configure unlock", "dur", time.Since(t0))
	locked = false
	a.mu.Unlock()
	a.notifyAutoAggregator()
	a.registerProviderGates()
	ctx.Logger().Info("aimanager: provider.configure done", "dur", time.Since(t0), "action", "add")
	return domain.AIManagerProviderConfigureResp{}, nil
}

// handleProviderSetTokenPlan updates a provider's Token Plan quota metadata in
// place: expiresAt, remainingPct, windowMs, and the isTokenPlan toggle. Only
// fields explicitly provided are changed (RemainingPct=-1 / WindowMs=-1 / empty
// ExpiresAt = unchanged). The update propagates to every aggregator pool unit
// referencing that provider (named aggregators via propagateProviderUpdate; the
// system auto-aggregator via notifyAutoAggregator) and is persisted.
//
// This is the targeted write path used by the UI "refresh balance" button and
// future provider-side syncers; the full provider.configure path also carries
// these fields.
func (a *Actor) handleProviderSetTokenPlan(ctx actor.PureContext, req domain.AIManagerProviderSetTokenPlanReq) (domain.AIManagerProviderSetTokenPlanResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerProviderSetTokenPlanResp{}, err
	}
	if req.ProviderName == "" {
		return domain.AIManagerProviderSetTokenPlanResp{Ok: false, Error: "providerName is required"}, nil
	}

	a.mu.Lock()
	idx := -1
	for i, p := range a.Providers {
		if p.Name == req.ProviderName {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.AIManagerProviderSetTokenPlanResp{Ok: false, Error: "provider not found"}, nil
	}

	p := a.Providers[idx]
	changed := false
	if req.RemainingPct >= 0 {
		if p.TokenPlanRemainingPct != req.RemainingPct {
			p.TokenPlanRemainingPct = req.RemainingPct
			changed = true
		}
	}
	if req.WindowMs >= 0 {
		if p.TokenPlanWindowMs != req.WindowMs {
			p.TokenPlanWindowMs = req.WindowMs
			changed = true
		}
	}
	// ExpiresAt: "" = unchanged, "none"/"-" = clear, anything else = set.
	switch {
	case req.ExpiresAt == "":
		// unchanged
	case req.ExpiresAt == "none" || req.ExpiresAt == "-":
		if p.TokenPlanExpiresAt != "" {
			p.TokenPlanExpiresAt = ""
			changed = true
		}
	default:
		if _, perr := time.Parse(time.RFC3339, req.ExpiresAt); perr != nil {
			a.mu.Unlock()
			return domain.AIManagerProviderSetTokenPlanResp{Ok: false, Error: "expiresAt must be RFC3339, 'none', or empty"}, nil
		}
		if p.TokenPlanExpiresAt != req.ExpiresAt {
			p.TokenPlanExpiresAt = req.ExpiresAt
			changed = true
		}
	}
	// IsTokenPlan: the request is optional, but since a zero bool is
	// indistinguishable from "false", we only apply it when the caller signals
	// intent via ExpiresAt/RemainingPct/WindowMs being present. If any quota
	// field is supplied and the provider isn't already a token-plan provider,
	// we enable it so the fields take effect.
	if req.RemainingPct >= 0 || req.WindowMs >= 0 || req.ExpiresAt != "" {
		if !p.IsTokenPlan {
			p.IsTokenPlan = true
			changed = true
		}
	}

	if !changed {
		a.mu.Unlock()
		return domain.AIManagerProviderSetTokenPlanResp{Ok: true}, nil
	}
	a.Providers[idx] = p
	toNotify, toStop := a.propagateProviderUpdate(req.ProviderName, p, false)
	a.configVersion++
	a.saveOrLog(ctx)
	a.mu.Unlock()

	for _, r := range toStop {
		go func(r ref.Ref) { _ = ctx.Stop(r) }(r)
	}
	for _, id := range toNotify {
		a.notifyAggregator(id)
	}
	a.notifyAutoAggregator()
	ctx.Logger().Info("aimanager: provider.set_token_plan done", "provider", req.ProviderName, "notified", len(toNotify))
	return domain.AIManagerProviderSetTokenPlanResp{Ok: true}, nil
}

// handleProviderSetDisabled is the manual operator toggle for disabling or
// re-enabling a provider or a single (provider, model) unit. Provider-level
// (req.Model == "") sets p.Disabled; unit-level (req.Model != "") sets the
// model's Disabled. Both are persisted and propagated to all aggregators.
func (a *Actor) handleProviderSetDisabled(ctx actor.PureContext, req domain.AIManagerProviderSetDisabledReq) (domain.AIManagerProviderSetDisabledResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerProviderSetDisabledResp{}, err
	}
	if req.ProviderName == "" {
		return domain.AIManagerProviderSetDisabledResp{Ok: false, Error: "providerName is required"}, nil
	}

	a.mu.Lock()
	idx := -1
	for i, p := range a.Providers {
		if p.Name == req.ProviderName {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.mu.Unlock()
		return domain.AIManagerProviderSetDisabledResp{Ok: false, Error: "provider not found"}, nil
	}

	if req.Model != "" {
		// Unit-level toggle: find the model within the provider.
		midx := -1
		for j, m := range a.Providers[idx].Models {
			if m.Name == req.Model {
				midx = j
				break
			}
		}
		if midx < 0 {
			a.mu.Unlock()
			return domain.AIManagerProviderSetDisabledResp{Ok: false, Error: "model not found in provider"}, nil
		}
		if a.Providers[idx].Models[midx].Disabled == req.Disabled {
			// No-op — already in the requested state.
			a.mu.Unlock()
			return domain.AIManagerProviderSetDisabledResp{Ok: true}, nil
		}
		a.Providers[idx].Models[midx].Disabled = req.Disabled
	} else {
		// Provider-level toggle.
		if a.Providers[idx].Disabled == req.Disabled {
			// No-op.
			a.mu.Unlock()
			return domain.AIManagerProviderSetDisabledResp{Ok: true}, nil
		}
		a.Providers[idx].Disabled = req.Disabled
	}

	toNotify, toStop := a.propagateProviderUpdate(req.ProviderName, a.Providers[idx], false)
	a.configVersion++
	a.saveOrLog(ctx)
	a.mu.Unlock()

	for _, r := range toStop {
		go func(r ref.Ref) { _ = ctx.Stop(r) }(r)
	}
	for _, id := range toNotify {
		a.notifyAggregator(id)
	}
	a.notifyAutoAggregator()
	ctx.Logger().Info("aimanager: provider.set_disabled done", "provider", req.ProviderName, "model", req.Model, "disabled", req.Disabled, "notified", len(toNotify))
	return domain.AIManagerProviderSetDisabledResp{Ok: true}, nil
}

// propagateProviderUpdate walks every user aggregator and refreshes units that
// reference providerName with the updated provider's metadata
// (Endpoint/MaxConcurrency/Protocol/MaxContextLength). Units whose Model is
// no longer in updated.Models are dropped — otherwise dispatch would send an
// invalid model name to the provider. AuthToken is intentionally NOT synced
// here: aggregators no longer store tokens; they resolve them from the provider
// at dispatch time via aimanager.provider.resolve_token. UserAgent and Proxy
// are synced from the provider so they can be overridden per-provider. The auto
// aggregator is skipped; it is rebuilt wholesale by notifyAutoAggregator.
//
// Caller MUST hold a.mu (write). Returns (toNotify, toStop) for the caller to
// act on outside the lock.
func (a *Actor) propagateProviderUpdate(providerName string, updated domain.Provider, windowsChanged bool) (toNotify []string, toStop []ref.Ref) {
	validModels := make(map[string]domain.ProviderModel, len(updated.Models))
	for _, m := range updated.Models {
		validModels[m.Name] = m
	}
	for configID, cfg := range a.aggregators {
		if configID == autoAggregatorID {
			continue
		}
		changed := windowsChanged
		kept := make([]domain.ManualCallableUnit, 0, len(cfg.Units))
		for _, u := range cfg.Units {
			if u.ProviderName != providerName {
				kept = append(kept, u)
				continue
			}
			pm, ok := validModels[u.Model]
			if !ok {
				changed = true
				continue
			}
			newModality := inferModality(pm.Name, pm.Modality)
			newProtocol := inferProtocol(pm.Name, pm.Protocol, updated.Kind)
			oldModality := u.Modality
			if oldModality == "" {
				oldModality = "chat"
			}
			if u.ProviderName != updated.Name ||
				u.Endpoint != updated.Endpoint ||
				u.MaxConcurrency != updated.MaxConcurrency ||
				u.Protocol != newProtocol ||
				u.MaxContextLength != pm.MaxContextLength ||
				oldModality != newModality ||
				u.UserAgent != updated.UserAgent ||
				u.Proxy != updated.Proxy ||
				u.Disabled != (updated.Disabled || pm.Disabled) ||
				u.IsTokenPlan != updated.IsTokenPlan ||
				u.TokenPlanExpiresAt != updated.TokenPlanExpiresAt ||
				u.TokenPlanRemainingPct != updated.TokenPlanRemainingPct ||
				u.TokenPlanWindowMs != updated.TokenPlanWindowMs {
				u.ProviderName = updated.Name
				u.Endpoint = updated.Endpoint
				u.MaxConcurrency = updated.MaxConcurrency
				u.Protocol = newProtocol
				u.MaxContextLength = pm.MaxContextLength
				u.Modality = newModality
				u.UserAgent = updated.UserAgent
				u.Proxy = updated.Proxy
				u.Disabled = updated.Disabled || pm.Disabled
				u.IsTokenPlan = updated.IsTokenPlan
				u.TokenPlanExpiresAt = updated.TokenPlanExpiresAt
				u.TokenPlanRemainingPct = updated.TokenPlanRemainingPct
				u.TokenPlanWindowMs = updated.TokenPlanWindowMs
				changed = true
			}
			kept = append(kept, u)
		}
		if !changed {
			continue
		}
		cfg.Units = kept
		a.aggregators[configID] = cfg
		if len(kept) == 0 && cfg.Name == "" {
			if r, ok := a.aggRefs[configID]; ok {
				toStop = append(toStop, r)
				delete(a.aggRefs, configID)
			}
			delete(a.aggActorIDs, configID)
			delete(a.aggregators, configID)
		} else {
			toNotify = append(toNotify, configID)
		}
	}
	// Aggregators deleted above may have been referenced by others; prune the
	// stranded references so persisted state stays consistent.
	pruneN, pruneS := a.pruneAggregatorRefsLocked()
	toNotify = append(toNotify, pruneN...)
	toStop = append(toStop, pruneS...)
	return toNotify, toStop
}

func (a *Actor) handleModelList(_ actor.PureContext) (domain.ModelListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	seen := make(map[string]bool)
	var out []domain.Model
	for _, p := range a.Providers {
		for _, m := range p.Models {
			if seen[m.Name] {
				continue
			}
			seen[m.Name] = true
			out = append(out, domain.Model{
				ID:        m.Name,
				Provider:  p.Name,
				MaxTokens: m.MaxTokens,
			})
		}
	}
	return domain.ModelListResp{Items: out}, nil
}

func (a *Actor) handleProviderFetchModels(_ actor.PureContext, req domain.AIManagerProviderFetchModelsReq) (domain.AIManagerProviderFetchModelsResp, error) {
	authToken := req.AuthToken
	// req.Proxy (unsaved form value) wins over the stored Provider.Proxy so a
	// provider being added can be probed through the proxy before it is saved.
	proxyURL := req.Proxy
	a.mu.RLock()
	for _, p := range a.Providers {
		if p.Name == req.Name {
			if proxyURL == "" {
				proxyURL = p.Proxy
			}
			if authToken == "" || authToken == "********" {
				authToken = p.AuthToken
			}
			break
		}
	}
	a.mu.RUnlock()

	if req.Kind == "anthropic" {
		if models, ok := fetchAnthropicModels(req.Endpoint, authToken, proxyURL); ok {
			return domain.AIManagerProviderFetchModelsResp{Models: models}, nil
		}
		return domain.AIManagerProviderFetchModelsResp{
			Models: toFetchedModels([]string{
				"claude-opus-4-7",
				"claude-opus-4",
				"claude-sonnet-4-6",
				"claude-sonnet-4-5",
				"claude-haiku-4-5",
			}, "anthropic"),
		}, nil
	}

	// Gemini does not expose an OpenAI-compatible /models list. Return the
	// known image-generation models so the user can pick without guessing.
	if req.Kind == "gemini" {
		return domain.AIManagerProviderFetchModelsResp{
			Models: toFetchedModels([]string{
				"gemini-3.1-flash-image-preview",
				"gemini-3-pro-image-preview",
				"gemini-3-pro-image",
				"gemini-2.5-flash-image-preview",
			}, "gemini"),
		}, nil
	}

	// "openai", "endpoint", and "responses" kinds all share the same
	// OpenAI-compatible GET {endpoint}/models listing (the Responses API
	// exposes the same model catalog over the same base URL).
	endpoint := strings.TrimRight(req.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}

	url := endpoint + "/models"
	httpReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return domain.AIManagerProviderFetchModelsResp{}, fmt.Errorf("build request: %w", err)
	}
	if authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
	}

	client, err := httpClientFor(proxyURL, 15*time.Second)
	if err != nil {
		return domain.AIManagerProviderFetchModelsResp{}, fmt.Errorf("build proxy client: %w", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return domain.AIManagerProviderFetchModelsResp{}, fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return domain.AIManagerProviderFetchModelsResp{}, fmt.Errorf("provider returned %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return domain.AIManagerProviderFetchModelsResp{}, fmt.Errorf("decode response: %w", err)
	}

	names := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			names = append(names, m.ID)
		}
	}
	// When the user asked for the openai protocol, first probe whether the
	// endpoint exposes the Responses API. If it does, fetch-models infers the
	// responses protocol for each model so the user rarely needs to pick it by
	// hand. A "responses" (or explicit protocol) request is never probed — the
	// user's selection wins, per dsh inferProtocol semantics.
	defaultKind := req.Kind
	if req.Kind == "openai" && probeResponsesSupport(context.Background(), endpoint, authToken, proxyURL) {
		defaultKind = "responses"
	}
	return domain.AIManagerProviderFetchModelsResp{Models: toFetchedModels(names, defaultKind)}, nil
}

// httpClientFor returns the *http.Client to use for a probe or fetch request.
// When proxyURL is non-empty, a proxy-aware client (cached per URL by
// llmclient.HTTPClientForProxy) is returned; otherwise a default client with
// the given Timeout is returned. timeout is ignored when proxyURL is set — the
// proxy client relies on context deadlines, matching how the production llm
// client uses it (pkg/actor/aiaggregator/actor.go probe path).
func httpClientFor(proxyURL string, timeout time.Duration) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{Timeout: timeout}, nil
	}
	return llmclient.HTTPClientForProxy(proxyURL)
}

// probeResponsesSupport tests whether an OpenAI-compatible endpoint exposes
// the Responses API by POSTing a minimal request to {base}/responses with
// max_output_tokens=1 (cost control, no stream). 404/405 — or a body that
// explicitly says the route is unknown — means the endpoint does not support
// the Responses API; any other status (200/400/401/429/5xx) means the route
// exists and is treated as supported. Network errors and timeouts fall back to
// "not supported" (conservative: reuse the openai protocol). No retries, no
// side effects. When proxyURL is set the probe is routed through the same
// proxy configured for the provider, so probes work in environments where the
// endpoint is only reachable via proxy.
func probeResponsesSupport(ctx context.Context, endpoint, authToken, proxyURL string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	body := `{"model":"__probe__","input":"ping","max_output_tokens":1}`
	baseURL := strings.TrimRight(endpoint, "/")
	httpReq, err := http.NewRequestWithContext(probeCtx, http.MethodPost, baseURL+"/responses", strings.NewReader(body))
	if err != nil {
		return false
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+authToken)
	}

	client, err := httpClientFor(proxyURL, 0)
	if err != nil {
		return false
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	// A missing route surfaces as 404/405. Some gateways instead answer with a
	// JSON body naming the unknown route even on other statuses; sniff for the
	// common markers so those fall back too.
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return false
	}
	sniff, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	lower := strings.ToLower(string(sniff))
	if strings.Contains(lower, "not found") || strings.Contains(lower, "unknown route") {
		return false
	}
	return true
}

// fetchAnthropicModels queries the Anthropic-compatible /v1/models endpoint.
// It uses x-api-key + anthropic-version headers (not Bearer auth). If the
// endpoint URL already ends with /v1 the models path is appended directly;
// otherwise /v1 is inserted. When proxyURL is set the request is routed
// through the same proxy configured for the provider. Returns (models, false)
// on any failure so the caller can fall back to a hardcoded list.
func fetchAnthropicModels(endpoint, authToken, proxyURL string) ([]domain.FetchedModel, bool) {
	if endpoint == "" {
		endpoint = "https://api.anthropic.com"
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(endpoint, "/v1") {
		endpoint += "/v1"
	}

	httpReq, err := http.NewRequest("GET", endpoint+"/models", nil)
	if err != nil {
		return nil, false
	}
	if authToken != "" {
		httpReq.Header.Set("x-api-key", authToken)
	}
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client, err := httpClientFor(proxyURL, 15*time.Second)
	if err != nil {
		return nil, false
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, false
	}

	names := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			names = append(names, m.ID)
		}
	}
	if len(names) == 0 {
		return nil, false
	}
	return toFetchedModels(names, "anthropic"), true
}

// toFetchedModels converts a list of model names into FetchedModel entries with
// an inferred modality ("chat" or "image") based on the model name heuristic.
func toFetchedModels(names []string, defaultKind string) []domain.FetchedModel {
	out := make([]domain.FetchedModel, 0, len(names))
	for _, n := range names {
		out = append(out, domain.FetchedModel{
			Name:     n,
			Modality: inferModality(n, ""),
			Protocol: inferProtocol(n, "", defaultKind),
		})
	}
	return out
}

// normalizeStrategy canonicalizes an aggregator selection-strategy name into
// the single wire form shared by schema, storage, and projection. It is the
// only place alias variants are accepted, so a legacy or mistyped value never
// reaches the aggregator or the frontend as an unrecognized string:
//   - "" / "round-robin" / "round_robin" / "roundrobin" → "round_robin" (default)
//   - "latency-aware" / "latency_aware" → "smart"
//   - "fallback", "smart", "standard" → unchanged
//   - legacy "priority" → "fallback"
//   - legacy "sticky" → "standard"
//   - any other value → "round_robin" (matches resolveStrategy's default)
//
// The system auto aggregator defaults to "smart" rather than round_robin; its
// call sites keep that default and only pass non-empty values through here.
func normalizeStrategy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "round-robin", "round_robin", "roundrobin":
		return "round_robin"
	case "fallback", "smart", "standard":
		return strings.ToLower(strings.TrimSpace(s))
	case "priority":
		return "fallback"
	case "sticky":
		return "standard"
	case "latency-aware", "latency_aware", "latencyaware":
		return "smart"
	default:
		return "round_robin"
	}
}

// handleAggregatorList returns descriptors for all spawned aggregator children.

func (a *Actor) handleAggregatorList(ctx actor.PureContext) (domain.AggregatorDescriptorListResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	caller := "unknown"
	if c := ctx.Caller(); c != nil {
		if svc, ok := c.Service(); ok && svc != "" {
			caller = svc
		} else {
			caller = c.ID().String()
		}
	}
	ctx.Logger().Info("aimanager: aggregator.list", "caller", caller, "aggRefs", len(a.aggRefs), "aggregators", len(a.aggregators))
	out := make([]domain.AggregatorDescriptor, 0, len(a.aggRefs))
	for configID, r := range a.aggRefs {
		cfg := a.aggregators[configID]
		out = append(out, domain.AggregatorDescriptor{
			ID:       configID,
			Name:     cfg.Name,
			ActorID:  r.ID().String(),
			Strategy: normalizeStrategy(cfg.Strategy),
			Disabled: cfg.Disabled,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return domain.AggregatorDescriptorListResp{Items: out}, nil
}

// rebuildAggregatorNames refreshes the projected actor-ID → name map from
// current in-memory state. Caller must hold a.mu if concurrency is possible.
func (a *Actor) rebuildAggregatorNames() {
	names := make(map[string]string, len(a.aggRefs))
	for configID, r := range a.aggRefs {
		cfg := a.aggregators[configID]
		if cfg.Name != "" {
			names[r.ID().String()] = cfg.Name
		}
	}
	a.AggregatorNames = names
}

// validateAggregatorUnits validates the unit list of an aggregator config being
// written to configID against the current aggregator map. It enforces three
// rules:
//
//  1. Each unit is either a concrete unit (Model and ProviderName both set,
//     AggregatorID empty) or a pure aggregator reference (AggregatorID set;
//     the concrete fields are ignored). A unit mixing both or providing
//     neither is rejected.
//  2. A reference must target an already-configured aggregator or the system
//     auto aggregator, and never configID itself.
//  3. The new reference edges must not close a cycle back to configID: every
//     cycle introduced by this config necessarily contains one of its new
//     edges, so walking from each referenced aggregator through the existing
//     reference edges and stopping at configID detects all of them.
//
// existing is read-only and may contain configID's previous config — only the
// new units' edges are followed for configID. Pure function, no side effects.
func validateAggregatorUnits(configID string, units []domain.ManualCallableUnit, existing map[string]domain.AIManagerAggregatorGetResp) error {
	for i, u := range units {
		switch {
		case u.AggregatorID != "" && (u.Model != "" || u.ProviderName != ""):
			return fmt.Errorf("aggregator %q unit %d: cannot mix aggregatorID %q with Model/ProviderName", configID, i, u.AggregatorID)
		case u.AggregatorID == "" && u.Model == "" && u.ProviderName == "":
			return fmt.Errorf("aggregator %q unit %d: must set either Model+ProviderName or aggregatorID", configID, i)
		case u.AggregatorID == "" && (u.Model == "" || u.ProviderName == ""):
			return fmt.Errorf("aggregator %q unit %d: concrete unit requires both Model and ProviderName", configID, i)
		}
	}

	for _, u := range units {
		if u.AggregatorID == "" {
			continue
		}
		target := u.AggregatorID
		if target == configID {
			return fmt.Errorf("aggregator %q: unit references itself (aggregatorID %q)", configID, target)
		}
		if target != autoAggregatorID {
			if _, ok := existing[target]; !ok {
				return fmt.Errorf("aggregator %q: unit references unknown aggregator %q", configID, target)
			}
		}
		if path := referenceCyclePath(configID, target, existing); len(path) > 0 {
			cycle := strings.Join(append([]string{configID}, path...), " → ")
			return fmt.Errorf("aggregator %q: reference cycle detected: %s", configID, cycle)
		}
	}
	return nil
}

// referenceCyclePath walks already-configured aggregator reference edges
// starting at node looking for configID. It returns the node path from node to
// configID (inclusive) when found, or nil. onPath tracks the current DFS path
// so a pre-existing cycle among other aggregators terminates instead of
// looping; only cycles that include the newly-configured aggregator are
// reported (every new cycle must contain one of its new edges).
func referenceCyclePath(configID, node string, existing map[string]domain.AIManagerAggregatorGetResp) []string {
	onPath := map[string]bool{}
	var walk func(n string) []string
	walk = func(n string) []string {
		if n == configID {
			return []string{n}
		}
		if onPath[n] {
			return nil
		}
		onPath[n] = true
		defer delete(onPath, n)
		cfg, ok := existing[n]
		if !ok {
			return nil
		}
		for _, u := range cfg.Units {
			if u.AggregatorID == "" {
				continue
			}
			if p := walk(u.AggregatorID); p != nil {
				return append([]string{n}, p...)
			}
		}
		return nil
	}
	return walk(node)
}

// pruneDanglingAggregatorRefs removes every unit whose AggregatorID target no
// longer exists in aggs. The system auto aggregator always counts as
// resolvable: it is rebuilt at spawn time and never persisted, so it may be
// absent from the map (notably during Load). Aggregators left with no units
// and an empty name are deleted as well, mirroring the provider-delete
// semantics; because that deletion can strand further references, pruning
// repeats until it reaches a fixpoint. It returns the IDs of the aggregators
// that were modified or deleted (deduplicated), for logging and actor cleanup.
func pruneDanglingAggregatorRefs(aggs map[string]domain.AIManagerAggregatorGetResp) []string {
	var changed []string
	seen := map[string]bool{}
	for {
		touched := false
		for id, cfg := range aggs {
			kept := make([]domain.ManualCallableUnit, 0, len(cfg.Units))
			for _, u := range cfg.Units {
				if u.AggregatorID != "" && u.AggregatorID != autoAggregatorID {
					if _, ok := aggs[u.AggregatorID]; !ok {
						touched = true
						continue
					}
				}
				kept = append(kept, u)
			}
			if len(kept) != len(cfg.Units) {
				cfg.Units = kept
				aggs[id] = cfg
				touched = true
				if !seen[id] {
					seen[id] = true
					changed = append(changed, id)
				}
			}
			if len(kept) == 0 && cfg.Name == "" {
				delete(aggs, id)
				touched = true
				if !seen[id] {
					seen[id] = true
					changed = append(changed, id)
				}
			}
		}
		if !touched {
			return changed
		}
	}
}

// pruneAggregatorRefsLocked applies pruneDanglingAggregatorRefs to the live
// aggregator map and disposes of the actor bookkeeping (aggRefs, aggActorIDs)
// of any aggregator the cascade deleted. It returns the config IDs of
// surviving aggregators whose unit list changed (they need a config push) and
// the refs of deleted aggregators (the caller must stop them after releasing
// a.mu). Caller must hold a.mu (write).
func (a *Actor) pruneAggregatorRefsLocked() (toNotify []string, toStop []ref.Ref) {
	changed := pruneDanglingAggregatorRefs(a.aggregators)
	if len(changed) == 0 {
		return nil, nil
	}
	for _, id := range changed {
		if _, ok := a.aggregators[id]; ok {
			toNotify = append(toNotify, id)
			continue
		}
		delete(a.aggActorIDs, id)
		if r, ok := a.aggRefs[id]; ok {
			toStop = append(toStop, r)
			delete(a.aggRefs, id)
		}
	}
	return toNotify, toStop
}

func (a *Actor) handleAggregatorConfigure(ctx actor.PureContext, req domain.AIManagerAggregatorConfigureReq) (_ domain.AIManagerAggregatorConfigureResp, err error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerAggregatorConfigureResp{}, err
	}

	t0 := time.Now()
	ctx.Logger().Info("aimanager: aggregator.configure start", "id", req.ID, "name", req.Name)

	a.mu.Lock()
	// locked tracks whether this handler still holds the write lock so the
	// panic-recover below only releases it when actually held (see
	// handleProviderConfigure for the stateless rationale).
	locked := true
	ctx.Logger().Info("aimanager: aggregator.configure lock acquired", "dur", time.Since(t0))

	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("aimanager: aggregator.configure panic", "panic", r)
			if locked {
				a.mu.Unlock()
			}
			err = fmt.Errorf("aimanager: aggregator.configure panic: %v", r)
		}
	}()

	configID := req.ID
	_, exists := a.aggregators[configID]

	// The system aggregator ID is reserved and managed internally.
	// Reject user attempts to create a new aggregator with this ID.
	if configID == autoAggregatorID && !exists {
		locked = false
		a.mu.Unlock()
		ctx.Logger().Warn("aimanager: rejected aggregator configure with reserved ID", "id", configID)
		return domain.AIManagerAggregatorConfigureResp{}, fmt.Errorf("aimanager: aggregator ID %q is reserved", autoAggregatorID)
	}

	// System-level auto aggregator: its units are derived from the provider
	// pool, not user-curated. Rebuild from providers and notify.
	if configID == autoAggregatorID {
		units := a.buildAutoUnits()
		prev := a.aggregators[autoAggregatorID]
		strategy := "smart"
		if prev.Strategy != "" {
			strategy = normalizeStrategy(prev.Strategy)
		} else if req.Strategy != "" {
			strategy = normalizeStrategy(req.Strategy)
		}
		a.aggregators[autoAggregatorID] = domain.AIManagerAggregatorGetResp{
			ID:       autoAggregatorID,
			Name:     "Auto (All Providers)",
			Strategy: strategy,
			Disabled: prev.Disabled,
			Units:    units,
		}
		if ref, ok := a.aggRefs[autoAggregatorID]; ok && ref != nil {
			a.notifyAggregator(autoAggregatorID)
		}
		a.rebuildAggregatorNames()
		a.configVersion++
		a.saveOrLog(ctx)
		ctx.Logger().Info("aimanager: aggregator.configure unlock", "dur", time.Since(t0))
		locked = false
		a.mu.Unlock()
		ctx.Logger().Info("aimanager: aggregator.configure done", "dur", time.Since(t0), "action", "upsert-auto", "units", len(units))
		return domain.AIManagerAggregatorConfigureResp{}, nil
	}

	// Delete: name empty and no units.
	if exists && req.Name == "" && len(req.Units) == 0 {
		delete(a.aggregators, configID)
		delete(a.aggActorIDs, configID)
		var toStop ref.Ref
		if r, ok := a.aggRefs[configID]; ok {
			toStop = r
			delete(a.aggRefs, configID)
		}
		// Deleting this aggregator strands every unit in other aggregators
		// that referenced it; prune those (and any aggregator the cascade
		// empties) so persisted state stays referentially consistent.
		pruneNotify, pruneStop := a.pruneAggregatorRefsLocked()
		a.rebuildAggregatorNames()
		a.configVersion++
		a.saveOrLog(ctx)
		ctx.Logger().Info("aimanager: aggregator.configure unlock", "dur", time.Since(t0))
		locked = false
		a.mu.Unlock()
		if toStop != nil {
			go func(r ref.Ref) { _ = ctx.Stop(r) }(toStop)
		}
		for _, r := range pruneStop {
			go func(r ref.Ref) { _ = ctx.Stop(r) }(r)
		}
		for _, id := range pruneNotify {
			a.notifyAggregator(id)
		}
		ctx.Logger().Info("aimanager: aggregator.configure done", "dur", time.Since(t0), "action", "delete")
		return domain.AIManagerAggregatorConfigureResp{}, nil
	}

	// Validate the new unit list before mutating state: every entry must be a
	// concrete unit or a pure aggregator reference, references must resolve to
	// an existing aggregator, and the new edges must not close a reference
	// cycle back to this aggregator.
	if err := validateAggregatorUnits(configID, req.Units, a.aggregators); err != nil {
		locked = false
		a.mu.Unlock()
		ctx.Logger().Warn("aimanager: aggregator.configure rejected", "id", configID, "error", err)
		return domain.AIManagerAggregatorConfigureResp{}, fmt.Errorf("aimanager: %w", err)
	}

	prevCfg := a.aggregators[configID]
	cfg := domain.AIManagerAggregatorGetResp{
		ID:       configID,
		Name:     req.Name,
		Strategy: normalizeStrategy(req.Strategy),
		Disabled: prevCfg.Disabled,
		Units:    make([]domain.ManualCallableUnit, 0, len(req.Units)),
	}
	if len(req.Units) > 0 {
		cfg.Units = make([]domain.ManualCallableUnit, len(req.Units))
		for i, u := range req.Units {
			// Backfill MaxContextLength from the provider when the caller
			// didn't supply it (the common path — frontend unitFromProvider
			// does not know the provider's model table).
			if u.MaxContextLength == 0 && u.ProviderName != "" && u.Model != "" {
				if pm := a.lookupProviderModel(u.ProviderName, u.Model); pm.Name != "" {
					u.MaxContextLength = pm.MaxContextLength
				}
			}
			cfg.Units[i] = u
		}
	}

	if !exists {
		ref := a.spawnAggregator(ctx, configID, "")
		if ref == nil {
			ctx.Logger().Warn("aimanager: aggregator.configure spawn failed", "configId", configID)
		}
	}
	a.aggregators[configID] = cfg

	a.rebuildAggregatorNames()
	a.configVersion++
	a.saveOrLog(ctx)
	ctx.Logger().Info("aimanager: aggregator.configure unlock", "dur", time.Since(t0))
	locked = false
	a.mu.Unlock()

	a.notifyAggregator(configID)
	ctx.Logger().Info("aimanager: aggregator.configure done", "dur", time.Since(t0), "action", "upsert")
	return domain.AIManagerAggregatorConfigureResp{}, nil
}

// handleAggregatorSetDisabled is the manual operator toggle for disabling or
// re-enabling a named aggregator. Persists the Disabled flag in the aggregator
// config, bumps config version, and notifies the running aggregator actor.
func (a *Actor) handleAggregatorSetDisabled(ctx actor.PureContext, req domain.AIManagerAggregatorSetDisabledReq) (domain.AIManagerAggregatorSetDisabledResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerAggregatorSetDisabledResp{}, err
	}
	if req.ID == "" {
		return domain.AIManagerAggregatorSetDisabledResp{Ok: false, Error: "id is required"}, nil
	}

	a.mu.Lock()
	cfg, ok := a.aggregators[req.ID]
	if !ok {
		a.mu.Unlock()
		return domain.AIManagerAggregatorSetDisabledResp{Ok: false, Error: "aggregator not found"}, nil
	}
	if cfg.Disabled == req.Disabled {
		a.mu.Unlock()
		// No-op.
		return domain.AIManagerAggregatorSetDisabledResp{Ok: true}, nil
	}
	cfg.Disabled = req.Disabled
	a.aggregators[req.ID] = cfg
	// Parents embedding this child project its Disabled flag onto their
	// aggregator-ref entries at resolve time (resolveUnitsWithDisabledRefs).
	// Without a push they keep a stale Disabled=false snapshot for up to one
	// poll cycle (15s) and keep dispatching to the now-disabled child.
	affectedParents := a.aggregatorIDsUsingAggregatorLocked(req.ID)
	a.configVersion++
	a.saveOrLog(ctx)
	a.mu.Unlock()

	a.notifyAggregator(req.ID)
	for _, parentID := range affectedParents {
		a.notifyAggregator(parentID)
	}
	ctx.Logger().Info("aimanager: aggregator.set_disabled done", "id", req.ID, "disabled", req.Disabled)
	return domain.AIManagerAggregatorSetDisabledResp{Ok: true}, nil
}

func (a *Actor) handleAggregatorGet(_ actor.PureContext, req domain.AIManagerAggregatorGetReq) (domain.AIManagerAggregatorGetResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cfg, ok := a.aggregators[req.ID]
	if !ok {
		return domain.AIManagerAggregatorGetResp{ID: req.ID, Units: []domain.ManualCallableUnit{}}, nil
	}
	cfg.Units = a.resolveUnitsWithCooldown(cfg.Units)
	cfg.Strategy = normalizeStrategy(cfg.Strategy)
	return cfg, nil
}

// resolveUnitsWithCooldown returns units for the aggregator config resolve path.
// ALL providers are included regardless of health state — disabled and
// cooling_down units are visible in the dropdown so the user can manually
// select them. Each unit is annotated from the llmclient health snapshot
// (HealthState/HealthReason/RecoveryMode/CooldownUntil) so the aggregator can
// display status and skip unhealthy units during auto-strategy selection. The
// aggregator's selectUnit/chatUnits already filters unhealthy units, so there
// is no need to strip them at this layer.
//
// Caller must hold a.mu.
func (a *Actor) resolveUnitsWithCooldown(units []domain.ManualCallableUnit) []domain.ManualCallableUnit {
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	annotated := make([]domain.ManualCallableUnit, 0, len(units))
	for _, u := range units {
		uState, uReason, uRecovery, uCD := unitHealthProjection(snap, u.ProviderName, u.Model, now)
		if uState != "" {
			u.HealthState = uState
			u.HealthReason = uReason
			u.RecoveryMode = uRecovery
			if uCD > 0 {
				u.CooldownUntil = uCD
			}
		}
		if p := a.lookupProvider(u.ProviderName); p != nil {
			// Always (re)compute so a cleared or expired window drops a stale
			// DisableUntil instead of blocking the unit past its window.
			u.DisableUntil = providerDisableDeadline(p.DisableWindows, now)
		}
		annotated = append(annotated, u)
	}
	return annotated
}

// resolveUnitsWithDisabledRefs annotates the units with health/cooldown data
// (same as resolveUnitsWithCooldown) and additionally projects the child
// aggregator's Disabled flag onto each aggregator-ref unit so the parent
// aggregator can skip disabled children during selection.
//
// Caller must hold a.mu.
func (a *Actor) resolveUnitsWithDisabledRefs(units []domain.ManualCallableUnit) []domain.ManualCallableUnit {
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	annotated := make([]domain.ManualCallableUnit, 0, len(units))
	for _, u := range units {
		uState, uReason, uRecovery, uCD := unitHealthProjection(snap, u.ProviderName, u.Model, now)
		if uState != "" {
			u.HealthState = uState
			u.HealthReason = uReason
			u.RecoveryMode = uRecovery
			if uCD > 0 {
				u.CooldownUntil = uCD
			}
		}
		if u.AggregatorID != "" {
			if child, ok := a.aggregators[u.AggregatorID]; ok {
				u.Disabled = child.Disabled
			}
		} else if p := a.lookupProvider(u.ProviderName); p != nil {
			u.DisableUntil = providerDisableDeadline(p.DisableWindows, now)
		}
		annotated = append(annotated, u)
	}
	return annotated
}

func (a *Actor) handleAggregatorResolve(ctx actor.PureContext, req domain.AIManagerAggregatorResolveReq) (domain.AIManagerAggregatorResolveResp, error) {
	t0 := time.Now()
	a.mu.RLock()
	dur := time.Since(t0)
	defer a.mu.RUnlock()
	if dur > 100*time.Millisecond {
		ctx.Logger().Warn("aimanager: aggregator.resolve lock wait", "dur", dur, "id", req.ID)
	}

	if cfg, ok := a.aggregators[req.ID]; ok {
		return domain.AIManagerAggregatorResolveResp{
			ID:       cfg.ID,
			Name:     cfg.Name,
			Strategy: normalizeStrategy(cfg.Strategy),
			Disabled: cfg.Disabled,
			Units:    a.resolveUnitsWithDisabledRefs(cfg.Units),
		}, nil
	}

	// Fallback: req.Id may be an actor ID (from aiaggregator periodic poll).
	// Find the configID whose spawned actor matches the requested actor ID.
	for cid, ref := range a.aggRefs {
		if ref.ID().String() == req.ID {
			if cfg, ok := a.aggregators[cid]; ok {
				return domain.AIManagerAggregatorResolveResp{
					ID:       cfg.ID,
					Name:     cfg.Name,
					Strategy: normalizeStrategy(cfg.Strategy),
					Disabled: cfg.Disabled,
					Units:    a.resolveUnitsWithDisabledRefs(cfg.Units),
				}, nil
			}
			break
		}
	}

	return domain.AIManagerAggregatorResolveResp{ID: req.ID}, nil
}

// notifyAggregator pushes a version bump + full configuration to the target child.
// It acquires its own lock — callers must NOT hold a.mu when invoking this.
func (a *Actor) notifyAggregator(configID string) {
	a.mu.Lock()
	ref, ok := a.aggRefs[configID]
	cfg := a.aggregators[configID]
	a.configVersion++
	version := a.configVersion
	units := a.resolveUnitsWithDisabledRefs(cfg.Units)
	a.mu.Unlock()

	if !ok || ref == nil {
		return
	}

	// Version push
	verReq := domain.ConfigVersionPush{Version: version}
	verCall := ref.Invoke(a.lifecycleCtx, "aiaggregator.config_version", verReq)
	_ = verCall.Close()

	// Full config push
	resp := domain.AIManagerAggregatorResolveResp{
		ID:       cfg.ID,
		Name:     cfg.Name,
		Strategy: normalizeStrategy(cfg.Strategy),
		Disabled: cfg.Disabled,
		Units:    units,
	}
	cfgCall := ref.Invoke(a.lifecycleCtx, "aiaggregator.config_apply", resp)
	_ = cfgCall.Close()
}

// notifyAutoAggregator pushes a config with dynamically-built units (all
// providers) to the system-level auto aggregator child. It acquires its own
// lock — callers must NOT hold a.mu when invoking this.
func (a *Actor) notifyAutoAggregator() {
	a.mu.Lock()
	ref, ok := a.aggRefs[autoAggregatorID]
	if !ok || ref == nil {
		a.mu.Unlock()
		return
	}
	a.configVersion++
	version := a.configVersion
	units := a.buildAutoUnits()
	strategy := "smart"
	if cfg, exists := a.aggregators[autoAggregatorID]; exists {
		cfg.Units = units
		if cfg.Strategy != "" {
			strategy = normalizeStrategy(cfg.Strategy)
		}
		cfg.Strategy = strategy
		a.aggregators[autoAggregatorID] = cfg
	}
	a.mu.Unlock()

	// Version push
	verReq := domain.ConfigVersionPush{Version: version}
	verCall := ref.Invoke(a.lifecycleCtx, "aiaggregator.config_version", verReq)
	_ = verCall.Close()

	// Full config push with dynamic units.
	resp := domain.AIManagerAggregatorResolveResp{
		ID:       autoAggregatorID,
		Name:     "Auto (All Providers)",
		Strategy: strategy,
		Units:    units,
	}
	cfgCall := ref.Invoke(a.lifecycleCtx, "aiaggregator.config_apply", resp)
	_ = cfgCall.Close()
}

// handleConfigExport serializes all providers and aggregators to JSON.
func (a *Actor) handleConfigExport(_ actor.PureContext) (domain.AIManagerConfigExportResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	type exportData struct {
		Providers   []domain.Provider                            `json:"providers"`
		Aggregators map[string]domain.AIManagerAggregatorGetResp `json:"aggregators"`
	}

	data := exportData{
		Providers:   make([]domain.Provider, 0, len(a.Providers)),
		Aggregators: make(map[string]domain.AIManagerAggregatorGetResp, len(a.aggregators)),
	}

	for _, p := range a.Providers {
		data.Providers = append(data.Providers, p)
	}

	for configID, agg := range a.aggregators {
		if configID == autoAggregatorID {
			continue
		}
		agg.Strategy = normalizeStrategy(agg.Strategy)
		data.Aggregators[configID] = agg
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return domain.AIManagerConfigExportResp{}, fmt.Errorf("aimanager: marshal export: %w", err)
	}
	return domain.AIManagerConfigExportResp{Data: string(b)}, nil
}

// handleConfigImport replaces all user-defined providers from JSON.
// Aggregators are intentionally left untouched; only providers are imported.
func (a *Actor) handleConfigImport(ctx actor.PureContext, req domain.AIManagerConfigImportReq) (_ domain.AIManagerConfigImportResp, err error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.AIManagerConfigImportResp{}, err
	}

	t0 := time.Now()
	ctx.Logger().Info("aimanager: config.import start")

	var payload struct {
		Providers   []domain.Provider                            `json:"providers"`
		Aggregators map[string]domain.AIManagerAggregatorGetResp `json:"aggregators"`
	}
	if err := json.Unmarshal([]byte(req.Data), &payload); err != nil {
		return domain.AIManagerConfigImportResp{}, fmt.Errorf("aimanager: unmarshal import: %w", err)
	}

	a.mu.Lock()
	// locked tracks whether this handler still holds the write lock so the
	// panic-recover below only releases it when actually held (see
	// handleProviderConfigure for the stateless rationale).
	locked := true
	ctx.Logger().Info("aimanager: config.import lock acquired", "dur", time.Since(t0))

	defer func() {
		if r := recover(); r != nil {
			ctx.Logger().Error("aimanager: config.import panic", "panic", r)
			if locked {
				a.mu.Unlock()
			}
			err = fmt.Errorf("aimanager: config.import panic: %v", r)
		}
	}()

	// Replace providers.
	a.Providers = make([]domain.Provider, 0, len(payload.Providers))
	for _, p := range payload.Providers {
		a.Providers = append(a.Providers, p)
	}
	// Re-inject cooldown policy (constants may change across imports).
	a.applyCooldownPolicy()

	// Rebuild the auto aggregator's units from the new providers while leaving
	// user-defined aggregators untouched.
	notifyAuto := false
	if _, ok := a.aggRefs[autoAggregatorID]; ok {
		units := a.buildAutoUnits()
		a.aggregators[autoAggregatorID] = domain.AIManagerAggregatorGetResp{
			ID:    autoAggregatorID,
			Name:  "Auto (All Providers)",
			Units: units,
		}
		notifyAuto = true
	}
	a.configVersion++
	a.saveOrLog(ctx)
	ctx.Logger().Info("aimanager: config.import unlock", "dur", time.Since(t0))
	locked = false
	a.mu.Unlock()

	if notifyAuto {
		a.notifyAggregator(autoAggregatorID)
	}
	a.registerProviderGates()
	ctx.Logger().Info("aimanager: config.import done", "dur", time.Since(t0))
	return domain.AIManagerConfigImportResp{}, nil
}
