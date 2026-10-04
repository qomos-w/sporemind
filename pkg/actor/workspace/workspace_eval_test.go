package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/runtime"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleEvalReturnsResult(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{
		Script: `export fun run(n: int): int { return n * 2 }`,
		Args:   []any{21},
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error != "" {
		t.Fatalf("unexpected Error: %q", resp.Error)
	}
	if n, ok := resp.Result.(float64); !ok || n != 42 {
		t.Errorf("Result = %#v, want 42", resp.Result)
	}
}

func TestHandleEvalMapResult(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{
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

func TestHandleEvalCompileErrorReturnsErrorField(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{Script: `fun (`})
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
	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{
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

	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{Script: `fun helper(): int { return 1 }`})
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

	if _, err := a.handleEval(ctx, domain.WorkspaceEvalReq{}); err == nil {
		t.Fatal("empty Script must be a transport error")
	}
}

func TestHandleEvalAnonymousRoleDenied(t *testing.T) {
	a := &Actor{}
	ctx := &testutil.FakeCtx{Identity_: id.Identity{Role: "anonymous"}}

	if _, err := a.handleEval(ctx, domain.WorkspaceEvalReq{Script: `export fun run(): int { return 1 }`}); err == nil {
		t.Fatal("anonymous role must be denied")
	}
}

func TestHandleEvalNoHostBindings(t *testing.T) {
	a := &Actor{}
	ctx := testutil.SystemCtx(testutil.GenActorID())

	// The eval runtime binds no host functions: importing "host" must fail
	// to compile, proving the surface is pure computation.
	resp, err := a.handleEval(ctx, domain.WorkspaceEvalReq{
		Script: `import "host"` + "\n" + `export fun run(): int { return host.invoke("workspace.list_agents", {}) as int }`,
	})
	if err != nil {
		t.Fatalf("handleEval: %v", err)
	}
	if resp.Error == "" {
		t.Error("Error is empty, want compile failure on unbound host import")
	}
}

func TestHandleSporeSyntax(t *testing.T) {
	a := &Actor{}

	resp, err := a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{})
	if err != nil {
		t.Fatalf("default lang: %v", err)
	}
	if resp.Lang != "en" || !strings.Contains(resp.Markdown, "sporescript language reference") {
		t.Errorf("default resp = lang %q, %d bytes; want en reference", resp.Lang, len(resp.Markdown))
	}

	resp, err = a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{Lang: "zh"})
	if err != nil {
		t.Fatalf("zh lang: %v", err)
	}
	if resp.Lang != "zh" || !strings.Contains(resp.Markdown, "sporescript 语言参考") {
		t.Errorf("zh resp = lang %q; want zh reference", resp.Lang)
	}

	if _, err := a.handleSporeSyntax(nil, domain.WorkspaceSporeSyntaxReq{Lang: "fr"}); err == nil {
		t.Fatal("unsupported Lang must error")
	}
}

func TestHandleSearchCallablesFiltersAndLimits(t *testing.T) {
	a := &Actor{}
	a.topo = &fakeTopology{nodes: []runtime.ActorNode{
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
	resp, err := a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Query: "TOAST"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 2 || resp.Total != 2 {
		t.Fatalf("toast query = %d items (total %d), want 2/2", len(resp.Items), resp.Total)
	}

	// Description match across services.
	resp, err = a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Query: "ping"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].Name != "demo.ping" {
		t.Fatalf("ping query items = %+v", resp.Items)
	}

	// Limit truncates Items but Total still counts every match.
	resp, err = a.handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{Limit: 1})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Items) != 1 || resp.Total != 3 {
		t.Fatalf("limited search = %d items (total %d), want 1/3", len(resp.Items), resp.Total)
	}
}

func TestHandleSearchCallablesWithoutTopology(t *testing.T) {
	if _, err := (&Actor{}).handleSearchCallables(nil, domain.WorkspaceSearchCallablesReq{}); err == nil {
		t.Fatal("missing topology provider must error")
	}
}
