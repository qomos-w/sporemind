// Package aistatsquery is a stateless, non-actor query façade over the global
// aistats system actor. It resolves the actor through a caller-supplied ref
// resolver (typically ctx.LookupService) and returns aggregated per-provider+model
// telemetry for load-aware scheduling. It performs no caching and never panics:
// a resolution or invocation failure is returned as an error so callers degrade
// gracefully.
package aistatsquery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// Key uniquely identifies a provider+model pair for lookups. It is
// "provider::model".
type Key string

// MakeKey builds a Key from a provider and model.
func MakeKey(provider, model string) Key {
	return Key(provider + "::" + model)
}

// Split returns the provider and model components of a key.
func (k Key) Split() (provider, model string) {
	parts := strings.SplitN(string(k), "::", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return string(k), ""
}

// Stats is the aggregated telemetry for one provider+model pair over the
// queried window. All rate fields are safe to use even with zero requests
// (they report 0).
type Stats struct {
	Provider string
	Model    string
	// Requests is the total number of recorded requests in the window.
	Requests int64
	// Errors is the number of failed requests.
	Errors int64
	// ErrorRate is Errors/Requests in [0,1].
	ErrorRate float64
	// AvgLatencyMs is the mean full-response latency (LatencySumMs/Requests).
	AvgLatencyMs float64
	// CostTotal is the cumulative cost in the window.
	CostTotal float64
	// InputTokens / OutputTokens are cumulative token counts.
	InputTokens  int64
	OutputTokens int64
}

// Service queries the global aistats actor. It holds no state of its own; the
// resolve callback is invoked on every query so a stale ref is never cached.
type Service struct {
	resolve func() (ref.Ref, error)
	timeout time.Duration
}

// Option configures a Service.
type Option func(*Service)

// WithTimeout sets the per-call timeout (default 5s).
func WithTimeout(d time.Duration) Option {
	return func(s *Service) { s.timeout = d }
}

// New returns a Service that resolves the global aistats actor through resolve.
func New(resolve func() (ref.Ref, error), opts ...Option) *Service {
	s := &Service{resolve: resolve, timeout: 2 * time.Second}
	for _, o := range opts {
		o(s)
	}
	return s
}

// aggregatesResult is the subset of gen.AIStatsAggregatesResp we need.
type aggregatesResult struct {
	Models []gen.AIStatsModelAggregate
}

// QueryProviderModel returns aggregated stats for a single provider+model. An
// absent model reports a zero-value Stats (no error) so callers can treat
// "no data yet" the same as "healthy".
func (s *Service) QueryProviderModel(ctx context.Context, provider, model string) (Stats, error) {
	all, err := s.fetchAggregates(ctx)
	if err != nil {
		return Stats{}, err
	}
	return findModelStats(all, provider, model), nil
}

// QueryMultipleProviderModels returns stats for every requested key in one
// aggregates call. Keys without telemetry are still present with zero-value
// Stats so the caller can distinguish "queried, no data" from "not queried".
func (s *Service) QueryMultipleProviderModels(ctx context.Context, keys []Key) (map[Key]Stats, error) {
	all, err := s.fetchAggregates(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[Key]Stats, len(keys))
	for _, k := range keys {
		provider, model := k.Split()
		out[k] = findModelStats(all, provider, model)
	}
	return out, nil
}

// QueryAllAggregates returns stats for every model in the aistats aggregates
// response, keyed by provider::model. Designed for background refresh: the
// caller stores the full map locally and the strategy reads from it without
// any actor calls.
func (s *Service) QueryAllAggregates(ctx context.Context) (map[Key]Stats, error) {
	all, err := s.fetchAggregates(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[Key]Stats, len(all))
	for _, m := range all {
		k := MakeKey(m.Provider, m.Model)
		out[k] = modelStatsFromAggregate(m)
	}
	return out, nil
}

// fetchAggregates resolves the global aistats actor and invokes
// aistats.aggregates with an empty WorkspaceID (global cross-workspace view).
func (s *Service) fetchAggregates(ctx context.Context) ([]gen.AIStatsModelAggregate, error) {
	r, err := s.resolve()
	if err != nil {
		return nil, fmt.Errorf("aistatsquery: resolve aistats actor: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	call := r.Invoke(callCtx, "aistats.aggregates", gen.AIStatsAggregatesReq{})
	v, err := call.Final(callCtx)
	if err != nil {
		return nil, fmt.Errorf("aistatsquery: aistats.aggregates failed: %w", err)
	}

	var resp gen.AIStatsAggregatesResp
	switch x := v.(type) {
	case gen.AIStatsAggregatesResp:
		resp = x
	case *gen.AIStatsAggregatesResp:
		if x != nil {
			resp = *x
		}
	default:
		body, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("aistatsquery: marshal aggregates response: %w", err)
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("aistatsquery: unmarshal aggregates response: %w", err)
		}
	}
	return resp.Models, nil
}

// findModelStats locates a provider+model in the aggregates and derives Stats.
func findModelStats(models []gen.AIStatsModelAggregate, provider, model string) Stats {
	for _, m := range models {
		if m.Provider == provider && m.Model == model {
			return modelStatsFromAggregate(m)
		}
	}
	return Stats{Provider: provider, Model: model}
}

// modelStatsFromAggregate converts an AIStatsModelAggregate into Stats.
func modelStatsFromAggregate(m gen.AIStatsModelAggregate) Stats {
	c := m.Counters
	st := Stats{
		Provider:     m.Provider,
		Model:        m.Model,
		Requests:     c.RequestCount,
		Errors:       c.ErrorCount,
		CostTotal:    c.CostTotal,
		InputTokens:  c.InputTokens,
		OutputTokens: c.OutputTokens,
	}
	if c.RequestCount > 0 {
		st.ErrorRate = float64(c.ErrorCount) / float64(c.RequestCount)
		st.AvgLatencyMs = float64(c.LatencySumMs) / float64(c.RequestCount)
	}
	return st
}
