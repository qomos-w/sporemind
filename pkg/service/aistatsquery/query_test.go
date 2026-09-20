package aistatsquery

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

type fakeRef struct {
	result any
	err    error
}

var _ ref.Ref = (*fakeRef)(nil)

func (f *fakeRef) ID() id.ActorID          { return id.ActorID{} }
func (f *fakeRef) Service() (string, bool) { return "", false }
func (f *fakeRef) Invoke(_ context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	if f.err != nil {
		return invoke.NewCall(invoke.CallModeUnary, &errStream{err: f.err})
	}
	return invoke.NewCall(invoke.CallModeUnary, &oneShotStream{v: f.result})
}

type oneShotStream struct{ v any; done bool }

func (s *oneShotStream) Recv() (any, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.v, nil
}
func (s *oneShotStream) RecvRaw() ([]byte, error) { return nil, io.EOF }
func (s *oneShotStream) Close() error              { return nil }

type errStream struct{ err error }

func (s *errStream) Recv() (any, error)       { return nil, s.err }
func (s *errStream) RecvRaw() ([]byte, error) { return nil, s.err }
func (s *errStream) Close() error              { return nil }

func TestQueryProviderModel_Found(t *testing.T) {
	svc := New(func() (ref.Ref, error) {
		return &fakeRef{result: gen.AIStatsAggregatesResp{
			Models: []gen.AIStatsModelAggregate{
				{Provider: "openai", Model: "gpt-4o", Counters: gen.AIStatsCounters{
					RequestCount: 10, ErrorCount: 2, LatencySumMs: 5000, CostTotal: 1.5,
					InputTokens: 1000, OutputTokens: 2000,
				}},
				{Provider: "anthropic", Model: "claude", Counters: gen.AIStatsCounters{
					RequestCount: 5, ErrorCount: 0, LatencySumMs: 1000,
				}},
			},
		}}, nil
	})

	st, err := svc.QueryProviderModel(context.Background(), "openai", "gpt-4o")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if st.Requests != 10 {
		t.Fatalf("requests: got %d, want 10", st.Requests)
	}
	if st.ErrorRate != 0.2 {
		t.Fatalf("error rate: got %f, want 0.2", st.ErrorRate)
	}
	if st.AvgLatencyMs != 500 {
		t.Fatalf("avg latency: got %f, want 500", st.AvgLatencyMs)
	}
	if st.CostTotal != 1.5 {
		t.Fatalf("cost: got %f, want 1.5", st.CostTotal)
	}
}

func TestQueryProviderModel_NotFound(t *testing.T) {
	svc := New(func() (ref.Ref, error) {
		return &fakeRef{result: gen.AIStatsAggregatesResp{}}, nil
	})
	st, err := svc.QueryProviderModel(context.Background(), "x", "y")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if st.Requests != 0 || st.ErrorRate != 0 || st.AvgLatencyMs != 0 {
		t.Fatalf("zero-value stats expected, got %+v", st)
	}
	if st.Provider != "x" || st.Model != "y" {
		t.Fatalf("provider/model not echoed: got %+v", st)
	}
}

func TestQueryMultipleProviderModels(t *testing.T) {
	svc := New(func() (ref.Ref, error) {
		return &fakeRef{result: gen.AIStatsAggregatesResp{
			Models: []gen.AIStatsModelAggregate{
				{Provider: "p1", Model: "m1", Counters: gen.AIStatsCounters{RequestCount: 3, ErrorCount: 1, LatencySumMs: 900}},
				{Provider: "p2", Model: "m2", Counters: gen.AIStatsCounters{RequestCount: 0}},
			},
		}}, nil
	})

	keys := []Key{MakeKey("p1", "m1"), MakeKey("p2", "m2"), MakeKey("p3", "m3")}
	out, err := svc.QueryMultipleProviderModels(context.Background(), keys)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(out))
	}
	st1 := out[MakeKey("p1", "m1")]
	if st1.Requests != 3 || st1.ErrorRate != 1.0/3.0 || st1.AvgLatencyMs != 300 {
		t.Fatalf("p1/m1 stats wrong: %+v", st1)
	}
	st3 := out[MakeKey("p3", "m3")]
	if st3.Requests != 0 {
		t.Fatalf("p3/m3 should be zero-value, got %+v", st3)
	}
}

func TestQuery_ResolveFailure(t *testing.T) {
	svc := New(func() (ref.Ref, error) {
		return nil, errors.New("actor not found")
	})
	_, err := svc.QueryProviderModel(context.Background(), "p", "m")
	if err == nil {
		t.Fatal("expected error on resolve failure")
	}
}

func TestQuery_InvokeFailure(t *testing.T) {
	svc := New(func() (ref.Ref, error) {
		return &fakeRef{err: errors.New("timeout")}, nil
	})
	_, err := svc.QueryProviderModel(context.Background(), "p", "m")
	if err == nil {
		t.Fatal("expected error on invoke failure")
	}
}

func TestKeySplit(t *testing.T) {
	k := MakeKey("openai", "gpt-4o")
	p, m := k.Split()
	if p != "openai" || m != "gpt-4o" {
		t.Fatalf("split: got %q/%q", p, m)
	}
}