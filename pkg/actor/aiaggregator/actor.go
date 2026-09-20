package aiaggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/compaction"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/llmclient/nativetools"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
	"github.com/qomos-w/sporemind/pkg/tokenest"
)

// systemAggregatorID is the id of the auto aggregator spawned by aimanager to
// serve unit-kind and auto-kind refs routed from agents. It is the only
// aggregator that performs on-demand unit resolution (constructing an ad-hoc
// CallableUnit from provider config when a requested model is not in its pool).
// Mirrors aimanager.autoAggregatorID.
const systemAggregatorID = "system"

// thinkOpenTag / thinkCloseTag are the inline reasoning markers some
// OpenAI-compatible models emit inside the regular content stream instead
// of the dedicated reasoning_content channel.
const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

// stripThinkTags removes the inline reasoning markers some OpenAI-compatible
// models emit inside the regular content stream, keeping the enclosed text so
// it can still serve as the inferred title.
func stripThinkTags(s string) string {
	if !strings.Contains(s, thinkOpenTag) {
		return s
	}
	s = strings.ReplaceAll(s, thinkOpenTag, "")
	s = strings.ReplaceAll(s, thinkCloseTag, "")
	return s
}

const (
	intentOpenMarker  = "【INTENT】"
	intentCloseMarker = "【/INTENT】"
)

type intentExtractor struct {
	tf      thinkFilter
	matched bool
	pending string
	content strings.Builder
	done    bool
}

func (e *intentExtractor) feedText(delta string) (done bool, result string) {
	visible := e.tf.feed(delta)
	for _, r := range visible {
		if e.done {
			break
		}
		e.feedRune(r)
	}
	return e.done, e.result()
}

// feedRune processes a single visible rune, advancing the open/close marker
// state machine and accumulating confirmed content.
func (e *intentExtractor) feedRune(r rune) {
	if e.done {
		return
	}
	e.pending += string(r)

	if !e.matched {
		idx := strings.Index(e.pending, intentOpenMarker)
		if idx >= 0 {
			e.matched = true
			e.pending = e.pending[idx+len(intentOpenMarker):]
		} else {
			// Keep only the tail that could still be a prefix of the open marker.
			keep := markerPrefixLen(e.pending, intentOpenMarker)
			e.pending = e.pending[len(e.pending)-keep:]
			return
		}
	}

	// Open marker matched; the remainder of pending is candidate content.
	if idx := strings.Index(e.pending, intentCloseMarker); idx >= 0 {
		e.content.WriteString(e.pending[:idx])
		e.done = true
		return
	}
	if nl := strings.IndexByte(e.pending, '\n'); nl >= 0 {
		e.content.WriteString(e.pending[:nl])
		e.done = true
		return
	}
	if pd := strings.IndexRune(e.pending, '。'); pd >= 0 {
		e.content.WriteString(e.pending[:pd])
		e.done = true
		return
	}
	// Flush bytes that can no longer extend a close-marker prefix, keeping only
	// the tail that could still complete intentCloseMarker.
	keep := markerPrefixLen(e.pending, intentCloseMarker)
	e.content.WriteString(e.pending[:len(e.pending)-keep])
	e.pending = e.pending[len(e.pending)-keep:]
}

// result returns the extracted title. It is only meaningful once done is true;
// before that it returns whatever has been accumulated, which the caller should
// ignore.
func (e *intentExtractor) result() string {
	if !e.done {
		return ""
	}
	return strings.TrimSpace(e.content.String())
}

// sanitizeIntentTitle strips residual intent markers from an inferred title.
// Models occasionally emit the open marker twice or drop the close marker
// entirely; in both cases the marker text itself leaks into the result (a
// duplicated open marker survives the extractor as content, and the raw-text
// fallback keeps an unterminated wrapper verbatim). Take the text after the
// last open marker, then cut at the first close marker if present.
func sanitizeIntentTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, intentOpenMarker); i >= 0 {
		s = s[i+len(intentOpenMarker):]
	}
	if j := strings.Index(s, intentCloseMarker); j >= 0 {
		s = s[:j]
	}
	return strings.TrimSpace(s)
}

// thinkFilter is a streaming filter that drops inline <think>...</think>
// regions from a content stream while emitting the surrounding visible text.
// It maintains a small pending buffer across deltas so a tag split across two
// deltas is still detected.
type thinkFilter struct {
	inThink bool
	pending string
}

// feed consumes a chunk and returns the visible (non-think) text contained in
// it, plus any text flushed from a prior partial tag.
func (f *thinkFilter) feed(s string) string {
	f.pending += s
	var out strings.Builder
	for {
		if f.inThink {
			idx := strings.Index(f.pending, thinkCloseTag)
			if idx >= 0 {
				f.pending = f.pending[idx+len(thinkCloseTag):]
				f.inThink = false
				continue
			}
			keep := markerPrefixLen(f.pending, thinkCloseTag)
			f.pending = f.pending[len(f.pending)-keep:]
			break
		}
		idx := strings.Index(f.pending, thinkOpenTag)
		if idx >= 0 {
			out.WriteString(f.pending[:idx])
			f.pending = f.pending[idx+len(thinkOpenTag):]
			f.inThink = true
			continue
		}
		keep := markerPrefixLen(f.pending, thinkOpenTag)
		out.WriteString(f.pending[:len(f.pending)-keep])
		f.pending = f.pending[len(f.pending)-keep:]
		break
	}
	return out.String()
}

// markerPrefixLen returns the byte length of the longest suffix of s that is a
// proper prefix of marker. It preserves only bytes that can still complete a
// marker after the next stream delta.
func markerPrefixLen(s, marker string) int {
	limit := len(marker) - 1
	if len(s) < limit {
		limit = len(s)
	}
	for n := limit; n > 0; n-- {
		if strings.HasSuffix(s, marker[:n]) {
			return n
		}
	}
	return 0
}

// CallableUnit is a single invocable (model + endpoint + protocol).
// AuthToken is intentionally NOT stored here — it is resolved from the
// provider via aimanager.provider.resolve_token at dispatch time so a
// rotated key never goes stale in the aggregator.
type CallableUnit struct {
	ID string `json:"id"`
	// AggregatorID identifies a nested aggregator pool entry. Nested entries
	// intentionally have no provider endpoint of their own; dispatch delegates
	// to the referenced child aggregator.
	AggregatorID     string `json:"aggregatorId,omitempty"`
	Model            string `json:"model"`
	Endpoint         string `json:"endpoint"`
	ProviderName     string `json:"providerName"`
	Protocol         string `json:"protocol"`
	Modality         string `json:"modality,omitempty"` // "chat" (default) | "image"
	IsReasoning      bool   `json:"isReasoning,omitempty"`
	UserAgent        string `json:"userAgent,omitempty"`
	Proxy            string `json:"proxy,omitempty"` // HTTP(S)/SOCKS5 proxy URL; mirror of Provider.Proxy
	MaxConcurrency   int32  `json:"maxConcurrency,omitempty"`
	MaxContextLength int32  `json:"maxContextLength,omitempty"`
	// DisableUntil is the Unix-seconds deadline of the active daily disable
	// window, computed by aimanager from Provider.DisableWindows. It is a
	// recurring time-of-day gate, not a failure-driven cooldown. Operator
	// policy that must hard-block the unit; health state is in llmclient.
	DisableUntil int64 `json:"disableUntil,omitempty"`
	// Disabled is a manual operator override projected from aimanager. When true
	// the unit is hard-skipped during selection, independent of health state or
	// disable windows. It is an operator policy, not a failure-driven record.
	Disabled bool `json:"disabled,omitempty"`
	// ReasoningEffort is the pool-unit default reasoning effort, applied when a
	// dispatch request does not specify one. Free-text effort value
	// ("none" | "low" | "medium" | "high" | "xhigh" | "max" | "ultra" | "").
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	// Token Plan mirror synced from Provider by aimanager. Used by the
	// selectUnit token-plan hard filter (Phase 4) and the smart strategy
	// consumption-rate weight factor (Phase 6). Read-only on the unit; the
	// authority is the Provider config.
	IsTokenPlan           bool   `json:"isTokenPlan,omitempty"`
	TokenPlanExpiresAt    string `json:"tokenPlanExpiresAt,omitempty"`    // RFC3339; empty = no expiry
	TokenPlanRemainingPct int32  `json:"tokenPlanRemainingPct,omitempty"` // 0~100; 0 = exhausted
	TokenPlanWindowMs     int64  `json:"tokenPlanWindowMs,omitempty"`     // rate eval window in ms
	// OnDemand marks a unit constructed ad-hoc from a provider config lookup,
	// not present in the configured pool. Such units participate in dispatch
	// (including concurrency control via the per-endpoint semaphore). Health
	// feedback is recorded via llmclient (the global health layer), not stored
	// locally — the configured pool is never polluted.
	OnDemand bool `json:"onDemand,omitempty"`
}

// SelectRequest carries the context needed by a strategy to select a unit.
type SelectRequest struct {
	AgentID  string
	SlotKind string
	Unit     domain.ModelUnit
	// UnitPinned marks a hard user selection (unit-locked slot). Pinned
	// selections bypass transient cooling states so a user retry genuinely
	// re-attempts the chosen unit; hard bans (auth-disabled, daily disable
	// window, unavailable child aggregator) still apply. Rotation/failover
	// requests must leave it false.
	UnitPinned bool
}

// Strategy selects a CallableUnit from the pool for a given request.
type Strategy interface {
	Select(req SelectRequest, units []CallableUnit) (*CallableUnit, error)
}

// rrAgentPin records the last unit assigned to an (agent, slot) key by
// RoundRobinStrategy. cursor is the index the assignment occupied in the
// matched pool at assignment time; the next reassignment resumes from
// cursor+1 (wrapping).
type rrAgentPin struct {
	lastID string
	cursor int
}

// RoundRobinStrategy assigns each (agentID, slotKind) a unit and keeps it
// assigned while that unit remains in the eligible pool — a healthy assigned
// unit is never rotated away. First assignments draw from a shared rotation
// cursor so agents spread across the pool; when an assigned unit drops out of
// the pool (cooldown, disabled, token-plan exhausted, pool reshaped), the next
// unit after that agent's last assigned index is assigned, wrapping around.
// Requests without an AgentID degrade to classic per-request rotation.
// Matching follows matchUnits semantics: a naked model (model without
// provider) is rejected; auto-pick (empty unit) matches the whole pool.
type RoundRobinStrategy struct {
	mu     sync.Mutex
	idx    map[string]int        // shared rotation cursor: model -> next index
	agents map[string]rrAgentPin // (agent, slot) -> last assignment
}

func NewRoundRobinStrategy() *RoundRobinStrategy {
	return &RoundRobinStrategy{
		idx:    make(map[string]int),
		agents: make(map[string]rrAgentPin),
	}
}

func (s *RoundRobinStrategy) Select(req SelectRequest, units []CallableUnit) (*CallableUnit, error) {
	matched := matchUnits(req.Unit, units)
	if len(matched) == 0 {
		return nil, noMatchError(req.Unit)
	}
	if len(matched) == 1 {
		return &matched[0], nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := pinKey(req)
	if key != "" {
		if pin, ok := s.agents[key]; ok {
			for i := range matched {
				if matched[i].ID == pin.lastID {
					s.agents[key] = rrAgentPin{lastID: pin.lastID, cursor: indexOfUnit(units, pin.lastID)}
					return &matched[i], nil
				}
			}
			// Reassignment: the assigned unit left the eligible pool, and the
			// caller has already removed it from the slice — later units
			// shifted toward the stored cursor position, so the next unit down
			// from the last assignment sits at (clamped) cursor itself. Scan
			// forward (wrapping) for the first still-eligible unit.
			start := pin.cursor
			if start >= len(units) {
				start = len(units) - 1
			}
			if start < 0 {
				start = 0
			}
			for off := 0; off < len(units); off++ {
				pos := (start + off) % len(units)
				for i := range matched {
					if matched[i].ID == units[pos].ID {
						s.agents[key] = rrAgentPin{lastID: units[pos].ID, cursor: pos}
						return &units[pos], nil
					}
				}
			}
		}
	}
	// First assignment (or agent-less request): draw from the shared rotation
	// cursor so load spreads across the pool.
	i := s.idx[req.Unit.Model] % len(matched)
	s.idx[req.Unit.Model] = i + 1
	if key != "" {
		s.agents[key] = rrAgentPin{lastID: matched[i].ID, cursor: indexOfUnit(units, matched[i].ID)}
	}
	return &matched[i], nil
}

// indexOfUnit returns the position of the unit ID in the pool, or -1 when
// absent.
func indexOfUnit(units []CallableUnit, id string) int {
	for i := range units {
		if units[i].ID == id {
			return i
		}
	}
	return -1
}

// Actor is a named callable-unit pool. It receives its full configuration
// (units + fast model) from aimanager via resolve or config push, and routes
// dispatch requests to the appropriate protocol client per selected unit.
type Actor struct {
	actor.Host
	id           string
	name         string
	localVersion int64
	units        []CallableUnit
	aimanagerRef ref.Ref
	strategy     Strategy
	registry     llmclient.Registry
	nativeTools  *nativetools.Registry
	lifecycleCtx context.Context // cancelled when the actor stops
	actorCtx     actor.Context   // stored on start for cross-actor lookups
	mu           sync.RWMutex    // protects units, name, localVersion, disabled
	// configID is the aimanager-side aggregator config id (the key parents use
	// in aggregator-ref pool entries and in the llmclient aggregator health
	// registry). Learned from applyResolvedConfig (resp.ID); a.id is a
	// canonical actor id and must NOT be used as the registry key — parents
	// look children up by config id, so an actor-id-keyed registration is
	// invisible to them.
	configID string
	// disabled is a manual operator override for the aggregator itself. When
	// true, dispatch entry fails fast with a clear error and no fallback.
	disabled bool

	// noImageInput records units (provider::model) whose model rejected image
	// input with a 400/404 "does not support image" upstream error. Learned at
	// dispatch time and used to strip image blocks from the wire request up
	// front. In-memory and self-healing — a restart re-learns via one failed
	// request, mirroring the process-local health layer. Guarded by mu.
	noImageInput map[string]bool

	// learnedReasoning records units (provider::model) that produced only
	// reasoning_content during intent inference — they behave as reasoning
	// models even when the name heuristic (isReasoningModel) says otherwise.
	// markUnitReasoning writes here instead of a.units so the 15s config poll
	// (applyResolvedConfig rebuilds the pool) cannot wipe the learned flag.
	// In-memory and self-healing, mirroring noImageInput. Guarded by mu.
	learnedReasoning map[string]bool

	// aistatsRef caches the global aistats system actor ref so telemetry
	// submission does one discovery call.
	statsSvc   *aistatsquery.Service // lazily built for smart strategy
	statsSvcMu sync.Mutex

	// visionPinMu guards the media-vision-binding cache. The binding lives in
	// the media actor's persisted store and changes rarely; a short TTL keeps
	// recognition dispatches off the cross-actor lookup hot path while staying
	// close enough to the configured selection.
	visionPinMu   sync.Mutex
	visionPin     *visionPinValue
	visionPinAt   time.Time

	// imageRecogMu guards the bounded recognition cache (image URL + prompt
	// hash → answer). It collapses repeated recognition of the same image
	// across dispatch retries into a single vision call.
	imageRecogMu    sync.Mutex
	imageRecogCache map[string]domain.AIAggregatorImageRecognizeResp
	imageRecogOrder []string
	statsCache *statsCache // per-unit stats TTL cache for smart strategy

	// oracleRef caches the global oracle system actor ref so failure
	// diagnostics do one discovery call per live actor generation
	// (mirrors the aistatsRef live-check pattern).
	oracleMu  sync.Mutex
	oracleRef ref.Ref

	// assignmentCache holds the provider→agent-count snapshot for standard
	// strategy load-balancing. Refreshed by startAssignmentRefresher.
	assignmentCache   *assignmentCache
	assignmentCacheMu sync.Mutex

	// dispatchActivity tracks the in-flight dispatch activity for each
	// unit ID (concrete or aggregator-ref) currently dispatched by this
	// aggregator. Written at dispatch begin/end (and on stream-open / rotation)
	// by handleDispatch; read by handleStatus and by the child-status
	// aggregation path so the composer sees which units are busy. Transient
	// and aggregator-local — never persisted.
	dispatchActivityMu sync.Mutex
	dispatchActivity   map[string]domain.DispatchActivity

	// configLoaded tracks whether the aggregator has successfully loaded its
	// configuration at least once (via lazy load, version push, config apply,
	// or poll). Until true, ensureConfigLoaded retries resolveConfig on every
	// dispatch. Once true, lazy loading stops — subsequent config changes are
	// handled by version push and the periodic poll.
	configLoaded bool
	// configLoadMu serializes lazy-load attempts so concurrent dispatches
	// don't all fire resolveConfig simultaneously, while still allowing retry
	// when the previous attempt failed (unlike the former sync.Once which
	// permanently blocked retry on failure).
	configLoadMu sync.Mutex

	// exposeOnce ensures the system aggregator exposes its service domain
	// exactly once, after the aimanager config id has been learned.
	exposeOnce sync.Once

	// childRefCache caches aggregator-ref config IDs resolved to live child
	// actor refs, with TTLs. Without it every nested dispatch re-invokes
	// aimanager.aggregator_list; when a child's pool is cooling and the parent
	// keeps rotating into it, that lookup can run thousands of times per
	// second (the observed aggregator_list storm). Positive hits live
	// childRefTTL; negative hits (id not listed) live childRefNegativeTTL so
	// a misconfigured id cannot spin the lookup either.
	childRefMu    sync.Mutex
	childRefCache map[string]childRefEntry
}

// childRefEntry is one cached child-aggregator ref resolution. A nil ref is a
// negative entry (config id present in the pool but not resolvable right now).
type childRefEntry struct {
	ref ref.Ref
	at  time.Time
}

const (
	childRefTTL         = 30 * time.Second
	childRefNegativeTTL = 3 * time.Second
)

// NewActor constructs an unstarted aiaggregator factory.
func NewActor() func() actor.Actor {
	return func() actor.Actor {
		return &Actor{
			strategy: NewRoundRobinStrategy(),
		}
	}
}

func (a *Actor) Type() string { return "aiaggregator" }

func (a *Actor) OnStart(ctx actor.Context) error {
	a.id = ctx.Self().ID().String()
	a.lifecycleCtx = ctx.Lifecycle()
	a.actorCtx = ctx
	a.registry = newBuiltinRegistry()
	a.nativeTools = nativetools.DefaultRegistry()
	ctx.Logger().Info("aiaggregator: starting", "id", a.id)
	a.aimanagerRef = ctx.Parent()

	// Declare the aiaggregator domain at OnStart (not inside the config-apply
	// path): RegisterDomain stamps the service name on this actor's callables
	// so the compiled manifest carries service="aiaggregator" — the host
	// protocol extraction only exposes service-scoped callables, and the
	// late, config-gated Expose() below is invisible to the export snapshot.
	// Expose itself stays config-gated (only the system instance owns the
	// service); domain declaration is namespace tracking and is unconditional.
	ctx.RegisterDomain("aiaggregator")

	// Config poll lane: handlePoll → resolveConfig awaits a cross-actor
	// aimanager.aggregator_resolve Invoke, so a slow/hung aimanager would
	// otherwise pin the owner lane (Owner Lane 禁阻塞 red line). The
	// self-rearming 15s tick is routed to a dedicated stateful lane; its
	// state writes (applyResolvedConfig) are already a.mu-guarded, so it
	// runs safely alongside owner-lane handlers. Mirrors agent_exec
	// (agent.go:737).
	if err := ctx.RegisterLoop("aiagg_poll", actor.ModeStateful); err != nil {
		return fmt.Errorf("aiaggregator: register poll loop: %w", err)
	}

	if err := ctx.Register("aiaggregator.config_version", a.handleVersionPush, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register config.version: %w", err)
	}
	if err := ctx.Register("aiaggregator.config_poll", a.handlePoll, actor.Internal(), actor.WithLoop("aiagg_poll")); err != nil {
		return fmt.Errorf("aiaggregator: register config.poll: %w", err)
	}
	if err := ctx.Register("aiaggregator.config_apply", a.handleConfigApply, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register config.apply: %w", err)
	}
	if err := ctx.Register("aiaggregator.dispatch", a.handleDispatch, actor.Public(), actor.Streaming[domain.AggregatorChunk]()); err != nil {
		return fmt.Errorf("aiaggregator: register dispatch: %w", err)
	}
	if err := ctx.Register("aiaggregator.summarize", a.handleSummarize, actor.Public()); err != nil {
		return fmt.Errorf("aiaggregator: register summarize: %w", err)
	}
	if err := ctx.Register("aiaggregator.probe_tokens", a.handleProbeTokens, actor.Public()); err != nil {
		return fmt.Errorf("aiaggregator: register probe_tokens: %w", err)
	}
	if err := ctx.Register("aiaggregator.intent", a.handleIntent, actor.Public()); err != nil {
		return fmt.Errorf("aiaggregator: register intent: %w", err)
	}
	if err := ctx.Register("aiaggregator.tool_judge", a.handleIntent, actor.Public()); err != nil {
		return fmt.Errorf("aiaggregator: register tool_judge: %w", err)
	}
	if err := ctx.Register("aiaggregator.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("aiaggregator: register status: %w", err)
	}
	if err := ctx.Register("aiaggregator.image_resolve", a.handleImageResolve, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register image.resolve: %w", err)
	}
	if err := ctx.Register("aiaggregator.video_resolve", a.handleVideoResolve, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register video.resolve: %w", err)
	}
	if err := ctx.Register("aiaggregator.image_recognize", a.handleImageRecognize, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register image_recognize: %w", err)
	}
	if err := ctx.Register("aiaggregator.provider_health_reset", a.handleProviderHealthReset, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register provider.health_reset: %w", err)
	}

	// Stats records are emitted as events (aistats.record) rather than
	// invokes: no PendingTable slot, no aistats owner-loop serialization,
	// no per-call goroutine. The event bus provides drop-oldest backpressure.
	if err := ctx.RegisterEventKind("aistats.record", domain.AIStatsRecord{}, actor.Internal()); err != nil {
		return fmt.Errorf("aiaggregator: register event kind aistats.record: %w", err)
	}

	// Config is pushed by aimanager after spawn (via notifyAggregator) or
	// picked up by the periodic poll. Avoid resolveConfig here to prevent
	// deadlock: OnStart runs under aimanager's write lock during spawn, and
	// resolveConfig calls back to aimanager.aggregator.resolve which needs
	// a read lock on the same mutex.

	// The self-rearming config poll runs on the "aiagg_poll" lane (the
	// config_poll handler declares actor.WithLoop("aiagg_poll"), which the
	// delayed self-call inherits).
	if err := ctx.After(15*time.Second, "aiaggregator.config_poll", nil); err != nil {
		ctx.Logger().Error("aiaggregator: schedule first poll failed", "error", err)
	}

	// Start the background stats refresher for SmartStrategy. It polls
	// aistats every 10s and stores the snapshot in the local statsCache;
	// the dispatch path reads from the cache without any actor calls.
	a.startStatsRefresher()

	// Start the background assignment refresher for StandardStrategy. It polls
	// aimanager.provider_assignments and stores the snapshot locally.
	a.startAssignmentRefresher()

	return nil
}

// handleVersionPush receives a version push from aimanager.
func (a *Actor) handleVersionPush(_ actor.PureContext, req domain.ConfigVersionPush) error {
	a.mu.RLock()
	same := req.Version == a.localVersion
	a.mu.RUnlock()
	if same {
		return nil
	}
	if err := a.resolveConfig(); err != nil {
		return err
	}
	a.mu.Lock()
	a.localVersion = req.Version
	a.mu.Unlock()
	return nil
}

// handleConfigApply receives aggregator-level settings directly from aimanager.
func (a *Actor) handleConfigApply(_ actor.PureContext, req domain.AIManagerAggregatorResolveResp) error {
	a.applyResolvedConfig(req)
	return nil
}

// providerHealthResetReq mirrors the reset broadcast sent by aimanager after a
// manual provider health reset. Health state itself is owned by llmclient; the
// aggregator keeps this callable for protocol compatibility with older
// aimanager broadcasts and has no local state to clear.
type providerHealthResetReq struct {
	ProviderName string `json:"providerName"`
}

func (a *Actor) handleProviderHealthReset(_ actor.PureContext, _ providerHealthResetReq) error {
	return nil
}

// handlePoll is triggered by the periodic timer (every 15s). It runs on the
// dedicated "aiagg_poll" lane (see OnStart), so the resolveConfig Await never
// occupies the owner lane; it re-arms its own next tick.
func (a *Actor) handlePoll(ctx actor.Context) error {
	if err := a.resolveConfig(); err != nil {
		ctx.Logger().Error("poll: resolve config failed", "error", err)
	}
	scheduleNextPoll(ctx)
	return nil
}

// ensureConfigLoaded performs a one-shot resolveConfig when config has not
// been loaded yet. This covers the cold-start window: aimanager's
// notifyAggregator sends config via fire-and-forget Invoke, so the
// aggregator's OnStart may complete before config_apply is processed.
// Without this lazy load, the first dispatch/intent after startup fails
// with "no callable unit for model" until the 15s periodic poll fills the
// pool.
//
// Unlike the former sync.Once design, this retries on failure: if
// resolveConfig fails (e.g. aimanager temporarily busy during concurrent
// worker creation), the next dispatch attempts again. configLoadMu
// serializes attempts so concurrent dispatches share a single resolve.
func (a *Actor) ensureConfigLoaded() {
	a.mu.RLock()
	loaded := a.configLoaded || len(a.units) > 0
	a.mu.RUnlock()
	if loaded {
		return
	}
	a.configLoadMu.Lock()
	defer a.configLoadMu.Unlock()
	// Double-check after acquiring the lock — another handler may have
	// completed a resolveConfig (or processed a config_apply) while we
	// waited.
	a.mu.RLock()
	loaded = a.configLoaded || len(a.units) > 0
	a.mu.RUnlock()
	if loaded {
		return
	}
	if err := a.resolveConfig(); err != nil {
		a.actorCtx.Logger().Warn("aiaggregator: lazy config load failed", "error", err)
		return // configLoaded stays false → next dispatch retries
	}
	// resolveConfig → applyResolvedConfig sets configLoaded = true.
}

// resolveConfig calls aimanager.aggregator.resolve to fetch the full unit list.
func (a *Actor) resolveConfig() error {
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.aggregator_resolve", domain.AIManagerAggregatorResolveReq{ID: a.id})
	result, err := call.Final(callCtx)
	if err != nil {
		return fmt.Errorf("resolve: call failed: %w", err)
	}

	resp, err := decodeResolveResp(result)
	if err != nil {
		return fmt.Errorf("resolve: decode: %w", err)
	}

	a.applyResolvedConfig(resp)
	return nil
}

// isReasoningModel reports whether a model name identifies a reasoning/thinking
// model. These models are unsuitable for title inference because they tend to
// emit chain-of-thought in reasoning_content and either ignore the title
// prompt or leave the regular content stream empty.
func isReasoningModel(model string) bool {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "thinking"):
		return true
	case strings.Contains(m, "reasoning"):
		return true
	case strings.Contains(m, "reasoner"):
		return true
	case m == "deepseek-r1" || strings.HasPrefix(m, "deepseek-r1-"):
		return true
	}
	return false
}

func (a *Actor) applyResolvedConfig(resp domain.AIManagerAggregatorResolveResp) {
	// Health state is process-wide in llmclient and intentionally is not merged
	// into the freshly loaded routing configuration. This method only rebuilds
	// the route metadata and token-plan mirrors supplied by aimanager.
	// Merge runtime-learned reasoning flags (intent inference observed a
	// reasoning-only stream) into the freshly loaded pool — the rebuild would
	// otherwise wipe markUnitReasoning's state every 15s poll.
	a.mu.RLock()
	learned := a.learnedReasoning
	a.mu.RUnlock()
	units := make([]CallableUnit, 0, len(resp.Units))
	// Pre-apply identity: configID from a previous apply, else the actor id
	// (test-constructed actors use the config id as the actor id).
	selfKey := a.aggHealthKey()
	for _, u := range resp.Units {
		if u.AggregatorID != "" && (u.AggregatorID == resp.ID || u.AggregatorID == selfKey) {
			// Config-time cycle guard: a pool entry referencing this
			// aggregator itself would recurse forever. resp.ID is the config
			// id this pool belongs to (a.id is an unrelated actor ULID).
			continue
		}
		unitID := u.ProviderName + "::" + u.Model
		if u.AggregatorID != "" {
			unitID = "agg:" + u.AggregatorID
		}
		cu := CallableUnit{
			ID:                    unitID,
			AggregatorID:          u.AggregatorID,
			Model:                 u.Model,
			Endpoint:              u.Endpoint,
			ProviderName:          u.ProviderName,
			Protocol:              u.Protocol,
			Modality:              u.Modality,
			IsReasoning:           u.IsReasoning || isReasoningModel(u.Model) || learned[u.ProviderName+"::"+u.Model],
			UserAgent:             u.UserAgent,
			Proxy:                 u.Proxy,
			MaxConcurrency:        u.MaxConcurrency,
			MaxContextLength:      u.MaxContextLength,
			DisableUntil:          u.DisableUntil,
			Disabled:              u.Disabled,
			ReasoningEffort:       u.ReasoningEffort,
			IsTokenPlan:           u.IsTokenPlan,
			TokenPlanExpiresAt:    u.TokenPlanExpiresAt,
			TokenPlanRemainingPct: u.TokenPlanRemainingPct,
			TokenPlanWindowMs:     u.TokenPlanWindowMs,
		}
		units = append(units, cu)
	}
	a.mu.Lock()
	a.name = resp.Name
	a.configID = resp.ID
	a.units = units
	a.disabled = resp.Disabled
	a.configLoaded = true
	a.strategy = a.resolveStrategy(resp.Strategy)
	a.mu.Unlock()
	// An aggregator disabled by operator policy is unavailable to parents from
	// the moment its config loads, not just from the first doomed dispatch.
	// Recovery is config-side: a re-enabled config's next selection clears
	// the registration (reportOwnPoolHealth) or the TTL expires.
	if key := a.aggHealthKey(); resp.Disabled && key != "" && key != systemAggregatorID {
		llmclient.RegisterAggregatorUnavailableUntil(key, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
	}

	// Expose the system aggregator as the canonical "aiaggregator" service so
	// the plugin host bridge can route SDK llm.complete/llm.chat reverse calls
	// to aiaggregator.dispatch. Only the system instance may own this domain;
	// custom/manual aggregators keep their config-id-scoped identities.
	if a.isSystemAggregator() && a.actorCtx != nil {
		a.exposeOnce.Do(func() {
			if err := a.actorCtx.RegisterDomain("aiaggregator").Expose(); err != nil {
				a.actorCtx.Logger().Error("aiaggregator: expose system service failed", "error", err)
			} else {
				a.actorCtx.Logger().Info("aiaggregator: exposed system service")
			}
		})
	}
}

// aggHealthKey returns the key this aggregator registers itself under in the
// llmclient aggregator health registry. Parents reference a nested child by
// its aimanager config id (CallableUnit.AggregatorID), so the config id is the
// only key they can ever find. The actor id is a canonical ULID in production
// (unrelated to the config id) and only serves as a fallback for actors that
// never loaded config (test-constructed actors).
func (a *Actor) aggHealthKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.configID != "" {
		return a.configID
	}
	return a.id
}

// isSystemAggregator reports whether this actor is the aimanager-spawned auto
// aggregator ("system"). Production actors carry a canonical actor id that is
// not the literal "system", so the check prefers the config id learned from
// applyResolvedConfig and falls back to the actor id (test construction).
func (a *Actor) isSystemAggregator() bool {
	return a.aggHealthKey() == systemAggregatorID
}

// resolveUnitToken fetches the current AuthToken for the unit's provider from
// aimanager. Called per-dispatch so a rotated provider key takes effect on the
// next request without needing to re-configure the aggregator. Returns empty
// string for ad-hoc units (no ProviderName) — those have no backing provider.
func (a *Actor) resolveUnitToken(unit CallableUnit) (string, error) {
	if unit.ProviderName == "" {
		return "", nil
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.provider_resolve_token", domain.AIManagerProviderResolveTokenReq{Name: unit.ProviderName})
	result, err := call.Final(callCtx)
	if err != nil {
		return "", fmt.Errorf("resolve token for provider %q: %w", unit.ProviderName, err)
	}
	switch r := result.(type) {
	case domain.AIManagerProviderResolveTokenResp:
		return r.AuthToken, nil
	case *domain.AIManagerProviderResolveTokenResp:
		if r == nil {
			return "", nil
		}
		return r.AuthToken, nil
	case []byte:
		if len(r) == 0 {
			return "", nil
		}
		var resp domain.AIManagerProviderResolveTokenResp
		if err := json.Unmarshal(r, &resp); err != nil {
			return "", fmt.Errorf("resolve token: decode bytes: %w", err)
		}
		return resp.AuthToken, nil
	default:
		body, err := json.Marshal(r)
		if err != nil {
			return "", fmt.Errorf("resolve token: marshal %T: %w", r, err)
		}
		var resp domain.AIManagerProviderResolveTokenResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return "", fmt.Errorf("resolve token: decode %T: %w", r, err)
		}
		return resp.AuthToken, nil
	}
}

// decodeResolveResp projects a Plan result back to domain.AIManagerAggregatorResolveResp.
func decodeResolveResp(v any) (domain.AIManagerAggregatorResolveResp, error) {
	switch x := v.(type) {
	case domain.AIManagerAggregatorResolveResp:
		return x, nil
	case *domain.AIManagerAggregatorResolveResp:
		if x != nil {
			return *x, nil
		}
		return domain.AIManagerAggregatorResolveResp{}, nil
	case []byte:
		if len(x) == 0 {
			return domain.AIManagerAggregatorResolveResp{}, nil
		}
		var resp domain.AIManagerAggregatorResolveResp
		if err := json.Unmarshal(x, &resp); err != nil {
			return domain.AIManagerAggregatorResolveResp{}, fmt.Errorf("decode bytes: %w", err)
		}
		return resp, nil
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.AIManagerAggregatorResolveResp{}, fmt.Errorf("re-marshal %T: %w", v, err)
		}
		var resp domain.AIManagerAggregatorResolveResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return domain.AIManagerAggregatorResolveResp{}, fmt.Errorf("decode %T: %w", v, err)
		}
		return resp, nil
	}
}

// decodeResolveModelResp projects an aimanager.provider.resolve_model result
// back to domain.AIManagerProviderResolveModelResp, tolerating the same set of
// transport shapes decodeResolveResp handles.
func decodeResolveModelResp(v any) (domain.AIManagerProviderResolveModelResp, error) {
	switch x := v.(type) {
	case domain.AIManagerProviderResolveModelResp:
		return x, nil
	case *domain.AIManagerProviderResolveModelResp:
		if x != nil {
			return *x, nil
		}
		return domain.AIManagerProviderResolveModelResp{}, nil
	case []byte:
		if len(x) == 0 {
			return domain.AIManagerProviderResolveModelResp{}, nil
		}
		var resp domain.AIManagerProviderResolveModelResp
		if err := json.Unmarshal(x, &resp); err != nil {
			return domain.AIManagerProviderResolveModelResp{}, fmt.Errorf("decode bytes: %w", err)
		}
		return resp, nil
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.AIManagerProviderResolveModelResp{}, fmt.Errorf("re-marshal %T: %w", v, err)
		}
		var resp domain.AIManagerProviderResolveModelResp
		if err := json.Unmarshal(body, &resp); err != nil {
			return domain.AIManagerProviderResolveModelResp{}, fmt.Errorf("decode %T: %w", v, err)
		}
		return resp, nil
	}
}

func scheduleNextPoll(ctx actor.Context) {
	if err := ctx.After(15*time.Second, "aiaggregator.config_poll", nil); err != nil {
		ctx.Logger().Error("poll: schedule next failed", "error", err)
	}
}

// reportProviderSuccess notifies aimanager that a provider request succeeded,
// so it can clear the provider-level health record. Health state is owned by
// llmclient; this call is a best-effort side channel for cross-aggregator
// observability. Runs in its own timeout.
func (a *Actor) reportProviderSuccess(providerName string) {
	if providerName == "" {
		return
	}

	type reportProviderSuccessReq struct {
		ProviderName string `json:"providerName"`
	}

	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.provider_report_success", reportProviderSuccessReq{ProviderName: providerName})
	_ = call.Close()
}

// unitInDisableWindow reports whether the unit is inside an active daily
// disable window (DisableUntil set in the future). Unlike failure-driven
// cooldown — which is advisory and never revokes dispatch — a disable window
// is an explicit operator policy that must hard-block the unit.
func unitInDisableWindow(u CallableUnit, now int64) bool {
	return u.DisableUntil > 0 && now < u.DisableUntil
}

// chatUnits returns all non-image units in the configured pool, excluding
// active operator disable windows and manually disabled units. Failure health
// is applied by the selection methods after matching, so an unhealthy exact
// pool match cannot silently fall through to an on-demand resolve of the same
// unit.
func (a *Actor) chatUnits() []CallableUnit {
	a.mu.RLock()
	defer a.mu.RUnlock()
	now := time.Now().Unix()
	out := make([]CallableUnit, 0, len(a.units))
	for _, u := range a.units {
		if u.Modality == "image" || u.Disabled || unitInDisableWindow(u, now) {
			continue
		}
		out = append(out, u)
	}
	return out
}

func filterAvailableUnits(units []CallableUnit, now time.Time) []CallableUnit {
	snap := llmclient.HealthSnapshot()
	available := make([]CallableUnit, 0, len(units))
	for _, u := range units {
		if unitAvailable(u, snap, now) {
			available = append(available, u)
		}
	}
	return available
}

// isSelfAggregatorRef reports whether the pool entry references this actor
// itself — a config mistake (or stale state) that would recurse forever if
// dispatched, since routing to the child equals looping back here. Compares
// against the config id (aggHealthKey): a.id is an actor ULID that never
// equals a pool entry's AggregatorID in production.
func (a *Actor) isSelfAggregatorRef(u CallableUnit) bool {
	return u.AggregatorID != "" && u.AggregatorID == a.aggHealthKey()
}

// selectUnit picks a unit for a request. When unit.Model is empty it delegates
// to the strategy; otherwise it tries an exact pool match first. Concurrency
// gating is NOT done here — it is handled by the global per-provider
// ProviderGate (pkg/llmclient/concurrency.go) which blocks (queues) when the
// provider is saturated. The aggregator only runs the strategy and returns the
// chosen unit; the caller acquires the provider gate slot before dispatching.
//
// If the strategy finds no pool match for a specific (provider, model), the
// system aggregator falls back to on-demand resolution via aimanager, so a
// unit-kind ref can be served even when its model is not in the auto pool.
func (a *Actor) selectUnit(req SelectRequest) (*CallableUnit, error) {
	// After every selection attempt, reflect the resulting pool health in the
	// aggregator-level registry so parents can skip this aggregator while its
	// whole pool is down (and clear once any unit is available again).
	defer a.reportOwnPoolHealth()

	pool := a.chatUnits()
	var matched []CallableUnit
	for _, u := range matchUnits(req.Unit, pool) {
		if a.isSelfAggregatorRef(u) {
			continue
		}
		matched = append(matched, u)
	}
	if len(matched) == 0 {
		return a.selectOnDemandUnit(req.Unit, req.UnitPinned)
	}

	// Token-plan and global health are hard filters. A pinned unhealthy unit
	// must fail as unavailable rather than silently resolve the same pair
	// on-demand; auto-selection may use the remaining healthy candidates.
	tokenFiltered := make([]CallableUnit, 0, len(matched))
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	for _, u := range matched {
		if tokenPlanExhausted(u) {
			continue
		}
		// A pinned (unit-locked) selection bypasses transient cooling states
		// so a user retry genuinely re-attempts the chosen unit; hard bans
		// (auth-disabled, daily disable window, unavailable child
		// aggregator) still block it.
		if req.UnitPinned {
			if !pinnedUnitSelectable(u, snap, now) {
				continue
			}
		} else if !unitAvailable(u, snap, now) {
			continue
		}
		tokenFiltered = append(tokenFiltered, u)
	}
	if len(tokenFiltered) == 0 {
		if req.Unit.Model != "" || req.Unit.Provider != "" {
			return nil, noMatchError(req.Unit)
		}
		return nil, errUnitsTokenPlanExhausted
	}

	// Prefer healthy units for auto-selection. The remaining candidates are
	// already available; this preserves strategy-specific ordering.
	tokenFiltered = preferHealthy(tokenFiltered)

	// Strategy selection — SmartStrategy reads from the local async cache
	// (never blocks). Round-robin / fallback are O(1).
	return a.strategy.Select(req, tokenFiltered)
}

// selectOnDemandUnit serves a (provider, model) absent from this aggregator's
// pool. Only the system aggregator (which serves arbitrary unit-kind refs)
// constructs an ad-hoc unit from provider config; named aggregators honor
// their explicitly-curated pools. Concurrency gating is handled by the global
// ProviderGate, not here. pinned applies the unit-locked-slot rule: transient
// cooling is bypassed (the user's retry re-attempts the pair), auth-disabled
// still blocks.
func (a *Actor) selectOnDemandUnit(unit domain.ModelUnit, pinned bool) (*CallableUnit, error) {
	if !a.isSystemAggregator() {
		return nil, noMatchError(unit)
	}
	od, err := a.resolveOnDemandUnit(unit)
	if err != nil {
		return nil, noMatchError(unit)
	}
	if od.Disabled {
		return nil, noMatchError(unit)
	}
	if tokenPlanExhausted(*od) {
		return nil, errUnitsTokenPlanExhausted
	}
	if unitInDisableWindow(*od, time.Now().Unix()) {
		return nil, fmt.Errorf("aiaggregator: unit %s/%s is inside its daily disable window", od.ProviderName, od.Model)
	}
	// Skip on-demand units that are cooling down or disabled in the
	// llmclient health layer. applyStreamOpenFailure records on-demand
	// failures there, so this prevents re-selecting a known-bad pair.
	now := time.Now()
	snap := llmclient.HealthSnapshot()
	if pinned {
		if !pinnedUnitSelectable(*od, snap, now) {
			return nil, noMatchError(unit)
		}
	} else if !unitAvailable(*od, snap, now) {
		return nil, noMatchError(unit)
	}
	return od, nil
}

// tokenPlanExhausted reports whether a unit's token plan is expired or has no
// remaining quota. Non-token-plan units are always available. A token-plan unit
// with no expiry set and remaining > 0 is available; one with remaining <= 0 or
// a past expiry date is skipped. Saturation/plan exhaustion does not trigger
// cooldown — it is a hard skip distinct from concurrency saturation.
func tokenPlanExhausted(u CallableUnit) bool {
	if !u.IsTokenPlan {
		return false
	}
	if u.TokenPlanRemainingPct <= 0 {
		return true
	}
	if u.TokenPlanExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, u.TokenPlanExpiresAt); err == nil && time.Now().After(exp) {
			return true
		}
	}
	return false
}

// resolveOnDemandUnit constructs a CallableUnit for a (provider, model) pair
// that is not in this aggregator's pool by looking up the provider's
// connection metadata via aimanager.provider.resolve_model. The returned unit
// is marked OnDemand: it participates in dispatch (including per-endpoint
// concurrency control) but is never written into a.units and never receives
// cooldown feedback, so the static pool is not polluted.
func (a *Actor) resolveOnDemandUnit(unit domain.ModelUnit) (*CallableUnit, error) {
	if unit.Provider == "" || unit.Model == "" {
		return nil, fmt.Errorf("on-demand resolve requires both provider and model")
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.provider_resolve_model", domain.AIManagerProviderResolveModelReq{Name: unit.Provider, Model: unit.Model})
	result, err := call.Final(callCtx)
	if err != nil {
		return nil, fmt.Errorf("on-demand resolve %s/%s: %w", unit.Provider, unit.Model, err)
	}
	resp, err := decodeResolveModelResp(result)
	if err != nil {
		return nil, fmt.Errorf("on-demand resolve %s/%s: %w", unit.Provider, unit.Model, err)
	}
	if resp.Endpoint == "" && resp.Protocol == "" {
		return nil, fmt.Errorf("on-demand resolve %s/%s: not found", unit.Provider, unit.Model)
	}
	return &CallableUnit{
		ID:                    unit.Provider + "::" + unit.Model,
		Model:                 unit.Model,
		Endpoint:              resp.Endpoint,
		ProviderName:          unit.Provider,
		Protocol:              resp.Protocol,
		Modality:              resp.Modality,
		IsReasoning:           isReasoningModel(unit.Model),
		MaxConcurrency:        resp.MaxConcurrency,
		MaxContextLength:      resp.MaxContextLength,
		UserAgent:             resp.UserAgent,
		Proxy:                 resp.Proxy,
		DisableUntil:          resp.DisableUntil,
		Disabled:              resp.Disabled,
		IsTokenPlan:           resp.IsTokenPlan,
		TokenPlanExpiresAt:    resp.TokenPlanExpiresAt,
		TokenPlanRemainingPct: resp.TokenPlanRemainingPct,
		TokenPlanWindowMs:     resp.TokenPlanWindowMs,
		OnDemand:              true,
	}, nil
}

// resolveIntentUnit finds the exact pool unit the caller (agent fast slot)
// pinned for title inference. A unit-kind match requires BOTH model and
// provider; a naked model (provider empty) must not match across providers.
// Named aggregators only serve their curated pool; the system aggregator
// falls back to on-demand construction for models not in the auto pool.
func (a *Actor) resolveIntentUnit(unit domain.ModelUnit) (*CallableUnit, error) {
	if unit.Model == "" || unit.Provider == "" {
		return nil, fmt.Errorf("unit %s/%s not available", unit.Provider, unit.Model)
	}
	pool := a.chatUnits()
	for i := range pool {
		if pool[i].Model == unit.Model && pool[i].ProviderName == unit.Provider {
			return &pool[i], nil
		}
	}
	if a.isSystemAggregator() {
		if onDemand, err := a.resolveOnDemandUnit(unit); err == nil {
			if unitInDisableWindow(*onDemand, time.Now().Unix()) {
				return nil, fmt.Errorf("unit %s/%s is inside its daily disable window", unit.Provider, unit.Model)
			}
			// Skip on-demand units that are cooling down or disabled in
			// the llmclient health layer, mirroring selectOnDemandUnit.
			now := time.Now()
			snap := llmclient.HealthSnapshot()
			if !unitAvailable(*onDemand, snap, now) {
				return nil, fmt.Errorf("unit %s/%s not available", unit.Provider, unit.Model)
			}
			return onDemand, nil
		}
	}
	return nil, fmt.Errorf("unit %s/%s not available", unit.Provider, unit.Model)
}

// markUnitReasoning records that a unit behaves as a reasoning model for
// title inference, so future calls skip it. The flag lives in learnedReasoning
// keyed by provider::model — writing to a.units would be wiped by the next
// applyResolvedConfig pool rebuild (15s poll), re-selecting the same
// reasoning-only unit on every title inference.
func (a *Actor) markUnitReasoning(unitID string) {
	parts := strings.SplitN(unitID, "::", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.learnedReasoning == nil {
		a.learnedReasoning = make(map[string]bool)
	}
	a.learnedReasoning[unitID] = true
	// Also update the live pool copy so intent candidate selection skips the
	// unit before the next config reload.
	for i := range a.units {
		if a.units[i].ID == unitID {
			a.units[i].IsReasoning = true
			break
		}
	}
}

// derefModelUnit returns the value behind an optional ModelUnit pointer,
// or an empty unit when nil.
func derefModelUnit(u *domain.ModelUnit) domain.ModelUnit {
	if u == nil {
		return domain.ModelUnit{}
	}
	return *u
}

// hasInteractionSubmitTool reports whether the tool set includes plan_submit or
// goal_submit, signaling that the LLM should generate a plan or goal
// interpretation before calling the tool. The caller uses this to extend the
// stream idle timeout so deep reasoning is not killed prematurely.
func hasInteractionSubmitTool(tools []domain.ToolSpec) bool {
	for _, t := range tools {
		name := strings.ToLower(t.Name)
		if name == "plan_submit" || name == "goal_submit" {
			return true
		}
		// Also match the underscore-ized callable ID (e.g. "plan_submit" →
		// "plan_submit") since tool names can be derived from either.
		full := strings.ToLower(strings.ReplaceAll(t.CallableID, ".", "_"))
		if full == "plan_submit" || full == "goal_submit" {
			return true
		}
	}
	return false
}

// dispatchSource abstracts a committed upstream once stream-open succeeds. A
// direct source owns an llmclient.Stream plus its provider-gate release; a
// nested source owns a child aggregator plan node (plus its receive channel)
// whose open relay will reuse the buffered first chunk.
type dispatchSource struct {
	// stream is set for direct (concrete-unit) dispatches.
	stream llmclient.Stream
	// release is the provider-gate release func for direct dispatches.
	release func()
	// nested is set for aggregator-ref dispatches.
	nested *nestedAggregatorStream
}

// nestedAggregatorStream relays chunks from a child aggregator dispatch. The
// child node is spawned here (Planner capability) and destroyed here; the
// parent relay does not touch the tree.
type nestedAggregatorStream struct {
	node   plan.Node
	recv   <-chan plan.RecvResult
	ctx    context.Context
	cancel context.CancelFunc
	// buffered holds the first chunk that confirmed the stream opened
	// (ResolvedUnit), plus any chunks that arrived before the caller's
	// first Read call.
	buffered []domain.AggregatorChunk
	// done is closed once the node has been stopped and cleaned up.
	done chan struct{}
}

// release stops the child plan node, destroys it, and cancels the child
// context. Idempotent and safe to call multiple times.
func (s *nestedAggregatorStream) release(a *Actor) {
	select {
	case <-s.done:
		return
	default:
	}
	close(s.done)
	s.cancel()
	if s.node != nil {
		_ = s.node.Stop()
	}
	if a != nil && a.actorCtx != nil {
		_ = a.actorCtx.Destroy(s.node.Ref())
	}
}

// Read decodes the next AggregatorChunk from the child stream, replaying
// buffered chunks first. It returns io.EOF when the child stream ends.
func (s *nestedAggregatorStream) Read() (domain.AggregatorChunk, error) {
	if len(s.buffered) > 0 {
		c := s.buffered[0]
		s.buffered = s.buffered[1:]
		return c, nil
	}
	select {
	case r, ok := <-s.recv:
		if !ok {
			return domain.AggregatorChunk{}, io.EOF
		}
		if errors.Is(r.Err, io.EOF) {
			return domain.AggregatorChunk{}, io.EOF
		}
		if r.Err != nil {
			return domain.AggregatorChunk{}, r.Err
		}
		c, err := decodeAggregatorChunk(r.Value)
		if err != nil {
			return domain.AggregatorChunk{}, err
		}
		return c, nil
	case <-s.ctx.Done():
		return domain.AggregatorChunk{}, io.EOF
	}
}

// decodeAggregatorChunk projects a child-dispatch value into a typed chunk,
// tolerating the transport shapes decodeResolveResp handles.
func decodeAggregatorChunk(v any) (domain.AggregatorChunk, error) {
	switch x := v.(type) {
	case domain.AggregatorChunk:
		return x, nil
	case *domain.AggregatorChunk:
		if x != nil {
			return *x, nil
		}
		return domain.AggregatorChunk{}, nil
	case []byte:
		if len(x) == 0 {
			return domain.AggregatorChunk{}, nil
		}
		var c domain.AggregatorChunk
		if err := json.Unmarshal(x, &c); err != nil {
			return domain.AggregatorChunk{}, fmt.Errorf("decode chunk bytes: %w", err)
		}
		return c, nil
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.AggregatorChunk{}, fmt.Errorf("re-marshal %T: %w", v, err)
		}
		var c domain.AggregatorChunk
		if err := json.Unmarshal(body, &c); err != nil {
			return domain.AggregatorChunk{}, fmt.Errorf("decode %T: %w", v, err)
		}
		return c, nil
	}
}

// resolveChildAggregatorRef resolves an aggregator pool entry's config ID to a
// live child actor ref. It queries aimanager.aggregator_list (TTL-cached —
// see childRefCache) and maps the config ID to the spawned aggregator's
// ActorID, mirroring the agent-side aggregator discovery pattern.
func (a *Actor) resolveChildAggregatorRef(childID string) (ref.Ref, error) {
	notFound := fmt.Errorf("aggregator ref %q: not found", childID)
	if a.actorCtx == nil {
		return nil, fmt.Errorf("aggregator ref %q: actor context unavailable", childID)
	}
	if a.aimanagerRef == nil {
		return nil, fmt.Errorf("aggregator ref %q: aimanager reference unavailable", childID)
	}
	now := time.Now()
	a.childRefMu.Lock()
	if a.childRefCache == nil {
		a.childRefCache = make(map[string]childRefEntry)
	} else if e, ok := a.childRefCache[childID]; ok {
		ttl := childRefTTL
		if e.ref == nil {
			ttl = childRefNegativeTTL
		}
		if now.Sub(e.at) < ttl {
			a.childRefMu.Unlock()
			if e.ref == nil {
				return nil, notFound
			}
			return e.ref, nil
		}
		delete(a.childRefCache, childID)
	}
	a.childRefMu.Unlock()
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, domain.DefaultInvokeTimeout)
	defer cancel()
	call := a.aimanagerRef.Invoke(callCtx, "aimanager.aggregator_list", nil)
	result, err := call.Final(callCtx)
	_ = call.Close()
	if err != nil {
		return nil, fmt.Errorf("resolve aggregator ref %q: list: %w", childID, err)
	}
	var resp domain.AggregatorDescriptorListResp
	switch v := result.(type) {
	case domain.AggregatorDescriptorListResp:
		resp = v
	default:
		body, merr := json.Marshal(result)
		if merr != nil {
			return nil, fmt.Errorf("resolve aggregator ref %q: marshal list: %w", childID, merr)
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("resolve aggregator ref %q: decode list: %w", childID, err)
		}
	}
	var resolved ref.Ref
	for _, d := range resp.Items {
		if d.ID != childID || d.ActorID == "" {
			continue
		}
		cid, perr := identity.ParseCanonicalID(d.ActorID)
		if perr != nil {
			return nil, fmt.Errorf("resolve aggregator ref %q: malformed actor id: %w", childID, perr)
		}
		if r, ok := a.actorCtx.LookupID(id.From(cid)); ok {
			resolved = r
			break
		}
	}
	a.childRefMu.Lock()
	a.childRefCache[childID] = childRefEntry{ref: resolved, at: now}
	a.childRefMu.Unlock()
	if resolved == nil {
		return nil, notFound
	}
	return resolved, nil
}

// tryDispatchNestedAggregator dispatches to a child aggregator and waits for
// the stream to open (first ResolvedUnit chunk). On success it returns a
// nested relay wrapper whose buffered chunks include the child's
// ResolvedUnit. The wrapper is the caller's only handle on the child: its
// release func stops and destroys the plan node.
//
// The parent's req.Unit (when set) is forwarded to the child so the child can
// try the pinned unit first — soft-pin semantics. When req.Unit is nil (auto
// pick), the child selects on its own. If the child's pinned unit is
// unavailable, the child falls back to its own pool; if the child's whole pool
// is exhausted, the parent rotates to its next pool entry.
// nestedAggregatorOpenTimeout is the dedicated bound for a nested child's
// stream-open wait (domain.NestedAggregatorOpenTimeout); a package var so
// tests can shrink it.
var nestedAggregatorOpenTimeout = domain.NestedAggregatorOpenTimeout

func (a *Actor) tryDispatchNestedAggregator(ctx actor.PureContext, parentCtx context.Context, unit CallableUnit, req domain.SendSessionMessageReq) (*nestedAggregatorStream, func(), error) {
	// Runtime cycle guard: never route to our own aggregator id (config id —
	// a.id is an unrelated actor ULID in production).
	if unit.AggregatorID == a.aggHealthKey() {
		return nil, nil, fmt.Errorf("aiaggregator: refusing recursive dispatch to self (%q)", unit.AggregatorID)
	}
	childRef, err := a.resolveChildAggregatorRef(unit.AggregatorID)
	if err != nil {
		return nil, nil, err
	}
	planner := ctx.Planner()
	if planner == nil {
		return nil, nil, fmt.Errorf("aiaggregator: planner capability unavailable")
	}

	childReq := req
	node, err := planner.Plan(childRef, "aiaggregator.dispatch", childReq)
	if err != nil {
		return nil, nil, fmt.Errorf("nested dispatch to %q: plan: %w", unit.AggregatorID, err)
	}

	// The child ctx derives from the PARENT dispatch ctx (not lifecycleCtx) so
	// the parent's idle-timeout cancelAll and caller cancellation propagate
	// into the nested dispatch — otherwise a stuck child holds the parent
	// blocked on the first-chunk wait until the child's own idle timer fires.
	streamCtx, cancel := context.WithCancel(parentCtx)
	nested := &nestedAggregatorStream{
		node:   node,
		ctx:    streamCtx,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	release := func() { nested.release(a) }
	fail := func(err error) (*nestedAggregatorStream, func(), error) {
		release()
		return nil, nil, err
	}

	if err := node.Start(streamCtx); err != nil {
		return fail(fmt.Errorf("nested dispatch to %q: start: %w", unit.AggregatorID, err))
	}
	nested.recv = plan.RecvChan(streamCtx, node)

	// Bound the open phase with a dedicated deadline, well under the parent's
	// StreamIdleTimeout budget: a child whose whole pool is unreachable
	// rotates unit-by-unit inside this single wait, and each doomed unit can
	// burn dial+TLS+header timeouts. On expiry the child is registered
	// short-term unavailable and the parent rotates to its own next
	// candidate instead of holding the dispatch hostage.
	openCtx, openCancel := context.WithTimeout(streamCtx, nestedAggregatorOpenTimeout)
	defer openCancel()

	// Stream-open boundary: the child has opened its own stream only once a
	// ResolvedUnit chunk arrives. Everything buffered before that point may
	// be chunk data (usage/prologue) or a terminal error. Only a ResolvedUnit
	// counts as open — a mid-stream error before it is an open failure.
	var first domain.AggregatorChunk
	sawFirst := false
	for {
		// Guard the first-chunk wait with the child ctx (derived from the
		// parent dispatch ctx): without this, a child that neither opens nor
		// fails blocks the parent goroutine indefinitely — its cleanup defer
		// and gate release never run.
		var r plan.RecvResult
		var ok bool
		select {
		case r, ok = <-nested.recv:
		case <-openCtx.Done():
			if errors.Is(openCtx.Err(), context.DeadlineExceeded) {
				// The child is not provably dead (could be a slow rotation),
				// so only register for the default TTL: bounded skip, quick
				// re-probe once the TTL lapses.
				llmclient.RegisterAggregatorUnavailableUntil(unit.AggregatorID, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
			}
			return fail(fmt.Errorf("nested dispatch to %q: child stream open wait: %w", unit.AggregatorID, openCtx.Err()))
		}
		if !ok {
			return fail(fmt.Errorf("nested dispatch to %q: child stream closed before open", unit.AggregatorID))
		}
		if errors.Is(r.Err, io.EOF) {
			return fail(fmt.Errorf("nested dispatch to %q: child stream ended before open", unit.AggregatorID))
		}
		if r.Err != nil {
			return fail(fmt.Errorf("nested dispatch to %q: child exhausted: %w", unit.AggregatorID, r.Err))
		}
		c, derr := decodeAggregatorChunk(r.Value)
		if derr != nil {
			return fail(fmt.Errorf("nested dispatch to %q: decode first chunk: %w", unit.AggregatorID, derr))
		}
		if c.Kind == domain.AggregatorChunkResolvedUnit {
			found := c
			first = found
			sawFirst = true
			break
		}
		// Chunk data arriving before the ResolvedUnit: not yet open.
		nested.buffered = append(nested.buffered, c)
	}
	if !sawFirst {
		return fail(fmt.Errorf("nested dispatch to %q: child stream opened without resolved_unit", unit.AggregatorID))
	}
	firstChunk := first
	nested.buffered = append([]domain.AggregatorChunk{firstChunk}, nested.buffered...)
	return nested, release, nil
}

// setDispatchActivity records the in-flight dispatch activity for a unit ID.
// depth 0 = this aggregator's own dispatch; propagated-from-child activities
// use depth > 0 (set by handleStatus, not here). For aggregator-ref entries,
// aggID is the child aggregator's config ID; for concrete units it is empty.
func (a *Actor) setDispatchActivity(unitID, state string, req domain.SendSessionMessageReq, depth int32, aggID string) {
	if unitID == "" {
		return
	}
	a.dispatchActivityMu.Lock()
	defer a.dispatchActivityMu.Unlock()
	if a.dispatchActivity == nil {
		a.dispatchActivity = make(map[string]domain.DispatchActivity)
	}
	a.dispatchActivity[unitID] = domain.DispatchActivity{
		State:        state,
		SessionID:    req.SessionID,
		AgentID:      req.AgentID,
		SlotKind:     req.SlotKind,
		StartedAt:    time.Now().Unix(),
		Depth:        depth,
		AggregatorID: aggID,
	}
}

// clearDispatchActivity removes the in-flight dispatch activity for a unit ID.
func (a *Actor) clearDispatchActivity(unitID string) {
	if unitID == "" {
		return
	}
	a.dispatchActivityMu.Lock()
	defer a.dispatchActivityMu.Unlock()
	delete(a.dispatchActivity, unitID)
}

// snapshotDispatchActivity returns a copy of the current in-flight dispatch
// activity for a unit ID, or nil when the unit is idle.
func (a *Actor) snapshotDispatchActivity(unitID string) *domain.DispatchActivity {
	a.dispatchActivityMu.Lock()
	defer a.dispatchActivityMu.Unlock()
	act, ok := a.dispatchActivity[unitID]
	if !ok {
		return nil
	}
	cp := act
	return &cp
}

// snapshotAllDispatchActivities returns a copy of the entire in-flight activity
// map. The caller may iterate it without holding the lock.
func (a *Actor) snapshotAllDispatchActivities() map[string]domain.DispatchActivity {
	a.dispatchActivityMu.Lock()
	defer a.dispatchActivityMu.Unlock()
	out := make(map[string]domain.DispatchActivity, len(a.dispatchActivity))
	for k, v := range a.dispatchActivity {
		out[k] = v
	}
	return out
}

// queryChildDispatchActivity invokes aiaggregator.status on the child
// aggregator referenced by the pool entry and returns the deepest active
// (non-idle) dispatch activity found among its units, or nil when the child
// cannot be reached or has no active dispatch. This is the "child state
// propagates upward" path: handleStatus calls it for each aggregator-ref entry
// and projects the returned activity onto the parent's view of that entry.
//
// The query is bounded by childStatusTimeout; a slow or unreachable child
// yields nil (the parent falls back to its own activity record for the entry).
func (a *Actor) queryChildDispatchActivity(childID string) *domain.DispatchActivity {
	childRef, err := a.resolveChildAggregatorRef(childID)
	if err != nil {
		return nil
	}
	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, childStatusTimeout)
	defer cancel()
	call := childRef.Invoke(callCtx, "aiaggregator.status", nil)
	result, err := call.Final(callCtx)
	_ = call.Close()
	if err != nil || result == nil {
		return nil
	}
	var status domain.AIAggregatorStatusResp
	switch v := result.(type) {
	case domain.AIAggregatorStatusResp:
		status = v
	case *domain.AIAggregatorStatusResp:
		if v == nil {
			return nil
		}
		status = *v
	default:
		body, merr := json.Marshal(result)
		if merr != nil {
			return nil
		}
		if err := json.Unmarshal(body, &status); err != nil {
			return nil
		}
	}
	// Pick the deepest active (non-idle) dispatch activity among the child's
	// units. When several children of the child are active, the deepest
	// (max Depth) carries the most specific in-flight information.
	var deepest *domain.DispatchActivity
	for i := range status.Units {
		act := status.Units[i].DispatchActivity
		if act == nil || act.State == "" {
			continue
		}
		if deepest == nil || act.Depth > deepest.Depth {
			cp := *act
			deepest = &cp
		}
	}
	return deepest
}

// aggregatedDispatchActivity returns the DispatchActivity to surface on the
// parent's view of a pool entry. For aggregator-ref entries it queries the
// child aggregator and propagates its deepest active activity upward (Depth
// increments, AggregatorID names the child source); the parent's own activity
// record for the entry is used as a fallback when the child is unreachable or
// has no active dispatch (e.g. during the brief trying window before the child
// records its own dispatch). For concrete units it returns the parent's own
// record directly.
func (a *Actor) aggregatedDispatchActivity(u CallableUnit) *domain.DispatchActivity {
	if u.AggregatorID != "" {
		if child := a.queryChildDispatchActivity(u.AggregatorID); child != nil {
			out := *child
			out.Depth = child.Depth + 1
			out.AggregatorID = u.AggregatorID
			return &out
		}
		// Fall back to the parent's own transient record for the aggregator
		// ref (e.g. "trying" while the child stream has not yet opened).
		return a.snapshotDispatchActivity(u.ID)
	}
	return a.snapshotDispatchActivity(u.ID)
}

// unitFailureSuffix renders the serving unit identity appended to dispatch
// failure errors. Callers only see the requested unit — under aggregator/auto
// routing the serving unit may differ per attempt — so every upstream failure
// must name the concrete provider/model/endpoint that actually failed; a bare
// "stream idle timeout" leaves retry diagnostics unattributable. Nested
// aggregator pool entries have no endpoint of their own; the child's own error
// carries the deep unit, so only the aggregator id is appended there.
func unitFailureSuffix(u *CallableUnit) string {
	if u == nil {
		return ""
	}
	if u.AggregatorID != "" {
		return " [aggregator=" + u.AggregatorID + "]"
	}
	return fmt.Sprintf(" [provider=%s model=%s endpoint=%s]", u.ProviderName, u.Model, u.Endpoint)
}

// handleDispatch streams provider chunks to the caller. It selects a
// callable unit, opens a streaming HTTP request, and forwards each
// llmclient.Event as a domain.AggregatorChunk via emit.Send. A pool entry
// carrying an AggregatorID is routed to the child aggregator instead, whose
// chunks are relayed verbatim (deep unit, deep text, deep usage).
func (a *Actor) handleDispatch(ctx actor.PureContext, req domain.SendSessionMessageReq, emit actor.Emitter) error {
	a.ensureConfigLoaded()

	// Aggregator-level disabled is an operator policy: fail fast with a clear
	// error, no fallback, no silent degradation. Register unavailable so
	// parents skip this aggregator instead of re-probing every dispatch; the
	// TTL expiry allows one re-probe per window, and a re-enabled config
	// recovers on the next successful selection.
	a.mu.RLock()
	if a.disabled {
		a.mu.RUnlock()
		if key := a.aggHealthKey(); key != "" && key != systemAggregatorID {
			llmclient.RegisterAggregatorUnavailableUntil(key, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
		}
		return fmt.Errorf("aiaggregator.dispatch: aggregator %q is disabled", a.name)
	}
	a.mu.RUnlock()

	requestUnit := derefModelUnit(req.Unit)
	selReq := SelectRequest{
		AgentID:  req.AgentID,
		SlotKind: req.SlotKind,
		Unit:     requestUnit,
	}

	// A pinned unit (both model and provider specified) only orders the FIRST
	// attempt. It never disables failover: once dispatch goes through an
	// aggregator, any failover-eligible stream-open failure rotates to another
	// unit in the same pool. EXCEPT when the caller set UnitPinned — a hard
	// user selection from a unit-locked slot must surface the failure instead
	// of silently switching models.
	unitPinned := req.UnitPinned && requestUnit.Model != "" && requestUnit.Provider != ""

	selReq.UnitPinned = unitPinned
	unit, err := a.selectUnit(selReq)
	if err != nil {
		// For non-pinned specific-unit requests, a selection failure means the
		// requested unit is already unavailable (cooling down, disabled, or token
		// plan exhausted). Fall back to auto-selection within the same pool
		// instead of returning "no callable unit" to the caller. The stream-open
		// failover loop below will still rotate if the auto-selected unit fails.
		if !unitPinned && (requestUnit.Model != "" || requestUnit.Provider != "") {
			ctx.Logger().Warn("aiaggregator: requested unit unavailable, falling back to pool auto-selection",
				"error", err.Error(), "unit", requestUnit)
			autoReq := selReq
			autoReq.Unit = domain.ModelUnit{}
			unit, err = a.selectUnit(autoReq)
		}
		if err != nil {
			return fmt.Errorf("aiaggregator.dispatch: %w", err)
		}
	}

	// Fire-and-forget the provider assignment to aimanager so all aggregators
	// can load-balance new/fallback picks. Mirrors the provider_report_failure
	// fire-and-forget pattern. Never emitted for aggregator refs — the child
	// aggregator report its own deep provider.
	if unit.AggregatorID == "" {
		a.reportProviderAssign(selReq.AgentID, selReq.SlotKind, unit.ProviderName)
	}

	failoverReq := selReq
	failoverReq.Unit = domain.ModelUnit{}
	// Rotation re-selects from the whole pool: never honor the pin's
	// cooling bypass for failover candidates.
	failoverReq.UnitPinned = false

	streamCtx, cancelAll := context.WithCancel(a.lifecycleCtx)
	defer cancelAll()

	// When plan_submit or goal_submit is among the requested tools the LLM is
	// expected to generate a plan or goal interpretation before calling the
	// tool. Deep reasoning can run well past the normal 3-minute idle budget,
	// so use the extended timeout to avoid killing the stream prematurely.
	idleTimeout := domain.StreamIdleTimeout
	if hasInteractionSubmitTool(req.Tools) {
		idleTimeout = domain.PlanGoalStreamIdleTimeout
	}

	resetCh := make(chan struct{}, 1)
	idleTimeoutCh := make(chan struct{}, 1)
	go func() {
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()
		for {
			select {
			case <-streamCtx.Done():
				return
			case <-resetCh:
				// Go 1.23+ timer channels are unbuffered: a false Stop()
				// does not guarantee a pending value, so blocking on
				// <-timer.C here deadlocks the watchdog forever (observed
				// 2026-08-28). Stop+Reset suffices — stale sends are
				// dropped under the new semantics.
				timer.Stop()
				// First stream event arrived; the initial idleTimeout covered
				// the first-token wait. Shrink the window for subsequent chunks.
				timer.Reset(domain.StreamChunkIdleTimeout)
			case <-timer.C:
				select {
				case idleTimeoutCh <- struct{}{}:
				default:
				}
				cancelAll()
				return
			case <-emit.Done():
				cancelAll()
				return
			case <-ctx.Done():
				cancelAll()
				return
			}
		}
	}()

	// Stream-open candidate loop. Try the selected unit; if the stream fails to
	// open (before any chunk is emitted) with a failover-eligible error, mark
	// the unit unavailable for this selection and attempt the next healthy
	// candidate in the same pool. Once the stream opens, no further switching.
	// An aggregator-ref entry delegates the whole attempt to the child
	// aggregator: a child open failure (child pool exhausted) counts as THIS
	// entry's open failure and rotates to the parent's next candidate; any
	// child success commits the parent.
	var source *dispatchSource
	var release func()
	triedIDs := make(map[string]bool)
	stopRetry := &stopRetryBudget{}
	// strippedRetryIDs guards the image-strip retry per unit: a 400 "model
	// does not support image input" marks the unit text-only and retries it
	// once — the retry's buildLLMRequest strips image blocks into text
	// placeholders (session history untouched). Each unit gets at most one
	// stripped retry per dispatch; a repeat occurrence falls through to
	// normal stop handling instead of looping.
	strippedRetryIDs := make(map[string]bool)

	// activeUnitID tracks the unit currently dispatched/streamed so a single
	// deferred cleanup clears the right entry on every exit path (success,
	// error, or caller cancellation). Updated at every rotation.
	activeUnitID := unit.ID
	defer func() {
		if activeUnitID != "" {
			a.clearDispatchActivity(activeUnitID)
		}
	}()

	// Record the initial dispatch activity: the selected unit is "trying".
	a.setDispatchActivity(unit.ID, DispatchStateTrying, req, 0, unit.AggregatorID)

	// Idempotent per-attempt cleanup. Registered once so every terminal path
	// releases the committed stream and gate; mid-stream rotation invokes it
	// explicitly first, leaving the deferred call a no-op.
	cleanup := func() {
		if source != nil && source.stream != nil {
			source.stream.Close()
			source.stream = nil
		}
		if release != nil {
			release()
			release = nil
		}
	}
	defer cleanup()

	// attempt wraps the candidate loop and the streaming section so a
	// zero-output mid-stream cut (ErrStreamClosed before any content chunk)
	// can fail over to the next unit exactly like a stream-open failure.
attempt:
	for {
		for { // stream-open candidate loop (see attempt above)
			triedIDs[unit.ID] = true

			// Caller-budget gate: this loop runs off the actor lifecycle
			// context, so a dead caller (outer invoke deadline exhausted)
			// must stop rotation HERE with the last unit error — otherwise
			// the loop keeps grinding dead units long after the caller gave
			// up (observed live: stream-open failures still logging 7s
			// after the reverse llm call's "context deadline exceeded").
			select {
			case <-ctx.Done():
				if err != nil {
					return fmt.Errorf("aiaggregator.dispatch: stream open%s: caller budget exhausted: %w", unitFailureSuffix(unit), err)
				}
				return errors.New("aiaggregator.dispatch: caller budget exhausted before stream open")
			default:
			}

			// If the idle timer already fired during a previous attempt, stop
			// cycling and return the idle-timeout error.
			select {
			case <-idleTimeoutCh:
				idleErr := llmclient.ErrStreamIdleTimeout
				ctx.Logger().Error("aiaggregator: idle timeout before stream open",
					"unit", unit.ID, "provider", unit.ProviderName)
				a.applyStreamOpenFailure(unit, idleErr, llmclient.ClassifyStreamOpenError(idleErr))
				a.submitStatsRecord(ctx, req, unit, llmclient.RequestTelemetry{}, idleErr)
				return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w", unitFailureSuffix(unit), idleErr)
			default:
			}

			if unit.AggregatorID != "" {
				nested, nestedRelease, err := a.tryDispatchNestedAggregator(ctx, streamCtx, *unit, req)
				if err == nil {
					source = &dispatchSource{nested: nested}
					release = nestedRelease
					// The child stream is open: the aggregator-ref entry is
					// committed and streaming; the child's own status carries
					// the deep unit detail, which handleStatus propagates up.
					a.setDispatchActivity(unit.ID, DispatchStateInUse, req, 0, unit.AggregatorID)
					break // child stream opened — commit
				}
				// Child failed to open (its whole pool exhausted). Treat as this
				// entry's open failure and rotate, exactly like a direct unit's
				// open failure.
				select {
				case <-idleTimeoutCh:
					err = llmclient.ErrStreamIdleTimeout
				default:
				}
				ctx.Logger().Error("aiaggregator: nested stream open failed",
					"unit", unit.ID, "aggregator", unit.AggregatorID, "error", truncateErrMessage(err.Error(), 512))
				// The failure itself is the availability signal: whatever made
				// the child unable to open (pool exhausted, all units disabled,
				// config gone, child crashed), skip this ref for the default
				// TTL so the parent stops re-probing it at dispatch rate.
				// Never shortens a longer deadline the child registered itself.
				if errors.Is(err, errPoolExhausted) {
					llmclient.RegisterAggregatorUnavailableUntil(unit.AggregatorID, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
				}
				classification := llmclient.ClassifyStreamOpenError(err)
				// Aggregator refs manage their own health: no cooldown, no
				// failure count, no provider report on the parent entry.
				if unitPinned {
					return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w", unitFailureSuffix(unit), err)
				}
				if !classification.Rotatable && !stopRetry.consume(classification, err) {
					return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w", unitFailureSuffix(unit), err)
				}
				nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
				if selErr != nil {
					// Redundant with the returned errPoolExhausted-wrapped error
					// and the per-unit failure log above; Debug keeps the tried
					// count without re-warning on every exhausted dispatch.
					ctx.Logger().Debug("aiaggregator: no more failover candidates",
						"tried", len(triedIDs), "lastError", err.Error())
					return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w: %w", unitFailureSuffix(unit), err, errPoolExhausted)
				}
				// Rotation: clear the failed aggregator-ref's activity and mark
				// the next candidate as "trying".
				a.clearDispatchActivity(activeUnitID)
				activeUnitID = nextUnit.ID
				a.setDispatchActivity(nextUnit.ID, DispatchStateTrying, req, 0, nextUnit.AggregatorID)
				unit = nextUnit
				continue
			}

			var stream llmclient.Stream
			stream, release, err = a.tryDispatchUnit(ctx, *unit, req, streamCtx)
			if err == nil {
				source = &dispatchSource{stream: stream, release: release}
				// Stream opened: the unit is now actively serving the session.
				a.setDispatchActivity(unit.ID, DispatchStateInUse, req, 0, unit.AggregatorID)
				break // stream opened — proceed to streaming
			}

			// Override with idle timeout if the timer fired during the attempt.
			select {
			case <-idleTimeoutCh:
				err = llmclient.ErrStreamIdleTimeout
			default:
			}

			ctx.Logger().Error("aiaggregator: stream open failed",
				"unit", unit.ID,
				"provider", unit.ProviderName,
				"error", truncateErrMessage(err.Error(), 512))

			classification := llmclient.ClassifyStreamOpenError(err)
			a.applyStreamOpenFailure(unit, err, classification)
			// No stream was opened, so there is no telemetry snapshot; record the
			// failed attempt with a zero snapshot so provider errors (e.g. 429/401/
			// 503/403) are attributable by error code in the dashboard.
			a.submitStatsRecord(ctx, req, unit, llmclient.RequestTelemetry{}, err)

		// Image-input capability: the model rejected the request because it
		// cannot accept image blocks — a per-unit capability, not a health
		// failure. Mark the unit and retry it once with image blocks
		// rewritten: recognition text when a vision model is available
		// (media-configured binding first), the omitted-placeholder
		// otherwise (history untouched). This runs before the UnitPinned
		// early return so a locked unit also recovers instead of surfacing
		// a doomed 400.
		if llmclient.IsImageInputUnsupportedErr(err) {
			a.markNoImageInput(*unit)
			if requestHasImageBlocks(req) && !strippedRetryIDs[unit.ID] {
				strippedRetryIDs[unit.ID] = true
				substituted := false
				req, substituted = a.substituteImagesForTextOnlyRetry(ctx, req)
				if substituted {
					ctx.Logger().Info("aiaggregator: model rejected image input, retrying unit with recognized image text",
						"unit", unit.ID, "provider", unit.ProviderName, "model", unit.Model)
				} else {
					ctx.Logger().Info("aiaggregator: model rejected image input, retrying unit with images stripped",
						"unit", unit.ID, "provider", unit.ProviderName, "model", unit.Model)
				}
				continue // same unit; buildLLMRequest applies the marker
			}
		}

			// UnitPinned requests fail loudly: the hard user selection must
			// surface the failure rather than rotate to another pool unit.
			// Stop-class errors (400/404 configuration failures, cancellation)
			// also return the exact failure — except via stopRetry: the first
			// stop-class failure records its status and rotates; a repeat of
			// that status terminates, a different stop code rotates again.
			if unitPinned {
				return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w", unitFailureSuffix(unit), err)
			}
			if !classification.Rotatable && !stopRetry.consume(classification, err) {
				return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w", unitFailureSuffix(unit), err)
			}

			// Failover: select the next healthy candidate from the whole pool,
			// excluding units that already failed in this dispatch. The pin is
			// cleared (failoverReq) so rotation is not confined to the pinned
			// (model, provider) pair.
			nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
			if selErr != nil {
				ctx.Logger().Debug("aiaggregator: no more failover candidates",
					"tried", len(triedIDs), "lastError", err.Error())
				return fmt.Errorf("aiaggregator.dispatch: stream open%s: %w: %w", unitFailureSuffix(unit), err, errPoolExhausted)
			}
			// Rotation: clear the failed unit's activity and mark the next
			// candidate as "trying".
			a.clearDispatchActivity(activeUnitID)
			activeUnitID = nextUnit.ID
			a.setDispatchActivity(nextUnit.ID, DispatchStateTrying, req, 0, nextUnit.AggregatorID)
			unit = nextUnit
		}

		if source.nested != nil {
			// Nested dispatch: relay the child's chunks verbatim. The child's
			// ResolvedUnit (deep unit) came first from its stream and is buffered
			// in the wrapper — no parent ResolvedUnit is emitted.
			nested := source.nested
			// contentEmitted mirrors the direct path: whether any output frame
			// (text/reasoning/tool) has been relayed to the caller. Usage and
			// stop frames are excluded as last-wins metadata.
			contentEmitted := false
			for {
				chunk, rerr := nested.Read()
				if rerr != nil && !errors.Is(rerr, io.EOF) {
					// Zero-relay child cut: nothing has reached the caller, so
					// rotate to the parent pool's next candidate exactly like a
					// stream-open failure instead of aborting the dispatch.
					// Once content was relayed, rotation would duplicate output,
					// so the error stands. No parent-side cooldown feedback:
					// aggregator refs manage their own health, and the child has
					// already cooled the deep unit that cut.
					classification := llmclient.ClassifyStreamOpenError(rerr)
					if !contentEmitted && !unitPinned &&
						(classification.Rotatable || stopRetry.consume(classification, rerr)) {
						// Child-side exhaustion surfaced as a cut: same
						// failure-is-signal rule as the open-failure branch —
						// skip the exhausted child for the default TTL.
						if errors.Is(rerr, errPoolExhausted) {
							llmclient.RegisterAggregatorUnavailableUntil(unit.AggregatorID, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
						}
						ctx.Logger().Warn("aiaggregator: zero-relay nested stream cut, rotating",
							"unit", unit.ID,
							"aggregator", unit.AggregatorID,
							"childError", rerr.Error())
						cleanup()
						nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
						if selErr != nil {
						ctx.Logger().Debug("aiaggregator: no more failover candidates after nested cut",
							"tried", len(triedIDs), "lastError", rerr.Error())
						return fmt.Errorf("aiaggregator.dispatch: nested stream%s: %w: %w", unitFailureSuffix(unit), rerr, errPoolExhausted)
						}
						a.clearDispatchActivity(activeUnitID)
						activeUnitID = nextUnit.ID
						a.setDispatchActivity(nextUnit.ID, DispatchStateTrying, req, 0, nextUnit.AggregatorID)
						unit = nextUnit
						continue attempt
					}
				ctx.Logger().Error("aiaggregator: nested stream error", "error", truncateErrMessage(rerr.Error(), 512))
				return fmt.Errorf("aiaggregator.dispatch: nested stream%s: %w", unitFailureSuffix(unit), rerr)
				}
				if errors.Is(rerr, io.EOF) {
					break
				}
				switch chunk.Kind {
				case domain.AggregatorChunkText, domain.AggregatorChunkReasoning,
					domain.AggregatorChunkToolUseStart, domain.AggregatorChunkToolUseInputDelta,
					domain.AggregatorChunkToolUseComplete:
					contentEmitted = true
				}
				// Every relayed chunk resets the idle timer, matching the direct
				// path. The ResolvedUnit chunk resets it too — for a nested
				// dispatch the child's first chunk is the deep open marker and
				// the gap after it is a normal first-token wait.
				select {
				case resetCh <- struct{}{}:
				default:
				}
				if err := emit.Send(chunk); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit nested chunk: %w", err)
				}
			}
			select {
			case <-idleTimeoutCh:
				return fmt.Errorf("aiaggregator.dispatch: nested stream%s: %w", unitFailureSuffix(unit), llmclient.ErrStreamIdleTimeout)
			default:
			}
			return nil
		}

		if source.stream == nil {
			return fmt.Errorf("aiaggregator.dispatch: no stream committed%s", unitFailureSuffix(unit))
		}
		stream := source.stream

		// Report the concrete unit the aggregator selected for this dispatch so
		// the caller (turn engine) can surface the real executing model under
		// aggregator/auto routing. Emitted before content; ignored by callers
		// that don't consume it.
		if err := emit.Send(domain.AggregatorChunk{
			Kind: domain.AggregatorChunkResolvedUnit,
			ResolvedUnit: &domain.ModelUnit{
				Model:    unit.Model,
				Provider: unit.ProviderName,
			},
		}); err != nil {
			ctx.Logger().Warn("aiaggregator: failed to emit resolved_unit chunk", "error", err)
		}

		completed := false
		// contentEmitted tracks whether any output frame (text/reasoning/tool)
		// has been relayed to the caller. Usage frames are excluded: they are
		// last-wins metadata and Anthropic sends one before any content.
		contentEmitted := false
		for ev := range stream.Events() {
			select {
			case resetCh <- struct{}{}:
			default:
			}
			switch ev.Kind {
			case llmclient.EventTextDelta:
				contentEmitted = true
				if err := emit.Send(domain.AggregatorChunk{
					Kind: domain.AggregatorChunkText,
					Text: ev.Text,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit text: %w", err)
				}
			case llmclient.EventReasoningDelta:
				contentEmitted = true
				if err := emit.Send(domain.AggregatorChunk{
					Kind: domain.AggregatorChunkReasoning,
					Text: ev.Text,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit reasoning: %w", err)
				}
			case llmclient.EventUsage:
				if ev.Usage == nil {
					continue
				}
				if err := emit.Send(domain.AggregatorChunk{
					Kind: domain.AggregatorChunkUsage,
					Usage: &domain.UsageData{
						InputTokens:              int64(ev.Usage.InputTokens),
						OutputTokens:             int64(ev.Usage.OutputTokens),
						TotalTokens:              int64(ev.Usage.TotalTokens),
						CacheCreationInputTokens: int64(ev.Usage.CacheCreationInputTokens),
						CacheReadInputTokens:     int64(ev.Usage.CacheReadInputTokens),
						ReasoningTokens:          int64(ev.Usage.ReasoningTokens),
						MaxContextLength:         unit.MaxContextLength,
					},
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit usage: %w", err)
				}
			case llmclient.EventToolUseStart:
				contentEmitted = true
				if err := emit.Send(domain.AggregatorChunk{
					Kind:      domain.AggregatorChunkToolUseStart,
					ToolUseID: ev.ToolUseID,
					ToolName:  ev.ToolName,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit tool_use_start: %w", err)
				}
			case llmclient.EventToolUseInputDelta:
				contentEmitted = true
				if err := emit.Send(domain.AggregatorChunk{
					Kind:       domain.AggregatorChunkToolUseInputDelta,
					ToolUseID:  ev.ToolUseID,
					InputDelta: ev.InputDelta,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit tool_use_input_delta: %w", err)
				}
			case llmclient.EventToolUseComplete:
				contentEmitted = true
				if err := emit.Send(domain.AggregatorChunk{
					Kind:      domain.AggregatorChunkToolUseComplete,
					ToolUseID: ev.ToolUseID,
					ToolName:  ev.ToolName,
					Input:     ev.Input,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit tool_use_complete: %w", err)
				}
			case llmclient.EventStop:
				if err := emit.Send(domain.AggregatorChunk{
					Kind:       domain.AggregatorChunkStop,
					StopReason: ev.StopReason,
				}); err != nil {
					return fmt.Errorf("aiaggregator.dispatch: emit stop: %w", err)
				}
				completed = true
			case llmclient.EventError:
				select {
				case <-idleTimeoutCh:
					a.applyStreamOpenFailure(unit, llmclient.ErrStreamIdleTimeout, llmclient.ClassifyStreamOpenError(llmclient.ErrStreamIdleTimeout))
					a.submitStatsRecord(ctx, req, unit, stream.Telemetry(), llmclient.ErrStreamIdleTimeout)
					return fmt.Errorf("aiaggregator.dispatch: stream%s: %w", unitFailureSuffix(unit), llmclient.ErrStreamIdleTimeout)
				default:
				}
				// Zero-output mid-stream cut (ErrStreamClosed): the unit opened
				// the stream but dropped it before any content chunk was
				// relayed. Nothing has reached the caller, so fail over to the
				// next healthy unit exactly like a stream-open failure instead
				// of aborting the turn. Once content was emitted, rotation
				// would duplicate output, so the error stands.
				if errors.Is(ev.Err, llmclient.ErrStreamClosed) && !contentEmitted && !unitPinned {
					ctx.Logger().Warn("aiaggregator: zero-output stream cut, rotating",
						"unit", unit.ID,
						"provider", unit.ProviderName)
					a.applyStreamOpenFailure(unit, ev.Err, llmclient.ClassifyStreamOpenError(ev.Err))
					a.submitStatsRecord(ctx, req, unit, stream.Telemetry(), ev.Err)
					cleanup()
					nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
					if selErr != nil {
					ctx.Logger().Debug("aiaggregator: no more failover candidates after stream cut",
						"tried", len(triedIDs), "lastError", ev.Err.Error())
					return fmt.Errorf("aiaggregator.dispatch: stream%s: %w: %w", unitFailureSuffix(unit), ev.Err, errPoolExhausted)
					}
					a.clearDispatchActivity(activeUnitID)
					activeUnitID = nextUnit.ID
					a.setDispatchActivity(nextUnit.ID, DispatchStateTrying, req, 0, nextUnit.AggregatorID)
					unit = nextUnit
					continue attempt
				}
			ctx.Logger().Error("aiaggregator: stream error event", "error", ev.Err)
			a.applyStreamOpenFailure(unit, ev.Err, llmclient.ClassifyStreamOpenError(ev.Err))
			a.submitStatsRecord(ctx, req, unit, stream.Telemetry(), ev.Err)
			return fmt.Errorf("aiaggregator.dispatch: stream%s: %w", unitFailureSuffix(unit), ev.Err)
			case llmclient.EventDone:
				// terminal frame; loop will exit when the channel closes
				completed = true
			}
		}

		select {
		case <-idleTimeoutCh:
			a.applyStreamOpenFailure(unit, llmclient.ErrStreamIdleTimeout, llmclient.ClassifyStreamOpenError(llmclient.ErrStreamIdleTimeout))
			a.submitStatsRecord(ctx, req, unit, stream.Telemetry(), llmclient.ErrStreamIdleTimeout)
			return fmt.Errorf("aiaggregator.dispatch: stream%s: %w", unitFailureSuffix(unit), llmclient.ErrStreamIdleTimeout)
		default:
		}
		a.reportProviderSuccess(unit.ProviderName)
		// Only record a success stat when the stream completed naturally (stop/
		// done received). If the caller cancelled mid-stream — e.g. the turn
		// engine's own idle timeout firing first — the aggregator cannot tell that
		// apart from a benign pause, so it skips the record and lets the turn
		// engine own the failed-attempt stat.
		if completed {
			llmclient.RecordSuccess(unit.ProviderName, unit.Model)
			a.submitStatsRecord(ctx, req, unit, stream.Telemetry(), nil)
		}
		return nil
	}
}

// resolveOracleRef discovers the global oracle system actor via LookupService
// and caches the result so failure diagnostics do one discovery call per live
// actor generation; a restarted oracle is re-resolved instead of served from
// a stale ref.
func (a *Actor) resolveOracleRef() (ref.Ref, error) {
	if a.actorCtx == nil {
		// Actors constructed outside OnStart (tests) have no context to look
		// services up in; diagnostics are best-effort and silently skipped.
		return nil, fmt.Errorf("aiaggregator: actor context unavailable")
	}

	a.oracleMu.Lock()
	defer a.oracleMu.Unlock()

	if a.oracleRef != nil {
		if _, live := a.actorCtx.LookupID(a.oracleRef.ID()); live {
			return a.oracleRef, nil
		}
		a.oracleRef = nil
	}

	r, ok := a.actorCtx.LookupService("oracle")
	if !ok {
		return nil, fmt.Errorf("aiaggregator: global oracle actor not available")
	}
	a.oracleRef = r
	return r, nil
}

// truncateErrMessage truncates a long error message to a fixed size so that
// nested error wrapping chains do not inflate the stats record or the log.
// It mirrors the openai client respSnippet style.
func truncateErrMessage(s string, max int) string {
	if max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// submitStatsRecord sends the final request telemetry to the workspace aistats
// actor. It is fire-and-forget: aistats failures are logged but do not fail the
// dispatch return. Telemetry is read from the closed stream so it is complete.
// dispatchErr is the provider error that terminated the stream (nil on success);
// when non-nil the record is still submitted so failed requests appear in the
// model-unit performance dashboard, with ErrorCode/ErrorMessage derived from it.
// telemetry is the stream's captured snapshot; for stream-open failures (no
// stream exists yet) callers pass a zero-value RequestTelemetry.
func (a *Actor) submitStatsRecord(ctx actor.PureContext, req domain.SendSessionMessageReq, unit *CallableUnit, telemetry llmclient.RequestTelemetry, dispatchErr error) {
	if req.WorkspaceID == "" {
		return
	}
	record := a.buildStatsRecord(req, unit, telemetry, dispatchErr)
	if err := ctx.EmitEvent("aistats.record", record); err != nil {
		slog.Debug("aiaggregator: emit stats record event failed", "error", err, "id", record.ID)
	}
}

func (a *Actor) buildStatsRecord(req domain.SendSessionMessageReq, unit *CallableUnit, telemetry llmclient.RequestTelemetry, dispatchErr error) domain.AIStatsRecord {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	record := domain.AIStatsRecord{
		ID:           telemetry.RequestID,
		WorkspaceID:  req.WorkspaceID,
		ProjectID:    req.ProjectID,
		AgentID:      req.AgentID,
		SessionID:    req.SessionID,
		TurnID:       req.TurnID,
		RequestID:    telemetry.RequestID,
		Provider:     unit.ProviderName,
		Model:        unit.Model,
		StopReason:   telemetry.StopReason,
		ErrorMessage: telemetry.ErrorMessage,
		LatencyMs:    telemetry.LatencyMs,
		FirstTokenMs: telemetry.FirstTokenMs,
		StartedAt:    now,
		CompletedAt:  now,
	}
	if telemetry.ResponseModel != "" {
		record.ResponseModel = telemetry.ResponseModel
	}
	if telemetry.ResponseID != "" {
		record.ResponseID = telemetry.ResponseID
	}
	if telemetry.ClientRequestID != "" {
		record.ClientRequestID = telemetry.ClientRequestID
	}
	if telemetry.RequestID == "" {
		record.ID = uuid.NewString()
		record.RequestID = record.ID
	}
	if telemetry.Usage != nil {
		record.Usage = &domain.UsageData{
			InputTokens:              int64(telemetry.Usage.InputTokens),
			OutputTokens:             int64(telemetry.Usage.OutputTokens),
			TotalTokens:              int64(telemetry.Usage.TotalTokens),
			CacheCreationInputTokens: int64(telemetry.Usage.CacheCreationInputTokens),
			CacheReadInputTokens:     int64(telemetry.Usage.CacheReadInputTokens),
			ReasoningTokens:          int64(telemetry.Usage.ReasoningTokens),
		}
	}
	// Provider streams do not currently call telemetry.setError, so on a failed
	// dispatch the telemetry error fields are empty. Derive them from the
	// dispatch error so failed requests are still attributable by error type.
	if dispatchErr != nil {
		record.ErrorCode = llmclient.ClassifyErrorCode(dispatchErr, record.StopReason)
		if record.ErrorMessage == "" {
			record.ErrorMessage = truncateErrMessage(dispatchErr.Error(), 512)
		}
		if record.StopReason == "" {
			record.StopReason = llmclient.StopReasonError
		}
	} else if telemetry.ErrorCode != "" {
		record.ErrorCode = telemetry.ErrorCode
	}
	return record
}

// summarizeStreamHardCapForTokens is the absolute last-resort cap on a
// summarize LLM stream. Liveness is governed by the idle watchdog
// (domain.StreamIdleTimeout); this only catches a pathological stream that
// keeps trickling events forever. It scales with input tokens (matching the
// agent's compaction timeout) and adds a 2× margin so the aggregator never
// unilaterally cancels a stream before the caller gives up, avoiding a
// double-timeout race (aggregator cap vs caller select) that previously
// reported spurious timeouts.
func summarizeStreamHardCapForTokens(inputTokens int) time.Duration {
	return compaction.TimeoutForTokens(inputTokens) * 2
}

// handleSummarize makes a non-streaming LLM call and returns the full response text.
// Stream-open failures fail over across the pool exactly like handleDispatch:
// each attempt resolves the unit token, acquires the provider gate and opens
// the stream via tryDispatchUnit; a failover-eligible open error rotates to
// the next healthy pool unit (selectUnitExcluding, pin cleared), while
// stop-class errors (400/404 configuration, caller cancellation) terminate —
// with stopRetryBudget rotation when a subsequent stop code differs from the
// first (mirroring handleDispatch). Once the stream opens the response is
// committed to that unit — mid-stream failures surface to the caller without
// further rotation. The one exception is a stream that completes without any
// text delta (reasoning-only output, stop_reason "length"): that earns exactly
// one rotation to another unit before the caller sees a descriptive error.
func (a *Actor) handleSummarize(ctx actor.PureContext, req domain.SendSessionMessageReq) (domain.SummarizeResp, error) {
	a.ensureConfigLoaded()
	selReq := SelectRequest{
		AgentID:  req.AgentID,
		SlotKind: req.SlotKind,
		Unit:     derefModelUnit(req.Unit),
	}
	unit, err := a.selectUnit(selReq)
	if err != nil {
		return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.summarize: strategy select: %w", err)
	}

	// A pinned unit only orders the FIRST attempt; failover re-selects from
	// the whole pool (soft-pin semantics, same as dispatch).
	failoverReq := selReq
	failoverReq.Unit = domain.ModelUnit{}

	// Summarization is latency-sensitive and runs under the caller's deadline
	// (the agent's compaction timeout). Reasoning effort pushes completion past
	// that deadline for reasoning models while adding little to summary quality,
	// so clear it — mirroring handleProbeTokens. tryDispatchUnit builds the
	// request via buildLLMRequest, which falls back to the pool-unit default
	// reasoning effort when the request omits one, so both the request fields
	// and the per-attempt unit copy are zeroed to keep summaries fast.
	sumReq := req
	sumReq.ThinkingBudget = 0
	sumReq.ReasoningEffort = ""

	// Estimate input tokens from the request to scale the hard cap.
	inputTokens := tokenest.EstimateTokens(req.System)
	for _, msg := range req.Messages {
		for _, cb := range msg.Content {
			inputTokens += tokenest.EstimateTokens(cb.Text)
		}
	}
	hardCap := summarizeStreamHardCapForTokens(inputTokens)

	summarizeCtx, cancel := context.WithTimeout(a.lifecycleCtx, hardCap)
	defer cancel()

	// Candidate loop (mirrors handleDispatch), extended past stream-open
	// failover: a stream that completes WITHOUT any text delta also rotates
	// once. Reasoning models can burn the whole output budget on reasoning
	// (stop_reason "length") without emitting a single text chunk; another
	// pool unit usually produces a usable summary.
	triedIDs := make(map[string]bool)
	stopRetry := &stopRetryBudget{}
	emptyTextRotations := 1
	for {
		triedIDs[unit.ID] = true
		tryUnit := *unit
		tryUnit.ReasoningEffort = ""
		stream, release, openErr := a.tryDispatchUnit(ctx, tryUnit, sumReq, summarizeCtx)
		if openErr != nil {
			classification := llmclient.ClassifyStreamOpenError(openErr)
			a.applyStreamOpenFailure(unit, openErr, classification)
			// Stop-class errors return the exact failure — except via stopRetry:
			// the first records its status and rotates; a repeat of that status
			// terminates, a different stop code rotates again.
			if !classification.Rotatable && !stopRetry.consume(classification, openErr) {
				return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.summarize: stream open: %w", openErr)
			}
			nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
			if selErr != nil {
				return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.summarize: stream open: %w", openErr)
			}
			unit = nextUnit
			continue
		}

		text, stopReason, consumeErr := a.consumeSummarizeStream(stream, unit)
		release()
		if consumeErr != nil {
			return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.summarize: %w", consumeErr)
		}
		a.reportProviderSuccess(unit.ProviderName)
		llmclient.RecordSuccess(unit.ProviderName, unit.Model)
		if strings.TrimSpace(text) != "" {
			return domain.SummarizeResp{Text: text}, nil
		}

		// Completed with zero text. Rotating re-rolls on another unit; once the
		// rotation budget is spent (or the pool is exhausted) report the stop
		// reason instead of a silent empty success — callers cannot
		// distinguish "summarized to nothing" from a bug without it.
		noTextErr := fmt.Errorf("aiaggregator.summarize: model returned no text (stop_reason=%q, unit=%s/%s)", stopReason, unit.ProviderName, unit.Model)
		if emptyTextRotations == 0 {
			return domain.SummarizeResp{}, noTextErr
		}
		emptyTextRotations--
		nextUnit, selErr := a.selectUnitExcluding(failoverReq, triedIDs)
		if selErr != nil {
			return domain.SummarizeResp{}, noTextErr
		}
		unit = nextUnit
	}
}

// consumeSummarizeStream drains one opened summarize stream to completion and
// returns the assembled text and stop reason. Mid-stream errors and idle
// timeouts are returned as errors. The stream is closed before returning;
// the caller owns the provider-gate release.
func (a *Actor) consumeSummarizeStream(stream llmclient.Stream, unit *CallableUnit) (string, string, error) {
	defer stream.Close()
	var sb strings.Builder
	stopReason := ""
	idleTimeout := domain.StreamIdleTimeout
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	resetIdle := func() {
		timer.Stop()
		// First chunk arrived; subsequent inter-chunk silence uses the bounded
		// reasoning-safe window instead of treating a thinking pause as a stall.
		timer.Reset(domain.StreamChunkIdleTimeout)
	}
	events := stream.Events()
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			resetIdle()
			switch ev.Kind {
			case llmclient.EventTextDelta:
				sb.WriteString(ev.Text)
			case llmclient.EventStop:
				stopReason = ev.StopReason
			case llmclient.EventError:
				a.applyStreamOpenFailure(unit, ev.Err, llmclient.ClassifyStreamOpenError(ev.Err))
				return "", "", fmt.Errorf("stream: %w", ev.Err)
			}
		case <-timer.C:
			a.applyStreamOpenFailure(unit, llmclient.ErrStreamIdleTimeout, llmclient.ClassifyStreamOpenError(llmclient.ErrStreamIdleTimeout))
			return "", "", fmt.Errorf("stream: %w", llmclient.ErrStreamIdleTimeout)
		}
	}
	return sb.String(), stopReason, nil
}

// handleProbeTokens sends a MaxTokens=1 request to the same model/unit used by
// dispatch and returns the actual InputTokens from the provider's usage event.
// This gives a 100% accurate token count for the exact same serialization
// (system + tools + messages + provider formatting) that dispatch uses.
// ThinkingBudget and ReasoningEffort are cleared to avoid conflicts with
// reasoning models when MaxTokens is tiny.
func (a *Actor) handleProbeTokens(_ actor.PureContext, req domain.SendSessionMessageReq) (domain.ProbeTokensResp, error) {
	a.ensureConfigLoaded()
	selReq := SelectRequest{
		AgentID:  req.AgentID,
		SlotKind: req.SlotKind,
		Unit:     derefModelUnit(req.Unit),
	}
	unit, err := a.selectUnit(selReq)
	if err != nil {
		return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: strategy select: %w", err)
	}

	authToken, err := a.resolveUnitToken(*unit)
	if err != nil {
		return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: %w", err)
	}

	client, err := a.registry.NewClient(unit.Protocol, unit.Endpoint, authToken, unit.Proxy, 0)
	if err != nil {
		return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: %w", err)
	}

	llmReq := a.buildLLMRequest(*unit, req)
	llmReq.MaxTokens = 1
	llmReq.ThinkingBudget = 0
	llmReq.ReasoningEffort = ""

	probeCtx, cancel := context.WithTimeout(a.lifecycleCtx, 30*time.Second)
	defer cancel()
	release, err := llmclient.DefaultProviderGate.Acquire(probeCtx, unit.ProviderName)
	if err != nil {
		return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: provider gate: %w", err)
	}
	defer release()
	stream, err := client.Stream(probeCtx, llmReq)
	if err != nil {
		a.applyStreamOpenFailure(unit, err, llmclient.ClassifyStreamOpenError(err))
		return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: stream open: %w", err)
	}
	defer stream.Close()

	for ev := range stream.Events() {
		switch ev.Kind {
		case llmclient.EventUsage:
			if ev.Usage != nil {
				llmclient.RecordSuccess(unit.ProviderName, unit.Model)
				return domain.ProbeTokensResp{
					Usage: domain.UsageData{
						InputTokens:              int64(ev.Usage.InputTokens),
						OutputTokens:             int64(ev.Usage.OutputTokens),
						TotalTokens:              int64(ev.Usage.TotalTokens),
						CacheCreationInputTokens: int64(ev.Usage.CacheCreationInputTokens),
						CacheReadInputTokens:     int64(ev.Usage.CacheReadInputTokens),
					},
				}, nil
			}
		case llmclient.EventError:
			a.applyStreamOpenFailure(unit, ev.Err, llmclient.ClassifyStreamOpenError(ev.Err))
			return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: stream: %w", ev.Err)
		}
	}
	a.reportProviderSuccess(unit.ProviderName)
	return domain.ProbeTokensResp{}, fmt.Errorf("aiaggregator.probe_tokens: no usage event received")
}

// handleIntent makes a non-streaming LLM call via the fast model to infer
// the user's intent from the provided message. It returns a single-sentence
// intent summary.
//
// Stream-open failures fail over across the pool exactly like handleDispatch
// and handleSummarize: each attempt resolves credentials, acquires the
// provider gate and opens the stream; a failover-eligible open error rotates
// to the next candidate, while stop-class errors terminate (with
// stopRetryBudget rotation). Candidates include nested-aggregator refs — the
// child runs its own intent selection — so a pool made purely of refs still
// yields a title (the production failure mode behind the "unsupported
// protocol" fallback bug). Once the stream opens the response is committed to
// that unit — mid-stream failures surface to the caller without rotation.
func (a *Actor) handleIntent(ctx actor.PureContext, req domain.SendSessionMessageReq) (domain.SummarizeResp, error) {
	a.ensureConfigLoaded()

	var messages []domain.ChatMessage
	if len(req.Messages) > 0 {
		messages = req.Messages
	} else {
		return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: no messages provided for intent inference")
	}
	// Trim to the last few messages to avoid overwhelming the fast model with
	// long conversation history.
	if len(messages) > 4 {
		messages = messages[len(messages)-4:]
	}

	sys := req.System
	if sys == "" {
		sys = agentkit.AggregatorIntent
	}
	intentReq := domain.SendSessionMessageReq{
		System:   sys,
		Messages: messages,
		// Pin temperature to 0 for deterministic title output. Without this
		// the model falls back to its default (often ~1.0), producing
		// different titles for the same input across calls.
		Temperature: 0.001,
	}

	intentCtx, cancel := context.WithTimeout(a.lifecycleCtx, 30*time.Second)
	defer cancel()

	// Candidate ordering: pinned unit first (soft pin — on stream-open
	// failure the loop rotates into the pool candidates), then
	// non-reasoning concrete units, then reasoning concrete units, then
	// nested-aggregator refs.
	var candidates []*CallableUnit
	pinned := derefModelUnit(req.Unit)
	if pinned.Model != "" {
		if u, err := a.resolveIntentUnit(pinned); err == nil {
			candidates = append(candidates, u)
		}
	}
	if poolCands, err := a.selectIntentCandidates(); err == nil {
		for _, c := range poolCands {
			if len(candidates) > 0 && c.ID == candidates[0].ID {
				continue
			}
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: no callable units available")
	}

	stopRetry := &stopRetryBudget{}
	var unit *CallableUnit
	var stream llmclient.Stream
	var release func()
	var nested *nestedAggregatorStream
	var nestedRelease func()
	var openErr error
	for _, cand := range candidates {
		unit = cand
		if cand.AggregatorID != "" {
			// Nested-aggregator ref: the child performs its own unit
			// selection and intent inference semantics (disable-thinking,
			// reasoning avoidance) apply on the child's side.
			nested, nestedRelease, openErr = a.tryDispatchNestedAggregator(ctx, intentCtx, *cand, intentReq)
			if openErr == nil {
				break
			}
			if errors.Is(openErr, errPoolExhausted) {
				llmclient.RegisterAggregatorUnavailableUntil(cand.AggregatorID, time.Now().Add(llmclient.DefaultAggregatorUnavailableTTL))
			}
		} else {
			stream, release, openErr = a.tryIntentUnit(ctx, *cand, intentReq, intentCtx)
			if openErr == nil {
				break
			}
			a.applyStreamOpenFailure(cand, openErr, llmclient.ClassifyStreamOpenError(openErr))
		}
		ctx.Logger().Warn("aiaggregator: intent stream open failed, trying next candidate",
			"unit", cand.ID,
			"provider", cand.ProviderName,
			"aggregator", cand.AggregatorID,
			"error", truncateErrMessage(openErr.Error(), 512),
		)
		classification := llmclient.ClassifyStreamOpenError(openErr)
		if !classification.Rotatable && !stopRetry.consume(classification, openErr) {
			return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: stream open: %w", openErr)
		}
	}
	if openErr != nil {
		return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: stream open: %w: %w", openErr, errPoolExhausted)
	}
	defer func() {
		if release != nil {
			release()
		}
		if nestedRelease != nil {
			nestedRelease()
		}
	}()
	ctx.Logger().Info("aiaggregator: intent inference",
		"unit", unit.Model,
		"endpoint", unit.Endpoint,
		"protocol", unit.Protocol,
		"provider", unit.ProviderName,
		"reasoning", unit.IsReasoning,
		"aggregator", unit.AggregatorID,
		"pinned", pinned.Model != "",
	)

	// feedDelta relays one text/reasoning delta into the shared extractor.
	var extractor intentExtractor
	var rawText strings.Builder
	var reasoningText strings.Builder
	var textDeltas int
	var reasoningDeltas int
	var extracted string
	var extractedDone bool
	feedDelta := func(text string, reasoning bool) bool {
		if reasoning {
			reasoningDeltas++
			reasoningText.WriteString(text)
			return false
		}
		textDeltas++
		rawText.WriteString(text)
		done, result := extractor.feedText(text)
		if done {
			extracted = result
			extractedDone = true
		}
		return extractedDone
	}

	if nested != nil {
		// The child's buffered head includes its ResolvedUnit marker; skip
		// non-text chunks (usage, stop, tool frames are irrelevant here).
	nestedRead:
		for {
			c, rerr := nested.Read()
			if rerr != nil {
				if !errors.Is(rerr, io.EOF) {
					return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: nested stream: %w", rerr)
				}
				break
			}
			switch c.Kind {
			case domain.AggregatorChunkText:
				if feedDelta(c.Text, false) {
					break nestedRead
				}
			case domain.AggregatorChunkReasoning:
				feedDelta(c.Text, true)
			}
		}
	} else {
		defer stream.Close()
	readEvents:
		for ev := range stream.Events() {
			switch ev.Kind {
			case llmclient.EventTextDelta:
				if feedDelta(ev.Text, false) {
					break readEvents
				}
			case llmclient.EventReasoningDelta:
				feedDelta(ev.Text, true)
			case llmclient.EventError:
				a.applyStreamOpenFailure(unit, ev.Err, llmclient.ClassifyStreamOpenError(ev.Err))
				ctx.Logger().Warn("aiaggregator: intent stream error",
					"unit", unit.Model,
					"endpoint", unit.Endpoint,
					"protocol", unit.Protocol,
					"provider", unit.ProviderName,
					"error", ev.Err,
				)
				return domain.SummarizeResp{}, fmt.Errorf("aiaggregator.intent: stream: %w", ev.Err)
			}
		}
	}
	// If a unit produced only reasoning and no regular text, it behaves like a
	// reasoning model even if its name doesn't match the heuristic. Mark it in
	// the persistent unit list so future title inference calls skip it.
	if textDeltas == 0 && reasoningDeltas > 0 {
		a.markUnitReasoning(unit.ID)
	}

	text := extracted
	if text == "" {
		// Fallback: no complete intent wrapper was found. Fall back to the
		// legacy path of stripping inline think tags and hard-truncating the
		// remaining text, so a model that ignores the marker protocol still
		// yields a usable title rather than an empty one.
		text = strings.TrimSpace(stripThinkTags(rawText.String()))
	}
	if text == "" && reasoningDeltas > 0 {
		// Hybrid reasoning models (glm-4.5, MiniMax M2) can terminate with
		// the whole answer inside reasoning_content and an empty content
		// stream. Recover by running the same extraction over the reasoning
		// text: the model almost always repeats the marker-wrapped (or bare)
		// answer there.
		if done, result := extractor.feedText(reasoningText.String()); done && result != "" {
			text = result
		} else {
			text = strings.TrimSpace(stripThinkTags(reasoningText.String()))
		}
	}
	// Length is constrained only by the prompt (~20 chars); no code-side cap.
	text = sanitizeIntentTitle(text)
	ctx.Logger().Info("aiaggregator: intent inference done",
		"unit", unit.Model,
		"provider", unit.ProviderName,
		"textDeltas", textDeltas,
		"reasoningDeltas", reasoningDeltas,
		"textLen", len(text),
		"text", text,
	)
	llmclient.RecordSuccess(unit.ProviderName, unit.Model)
	return domain.SummarizeResp{Text: text}, nil
}

// tryIntentUnit opens a stream for title inference on a concrete unit. It is
// the intent-specific counterpart of tryDispatchUnit: the built request gets
// thinking disabled (hybrid models emit the whole answer as
// reasoning_content) and the reasoning effort zeroed — the reasoning-recovery
// fallback in handleIntent covers providers that ignore the switch. On
// failure the gate (when acquired) is released and the error returned; the
// caller decides failover.
func (a *Actor) tryIntentUnit(ctx actor.PureContext, unit CallableUnit, req domain.SendSessionMessageReq, streamCtx context.Context) (llmclient.Stream, func(), error) {
	authToken, err := a.resolveUnitToken(unit)
	if err != nil {
		return nil, nil, err
	}
	client, err := a.registry.NewClient(unit.Protocol, unit.Endpoint, authToken, unit.Proxy, 0)
	if err != nil {
		return nil, nil, err
	}
	tryUnit := unit
	tryUnit.ReasoningEffort = ""
	llmReq := a.buildLLMRequest(tryUnit, req)
	disableThinkingExtraBody(&llmReq)
	release, err := llmclient.DefaultProviderGate.Acquire(streamCtx, unit.ProviderName)
	if err != nil {
		return nil, nil, fmt.Errorf("provider gate: %w", err)
	}
	stream, err := client.Stream(streamCtx, llmReq)
	if err != nil {
		release()
		return nil, nil, err
	}
	return stream, release, nil
}

// selectIntentCandidates builds the ordered candidate list for auto-picked
// title inference: non-reasoning concrete units, reasoning concrete units,
// then nested-aggregator refs (health-gated, self-refs skipped). The child of
// a ref applies the same non-reasoning preference internally. Returns an error
// when no candidate is usable at all.
func (a *Actor) selectIntentCandidates() ([]*CallableUnit, error) {
	pool := preferHealthy(a.chatUnits())
	now := time.Now()
	concrete := make([]*CallableUnit, 0, len(pool))
	refs := make([]*CallableUnit, 0, len(pool))
	for i := range pool {
		u := &pool[i]
		if a.isSelfAggregatorRef(*u) {
			continue
		}
		if u.AggregatorID != "" {
			if !llmclient.IsAggregatorAvailable(u.AggregatorID, now) {
				continue
			}
			refs = append(refs, u)
			continue
		}
		if tokenPlanExhausted(*u) {
			continue
		}
		concrete = append(concrete, u)
	}
	nonReasoning := make([]*CallableUnit, 0, len(concrete))
	reasoning := make([]*CallableUnit, 0, len(concrete))
	for _, u := range concrete {
		if u.IsReasoning {
			reasoning = append(reasoning, u)
		} else {
			nonReasoning = append(nonReasoning, u)
		}
	}
	out := append(nonReasoning, reasoning...)
	out = append(out, refs...)
	if len(out) == 0 {
		return nil, fmt.Errorf("no callable units available")
	}
	return out, nil
}

// buildLLMRequest translates a domain.SendSessionMessageReq into the
// provider-neutral llmclient.Request. Provider-specific body extensions are
// resolved here from the selected unit via the actor's registry, so the
// generic client does not need to guess the provider from the model name.
func (a *Actor) buildLLMRequest(unit CallableUnit, req domain.SendSessionMessageReq) llmclient.Request {
	if a.unitNeedsImageSubstitution(unit) && requestHasImageBlocks(req) {
		// Text-only unit (learned via a 400, or known by family heuristic):
		// rewrite image blocks on the wire request before it is built. Blocks
		// with a cached recognition answer carry its text; the rest degrade to
		// placeholders. Request-scoped rewrite only — the session history
		// carried in req.Messages is never mutated.
		req = a.substituteImagesFromCache(req)
	}
	out := llmclient.Request{
		Model:           unit.Model,
		UserAgent:       unit.UserAgent,
		System:          req.System,
		ThinkingBudget:  int(req.ThinkingBudget),
		ReasoningEffort: req.ReasoningEffort,
		ExtraBody:       a.registry.ExtraBody(unit.Protocol, unit.Model),
		IsReasoning:     unit.IsReasoning,
	}
	// Fall back to the pool-unit default reasoning effort only when the caller
	// did not specify one (explicit request > pool-unit default).
	if out.ReasoningEffort == "" {
		out.ReasoningEffort = unit.ReasoningEffort
	}

	if req.Temperature != 0 {
		v := float64(req.Temperature)
		out.Temperature = &v
	}
	if req.TopP != 0 {
		v := float64(req.TopP)
		out.TopP = &v
	}
	if req.FrequencyPenalty != 0 {
		v := float64(req.FrequencyPenalty)
		out.FrequencyPenalty = &v
	}
	if req.PresencePenalty != 0 {
		v := float64(req.PresencePenalty)
		out.PresencePenalty = &v
	}
	if req.Seed != 0 {
		v := int(req.Seed)
		out.Seed = &v
	}
	if req.ToolChoice != "" {
		out.ToolChoice = req.ToolChoice
	}

	if len(req.SystemBlocks) > 0 {
		sbs := make([]llmclient.SystemBlock, len(req.SystemBlocks))
		for i, sb := range req.SystemBlocks {
			sbs[i] = llmclient.SystemBlock{
				Type:         sb.Type,
				Text:         sb.Text,
				CacheControl: sb.CacheControl,
			}
		}
		out.SystemBlocks = sbs
	}

	if len(req.Messages) > 0 {
		msgs := make([]llmclient.Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			blocks := make([]llmclient.Block, 0, len(m.Content))
			for _, b := range m.Content {
				blocks = append(blocks, llmclient.Block{
					Type:         b.Type,
					Text:         b.Text,
					ToolUseID:    b.ToolUseID,
					ToolName:     b.ToolName,
					Input:        b.Input,
					IsError:      b.IsError,
					CacheControl: b.CacheControl,
					ImageURL:     b.ImageURL,
					MimeType:     b.MimeType,
				})
			}
			msgs = append(msgs, llmclient.Message{Role: m.Role, Content: blocks, ReasoningContent: m.ReasoningContent})
		}
		out.Messages = msgs
	}
	if len(req.Tools) > 0 {
		// Reconcile provider-native tools (Type != "") against the resolved
		// unit's wire protocol: a native web_search is only valid on its
		// native protocol, so it is dropped when the model is reached via a
		// different protocol (e.g. glm over openai/endpoint). Standard
		// function tools are always kept; the message history is untouched.
		reconciled := a.nativeTools.Reconcile(req.Tools, unit.Protocol)
		tools := make([]llmclient.ToolSpec, 0, len(reconciled))
		for _, t := range reconciled {
			tools = append(tools, llmclient.ToolSpec{
				Name:         t.Name,
				Description:  t.Description,
				InputSchema:  t.InputSchema,
				Type:         t.Type,
				NativeConfig:  t.NativeConfig,
			})
		}
		out.Tools = tools
	}
	// DeepSeek hybrid-thinking models (deepseek-v* family: v3.1/v3.2/v4) think
	// by DEFAULT on the server side and reject a forced tool_choice while
	// thinking ("Thinking mode does not support this tool_choice"). When the
	// request forces a tool call and activates no explicit reasoning, disable
	// thinking so the forced call is servable. The unit-default reasoning
	// effort is cleared too — it would re-activate thinking and re-trigger the
	// provider rejection. An explicit request-level effort is left untouched:
	// effort + forced tool call is a real caller conflict the provider should
	// surface, not one this layer should silently resolve.
	if isForcedToolChoice(req.ToolChoice) && req.ReasoningEffort == "" && unit.Protocol == "openai" {
		if _, explicit := out.ExtraBody["thinking"]; !explicit && isDeepSeekHybridModel(unit.Model) {
			if out.ExtraBody == nil {
				out.ExtraBody = map[string]any{}
			}
			out.ExtraBody["thinking"] = map[string]any{"type": "disabled"}
			out.ReasoningEffort = ""
		}
	}
	return out
}

// isForcedToolChoice reports whether the tool-choice string pins the tool-call
// outcome ("required", a bare tool name, or the JSON object form) as opposed
// to the model-decided ""/"auto"/"none".
func isForcedToolChoice(choice string) bool {
	switch choice {
	case "", "auto", "none":
		return false
	}
	return true
}

// isDeepSeekHybridModel matches the DeepSeek hybrid models whose server
// default is thinking ON (deepseek-v3.1, v3.2, v4, …). deepseek-chat (V3,
// non-thinking default) and deepseek-reasoner (explicitly enabled via
// openAIExtraBody, which the disable switch must not override) are excluded.
func isDeepSeekHybridModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "deepseek-v")
}

// newBuiltinRegistry builds the actor-owned registry of built-in protocol
// descriptors. It is the single registration site for wire protocols; there
// is no package-level map or init() (per the Actor-Owns-State constraint).
// Adding a new protocol means adding a descriptor here plus its client
// implementation in pkg/llmclient — no switch to edit.
//
// ExtraBody policies encode the previously hard-coded Kimi/DeepSeek model
// rules. They are intentionally kept on the descriptor (not in domain types)
// so they never reach persistence; the registry owns runtime behaviour.
func newBuiltinRegistry() llmclient.Registry {
	r := llmclient.NewRegistry()
	r.MustRegister(llmclient.Descriptor{
		Protocol: "anthropic",
		Factory:  llmclient.NewAnthropicFactory(),
	})
	r.MustRegister(llmclient.Descriptor{
		Protocol:  "openai",
		Factory:   llmclient.NewOpenAIFactory(),
		ExtraBody: openAIExtraBody,
	})
	r.MustRegister(llmclient.Descriptor{
		Protocol: "endpoint",
		Factory:  llmclient.NewEndpointFactory(),
	})
	// Responses API is chat-completions-adjacent but uses its own wire shape
	// (POST /responses), so no ExtraBody policy: the Kimi/DeepSeek rules are
	// chat-completions-only and must not leak onto this protocol.
	r.MustRegister(llmclient.Descriptor{
		Protocol: "responses",
		Factory:  llmclient.NewResponsesFactory(),
	})
	return *r
}

// openAIExtraBody applies the OpenAI-compatible provider-specific request body
// extensions previously encoded by providerExtraBody. It is keyed on the final
// model name because a unit's provider name may be user-defined or third-party;
// the model name is what determines whether provider-native extensions like
// Kimi's chat_template_args are meaningful on the wire.
func openAIExtraBody(model string) map[string]any {
	m := strings.ToLower(model)
	if strings.Contains(m, "kimi") {
		// kimi-k2-thinking returns reasoning_content by default — no extra params needed.
		if strings.Contains(m, "kimi-k2-thinking") {
			return nil
		}
		// For k2.5 / k2p5 / k2-5 / base k2 / for-coding, enable thinking via chat_template_args.
		if strings.Contains(m, "k2.") || strings.Contains(m, "k2p") || strings.Contains(m, "k2-5") || strings.Contains(m, "for-coding") {
			return map[string]any{"chat_template_args": map[string]any{"enable_thinking": true}}
		}
		return nil
	}
	if strings.Contains(m, "deepseek") {
		// DeepSeek reasoning models (R1 / reasoner family) expose the thinking
		// content stream when the thinking parameter is explicitly enabled.
		if strings.Contains(m, "r1") || strings.Contains(m, "reasoner") {
			return map[string]any{"thinking": map[string]any{"type": "enabled"}}
		}
	}
	return nil
}

// disableThinkingExtraBody turns off provider-native thinking for short,
// non-conversational inference (titles, JSON verdicts). Only models with a
// known disable switch are touched; other providers keep their default.
func disableThinkingExtraBody(req *llmclient.Request) {
	if _, ok := req.ExtraBody["thinking"]; ok {
		return
	}
	m := strings.ToLower(req.Model)
	if strings.Contains(m, "glm") {
		if req.ExtraBody == nil {
			req.ExtraBody = map[string]any{}
		}
		req.ExtraBody["thinking"] = map[string]any{"type": "disabled"}
	}
}

// handleStatus returns the current aggregator state for observability.
// Units are returned as redacted views (no AuthToken) since this callable
// is Public() and must not leak provider credentials. Health state (state,
// reason, cooldown, recovery mode) is joined from the llmclient health
// snapshot — the aggregator itself stores no local health state.
//
// Each unit view also carries its in-flight DispatchActivity when the
// aggregator is dispatching through it. For aggregator-ref entries the
// activity is propagated upward from the child aggregator (depth+1,
// AggregatorID = the child's config ID); for concrete units it is the
// aggregator's own dispatch activity map entry.
func (a *Actor) handleStatus(_ actor.PureContext) (domain.AIAggregatorStatusResp, error) {
	a.ensureConfigLoaded()
	// Snapshot the pool, metadata, and health snapshots under the read
	// lock; the child-query path (aggregatedDispatchActivity) does
	// cross-actor invokes and must not hold a.mu.
	a.mu.RLock()
	units := make([]CallableUnit, len(a.units))
	copy(units, a.units)
	name := a.name
	version := a.localVersion
	disabled := a.disabled
	poolCount := len(a.units)
	a.mu.RUnlock()

	snap := llmclient.HealthSnapshot()
	aggSnap := llmclient.AggregatorHealthSnapshot()
	now := time.Now().Unix()
	nowT := time.Now()
	poolUnits := make([]domain.AICallableUnitView, 0, len(units))
	for _, u := range units {
		// Aggregator-ref entries have no provider endpoint; their health
		// comes from the aggregator-level registry (IsAggregatorAvailable),
		// not the unit-level snapshot. Skip disable-window logic — reference
		// entries have no time-of-day gate.
		if u.AggregatorID != "" {
			hs := healthStateHealthy
			hr := ""
			rm := ""
			var cdUntil int64
			if !llmclient.IsAggregatorAvailable(u.AggregatorID, nowT) {
				hs = healthStateDisabled
				// The child's whole pool is unavailable; a concrete reason
				// must reach the frontend or the provider dropdown renders
				// the raw i18n key ("health.reason.").
				hr = llmclient.HealthReasonAvailability
				// TTL self-healing: the registry deadline auto-expires,
				// so the recovery mode is "cooldown" (not manual action).
				rm = "cooldown"
				if e, ok := aggSnap[u.AggregatorID]; ok {
					cdUntil = e.UnavailableUntil.Unix()
				}
			}
			view := domain.AICallableUnitView{
				ID:            u.ID,
				AggregatorID:  u.AggregatorID,
				CooldownUntil: cdUntil,
				Disabled:      u.Disabled,
				HealthState:   hs,
				HealthReason:  hr,
				RecoveryMode:  rm,
			}
			// Propagate the child's in-flight dispatch activity upward onto
			// this aggregator-ref entry (depth+1, AggregatorID = child).
			view.DispatchActivity = a.aggregatedDispatchActivity(u)
			poolUnits = append(poolUnits, view)
			continue
		}
		h := unitHealthFromSnapshot(snap, u.ProviderName, u.Model)
		hs, hr := h.State, h.Reason
		// Normalize expired cooldown to healthy.
		if hs == healthStateCoolingDown && !nowT.Before(h.CooldownUntil) {
			hs = healthStateHealthy
			hr = ""
		}
		var cdUntil int64
		if hs == healthStateCoolingDown && nowT.Before(h.CooldownUntil) {
			cdUntil = h.CooldownUntil.Unix()
		}
		view := domain.AICallableUnitView{
			ID:                  u.ID,
			Model:               u.Model,
			Endpoint:            u.Endpoint,
			ProviderName:        u.ProviderName,
			Protocol:            u.Protocol,
			Modality:            u.Modality,
			CooldownUntil:       cdUntil,
			DisableUntil:        u.DisableUntil,
			Disabled:            u.Disabled,
			CooldownReason:      hr,
			ConsecutiveFailures: int32(h.ConsecutiveFailures),
			UserAgent:           u.UserAgent,
			MaxContextLength:    u.MaxContextLength,
			HealthState:         hs,
			HealthReason:        hr,
			RecoveryMode:        recoveryModeForHealth(hs),
		}
		view.DispatchActivity = a.aggregatedDispatchActivity(u)
		poolUnits = append(poolUnits, view)
	}
	// Append on-demand (provider, model) pairs currently non-healthy in
	// the llmclient snapshot that are not already in the pool, so operators
	// can observe why a unit-kind ref is not being served. Only non-healthy
	// entries are surfaced. UnitCount stays the pool size.
	poolKeys := make(map[string]bool, len(units))
	for _, u := range units {
		poolKeys[u.ProviderName+"::"+u.Model] = true
	}
	var onDemandViews []domain.AICallableUnitView
	for key, h := range snap {
		if poolKeys[key] {
			continue
		}
		hs := h.State
		hr := h.Reason
		if hs == healthStateCoolingDown && !nowT.Before(h.CooldownUntil) {
			continue // expired — not surfaced
		}
		if hs == healthStateHealthy {
			continue
		}
		var cdUntil int64
		if hs == healthStateCoolingDown && nowT.Before(h.CooldownUntil) {
			cdUntil = h.CooldownUntil.Unix()
		}
		onDemandViews = append(onDemandViews, domain.AICallableUnitView{
			ID:                  key,
			Model:               h.Model,
			ProviderName:        h.Provider,
			CooldownUntil:       cdUntil,
			CooldownReason:      hr,
			ConsecutiveFailures: int32(h.ConsecutiveFailures),
			OnDemand:            true,
			HealthState:         hs,
			HealthReason:        hr,
			LastFailureAt:       now,
			RecoveryMode:        recoveryModeForHealth(hs),
		})
	}
	allUnits := append(poolUnits, onDemandViews...)
	return domain.AIAggregatorStatusResp{
		ID:        a.id,
		Name:      name,
		Version:   version,
		UnitCount: int32(poolCount),
		Disabled:  disabled,
		Units:     allUnits,
	}, nil
}

// mediaResolveDiag explains why an image/video resolve found no usable unit
// instead of one generic message: a pinned (provider, model) that no unit
// matches, one whose modality is wrong, or a disabled/cooling unit.
func mediaResolveDiag(units []CallableUnit, provider, model, modality, example string, now int64) string {
	if provider == "" && model == "" {
		return fmt.Sprintf("no %s-generation units available — add a provider with a %s model (e.g. %s)", modality, modality, example)
	}
	var match *CallableUnit
	for i := range units {
		u := &units[i]
		if provider != "" && u.ProviderName != provider {
			continue
		}
		if model != "" && u.Model != model {
			continue
		}
		match = u
		break
	}
	switch {
	case match == nil:
		return fmt.Sprintf("no unit matches provider %q model %q — check provider settings", provider, model)
	case match.Modality != modality:
		return fmt.Sprintf("model %q on provider %q is a %q model, not %s-generation", model, provider, match.Modality, modality)
	case match.Disabled || unitInDisableWindow(*match, now):
		return fmt.Sprintf("%s-generation unit %s/%s is disabled or cooling down — re-enable it or wait out the disable window", modality, match.ProviderName, match.Model)
	}
	return fmt.Sprintf("no %s-generation units available", modality)
}

// handleImageResolve finds an image-generation callable unit, resolves its auth
// token via aimanager, and returns the credentials the caller needs to make a
// raw image-generation API request. If Provider/Model are specified, the first
// matching image unit is returned; otherwise the first available image unit.
func (a *Actor) handleImageResolve(_ actor.PureContext, req domain.AIAggregatorImageResolveReq) (domain.AIAggregatorImageResolveResp, error) {
	a.ensureConfigLoaded()
	// Snapshot units under read lock, then resolve token outside the lock
	// (resolveUnitToken does a cross-actor invoke which could be slow).
	a.mu.RLock()
	now := time.Now().Unix()
	var matched *CallableUnit
	for i := range a.units {
		u := &a.units[i]
		if u.Modality != "image" {
			continue
		}
		if u.Disabled || unitInDisableWindow(*u, now) {
			continue
		}
		if req.Provider != "" && u.ProviderName != req.Provider {
			continue
		}
		if req.Model != "" && u.Model != req.Model {
			continue
		}
		matched = u
		break
	}
	a.mu.RUnlock()
	if matched == nil {
		return domain.AIAggregatorImageResolveResp{}, fmt.Errorf("aiaggregator.image.resolve: %s", mediaResolveDiag(a.units, req.Provider, req.Model, "image", "gemini-2.5-flash-image", now))
	}
	token, err := a.resolveUnitToken(*matched)
	if err != nil {
		return domain.AIAggregatorImageResolveResp{}, fmt.Errorf("aiaggregator.image.resolve: %w", err)
	}
	return domain.AIAggregatorImageResolveResp{
		Model:        matched.Model,
		Endpoint:     matched.Endpoint,
		ProviderName: matched.ProviderName,
		Protocol:     matched.Protocol,
		AuthToken:    token,
		UserAgent:    matched.UserAgent,
		Proxy:        matched.Proxy,
	}, nil
}

// handleVideoResolve finds a video-generation callable unit, resolves its auth
// token via aimanager, and returns the credentials the caller needs to make a
// raw video-generation API request. Mirrors handleImageResolve but matches on
// Modality "video"; the resolved Protocol drives the videogen dispatcher.
func (a *Actor) handleVideoResolve(_ actor.PureContext, req domain.AIAggregatorVideoResolveReq) (domain.AIAggregatorVideoResolveResp, error) {
	a.ensureConfigLoaded()
	// Snapshot units under read lock, then resolve token outside the lock
	// (resolveUnitToken does a cross-actor invoke which could be slow).
	a.mu.RLock()
	now := time.Now().Unix()
	var matched *CallableUnit
	for i := range a.units {
		u := &a.units[i]
		if u.Modality != "video" {
			continue
		}
		if u.Disabled || unitInDisableWindow(*u, now) {
			continue
		}
		if req.Provider != "" && u.ProviderName != req.Provider {
			continue
		}
		if req.Model != "" && u.Model != req.Model {
			continue
		}
		matched = u
		break
	}
	a.mu.RUnlock()
	if matched == nil {
		return domain.AIAggregatorVideoResolveResp{}, fmt.Errorf("aiaggregator.video.resolve: %s", mediaResolveDiag(a.units, req.Provider, req.Model, "video", "veo-3.1", now))
	}
	token, err := a.resolveUnitToken(*matched)
	if err != nil {
		return domain.AIAggregatorVideoResolveResp{}, fmt.Errorf("aiaggregator.video.resolve: %w", err)
	}
	return domain.AIAggregatorVideoResolveResp{
		Model:        matched.Model,
		Endpoint:     matched.Endpoint,
		ProviderName: matched.ProviderName,
		Protocol:     matched.Protocol,
		AuthToken:    token,
		UserAgent:    matched.UserAgent,
		Proxy:        matched.Proxy,
	}, nil
}
