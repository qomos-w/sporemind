package workspace

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/scriptcard"
)

// ExecKind names the execution capability a task card declares in its
// frontmatter (data.exec.kind). The orchestrator reads it from the bound
// task card and dispatches to the matching Executor in the workspace's
// execKind registry; the orchestrator never branches on card type or
// implementation details — new execKinds land by registering a new
// Executor, with no migration to existing callables or cards.
type ExecKind = string

const (
	// ExecKindWorkerTask is the default executor: claim the bound task card
	// (status → doing), spawn a fresh 1:1 worker agent bound to it, and
	// hand the card body to the worker as its goal condition. It
	// subsumes the previous workspace.agent_spawn_assign path.
	ExecKindWorkerTask ExecKind = "worker_task"

	// ExecKindSubMap is the nested-workflow executor: when a task card
	// declares data.exec.kind = sub_map, the executor instantiates the
	// referenced template map, spawns an independent owner agent for the
	// new instance, and maps the sub-map's completion state back onto the
	// parent task card (outputs promotion, cancellation/failure propagation).
	// Fan-out is intentionally not implemented in this executor.
	ExecKindSubMap ExecKind = "sub_map"

	// ExecKindCrawl is the browser-crawl executor: when a task card declares
	// data.exec.kind = crawl, the executor starts the referenced crawl
	// definition card, polls the crawl task to completion, and writes the
	// results to the task card's data.task_outputs block on success.
	ExecKindCrawl ExecKind = "crawl"

	// ExecKindEventWait is the reactive executor: when a task card declares
	// data.exec.kind = event_wait, the executor arms a subscription to the
	// declared event kind (data.exec.event_kind, optional data.exec.event_service,
	// optional data.exec.timeout_s), returns immediately, and flips the card
	// done with the event payload as task_outputs when a matching event lands
	// (failed on timeout). Activation is asynchronous — no agent is spawned and
	// the owner's dispatch returns at once; see executor_eventwait.go.
	ExecKindEventWait ExecKind = "event_wait"

	// ExecKindToolCall is the deterministic tool executor: when a task card
	// declares data.exec.kind = toolcall, the executor resolves the declared
	// callable against the live topology snapshot, validates the merged
	// arguments against the callable's request layout, invokes it once, and
	// writes the result to the task card's data.task_outputs. Unlike
	// worker_task there is no LLM in the loop — the card author (the owner
	// agent) fixes the callable and arguments at plan time. v1 admits
	// read-only callables only (EffectKind none).
	ExecKindToolCall ExecKind = "toolcall"

	// ExecKindScatter is the dynamic fan-out executor: when a task card
	// declares data.exec.kind = scatter, the executor reads the array from
	// the upstream binding named by data.exec.source (resolved into the
	// claim's inputs), instantiates one child task card per element
	// (<scatter-card-id>-item-<idx>, idempotent), and joins: when every
	// child is done the scatter card flips done with data.task_outputs.
	// items aggregating the children's outputs in element order; a failed
	// child flips the scatter card failed. The join is driven by the
	// workspace's 5s scatter sweep, mirroring the project updater tick
	// that advances the children through frontier notifications.
	ExecKindScatter ExecKind = "scatter"

	// ExecKindGate is the workflow approval executor: when a task card
	// declares data.exec.kind = gate, the executor activates the card
	// (status → doing) and either:
	//
	//   - bypass mode (global permission mode yolo / autopilot): auto-flips
	//     the card to done with task_outputs.decision="auto_approved", or
	//   - normal mode: enqueues a companion-window toast whose ActionCallable
	//     is workspace.gate_approve; the card stays doing until the user
	//     approves or rejects, and workspace.gate_approve / workspace.gate_reject
	//     flip the terminal state via project.wiki_set_status CAS.
	//
	// The toast carries ActionArgs { TaskCardId, CallableId? } and
	// ActionSchemaID = WorkspaceGateApproveReqSchemaID so the main window
	// can re-dispatch with the right typed args. The optional data.exec
	// .callable field names the mutating callable the gate protects
	// (EffectKind must be mutating); it is forwarded to task_outputs for
	// audit, never re-invoked by the gate executor itself.
	ExecKindGate ExecKind = "gate"

	// ExecKindScript is the in-card sporescript executor: when a task card
	// declares data.exec.kind = script, the executor extracts a ```spore
	// fenced block from the card body, compiles and runs it inside a
	// per-claim spore VM (script.NewRuntime + LoadSource + CallContext),
	// and forwards host.invoke(callID, args) calls into the topology
	// under a per-card whitelist (data.exec.capabilities). No std
	// library is registered — the only host face the script can reach
	// is host.invoke, which is subject to the same effect-policy gate
	// as the toolcall executor (mutating callables still need
	// gate_card done + workflow stamped_effects). See executor_script.go
	// and [[spore-script-card-feasibility]] for the design contract.
	ExecKindScript ExecKind = "script"
)

// DefaultExecKind is the execKind used when a task card does not declare
// data.exec.kind (or declares it empty). It is intentionally equal to
// ExecKindWorkerTask today: the worker_task executor IS the existing
// spawn-worker behavior, so absent a declaration the legacy path is
// preserved bit-for-bit.
const DefaultExecKind ExecKind = ExecKindWorkerTask

// ClaimReq is the dispatcher's preflight output that every Executor
// receives. It carries the universal claim result (status CAS, body
// read, binding resolution) so executors do not have to repeat the
// project round-trip — they only do the kind-specific work (spawn a
// worker, start a crawl, instantiate a sub-map, ...).
//
// The dispatcher guarantees that Claimed is true when an executor
// runs; ClaimErr carries the failure reason otherwise and the
// executor is never invoked.
//
// Mapping from the wire WorkspaceAgentSpawnAssignReq (see the
// dispatcher for the canonical mapping):
//
//	CallerAgentID   ← req.CallerAgentID
//	ProjectID       ← resolved project actor id (req.ProjectID or caller)
//	BoundTaskCardID ← req.BoundTaskCardID
//	CardRaw         ← claim response.Raw (the full frontmatter)
//	Claimed         ← always true when an executor runs
//	PreviousStatus  ← claim response.PreviousStatus (used for rollback)
//	Body            ← frontmatter-stripped body + injected upstream inputs
//	Inputs          ← resolved data-flow bindings (map[input]value)
//	AgentKind       ← req.AgentKind (worker_task: required; others: ignored)
//	GoalTitle       ← req.To if non-empty, else req.InterpretedGoal
//	Unit            ← req.Unit (worker_task only; optional slot override)
//	MaxTurns        ← req.MaxTurns (worker_task only)
type ClaimReq struct {
	CallerAgentID   string
	ProjectID       string
	BoundTaskCardID string
	CardRaw         string
	Claimed         bool
	PreviousStatus  string
	Body            string // frontmatter-stripped, inputs-injected
	Inputs          map[string]any
	AgentKind       string
	GoalTitle       string
	Unit            *domain.ModelUnit
	MaxTurns       int32
	WorktreeBranch string // caller-specified branch name; empty = auto-generate
	// Preflight carries the result of the matching Preflight call
	// (must be non-nil when Execute runs). The dispatcher invokes
	// Preflight before the claim and threads the result through
	// to Execute; executors reject Preflight == nil as a
	// programming error.
	Preflight *PreflightResult
}

// ExecResp is the executor's reply. Different kinds may populate
// different fields; orchestrator code uses Kind to interpret. Today
// only AgentActorID is meaningful (worker_task). The GoalSummary is
// mirrored from the spawn-time goal so the caller can confirm
// lifecycle state without a separate agent_status round trip.
type ExecResp struct {
	AgentActorID string
	DisplayName  string
	Goal         gen.GoalSummary
}

// PreflightResult carries the resolved values from an Executor's
// Preflight call. The dispatcher passes it back to Execute so the
// spawn step does not re-resolve the same data; failing to round-
// trip it is a programming error (Execute rejects Preflight == nil).
//
// Only KindConfig is mandatory for every executor; the rest are
// worker_task-specific. Future kinds define their own PreflightResult
// shape — Go's structural typing means any struct satisfies this
// signature at the call site.
type PreflightResult struct {
	KindConfig    domain.AgentKindConfig
	DisplayName   string
	SpawnName     string
	ResolvedSlot  domain.ModelSlot
	// FastSlot / ExecutionSlot / ReviewSlot / SummarySlot carry the parent
	// agent's non-primary runtime slots resolved alongside ResolvedSlot so
	// Execute can copy them to the child spawn (the child inherits the
	// parent's fast/execution/review/summary configuration). The zero value
	// is an empty ([auto]) slot; Execute passes nil for those so the child
	// keeps the default [auto].
	FastSlot         domain.ModelSlot
	ExecutionSlot    domain.ModelSlot
	ReviewSlot       domain.ModelSlot
	SummarySlot      domain.ModelSlot
	// InheritedBundleIDs carries the spawn-time bundle IDs the worker
	// should inherit from the owner agent beyond app-kind defaults —
	// today only [plugin-dev] when the owner has that bundle mounted
	// (ownerInheritedPluginDevBundle). Resolved in Preflight so Execute
	// merges it into the spawn's ExtraBundleIDs via extraBundlesForAppKind
	// (idempotent: an already-present entry is never duplicated). nil
	// means nothing to inherit.
	InheritedBundleIDs []string
	CompactionPolicy   *gen.CompactionPolicy
	PermissionMode     string
}

// Executor is the contract every registered execKind must implement.
// Implementations are responsible for the kind-specific lifecycle:
// worker_task spawns a 1:1 agent; future kinds (crawl, sub_map) own
// their own execution semantics. The dispatcher calls Preflight
// before the universal claim so kind-specific failures leave the
// card untouched; on Preflight success the dispatcher claims the
// card and calls Execute to do the kind-specific work (spawn /
// crawl-start / sub-map-instantiate / ...).
type Executor interface {
	// Kind returns the registered ExecKind constant.
	Kind() ExecKind
	// Preflight runs kind-specific validation and resolution
	// WITHOUT side effects (no claim, no spawn). It runs BEFORE
	// the dispatcher's universal claim. Failures prevent the
	// claim and surface as the dispatch response.
	Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error)
	// Execute runs the kind-specific execution AFTER the
	// dispatcher has successfully claimed the card. req.Preflight
	// MUST carry the result of the matching Preflight call.
	Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error)
}

// Registry maps ExecKind → Executor. The orchestrator instantiates one
// per workspace actor (lazily, on first dispatch) and registers the
// built-in worker_task executor in OnStart. Future kinds register here
// the same way; nothing else in the workspace needs to change.
type Registry struct {
	mu        sync.RWMutex
	executors map[ExecKind]Executor
}

// NewRegistry returns an empty registry. Callers (typically OnStart)
// must Register each built-in executor before exposing dispatch.
func NewRegistry() *Registry {
	return &Registry{executors: map[ExecKind]Executor{}}
}

// Register installs e into the registry. Re-registering the same kind
// replaces the prior executor — this is intentional so future kinds
// can override built-ins for testing or policy-driven replacement
// (e.g. routing all worker_task traffic to a different executor in a
// staging workspace).
func (r *Registry) Register(e Executor) {
	if e == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.executors[e.Kind()] = e
}

// Lookup returns the executor for kind. The second result is false
// when no executor is registered for that kind — orchestrators MUST
// surface this as a stable, string-matchable error so callers can
// decide whether to fall back to a default or fail the dispatch.
func (r *Registry) Lookup(kind ExecKind) (Executor, bool) {
	if kind == "" {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.executors[kind]
	return e, ok
}

// Kinds returns the registered kinds in deterministic order. Used by
// diagnostics, debug surfaces, and tests; not part of the dispatch
// hot path.
func (r *Registry) Kinds() []ExecKind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ExecKind, 0, len(r.executors))
	for k := range r.executors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ErrUnknownExecKind is returned by the dispatcher when the bound task
// card declares an execKind that has no registered executor. The Error
// string is stable (formatted via "%q" + the registry's kind list) so
// callers can string-match to distinguish "kind not registered" from
// transport / claim errors.
type ErrUnknownExecKind struct {
	Kind  ExecKind
	Known []ExecKind
}

func (e *ErrUnknownExecKind) Error() string {
	known := append([]ExecKind(nil), e.Known...)
	sort.Strings(known)
	if len(known) == 0 {
		return fmt.Sprintf("workspace.executor: no executor registered for execKind %q (registry is empty)", e.Kind)
	}
	return fmt.Sprintf("workspace.executor: no executor registered for execKind %q (registered: %s)", e.Kind, strings.Join(known, ", "))
}

// IsUnknownExecKind reports whether err is an ErrUnknownExecKind. It
// exists so callers (and tests) can branch on the typed error without
// importing this package's internals.
func IsUnknownExecKind(err error) bool {
	_, ok := err.(*ErrUnknownExecKind)
	return ok
}

// ensureExecRegistry lazily initializes the workspace's execKind →
// executor registry and registers the built-in worker_task executor.
// Future executors register here the same way; nothing else in the
// workspace changes when a new kind lands. Idempotent — safe to call
// from OnStart and from the dispatcher (which uses it as a safety net
// for tests that bypass OnStart).
func (a *Actor) ensureExecRegistry() {
	if a.execRegistry != nil {
		return
	}
	a.execRegistry = NewRegistry()
	a.execRegistry.Register(newWorkerTaskExecutor(a))
	a.execRegistry.Register(newSubMapExecutor(a))
	a.execRegistry.Register(newCrawlExecutor(a))
	a.execRegistry.Register(newToolCallExecutor(a))
	a.execRegistry.Register(newEventWaitExecutor(a))
	a.execRegistry.Register(newScatterExecutor(a))
	a.execRegistry.Register(newGateExecutor(a))
	a.execRegistry.Register(newScriptExecutor(a))
}

// execKindOf parses the bound task card's frontmatter and returns the
// declared data.exec.kind (trimmed). An empty result means the card did
// not declare one — the dispatcher falls back to DefaultExecKind so
// legacy cards continue to take the worker_task path bit-for-bit.
//
// The lookup is pure (no project round-trip) so it can run on the
// ownerLoop without an extra invoke. Future kinds that read kind-
// specific configuration (crawl's target, sub_map's template id) will
// parse it from req.CardRaw via the same parser.
//
// Implementation note: this is a small, line-based reader tailored to
// the data.exec.kind shape, deliberately not importing the project's
// generic frontmatter parser (which lives in another package). Adding
// a cross-package dependency just to read one string would break the
// "workspace is the orchestrator" layering.
func execKindOf(cardRaw string) ExecKind {
	kind := parseExecKindField(cardRaw)
	return strings.TrimSpace(kind)
}

// scriptFenceWithoutExecKind reports whether a card raw contains a ```spore
// fenced block while declaring no data.exec.kind. Worker-task cards do not
// carry script fences, so the combination is the smell of a script card whose
// exec block was stripped (or was never authored); the dispatcher logs it
// before its worker_task fallback so the degradation is visible in logs.
func scriptFenceWithoutExecKind(cardRaw string) bool {
	if parseExecKindField(cardRaw) != "" {
		return false
	}
	_, ok := scriptcard.ExtractSporeBlock(cardRaw)
	return ok
}

// parseExecKindField locates the `kind:` line nested under
// `data:` and `exec:` in the frontmatter and returns its value as a
// string. It returns "" when the structure is absent or malformed
// (no frontmatter, missing exec block, scalar/empty kind).
//
// The scanner is intentionally permissive: trailing whitespace,
// different indent levels, and inline comments are tolerated. The
// frontmatter must be valid YAML-ish (block scalars only) — flow
// style `data: { exec: { kind: worker_task } }` is NOT supported here
// because the project's inline parser also rejects it for the data
// block; the project validator (validateCardExecKind) is the
// authoritative gate for declaration shape.
func parseExecKindField(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "---") {
		return ""
	}
	rest := raw[3:]
	end := strings.Index(rest, "---")
	if end < 0 {
		return ""
	}
	block := rest[:end]

	var sawData, sawExec bool
	var dataIndent, execIndent int

	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indent := leadingSpacesForExec(line)

		// Top-level fields (indent 0). Reset nested tracking.
		if indent == 0 {
			if !strings.HasPrefix(trimmed, "data:") && !strings.HasPrefix(trimmed, "data :") {
				sawData, sawExec = false, false
				continue
			}
			sawData = true
			sawExec = false
			dataIndent = 0
			continue
		}

		if !sawData {
			continue
		}
		if indent <= dataIndent {
			sawData = false
			sawExec = false
			continue
		}

		// We're inside data:.
		if !sawExec {
			if strings.HasPrefix(trimmed, "exec:") || strings.HasPrefix(trimmed, "exec :") {
				sawExec = true
				execIndent = indent
				continue
			}
			continue
		}

		if indent <= execIndent {
			// exec block ended; reset so a later exec is re-detected.
			sawExec = false
			continue
		}

		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) != "kind" {
			continue
		}
		// Strip surrounding quotes (the project parser uses unquote).
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			first, last := value[0], value[len(value)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		return value
	}
	return ""
}

// leadingSpacesForExec counts leading spaces/tabs on a line. Tab and
// space are both treated as one indent unit (the frontmatter convention
// used elsewhere in this codebase).
func leadingSpacesForExec(line string) int {
	n := 0
	for n < len(line) && (line[n] == ' ' || line[n] == '\t') {
		n++
	}
	return n
}
