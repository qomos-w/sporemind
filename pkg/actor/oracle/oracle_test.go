package oracle

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/plan"
	"github.com/qomos-w/gospore/promise"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

type fakePlanner struct {
	callFunc func(ctx context.Context, target ref.Ref, callID string, payload any) (any, error)
}

func (p fakePlanner) Plan(target ref.Ref, callID string, payload any, opts ...plan.Option) (plan.Node, error) {
	return nil, fmt.Errorf("not implemented")
}

func (p fakePlanner) Call(ctx context.Context, target ref.Ref, callID string, payload any) *promise.Promise[any] {
	return promise.Async(func(resolve func(any), reject func(any)) {
		result, err := p.callFunc(ctx, target, callID, payload)
		if err != nil {
			reject(err)
			return
		}
		resolve(result)
	})
}

func (p fakePlanner) Stream(ctx context.Context, target ref.Ref, callID string, payload any, onChunk func(any) error) *promise.Promise[any] {
	return promise.Reject[any](fmt.Errorf("not implemented"))
}

func newTestActorWithBundles(t *testing.T, bundleIDs []string) (*Actor, actor.Context) {
	t.Helper()
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	cfgBytes, _ := json.Marshal(domain.AgentKindConfig{
		Kind:             "coder",
		DefaultBundleIDs: bundleIDs,
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return nil, true
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "workspace.get_agent_kind_config" {
					return cfgBytes, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return a, ctx
}

func TestHandleCapabilityDiscoverRequiresAgentKind(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleCapabilityDiscover(ctx, domain.OracleCapabilityDiscoverReq{})
	if err == nil {
		t.Fatal("expected error for empty agentKind")
	}
}

func TestHandleCapabilityDiscoverFiltersByAllowedCallables(t *testing.T) {
	// workspace-tools bundle declares: workspace.list_project, workspace.list_agents, workspace.account
	a, ctx := newTestActorWithBundles(t, []string{"builtin:bundle:workspace-tools"})

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: []string{"workspace.list_project", "workspace.list_agents", "workspace.account", "unknown.tool"},
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(resp.Items))
	}
	if resp.Items[0].ID != "workspace.list_project" {
		t.Errorf("expected first candidate workspace.list_project, got %s", resp.Items[0].ID)
	}
	if resp.Items[0].Name != "list_project" {
		t.Errorf("expected first candidate bare name list_project, got %s", resp.Items[0].Name)
	}
	if resp.Items[0].RelevanceScore != 1.0 {
		t.Errorf("expected relevance score 1.0, got %f", resp.Items[0].RelevanceScore)
	}
	if resp.Items[0].Rationale == "" {
		t.Errorf("expected non-empty rationale")
	}
}

func TestHandleCapabilityDiscoverRespectsLimit(t *testing.T) {
	// file-tools bundle declares 8 callables; limit to 3
	a, ctx := newTestActorWithBundles(t, []string{"builtin:bundle:file-tools"})

	// Resolve expected tools from the bundle to use as AvailableToolNames
	allowed := agentkit.CallableIDsForBundles([]string{"builtin:bundle:file-tools"})
	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: allowed,
		Limit:              3,
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(resp.Items))
	}
}

func TestHandleCapabilityDiscoverDeduplicates(t *testing.T) {
	a, ctx := newTestActorWithBundles(t, []string{"builtin:bundle:workspace-tools"})

	allowed := agentkit.CallableIDsForBundles([]string{"builtin:bundle:workspace-tools"})
	dupNames := []string{allowed[0], allowed[0], allowed[0]}
	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: dupNames,
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 candidate after dedup, got %d", len(resp.Items))
	}
}

func TestHandleCapabilityDiscoverDefaultsLimitTo50(t *testing.T) {
	// Use all known bundles to get a large tool set.
	allBundleIDs := []string{}
	for _, c := range agentkit.BuiltinCards {
		allBundleIDs = append(allBundleIDs, c.Title)
	}
	allowed := agentkit.CallableIDsForBundles(allBundleIDs)
	if len(allowed) < 51 {
		t.Skipf("not enough bundle tools (%d) to test default limit", len(allowed))
	}
	a, ctx := newTestActorWithBundles(t, allBundleIDs)

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: allowed,
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 50 {
		t.Fatalf("expected default limit 50, got %d", len(resp.Items))
	}
}

func TestHandleCapabilityDiscoverFallbackOnMissingWorkspace(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	// No workspace service registered — LookupServiceFn returns false by default.

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: []string{"filesystem.list", "filesystem.read"},
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 0 {
		t.Fatalf("expected 0 candidates when workspace unavailable, got %d", len(resp.Items))
	}
}

func TestHandleCapabilityDiscoverFallbackOnPlannerError(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return nil, true
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				return nil, fmt.Errorf("planner error")
			},
		}
	}

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: []string{"filesystem.list", "filesystem.read"},
	}
	resp, err := a.handleCapabilityDiscover(ctx, req)
	if err != nil {
		t.Fatalf("handleCapabilityDiscover: %v", err)
	}

	if len(resp.Items) != 0 {
		t.Fatalf("expected 0 candidates when planner fails, got %d", len(resp.Items))
	}
}

func TestHandleCapabilityExplainRequiresID(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleCapabilityExplain(ctx, domain.OracleCapabilityExplainReq{})
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestHandleCapabilityExplainReturnsGuidance(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleCapabilityExplain(ctx, domain.OracleCapabilityExplainReq{ID: "fork_agent"})
	if err != nil {
		t.Fatalf("handleCapabilityExplain: %v", err)
	}
	if resp.Summary == "" {
		t.Errorf("expected non-empty summary")
	}
	if resp.ExpandMode != "fork_child" {
		t.Errorf("expected expand mode fork_child for agent.fork_*, got %s", resp.ExpandMode)
	}
}

func TestHandleReportDiagnostic(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	err := a.handleReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
		Severity: "error",
		Source:   "dispatch",
		Message:  "something failed",
	})
	if err != nil {
		t.Fatalf("handleReportDiagnostic: %v", err)
	}
	if len(a.diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(a.diagnostics))
	}
	if a.diagnostics[0].Severity != "error" {
		t.Errorf("expected severity error, got %s", a.diagnostics[0].Severity)
	}
}

func TestHandleListDiagnosticsFiltersAndPaginates(t *testing.T) {
	a := &Actor{}
	now := time.Now().UTC()
	a.diagnostics = []domain.Diagnostic{
		{ID: "d1", Severity: "error", Source: "dispatch", Message: "m1", Timestamp: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), AgentID: "a1"},
		{ID: "d2", Severity: "warning", Source: "tool_call", Message: "m2", Timestamp: now.Add(-1 * time.Hour).Format(time.RFC3339Nano), AgentID: "a2"},
		{ID: "d3", Severity: "info", Source: "agent", Message: "m3", Timestamp: now.Format(time.RFC3339Nano), AgentID: "a1"},
	}

	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleListDiagnostics(ctx, domain.OracleListDiagnosticsReq{
		Severity: "error",
		Limit:    10,
	})
	if err != nil {
		t.Fatalf("handleListDiagnostics: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "d1" {
		t.Fatalf("expected 1 error diagnostic d1, got %+v", resp.Items)
	}

	resp, err = a.handleListDiagnostics(ctx, domain.OracleListDiagnosticsReq{
		AgentID: "a1",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("handleListDiagnostics: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 diagnostics for agent a1, got %d", len(resp.Items))
	}

	resp, err = a.handleListDiagnostics(ctx, domain.OracleListDiagnosticsReq{
		Limit:  2,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("handleListDiagnostics: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 diagnostics with limit 2, got %d", len(resp.Items))
	}
	// Newest first: d3, d2, d1. Offset 1 => d2, d1.
	if resp.Items[0].ID != "d2" || resp.Items[1].ID != "d1" {
		t.Fatalf("expected [d2, d1], got [%s, %s]", resp.Items[0].ID, resp.Items[1].ID)
	}
}

func TestHandleListDiagnosticsRespectsMaxLimit(t *testing.T) {
	a := &Actor{}
	for i := 0; i < 250; i++ {
		a.diagnostics = append(a.diagnostics, domain.Diagnostic{
			ID:        fmt.Sprintf("d%d", i),
			Severity:  "info",
			Source:    "agent",
			Message:   "m",
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		})
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleListDiagnostics(ctx, domain.OracleListDiagnosticsReq{Limit: 500})
	if err != nil {
		t.Fatalf("handleListDiagnostics: %v", err)
	}
	if len(resp.Items) != maxDiagnosticsLimit {
		t.Fatalf("expected max limit %d, got %d", maxDiagnosticsLimit, len(resp.Items))
	}
}

func TestHandleGetDiagnostic(t *testing.T) {
	a := &Actor{}
	a.diagnostics = []domain.Diagnostic{
		{ID: "d1", Severity: "error", Source: "dispatch", Message: "m1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano)},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleGetDiagnostic(ctx, domain.OracleGetDiagnosticReq{ID: "d1"})
	if err != nil {
		t.Fatalf("handleGetDiagnostic: %v", err)
	}
	if resp.ID != "d1" {
		t.Errorf("expected d1, got %s", resp.ID)
	}

	_, err = a.handleGetDiagnostic(ctx, domain.OracleGetDiagnosticReq{ID: "missing"})
	if err == nil {
		t.Fatal("expected error for missing diagnostic")
	}
}

// TestReportDiagnosticToolcallFieldsRoundtrip verifies that the toolcall metadata
// (TargetService/ToolUseId/Input/Output) survives report → store → summary list →
// detail retrieval. The Problems panel reads both listDiagnostics (returns summary)
// and getDiagnostic (returns full record); both must carry the new fields.
func TestReportDiagnosticToolcallFieldsRoundtrip(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	req := domain.OracleReportDiagnosticReq{
		Severity:      "error",
		Source:        "tool_call",
		Message:       "tool execution failed",
		TurnID:        "turn-1",
		StepID:        "step-1",
		CallableID:    "project.read",
		TargetService: "project",
		ToolUseID:     "tu_abc",
		Input:         `{"Path":"foo.go"}`,
		Output:        "permission denied",
		Unit:          &domain.ModelUnit{Model: "claude-3", Provider: "anthropic"},
		HttpStatus:    0,
		RawData:       `{"id":"tu_abc","type":"function"}`,
	}
	if err := a.handleReportDiagnostic(ctx, req); err != nil {
		t.Fatalf("handleReportDiagnostic: %v", err)
	}

	// Storage path: the full Diagnostic carries all fields.
	if len(a.diagnostics) != 1 {
		t.Fatalf("expected 1 stored diagnostic, got %d", len(a.diagnostics))
	}
	stored := a.diagnostics[0]
	if stored.CallableID != "project.read" ||
		stored.TargetService != "project" ||
		stored.ToolUseID != "tu_abc" ||
		stored.Input != `{"Path":"foo.go"}` ||
		stored.Output != "permission denied" {
		t.Errorf("stored diagnostic dropped toolcall fields: %+v", stored)
	}

	// List path: DiagnosticSummary must mirror Diagnostic field-for-field,
	// otherwise the Problems panel loses data on page refresh.
	listResp, err := a.handleListDiagnostics(ctx, domain.OracleListDiagnosticsReq{Limit: 10})
	if err != nil {
		t.Fatalf("handleListDiagnostics: %v", err)
	}
	if len(listResp.Items) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(listResp.Items))
	}
	summary := listResp.Items[0]
	if summary.CallableID != "project.read" ||
		summary.TargetService != "project" ||
		summary.ToolUseID != "tu_abc" ||
		summary.Input != `{"Path":"foo.go"}` ||
		summary.Output != "permission denied" ||
		summary.Unit == nil ||
		summary.RawData != `{"id":"tu_abc","type":"function"}` {
		t.Errorf("summary dropped toolcall or previously-dropped fields: %+v", summary)
	}

	// Detail path: getDiagnostic must return the same.
	detail, err := a.handleGetDiagnostic(ctx, domain.OracleGetDiagnosticReq{ID: stored.ID})
	if err != nil {
		t.Fatalf("handleGetDiagnostic: %v", err)
	}
	if detail.CallableID != stored.CallableID || detail.TargetService != stored.TargetService {
		t.Errorf("detail fields do not match stored: detail=%+v stored=%+v", detail, stored)
	}
}

// TestReportDiagnosticTruncatesLargePayload verifies that very large Input/Output
// are not stored verbatim into the 200-entry ring. The recorder in the agent
// turn engine is responsible for truncation (truncateForDiagnostic), but if a caller
// ever passes raw payload the oracle itself should still bound maxRawDataBytes for RawData.
func TestReportDiagnosticTruncatesLargePayload(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	// 100 KB Input + 100 KB RawData — RawData should be capped to maxRawDataBytes.
	huge := strings.Repeat("x", 100*1024)
	if err := a.handleReportDiagnostic(ctx, domain.OracleReportDiagnosticReq{
		Severity: "warning",
		Source:   "tool_call",
		Message:  "big",
		Input:    huge,
		RawData:  huge,
	}); err != nil {
		t.Fatalf("handleReportDiagnostic: %v", err)
	}
	if len(a.diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(a.diagnostics))
	}
	stored := a.diagnostics[0]
	if len(stored.RawData) > maxRawDataBytes+50 {
		t.Errorf("RawData not truncated: got %d bytes (cap=%d)", len(stored.RawData), maxRawDataBytes)
	}
	if len(stored.Input) == len(huge) {
		// Oracle stores what the caller sends for Input (no truncation in oracle.go);
		// the truncation contract lives in the agent recorder. This assertion
		// documents that asymmetry.
		t.Logf("note: oracle.go does not truncate Input — the agent recorder (truncateForDiagnostic) does")
	}
}

func newTestActorWithServices(t *testing.T, svcs []domain.ServiceInfo) (*Actor, actor.Context) {
	t.Helper()
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "runtime.list_services" {
					return domain.RuntimeListServicesResp{Items: svcs}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return a, ctx
}

func TestHandleSearchServices(t *testing.T) {
	svcs := []domain.ServiceInfo{
		{Name: "workspace", ActorID: "a1", ActorType: "workspace"},
		{Name: "oracle", ActorID: "a2", ActorType: "oracle"},
		{Name: "filesystem", ActorID: "a3", ActorType: "filesystem"},
	}
	a, ctx := newTestActorWithServices(t, svcs)

	resp, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{})
	if err != nil {
		t.Fatalf("handleSearchServices: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 services, got %d", len(resp.Items))
	}

	filtered, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{Query: "work"})
	if err != nil {
		t.Fatalf("handleSearchServices filtered: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "workspace" {
		t.Fatalf("expected 1 workspace service, got %+v", filtered.Items)
	}

	limited, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{Limit: 2})
	if err != nil {
		t.Fatalf("handleSearchServices limited: %v", err)
	}
	if len(limited.Items) != 2 {
		t.Fatalf("expected 2 services with limit, got %d", len(limited.Items))
	}
}

func TestHandleSearchServicesRequiresPlanner(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	_, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{})
	if err == nil {
		t.Fatal("expected error when planner is unavailable")
	}
}

// countingDiscoveryCtx returns an oracle + context whose planner counts
// workspace.get_agent_kind_config fetches and serves the given bundles.
func countingDiscoveryCtx(t *testing.T, fetches *int32, bundles ...string) (*Actor, actor.Context) {
	t.Helper()
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	cfgBytes, _ := json.Marshal(domain.AgentKindConfig{
		Kind:             "coder",
		DefaultBundleIDs: bundles,
	})
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), nil), name == "workspace"
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "workspace.get_agent_kind_config" {
					atomic.AddInt32(fetches, 1)
					return cfgBytes, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}
	return a, ctx
}

func TestCapabilityDiscoverServesKindConfigFromCache(t *testing.T) {
	var fetches int32
	a, ctx := countingDiscoveryCtx(t, &fetches, "builtin:bundle:file-tools")
	a.kindAllowedTTL = 50 * time.Millisecond

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: []string{"project.read", "project.write"},
	}
	if _, err := a.handleCapabilityDiscover(ctx, req); err != nil {
		t.Fatalf("first discover: %v", err)
	}
	if _, err := a.handleCapabilityDiscover(ctx, req); err != nil {
		t.Fatalf("second discover (cached): %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("expected 1 workspace fetch for 2 discovers inside TTL, got %d", got)
	}

	// After the TTL expires the next discover re-fetches the kind config.
	time.Sleep(60 * time.Millisecond)
	if _, err := a.handleCapabilityDiscover(ctx, req); err != nil {
		t.Fatalf("third discover (after TTL): %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 2 {
		t.Fatalf("expected 2 workspace fetches after TTL expiry, got %d", got)
	}
}

func TestRefreshCapabilityCacheInvalidatesKindConfig(t *testing.T) {
	var fetches int32
	a, ctx := countingDiscoveryCtx(t, &fetches, "builtin:bundle:file-tools")

	req := domain.OracleCapabilityDiscoverReq{
		AgentKind:          "coder",
		AvailableToolNames: []string{"project.read"},
	}
	if _, err := a.handleCapabilityDiscover(ctx, req); err != nil {
		t.Fatalf("discover: %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}

	// refresh_capability_cache drops the cached set; the next discover re-fetches.
	if err := a.handleRefreshCapabilityCache(ctx); err != nil {
		t.Fatalf("refresh capability cache: %v", err)
	}
	if _, err := a.handleCapabilityDiscover(ctx, req); err != nil {
		t.Fatalf("discover after refresh: %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 2 {
		t.Fatalf("expected 2 fetches after explicit refresh, got %d", got)
	}
}

func TestSearchServicesServesServiceListFromCache(t *testing.T) {
	var fetches int32
	svcs := []domain.ServiceInfo{
		{Name: "workspace", ActorID: "a1", ActorType: "workspace"},
		{Name: "oracle", ActorID: "a2", ActorType: "oracle"},
		{Name: "filesystem", ActorID: "a3", ActorType: "filesystem"},
	}
	a := &Actor{}
	a.svcListTTL = time.Minute
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlanner{
			callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
				if callID == "runtime.list_services" {
					atomic.AddInt32(&fetches, 1)
					return domain.RuntimeListServicesResp{Items: svcs}, nil
				}
				return nil, fmt.Errorf("unexpected call %s", callID)
			},
		}
	}

	if _, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{}); err != nil {
		t.Fatalf("search all: %v", err)
	}
	if _, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{Query: "work"}); err != nil {
		t.Fatalf("search filtered: %v", err)
	}
	if _, err := a.handleSearchServices(ctx, domain.OracleSearchServicesReq{Limit: 1}); err != nil {
		t.Fatalf("search limited: %v", err)
	}
	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Fatalf("expected 1 runtime.list_services fetch for 3 searches inside TTL, got %d", got)
	}
}
