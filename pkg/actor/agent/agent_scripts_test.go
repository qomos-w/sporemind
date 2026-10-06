package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newScriptsTestActor(t *testing.T) *Actor {
	t.Helper()
	dir := t.TempDir()
	config.SetDataDirForTest(dir)
	a := &Actor{actorID: testutil.GenActorID().String()}
	if err := a.loadSavedScripts(); err != nil {
		t.Fatalf("loadSavedScripts: %v", err)
	}
	return a
}

func TestScriptSaveUpsertAndPersist(t *testing.T) {
	a := newScriptsTestActor(t)

	resp, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:        "sum-range",
		Description: "sum integers 1..n",
		Script:      "fun run(n: int): int { return n * (n + 1) / 2 }",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if resp.Name != "sum-range" || resp.Updated {
		t.Fatalf("first save resp = %+v, want {sum-range Updated=false}", resp)
	}

	// Upsert by the same Name reports Updated and replaces the body.
	resp, err = a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:   "sum-range",
		Script: "fun run(n: int): int { var s: int = 0\nreturn s }",
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !resp.Updated {
		t.Fatal("upsert Updated = false, want true")
	}
	if got := a.savedScripts["sum-range"].Script; !strings.Contains(got, "var s: int = 0") {
		t.Fatalf("upsert did not replace body: %q", got)
	}

	// A fresh actor over the same data dir restores the persisted doc.
	// Upsert is a full-record replace: the second save carried no
	// Description, so the reloaded record keeps the new body and drops the
	// old description.
	b := &Actor{actorID: a.actorID}
	if err := b.loadSavedScripts(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.savedScripts) != 1 {
		t.Fatalf("persisted round-trip = %+v", b.savedScripts)
	}
	got := b.savedScripts["sum-range"]
	if got.Description != "" || !strings.Contains(got.Script, "var s: int = 0") {
		t.Fatalf("reloaded record = %+v, want replaced body with dropped description", got)
	}
}

func TestScriptSaveValidation(t *testing.T) {
	a := newScriptsTestActor(t)

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Script: "fun run() {}"}); err == nil {
		t.Fatal("empty Name must error")
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "x"}); err == nil {
		t.Fatal("empty Script must error")
	}
	// Name must be trimmed: " x " upserts "x".
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: " x ", Script: "fun run() {}"}); err != nil {
		t.Fatalf("trimmed name save: %v", err)
	}
	if _, ok := a.savedScripts["x"]; !ok {
		t.Fatal("name was not trimmed to x")
	}
}

func TestScriptDelete(t *testing.T) {
	a := newScriptsTestActor(t)
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "gone", Script: "fun run() {}"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	resp, err := a.handleScriptDelete(nil, domain.AgentScriptDeleteReq{Name: "gone"})
	if err != nil || !resp.Deleted {
		t.Fatalf("delete = %+v, %v; want Deleted=true", resp, err)
	}
	if _, ok := a.savedScripts["gone"]; ok {
		t.Fatal("script still present after delete")
	}

	// The deletion is durable: a fresh actor sees nothing.
	b := &Actor{actorID: a.actorID}
	if err := b.loadSavedScripts(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(b.savedScripts) != 0 {
		t.Fatalf("deleted script survived reload: %+v", b.savedScripts)
	}

	// Unknown name is an explicit error.
	if _, err := a.handleScriptDelete(nil, domain.AgentScriptDeleteReq{Name: "nope"}); err == nil {
		t.Fatal("deleting unknown Name must error")
	}
}

func TestScriptRead(t *testing.T) {
	a := newScriptsTestActor(t)
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:        "sum-range",
		Description: "sum 1..n",
		Script:      "fun run(n: int): int { return n * (n + 1) / 2 }",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	resp, err := a.handleScriptRead(nil, domain.AgentScriptReadReq{Name: "sum-range"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if resp.Name != "sum-range" || resp.Description != "sum 1..n" ||
		!strings.Contains(resp.Script, "fun run(n: int): int") {
		t.Fatalf("read resp = %+v", resp)
	}
	if strings.TrimSpace(resp.Name) == "" {
		t.Fatal("read must return the full body")
	}

	if _, err := a.handleScriptRead(nil, domain.AgentScriptReadReq{Name: "nope"}); err == nil {
		t.Fatal("reading unknown Name must error")
	}
	if _, err := a.handleScriptRead(nil, domain.AgentScriptReadReq{Name: "  "}); err == nil {
		t.Fatal("empty Name must error")
	}
}

func TestSavedScriptToolsProjection(t *testing.T) {
	a := newScriptsTestActor(t)
	if got := a.savedScriptTools(); got != nil {
		t.Fatalf("empty set must project no tools, got %+v", got)
	}

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:        "sum-range",
		Description: "sum 1..n",
		Script:      "fun run(n: int): int { return n * (n + 1) / 2 }",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:        "echo map",
		Description: "typed map param",
		Script:      "fun run(m: map<string, int>, label: string): int { return 1 }",
	}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:   "broken",
		Script: "fun (",
	}); err != nil {
		t.Fatalf("save3: %v", err)
	}

	tools := a.savedScriptTools()
	if len(tools) != 3 {
		t.Fatalf("projected %d tools, want 3: %+v", len(tools), tools)
	}
	byName := map[string]domain.ToolSpec{}
	for _, ts := range tools {
		byName[ts.Name] = ts
	}

	sum := byName["script-sum-range"]
	if sum.CallableID != "script.sum-range" || sum.ServiceName != "" {
		t.Fatalf("sum-range spec = %+v", sum)
	}
	if !strings.Contains(sum.InputSchema, `"n":{"type":"integer"}`) {
		t.Fatalf("typed schema missing n:integer: %s", sum.InputSchema)
	}
	if !strings.Contains(sum.InputSchema, `"required":["n"]`) {
		t.Fatalf("n must be required: %s", sum.InputSchema)
	}
	if !strings.Contains(sum.Description, "run(n: int): int") {
		t.Fatalf("description must embed the signature: %q", sum.Description)
	}

	emap := byName["script-echo_map"]
	if !strings.Contains(emap.InputSchema, `"m":{"type":"object"}`) || !strings.Contains(emap.InputSchema, `"label":{"type":"string"}`) {
		t.Fatalf("map/string param schema wrong: %s", emap.InputSchema)
	}

	// Unparseable source degrades to the generic Args object, still
	// projected (the set stays total).
	broken := byName["script-broken"]
	if !strings.Contains(broken.InputSchema, `"Args"`) {
		t.Fatalf("broken source must fall back to generic Args: %s", broken.InputSchema)
	}
	if !strings.Contains(broken.Description, "script_read") {
		t.Fatalf("fallback description must point at script_read: %q", broken.Description)
	}
}

func TestExecuteSavedScriptCall(t *testing.T) {
	a := newScriptsTestActor(t)
	ctx := testutil.SystemCtx(testutil.GenActorID())

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:   "sum-range",
		Script: "fun run(n: int): int { return n * (n + 1) / 2 }",
	}); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Named tool input binds positionally to the signature.
	out, isErr := a.executeSavedScriptCall(ctx, "sum-range", `{"n": 100}`)
	if isErr {
		t.Fatalf("exec failed: %s", out)
	}
	if strings.TrimSpace(out) != "5050" {
		t.Fatalf("run(100) = %q, want 5050", out)
	}

	// Unknown name / malformed input are explicit tool errors.
	if _, isErr := a.executeSavedScriptCall(ctx, "nope", "{}"); !isErr {
		t.Fatal("unknown script must be a tool error")
	}
	if _, isErr := a.executeSavedScriptCall(ctx, "sum-range", `not-json`); !isErr {
		t.Fatal("malformed input must be a tool error")
	}

	// A runtime failure surfaces the diagnostic (read → edit → save is the fix).
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{
		Name:   "bad",
		Script: "fun run(): int { return 1 / 0 }",
	}); err != nil {
		t.Fatalf("save bad: %v", err)
	}
	if out, isErr := a.executeSavedScriptCall(ctx, "bad", `{}`); !isErr {
		t.Fatalf("runtime failure must error, got %q", out)
	}
}

func TestBuildSavedScriptsBlock(t *testing.T) {
	a := newScriptsTestActor(t)
	if a.buildSavedScriptsBlock() != nil {
		t.Fatal("empty set must yield nil block")
	}

	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "b-second", Description: "second", Script: "fun run(): int { return 2 }"}); err != nil {
		t.Fatalf("save b: %v", err)
	}
	if _, err := a.handleScriptSave(nil, domain.AgentScriptSaveReq{Name: "a-first", Description: "first", Script: "fun run(): int { return 1 }"}); err != nil {
		t.Fatalf("save a: %v", err)
	}

	block := a.buildSavedScriptsBlock()
	if block == nil {
		t.Fatal("nil block with saved scripts")
	}
	if !strings.Contains(block.Text, "## Saved Scripts") {
		t.Errorf("block missing header: %q", block.Text)
	}
	// Index-only: sorted one-liners with name, signature, description —
	// and no fenced bodies.
	aIdx := strings.Index(block.Text, "a-first")
	bIdx := strings.Index(block.Text, "b-second")
	if aIdx < 0 || bIdx < 0 || aIdx > bIdx {
		t.Errorf("entries not sorted by name (a@%d b@%d)", aIdx, bIdx)
	}
	if !strings.Contains(block.Text, "`run(): int`") {
		t.Errorf("signature missing from index: %q", block.Text)
	}
	if strings.Contains(block.Text, "```spore") {
		t.Errorf("index must not carry bodies: %q", block.Text)
	}
	if !strings.Contains(block.Text, "script-<name>") {
		t.Errorf("lead must point at the projected tools: %q", block.Text)
	}
}
