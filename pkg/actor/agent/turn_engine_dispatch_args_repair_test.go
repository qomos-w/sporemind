package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// 事故样本（oracle 诊断 2026-09-05 turn-386）：heredoc 换行以原始 \n 字节下发。
var sampleRawNewlineArgs = "{\"Args\": [\"-c\", \"cd dir; python3 - <<'EOF'\nimport json\nraw=open('$F',encoding='utf-8')\nEOF\"], \"Command\": \"bash\", \"timeout\": 30000}"

func TestEscapeRawControlChars_RawNewlinesInsideStringLiterals(t *testing.T) {
	repaired := escapeRawControlChars(sampleRawNewlineArgs)
	if !json.Valid([]byte(repaired)) {
		t.Fatalf("repaired args still invalid JSON: %q", repaired)
	}
	var v struct {
		Args    []string `json:"Args"`
		Command string   `json:"Command"`
		Timeout int      `json:"timeout"`
	}
	if err := json.Unmarshal([]byte(repaired), &v); err != nil {
		t.Fatalf("unmarshal repaired args: %v", err)
	}
	if v.Command != "bash" || v.Timeout != 30000 {
		t.Fatalf("structural fields lost: %+v", v)
	}
	if len(v.Args) != 2 || !strings.Contains(v.Args[1], "python3 - <<'EOF'\nimport json\n") {
		t.Fatalf("shell command content damaged: %q", v.Args)
	}
}

func TestEscapeRawControlChars_KeepsInterLiteralWhitespace(t *testing.T) {
	// 字面量之间的换行是合法 JSON 空白，不得转义。
	in := "{\n  \"a\": \"line1\nline2\",\n  \"b\": 1\n}"
	repaired := escapeRawControlChars(in)
	if !json.Valid([]byte(repaired)) {
		t.Fatalf("repaired invalid: %q", repaired)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(repaired), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v["b"] != float64(1) {
		t.Fatalf("inter-literal whitespace damaged: %v", v)
	}
}

func TestEscapeRawControlChars_PreservesExistingEscapes(t *testing.T) {
	// 已转义的 \"、\\ 与裸控制字节并存（Go 解释后 \n 为真实换行字节）。
	in := "{\"a\": \"quote \\\" backslash \\\\ newline\n end\"}"
	if json.Valid([]byte(in)) {
		t.Fatalf("test input should be malformed (raw newline), but it is valid")
	}
	repaired := escapeRawControlChars(in)
	if !json.Valid([]byte(repaired)) {
		t.Fatalf("repaired invalid: %q", repaired)
	}
	var v struct {
		A string `json:"a"`
	}
	if err := json.Unmarshal([]byte(repaired), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := "quote \" backslash \\ newline\n end"
	if v.A != want {
		t.Fatalf("content damaged: got %q, want %q", v.A, want)
	}
}

func TestEscapeRawControlChars_HealthyInputUnchanged(t *testing.T) {
	in := `{"a": 1, "b": "x"}`
	if got := escapeRawControlChars(in); got != in {
		t.Fatalf("healthy input changed: %q", got)
	}
}

func TestFinalizeDispatch_RepairableRawControlBytesRepaired(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{ContextWindowSize: 100000},
		},
	}
	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		stopReason: "tool_use",
		pending: []pendingToolCall{
			{ID: "tu-1", LLMName: "project.shell_exec", CallableID: "project.shell_exec", Input: sampleRawNewlineArgs},
		},
	})
	if e.pendingCalls[0].Input != escapeRawControlChars(sampleRawNewlineArgs) {
		t.Fatalf("input not repaired: %q", e.pendingCalls[0].Input)
	}
	if e.pendingCalls[0].MalformedArgs {
		t.Fatalf("repairable input must not be flagged MalformedArgs")
	}
}

func TestFinalizeDispatch_StructurallyBrokenArgsMarkedMalformed(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{ContextWindowSize: 100000},
		},
	}
	// 事故样本第二类损坏：Args 数组的结束 "] 缺失，无法修复。
	// fixture 与 oracle 诊断中保存的事故原始字节形状一致（heredoc 字符串
	// 未闭合 + 数组括号被吞）。
	broken := "{\"Args\": [\"-c\", \"cd dir; python3 - <<'EOF'\nprint('x')\nEOF, \"Command\": \"bash\", \"timeout\": 30000}"
	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		stopReason: "tool_use",
		pending: []pendingToolCall{
			{ID: "tu-1", LLMName: "project.shell_exec", CallableID: "project.shell_exec", Input: broken},
		},
	})
	if !e.pendingCalls[0].MalformedArgs {
		t.Fatalf("structurally broken input must be flagged MalformedArgs")
	}
	if e.pendingCalls[0].Input != "{}" {
		t.Fatalf("malformed input must fall back to {} for history safety, got %q", e.pendingCalls[0].Input)
	}
}

func TestFinalizeDispatch_EmptyArgsBecomeEmptyObject(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{
		logger:   newNopActorLogger(),
		turnID:   "turn-1",
		stepByID: map[string]domain.TurnAction{},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{ContextWindowSize: 100000},
		},
	}
	e.finalizeDispatch(ctx, "turn-1", dispatchResult{
		stopReason: "tool_use",
		pending: []pendingToolCall{
			{ID: "tu-1", LLMName: "project.read", CallableID: "project.read", Input: ""},
		},
	})
	if e.pendingCalls[0].MalformedArgs {
		t.Fatalf("empty args is a valid no-argument call, must not be flagged MalformedArgs")
	}
	if e.pendingCalls[0].Input != "{}" {
		t.Fatalf("empty args must become {}, got %q", e.pendingCalls[0].Input)
	}
}

func TestMalformedArgsResult_IsErrorAndMentionsResend(t *testing.T) {
	call := pendingToolCall{ID: "tu-1", LLMName: "project.shell_exec", RawToolCall: `{"id":"tu-1"}`}
	res := malformedArgsResult(call)
	if !res.isErr {
		t.Fatal("malformed args result must be an error")
	}
	if !strings.Contains(res.out, "malformed") || !strings.Contains(res.out, "resend") {
		t.Fatalf("result must explain the failure and tell the model to resend: %q", res.out)
	}
	if res.call.ID != "tu-1" {
		t.Fatalf("result must carry the original call for tool_use/tool_result pairing: %+v", res.call)
	}
}

func TestExecuteToolBatch_MalformedArgsSkippedSequential(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{logger: newNopActorLogger(), turnID: "turn-1"}
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{ID: "tu-1", LLMName: "project.shell_exec", CallableID: "project.shell_exec",
			Input: "{}", MalformedArgs: true, EffectKind: domain.EffectIrreversible},
	}}
	results := e.executeToolBatch(ctx, batch)
	if len(results) != 1 || !results[0].isErr {
		t.Fatalf("malformed call must produce an error result without executing: %+v", results)
	}
	if !strings.Contains(results[0].out, "malformed") {
		t.Fatalf("unexpected result text: %q", results[0].out)
	}
}

func TestExecuteToolBatch_MalformedArgsSkippedParallel(t *testing.T) {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	e := &turnEngine{logger: newNopActorLogger(), turnID: "turn-1"}
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{ID: "tu-1", LLMName: "project.read", CallableID: "project.read", Input: "{}", MalformedArgs: true},
		{ID: "tu-2", LLMName: "project.glob", CallableID: "project.glob", Input: "{}", MalformedArgs: true},
	}}
	results := e.executeToolBatch(ctx, batch)
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d", len(results))
	}
	for i, r := range results {
		if !r.isErr || !strings.Contains(r.out, "malformed") {
			t.Fatalf("result %d must be a malformed-args error without executing: %+v", i, r)
		}
	}
}

func TestBuildToolCallSummaries_MalformedArgsShowsFailureNotice(t *testing.T) {
	batch := toolExecutionBatch{calls: []pendingToolCall{
		{ID: "tu-1", CallableID: "project.shell_exec", Input: "{}", MalformedArgs: true},
		{ID: "tu-2", CallableID: "project.read", Input: `{"path":"a.txt"}`},
	}}
	summaries := buildToolCallSummaries(batch)
	if !strings.Contains(summaries[0].Input, "malformed") {
		t.Fatalf("permission panel must show the failure notice instead of {}: %q", summaries[0].Input)
	}
	if summaries[1].Input != `{"path":"a.txt"}` {
		t.Fatalf("healthy call input must be untouched: %q", summaries[1].Input)
	}
}
