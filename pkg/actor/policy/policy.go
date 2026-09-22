// Package policy implements the unified policy-judgment abstraction ("小脑"):
// typed questions (Choice / Score / Noul, mirroring the Jev System One
// primitives) evaluated against a state, returning typed answers with
// probabilities, confidence, and backend provenance.
//
// Backends:
//   - jev — direct HTTP to the Jev System One API. Cheapest and calibrated
//     (RLCD-trained probabilities).
//   - llm — prompt + strict JSON output via aiaggregator.dispatch with a
//     configured ModelUnit. Availability fallback only; self-reported
//     probabilities are marked Calibrated=false so consumers weight them.
//
// Degradation contract: failover fires on availability faults only (network
// error, 429, 5xx, missing credential). Low confidence is a legitimate answer
// and never triggers failover. When every backend fails the response carries
// Error with empty Answers — consumers fail open to their own rules.
//
// Concurrency (CLAUDE.md 高频 callable 注册模式): policy.decide and
// policy.status are PureContext handlers — they read a config snapshot under
// RLock and perform network IO on their own goroutine, never touching a lane.
// policy.configure is a low-frequency owner-lane mutate.
package policy

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
)

const (
	defaultJevModel    = "jev-latest"
	defaultJevEndpoint = "https://api.typesafe.ai/v1/systemone"

	jevHTTPTimeout = 15 * time.Second
	llmCallTimeout = 90 * time.Second

	backendAuto = "auto"
	backendJev  = "jev"
	backendLLM  = "llm"
)

// Actor owns the policy configuration and exposes the decide surface.
type Actor struct {
	actor.Host

	store        persist.Persist
	actorID      string
	http         *http.Client
	lifecycleCtx context.Context

	mu    sync.RWMutex
	state policyStore

	// Backend funcs are fields for test injection; defaults are wired in OnInit.
	jevBackend func(callCtx context.Context, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error)
	llmBackend func(pctx actor.PureContext, callCtx context.Context, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error)
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "policy" }

// OnInit loads persisted configuration.
func (a *Actor) OnInit(ctx actor.Context) error {
	var err error
	a.store, err = persist.New(config.PersistConfig("policy"))
	if err != nil {
		return err
	}
	a.actorID = ctx.Self().ID().String()
	a.http = &http.Client{Timeout: jevHTTPTimeout}
	a.lifecycleCtx = ctx.Lifecycle()

	a.jevBackend = func(callCtx context.Context, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return jevDecide(callCtx, a.http, cfg, req)
	}
	a.llmBackend = func(pctx actor.PureContext, callCtx context.Context, cfg PolicySnapshot, req gen.PolicyDecideReq) (map[string]gen.PolicyAnswer, error) {
		return llmDecide(pctx, callCtx, cfg, req)
	}

	if err := a.Load(); err != nil {
		ctx.Logger().Error("policy: load failed", "err", err)
	}
	return nil
}

// OnStart registers the policy callables and exposes the service domain.
func (a *Actor) OnStart(ctx actor.Context) error {
	if err := ctx.Register("policy.decide", a.handleDecide, actor.Public(),
		actor.WithDescription("Evaluate typed questions (Choice/Score/Noul) against a text state and return typed answers with probabilities, confidence, and backend provenance (jev | llm; llm answers are marked Calibrated=false). All questions are evaluated in one batch. Failover between backends happens on availability faults only; when all backends fail the response carries Error with empty Answers — fall back to your own rules then."),
	); err != nil {
		return fmt.Errorf("policy: register decide: %w", err)
	}
	if err := ctx.Register("policy.status", a.handleStatus, actor.Public()); err != nil {
		return fmt.Errorf("policy: register status: %w", err)
	}
	if err := ctx.Register("policy.configure", a.handleConfigure, actor.AdminOnly(),
		actor.WithDescription("Configure the policy judge: backend mode (auto | jev | llm), Jev API key/model/endpoint, and the LLM fallback ModelUnit. Jev.ApiKey empty preserves the stored key (redacted edit flow)."),
	); err != nil {
		return fmt.Errorf("policy: register configure: %w", err)
	}
	if err := ctx.RegisterDomain("policy").Expose(); err != nil {
		return fmt.Errorf("policy: expose service: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Persistence — implements persist.Persistent
// ---------------------------------------------------------------------------

// policyStore is the on-disk shape: backend mode plus the two backend configs.
// The Jev API key lives only here, in the policy persist namespace.
type policyStore struct {
	Backend string              `json:"backend,omitempty"` // auto | jev | llm
	Jev     gen.PolicyJevConfig `json:"jev,omitempty"`
	LLM     gen.PolicyLLMConfig `json:"llm,omitempty"`
}

// Load restores the persisted state into memory. Implements persist.Persistent.
func (a *Actor) Load() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("policy"))
		if err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := persist.LoadOrZero(a.store, a.actorID, &a.state); err != nil {
		return err
	}
	if a.state.Backend == "" {
		a.state.Backend = backendAuto
	}
	return nil
}

// Save persists the in-memory state to disk. Implements persist.Persistent.
func (a *Actor) Save() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.saveLocked()
}

func (a *Actor) saveLocked() error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("policy"))
		if err != nil {
			return err
		}
	}
	return a.store.Save(a.actorID, a.state)
}

// PolicySnapshot is the immutable config copy pure handlers work off.
type PolicySnapshot struct {
	Mode        string // auto | jev | llm
	JevKey      string
	JevModel    string
	JevEndpoint string
	LLMUnit     *gen.ModelUnit
}

// snapshotLocked returns the effective config with defaults applied.
// Caller holds a.mu (read or write).
func (a *Actor) snapshotLocked() PolicySnapshot {
	mode := a.state.Backend
	if mode == "" {
		mode = backendAuto
	}
	model := a.state.Jev.Model
	if model == "" {
		model = defaultJevModel
	}
	endpoint := a.state.Jev.Endpoint
	if endpoint == "" {
		endpoint = defaultJevEndpoint
	}
	return PolicySnapshot{
		Mode:        mode,
		JevKey:      a.state.Jev.APIKey,
		JevModel:    model,
		JevEndpoint: endpoint,
		LLMUnit:     a.state.LLM.Unit,
	}
}

// ---------------------------------------------------------------------------
// Callables
// ---------------------------------------------------------------------------

// handleDecide is the pure judge surface: config snapshot read, then network
// IO on this handler's own goroutine. It never mutates actor state.
func (a *Actor) handleDecide(ctx actor.PureContext, req gen.PolicyDecideReq) (gen.PolicyDecideResp, error) {
	if err := validateDecideReq(req); err != nil {
		return gen.PolicyDecideResp{}, err
	}

	a.mu.RLock()
	snap := a.snapshotLocked()
	a.mu.RUnlock()

	order := backendOrder(snap.Mode, req.Backend)

	callCtx, cancel := context.WithTimeout(a.lifecycleCtx, llmCallTimeout)
	defer cancel()

	start := time.Now()
	var reasons []string
	for i, b := range order {
		var answers map[string]gen.PolicyAnswer
		var err error
		switch b {
		case backendJev:
			answers, err = a.jevBackend(callCtx, snap, req)
		case backendLLM:
			answers, err = a.llmBackend(ctx, callCtx, snap, req)
		}
		if err == nil {
			return gen.PolicyDecideResp{
				Answers:   answers,
				Backend:   b,
				Degraded:  i > 0 && len(order) > 1,
				LatencyMs: time.Since(start).Milliseconds(),
			}, nil
		}
		reasons = append(reasons, fmt.Sprintf("%s: %v", b, err))
		// Failover only on availability faults; contract faults (e.g. HTTP 400
		// from a malformed request) abort loudly so drift is visible.
		if !isUnavailable(err) {
			break
		}
	}

	// Every backend faulted (or a contract fault aborted the chain): fail-open
	// contract — empty answers + the collected diagnostics.
	return gen.PolicyDecideResp{
		Answers:   map[string]gen.PolicyAnswer{},
		Backend:   "",
		LatencyMs: time.Since(start).Milliseconds(),
		Error:     strings.Join(reasons, "; "),
	}, nil
}

// handleStatus is the redacted configuration read.
func (a *Actor) handleStatus(_ actor.PureContext, _ gen.PolicyStatusReq) (gen.PolicyStatusResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	snap := a.snapshotLocked()
	return gen.PolicyStatusResp{
		Backend:       snap.Mode,
		JevConfigured: snap.JevKey != "",
		LLMConfigured: snap.LLMUnit != nil && snap.LLMUnit.Model != "",
		LLMUnit:       snap.LLMUnit,
		JevModel:      snap.JevModel,
		JevEndpoint:   snap.JevEndpoint,
	}, nil
}

// handleConfigure mutates the persisted policy configuration (owner lane).
func (a *Actor) handleConfigure(_ actor.Context, req gen.PolicyConfigureReq) (gen.PolicyConfigureResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if req.Backend != "" {
		switch req.Backend {
		case backendAuto, backendJev, backendLLM:
			a.state.Backend = req.Backend
		default:
			return gen.PolicyConfigureResp{}, fmt.Errorf("policy.configure: unknown backend %q (want auto|jev|llm)", req.Backend)
		}
	}
	if req.Jev != nil {
		if req.Jev.APIKey != "" {
			a.state.Jev.APIKey = req.Jev.APIKey
		}
		if req.Jev.Model != "" {
			a.state.Jev.Model = req.Jev.Model
		}
		if req.Jev.Endpoint != "" {
			a.state.Jev.Endpoint = req.Jev.Endpoint
		}
	}
	if req.LLM != nil {
		a.state.LLM.Unit = req.LLM.Unit
	}

	if err := a.saveLocked(); err != nil {
		return gen.PolicyConfigureResp{}, fmt.Errorf("policy.configure: save: %w", err)
	}
	snap := a.snapshotLocked()
	status := gen.PolicyStatusResp{
		Backend:       snap.Mode,
		JevConfigured: snap.JevKey != "",
		LLMConfigured: snap.LLMUnit != nil && snap.LLMUnit.Model != "",
		LLMUnit:       snap.LLMUnit,
		JevModel:      snap.JevModel,
		JevEndpoint:   snap.JevEndpoint,
	}
	return gen.PolicyConfigureResp{Status: status}, nil
}

// ---------------------------------------------------------------------------
// Failover chain
// ---------------------------------------------------------------------------

// backendOrder resolves the effective try-order for one call. The optional
// per-call override pins a single backend. Under auto mode the order is
// jev → llm; a non-first backend answering means Degraded=true.
func backendOrder(mode, override string) []string {
	if override == backendJev || override == backendLLM {
		return []string{override}
	}
	switch mode {
	case backendJev:
		return []string{backendJev}
	case backendLLM:
		return []string{backendLLM}
	default: // auto
		return []string{backendJev, backendLLM}
	}
}

// validateDecideReq checks the request shape before any backend is touched.
func validateDecideReq(req gen.PolicyDecideReq) error {
	if req.State == "" {
		return fmt.Errorf("policy.decide: State is required")
	}
	if len(req.Questions) == 0 {
		return fmt.Errorf("policy.decide: at least one question is required")
	}
	for key, q := range req.Questions {
		switch q.Type {
		case "choice":
			if len(q.Choices) == 0 {
				return fmt.Errorf("policy.decide: question %q: choice requires Choices", key)
			}
		case "score":
			if len(q.Levels) < 2 {
				return fmt.Errorf("policy.decide: question %q: score requires ≥2 Levels", key)
			}
		case "noul":
			// instructions only
		default:
			return fmt.Errorf("policy.decide: question %q: unknown type %q (want choice|score|noul)", key, q.Type)
		}
		if q.Instructions == "" {
			return fmt.Errorf("policy.decide: question %q: Instructions is required", key)
		}
	}
	return nil
}
