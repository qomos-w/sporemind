package agent

import (
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/protocol"
)

// schemaIDForTest looks a generated struct's registry id up by name so tests
// stay decoupled from numeric ids (mirrors pkg/protocol's test helper).
func schemaIDForTest(t *testing.T, name string) int32 {
	t.Helper()
	for id, structName := range gen.SchemaIDs {
		if structName == name {
			return int32(id)
		}
	}
	t.Fatalf("struct %q not in registry", name)
	return 0
}

func testForcedIOContract() forcedIOContract {
	return forcedIOContract{
		CardID:     "card-io-1",
		CallableID: "project.wiki_set_task_outputs",
		ToolSpec: domain.ToolSpec{
			Name:        "project-wiki-set-task-outputs",
			CallableID:  "project.wiki_set_task_outputs",
			ServiceName: "project",
		},
	}
}

func TestForcedIOToolChoiceJSON(t *testing.T) {
	got := forcedIOToolChoiceJSON("project-wiki-set-task-outputs")
	want := `{"type":"function","function":{"name":"project-wiki-set-task-outputs"}}`
	if got != want {
		t.Fatalf("forcedIOToolChoiceJSON = %s, want %s", got, want)
	}
	// Tool names needing JSON escaping must be escaped, not corrupt the object.
	if got := forcedIOToolChoiceJSON(`we"ird`); !strings.Contains(got, `we\"ird`) {
		t.Fatalf("quote not escaped: %s", got)
	}
}

func TestApplyForcedIOOverrides(t *testing.T) {
	spec := domain.ToolSpec{Name: "forced-tool", CallableID: "app.tool"}
	req := domain.SendSessionMessageReq{
		System:     "keep me",
		Messages:   []domain.ChatMessage{{Role: "user"}},
		Tools:      []domain.ToolSpec{{Name: "a"}, {Name: "b"}},
		ToolChoice: "auto",
		Unit:       nil,
	}
	applyForcedIOOverrides(&req, spec)
	if len(req.Tools) != 1 || req.Tools[0].Name != "forced-tool" {
		t.Fatalf("Tools = %v, want single forced-tool", req.Tools)
	}
	if req.ToolChoice != `{"type":"function","function":{"name":"forced-tool"}}` {
		t.Fatalf("ToolChoice = %s", req.ToolChoice)
	}
	if req.System != "keep me" || len(req.Messages) != 1 {
		t.Fatalf("unrelated fields mutated: %+v", req)
	}
}

func TestValidateForcedIOInput(t *testing.T) {
	layout, err := protocol.ResolveRequestLayout(domain.CallableInterface{
		Name:        "project.wiki_list_cards",
		ReqSchemaID: schemaIDForTest(t, "WikiListCardsReq"),
	})
	if err != nil {
		t.Fatalf("resolve layout: %v", err)
	}

	// Numeric coercion: LLM emits "5" as string; Normalize must coerce to the
	// layout family before validation and before the payload goes on the wire.
	got, err := validateForcedIOInput(layout, `{"Limit":"5"}`)
	if err != nil {
		t.Fatalf("valid coerced input rejected: %v", err)
	}
	if !strings.Contains(got, `"Limit":5`) {
		t.Fatalf("coerced output = %s, want Limit as number", got)
	}

	// Unknown keys pass through untouched.
	if _, err := validateForcedIOInput(layout, `{"Limit":3,"Extra":"x"}`); err != nil {
		t.Fatalf("unknown-key input rejected: %v", err)
	}

	// Wrong scalar family: a non-numeric string for an integer field fails.
	if _, err := validateForcedIOInput(layout, `{"Limit":"abc"}`); err == nil {
		t.Fatal("non-numeric Limit accepted")
	}

	// Non-object payload.
	if _, err := validateForcedIOInput(layout, `[1,2]`); err == nil {
		t.Fatal("array payload accepted")
	}
	if _, err := validateForcedIOInput(layout, `not-json`); err == nil {
		t.Fatal("invalid JSON accepted")
	}

	// Missing required field (WikiSetTaskOutputsReq requires CardId).
	reqLayout, err := protocol.ResolveRequestLayout(domain.CallableInterface{
		Name:        "project.wiki_set_task_outputs",
		ReqSchemaID: schemaIDForTest(t, "WikiSetTaskOutputsReq"),
	})
	if err != nil {
		t.Fatalf("resolve req layout: %v", err)
	}
	if _, err := validateForcedIOInput(reqLayout, `{}`); err == nil || !strings.Contains(err.Error(), "CardId") {
		t.Fatalf("missing required CardId not reported: %v", err)
	}

	// Nil layout (callable without ReqSchemaID): only JSON-object shape checked.
	out, err := validateForcedIOInput(nil, `{"Anything":true}`)
	if err != nil {
		t.Fatalf("nil layout rejected valid object: %v", err)
	}
	if out != `{"Anything":true}` {
		t.Fatalf("nil layout must pass payload through verbatim, got %s", out)
	}
	if _, err := validateForcedIOInput(nil, `"scalar"`); err == nil {
		t.Fatal("nil layout accepted non-object payload")
	}
}

func TestForcedIOPendingCall(t *testing.T) {
	contract := testForcedIOContract()

	// Zero calls: the LLM answered in text despite the forced choice.
	if _, err := forcedIOPendingCall(contract, nil); err == nil || !strings.Contains(err.Error(), "no tool call") {
		t.Fatalf("zero pending not rejected: %v", err)
	}
	// More than one call.
	two := []pendingToolCall{{ID: "1", LLMName: "a"}, {ID: "2", LLMName: "b"}}
	if _, err := forcedIOPendingCall(contract, two); err == nil || !strings.Contains(err.Error(), "2 tool calls") {
		t.Fatalf("multi pending not rejected: %v", err)
	}
	// Wrong tool.
	wrong := []pendingToolCall{{ID: "1", LLMName: "other-tool", Input: `{}`}}
	if _, err := forcedIOPendingCall(contract, wrong); err == nil || !strings.Contains(err.Error(), `called "other-tool"`) {
		t.Fatalf("wrong tool not rejected: %v", err)
	}
	// Malformed arguments.
	malformed := []pendingToolCall{{ID: "1", LLMName: contract.ToolSpec.Name, Input: `{`, MalformedArgs: true}}
	if _, err := forcedIOPendingCall(contract, malformed); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed args not rejected: %v", err)
	}
	// Accepted spellings: declared hyphen name, dotted callable, underscore form.
	for _, name := range []string{
		"project-wiki-set-task-outputs",
		"project.wiki_set_task_outputs",
		"project_wiki_set_task_outputs",
		strings.ToUpper("project-wiki-set-task-outputs"),
	} {
		call := []pendingToolCall{{ID: "1", LLMName: name, Input: `{}`}}
		got, err := forcedIOPendingCall(contract, call)
		if err != nil {
			t.Fatalf("alias %q rejected: %v", name, err)
		}
		if got.ID != "1" {
			t.Fatalf("returned call = %+v", got)
		}
	}
}

func TestMaybeEnterForcedDispatch(t *testing.T) {
	t.Run("contract found enters forced phase once", func(t *testing.T) {
		e, ctx := newLifecycleEngine(t)
		contract := testForcedIOContract()
		e.onForcedIOContract = func(_ actor.Context) (*forcedIOContract, error) {
			c := contract
			return &c, nil
		}
		if !e.maybeEnterForcedDispatch(ctx, e.turnID) {
			t.Fatal("first call must enter forced dispatch")
		}
		if e.loopState != LoopForcedDispatch {
			t.Fatalf("loopState = %v, want forced dispatch", e.loopState)
		}
		if e.forcedIO == nil || e.forcedIO.CardID != contract.CardID {
			t.Fatalf("forcedIO = %+v", e.forcedIO)
		}
		if !e.forcedIOAttempted {
			t.Fatal("attempted flag not set")
		}
		// Second terminal judgment in the same turn must not re-enter.
		e.loopState = LoopDispatch
		if e.maybeEnterForcedDispatch(ctx, e.turnID) {
			t.Fatal("second call must not re-enter")
		}
	})
	t.Run("no contract completes normally", func(t *testing.T) {
		e, ctx := newLifecycleEngine(t)
		e.onForcedIOContract = func(_ actor.Context) (*forcedIOContract, error) {
			return nil, nil
		}
		if e.maybeEnterForcedDispatch(ctx, e.turnID) {
			t.Fatal("nil contract must not enter")
		}
		if e.loopState != LoopDispatch {
			t.Fatalf("loopState mutated to %v", e.loopState)
		}
	})
	t.Run("resolution error fails the turn", func(t *testing.T) {
		e, ctx := newLifecycleEngine(t)
		e.onForcedIOContract = func(_ actor.Context) (*forcedIOContract, error) {
			return nil, errForcedIOTestBoom
		}
		if !e.maybeEnterForcedDispatch(ctx, e.turnID) {
			t.Fatal("resolution error must take over the terminal transition")
		}
		if e.loopState != LoopFailed {
			t.Fatalf("loopState = %v, want failed", e.loopState)
		}
		if !strings.Contains(e.turnError, "forced IO contract resolution failed") {
			t.Fatalf("turnError = %q", e.turnError)
		}
	})
	t.Run("nil callback is inert", func(t *testing.T) {
		e, ctx := newLifecycleEngine(t)
		if e.maybeEnterForcedDispatch(ctx, e.turnID) {
			t.Fatal("nil callback must not enter")
		}
	})
}

var errForcedIOTestBoom = &forcedIOTestError{}

type forcedIOTestError struct{}

func (*forcedIOTestError) Error() string { return "boom" }

func TestForcedIOInstructionText(t *testing.T) {
	contract := testForcedIOContract()
	contract.Description = "Report the turn's outcome summary."
	text := forcedIOInstructionText(contract)
	for _, want := range []string{
		"card-io-1",
		"project-wiki-set-task-outputs",
		"project.wiki_set_task_outputs",
		"Report the turn's outcome summary.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("instruction missing %q:\n%s", want, text)
		}
	}
	// No description: still valid, no trailing empty guidance.
	bare := forcedIOInstructionText(testForcedIOContract())
	if strings.Contains(bare, "Contract description:") {
		t.Errorf("empty description must not emit guidance header:\n%s", bare)
	}
}

func TestForcedIODeclaration(t *testing.T) {
	const agentID = "agent-1"
	cases := []struct {
		name            string
		data            map[string]any
		wantApplicable  bool
		wantCallable    string
		wantDescription string
	}{
		{"nil data", nil, false, "", ""},
		{"other owner", map[string]any{"ownerAgentId": "agent-2", "io": map[string]any{"callable": "a.b"}}, false, "", ""},
		{"no owner", map[string]any{"io": map[string]any{"callable": "a.b"}}, false, "", ""},
		{"no io block", map[string]any{"ownerAgentId": "agent-1"}, false, "", ""},
		{"empty callable", map[string]any{"ownerAgentId": "agent-1", "io": map[string]any{"callable": "  "}}, false, "", ""},
		{"io not a map", map[string]any{"ownerAgentId": "agent-1", "io": "x"}, false, "", ""},
		{
			"full declaration",
			map[string]any{"ownerAgentId": "agent-1", "io": map[string]any{"callable": " app.emit ", "description": "emit state"}},
			true, "app.emit", "emit state",
		},
	}
	for _, tc := range cases {
		callable, desc, applicable := forcedIODeclaration(tc.data, agentID)
		if applicable != tc.wantApplicable || callable != tc.wantCallable || desc != tc.wantDescription {
			t.Errorf("%s: got (%q,%q,%v), want (%q,%q,%v)",
				tc.name, callable, desc, applicable, tc.wantCallable, tc.wantDescription, tc.wantApplicable)
		}
	}
}

func TestDecodeForcedIOResult(t *testing.T) {
	if got := decodeForcedIOResult(`{"a":1}`); got == nil {
		t.Fatal("object result must decode to map")
	}
	if got := decodeForcedIOResult("plain text"); got != "plain text" {
		t.Fatalf("non-JSON passthrough = %v", got)
	}
	if got := decodeForcedIOResult(""); got != "" {
		t.Fatalf("empty passthrough = %v", got)
	}
}
