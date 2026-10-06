package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
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

func TestHandleEvalNoHostBindings(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	// The eval runtime binds no host functions: importing "host" must fail
	// to compile, proving the surface is pure computation.
	resp, err := a.handleEval(ctx, domain.AgentEvalReq{
		Script: `import "host"` + "\n" + `export fun run(): int { return host.invoke("workspace.list_agents", {}) as int }`,
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error == "" {
		t.Error("Error is empty, want compile failure on unbound host import")
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
