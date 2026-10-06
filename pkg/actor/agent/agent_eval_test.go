package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// Tests for the agent-local eval callable (moved off workspace.eval): pure
// computation, fixed budget envelope, diagnostics in resp.Error.

func TestHandleEvalReturnsResult(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: `export fun run(n: int): int { return n * 2 }`,
		Args:   []any{21},
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected Error: %q", resp.Error)
	}
	// The VM returns spore ints as int64 (not float64); both marshal to 42.
	if n, ok := asInt64(resp.Result); !ok || n != 42 {
		t.Errorf("Result = %#v (%T), want 42", resp.Result, resp.Result)
	}
}

func TestHandleEvalMapResult(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: "export fun run(): any {\n\tvar m: map = {}\n\tm[\"shape\"] = \"map\"\n\treturn m\n}",
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected Error: %q", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok || m["shape"] != "map" {
		t.Errorf("Result = %#v, want {shape: map}", resp.Result)
	}
}

func asInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		return int64(n), n == float64(int64(n))
	}
	return 0, false
}

// JSON-decoded float64 args must promote to spore ints (same normalization
// the runtime:spore skill path applies) so `is int` holds.
func TestHandleEvalIntArgNormalization(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: "export fun run(n: any): string {\n\tif n is int {\n\t\treturn \"int\"\n\t}\n\treturn \"other\"\n}",
		Args:   []any{float64(7)},
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected Error: %q", resp.Error)
	}
	if s, ok := resp.Result.(string); !ok || s != "int" {
		t.Errorf("Result = %#v, want \"int\"", resp.Result)
	}
}

func TestHandleEvalCompileErrorReturnsErrorField(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.AgentEvalReq{Script: `fun (`})
	if err != nil {
		t.Fatalf("compile failure must surface in Error, not transport: %v", err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "compile failed") {
		t.Errorf("Error = %q, want compile-failed diagnostic", resp.Error)
	}
}

func TestHandleEvalRuntimeErrorReturnsErrorField(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	// Map reads of missing keys throw in spore (see data-shape skill).
	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: "export fun run(): any {\n\tvar m: map = {}\n\treturn m[\"missing\"]\n}",
	})
	if err != nil {
		t.Fatalf("runtime failure must surface in Error, not transport: %v", err)
	}
	if resp.Error == "" {
		t.Error("Error is empty, want runtime-error diagnostic")
	}
}

func TestHandleEvalMissingRunEntry(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.AgentEvalReq{Script: `fun helper(): int { return 1 }`})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error == "" {
		t.Error("Error is empty, want missing-entry diagnostic")
	}
}

func TestHandleEvalEmptyScriptRejected(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	if _, err := a.handleEval(ctx, domain.AgentEvalReq{}); err == nil {
		t.Fatal("empty Script must be a transport error")
	}
}

func TestHandleEvalHostInvokeReachesBridge(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	// host is bound now: the import compiles and the invoke executes, then
	// fails on service resolution inside the bridge (no such service in the
	// test context) — proving reach flows through sporebridge, not a stub.
	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: "import { invoke } from \"host\"\n" + "export fun run(): string {\n\tvar r: map = invoke(\"nosuch.svc\", {})\n\treturn \"called\"\n}",
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error == "" {
		t.Fatal("Error is empty, want bridge resolution failure surfaced as diagnostic")
	}
	if !strings.Contains(resp.Error, "nosuch.svc") {
		t.Errorf("Error = %q, want diagnostic naming the unknown callable", resp.Error)
	}
}

func TestHandleEvalSyntax(t *testing.T) {
	a := &Actor{}

	resp, err := a.handleEvalSyntax(nil, domain.AgentEvalSyntaxReq{})
	if err != nil {
		t.Fatalf("default lang: %v", err)
	}
	if resp.Lang != "en" || !strings.Contains(resp.Markdown, "sporescript language reference") {
		t.Errorf("default resp = lang %q, %d bytes; want en reference", resp.Lang, len(resp.Markdown))
	}

	resp, err = a.handleEvalSyntax(nil, domain.AgentEvalSyntaxReq{Lang: "zh"})
	if err != nil {
		t.Fatalf("zh lang: %v", err)
	}
	if resp.Lang != "zh" || !strings.Contains(resp.Markdown, "sporescript 语言参考") {
		t.Errorf("zh resp = lang %q; want zh reference", resp.Lang)
	}

	if _, err := a.handleEvalSyntax(nil, domain.AgentEvalSyntaxReq{Lang: "fr"}); err == nil {
		t.Fatal("unsupported Lang must error")
	}
}

func TestHandleEvalCallablesFiltersAndLimits(t *testing.T) {
	a := &Actor{}
	a.topo = &mockTopologyProvider{snapshot: []runtime.ActorNode{
		{
			ID: "toast-actor",
			Callables: []domain.CallableInterface{
				{Name: "toast.show", Description: "show a toast", ServiceName: "toast"},
				{Name: "toast.read", Description: "read toast state", ServiceName: "toast"},
			},
		},
		{
			ID: "demo-actor",
			Callables: []domain.CallableInterface{
				{Name: "demo.ping", Description: "ping the demo actor", ServiceName: "demo"},
			},
		},
	}}

	// Query filter matches name and description, case-insensitively.
	resp, err := a.handleEvalCallables(nil, domain.AgentEvalCallablesReq{Query: "TOAST"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 2 || resp.Total != 2 {
		t.Fatalf("toast query = %d items (total %d), want 2/2", len(resp.Items), resp.Total)
	}

	// Description match across services.
	resp, err = a.handleEvalCallables(nil, domain.AgentEvalCallablesReq{Query: "ping"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "demo.ping" {
		t.Fatalf("ping query items = %+v", resp.Items)
	}

	// Limit truncates Items but Total still counts every match.
	resp, err = a.handleEvalCallables(nil, domain.AgentEvalCallablesReq{Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Total != 3 {
		t.Fatalf("limited search = %d items (total %d), want 1/3", len(resp.Items), resp.Total)
	}
}

func TestHandleEvalCallablesWithoutTopology(t *testing.T) {
	if _, err := (&Actor{}).handleEvalCallables(nil, domain.AgentEvalCallablesReq{}); err == nil {
		t.Fatal("missing topology provider must error")
	}
}

func TestHandleEvalOutputCap(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	// ~10KB per call × 8 calls ≈ 80KB > 64KB cap: must hit the host-side
	// output check with a clear diagnostic, not a transport error.
	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: "export fun run(): string {\n\tvar s: string = \"0123456789abcdef\"\n\tvar out: string = \"\"\n\tvar i: int = 0\n\tfor (i = 0; i < 5000; i = i + 1) {\n\t\tout = out + s\n\t}\n\treturn out\n}",
	})
	if err != nil {
		t.Fatalf("output overflow must surface in Error, not transport: %v", err)
	}
	if resp.Error == "" || !strings.Contains(resp.Error, "exceeds") {
		t.Errorf("Error = %q, want exceeds-cap diagnostic", resp.Error)
	}
}
