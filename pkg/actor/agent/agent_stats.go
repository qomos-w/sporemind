package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// resolveAistatsRef returns the global aistats system actor ref, discovered via
// LookupService. The result is cached on a.
func (a *Actor) resolveAistatsRef(ctx actor.Context) (ref.Ref, error) {
	if a.aistatsRef != nil {
		if _, ok := ctx.LookupID(a.aistatsRef.ID()); ok {
			return a.aistatsRef, nil
		}
		a.aistatsRef = nil
		a.aistatsActorID = ""
	}
	r, ok := ctx.LookupService("aistats")
	if !ok {
		return nil, fmt.Errorf("agent: global aistats actor not available")
	}
	a.aistatsRef = r
	a.aistatsActorID = r.ID().String()
	return r, nil
}

// resolveAistatsRefPure resolves the global aistats actor ref for a stateless
// handler. It never writes actor fields — the cached resolveAistatsRef mutates
// a.aistatsRef/a.aistatsActorID, which would race the owner lane when called
// from the pure loop. LookupService is a thread-safe service-table read, so the
// pure path always resolves the current actor (a restarted aistats re-registers
// under the same service name).
func resolveAistatsRefPure(ctx actor.PureContext) (ref.Ref, error) {
	r, ok := ctx.LookupService("aistats")
	if !ok {
		return nil, fmt.Errorf("agent: global aistats actor not available")
	}
	return r, nil
}

// handleSessionStats proxies to the workspace-scoped aistats actor and returns
// session-level totals for the status bar. It is a thin wrapper so callers do
// not need to know the aistats actor id. Registered stateless: it reads no
// actor fields and only performs a cross-actor query (which must not occupy
// the owner lane).
func (a *Actor) handleSessionStats(ctx actor.PureContext, req domain.AgentSessionStatsReq) (domain.AgentSessionStatsResp, error) {
	aistatsRef, err := resolveAistatsRefPure(ctx)
	if err != nil {
		return domain.AgentSessionStatsResp{}, err
	}

	query := domain.AIStatsQueryReq{
		Scope:   "agent",
		ScopeID: a.actorID,
		Limit:   req.Limit,
	}
	if req.Since != "" {
		query.Since = req.Since
	}
	if req.SessionID != "" {
		query.Scope = "session"
		query.ScopeID = req.SessionID
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := aistatsRef.Invoke(callCtx, "aistats.query", query)
	v, err := call.Final(callCtx)
	if err != nil {
		return domain.AgentSessionStatsResp{}, fmt.Errorf("agent: query aistats failed: %w", err)
	}
	var resp domain.AIStatsQueryResp
	switch x := v.(type) {
	case domain.AIStatsQueryResp:
		resp = x
	case *domain.AIStatsQueryResp:
		if x != nil {
			resp = *x
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return domain.AgentSessionStatsResp{}, fmt.Errorf("agent: marshal aistats query response: %w", err)
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return domain.AgentSessionStatsResp{}, fmt.Errorf("agent: unmarshal aistats query response: %w", err)
		}
	}

	counters := resp.Counters
	latestProvider := ""
	latestModel := ""
	if len(resp.Records) > 0 {
		latest := resp.Records[len(resp.Records)-1]
		latestProvider = latest.Provider
		latestModel = latest.Model
		if latest.ResponseModel != "" {
			latestModel = latest.ResponseModel
		}
	}

	return domain.AgentSessionStatsResp{
		Counters:       counters,
		CacheHitRate:   llmclient.CacheHitRate(int(counters.InputTokens), int(counters.CacheCreationInputTokens), int(counters.CacheReadInputTokens)),
		LatestProvider: latestProvider,
		LatestModel:    latestModel,
	}, nil
}

// costRateKey identifies a provider/model cost rate in the agent cache.
type costRateKey struct {
	provider string
	model    string
}

// costRateFor returns the cached cost rate for a provider/model, refreshing
// from the workspace aistats actor if the cache is empty or expired. Failures
// are logged and return (zero, false) so the caller can skip cost decoration.
func (a *Actor) costRateFor(ctx actor.Context, provider, model string) (llmclient.CostRate, bool) {
	a.costMu.RLock()
	if rate, ok := a.costRates[costRateKey{provider, model}]; ok && time.Now().Before(a.costExpires) {
		a.costMu.RUnlock()
		return rate, true
	}
	a.costMu.RUnlock()

	aistatsRef, err := a.resolveAistatsRef(ctx)
	if err != nil {
		ctx.Logger().Debug("agent: resolve aistats for cost rate failed", "error", err, "provider", provider, "model", model)
		return llmclient.CostRate{}, false
	}

	callCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := aistatsRef.Invoke(callCtx, "aistats.cost_list", gen.AIStatsCostListReq{Provider: provider, Model: model})
	v, err := call.Final(callCtx)
	if err != nil {
		ctx.Logger().Error("agent: list cost rates failed", "error", err, "provider", provider, "model", model)
		return llmclient.CostRate{}, false
	}
	var resp gen.AIStatsCostListResp
	switch x := v.(type) {
	case domain.AIStatsCostListResp:
		resp = x
	case *domain.AIStatsCostListResp:
		if x != nil {
			resp = *x
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			ctx.Logger().Error("agent: marshal cost list response failed", "error", err)
			return llmclient.CostRate{}, false
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			ctx.Logger().Error("agent: unmarshal cost list response failed", "error", err)
			return llmclient.CostRate{}, false
		}
	}
	if len(resp.Rates) == 0 {
		return llmclient.CostRate{}, false
	}

	rate := llmclientCostRate(resp.Rates[0])
	a.costMu.Lock()
	if a.costRates == nil {
		a.costRates = make(map[costRateKey]llmclient.CostRate)
	}
	a.costRates[costRateKey{provider, model}] = rate
	a.costExpires = time.Now().Add(5 * time.Minute)
	a.costMu.Unlock()
	return rate, true
}

// llmclientCostRate converts a schema cost rate into the llmclient calculator shape.
func llmclientCostRate(rate gen.AIStatsCostRate) llmclient.CostRate {
	cr := llmclient.CostRate{
		Input:      rate.CostInput,
		Output:     rate.CostOutput,
		CacheRead:  rate.CostCacheRead,
		CacheWrite: rate.CostCacheWrite,
	}
	for _, tier := range rate.Tiers {
		cr.Tiers = append(cr.Tiers, llmclient.CostTier{
			InputTokensAbove: tier.InputTokensAbove,
			Input:            tier.CostInput,
			Output:           tier.CostOutput,
			CacheRead:        tier.CostCacheRead,
			CacheWrite:       tier.CostCacheWrite,
		})
	}
	return cr
}

// submitStatsRecord submits a single aistats record as an event. It is the
// turn-engine-facing sink for failed-dispatch records (e.g. idle timeouts)
// that the aggregator cannot attribute on its own. The event path needs no
// ref resolution, no PendingTable slot, and no goroutine: aistats consumes
// from its subscription with drop-oldest backpressure.
func (a *Actor) submitStatsRecord(ctx actor.Context, record domain.AIStatsRecord) {
	if err := ctx.EmitEvent("aistats.record", record); err != nil {
		slog.Debug("agent: emit stats record event failed", "error", err, "id", record.ID)
	}
}
