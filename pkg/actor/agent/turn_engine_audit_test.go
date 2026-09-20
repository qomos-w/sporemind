package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

func TestAskUserToolSpec_DescribesTextEncodedAnswer(t *testing.T) {
	spec := askUserToolSpec()
	if spec.Name != "ask_user" {
		t.Fatalf("Name = %q, want ask_user", spec.Name)
	}
	for _, want := range []string{"JSON object encoded as text", "question indices as keys", "selected option labels as values", `{"0":"Option A"}`} {
		if !strings.Contains(spec.Description, want) {
			t.Fatalf("Description %q does not contain %q", spec.Description, want)
		}
	}
}

func TestCheckBatchPermission_DeniesDisallowedCardScope(t *testing.T) {
	e := &turnEngine{
		allowCallableScope: func(callableID string) bool { return callableID != "project.secret" },
	}
	decision, reason := e.checkBatchPermission(testutil.AnonCtx(testutil.GenActorID()), toolExecutionBatch{calls: []pendingToolCall{{CallableID: "project.secret", EffectKind: domain.EffectNone}}})
	if decision != "deny" || reason == "" {
		t.Fatalf("expected scope denial, got decision=%q reason=%q", decision, reason)
	}
}

func TestCallableScopeAllowed(t *testing.T) {
	scopes := map[string]string{
		"project.worktree_exit":   "system", // legacy persisted mount scope
		"project.write":           "project",
		"builtin.read":            "builtin",
		"dep.tool":                "dependency",
		"user.tool":               "user",
		"app.novelking.book_list": "app", // bound-app bundle mount (agent_bind_app)
	}
	for id, scope := range scopes {
		if !callableScopeAllowed(map[string]string{id: scope}, id) {
			t.Errorf("callableScopeAllowed(%q, %q) = false, want true", scope, id)
		}
	}
	if !callableScopeAllowed(map[string]string{}, "unlisted.tool") {
		t.Error("callables not contributed by any card must be unrestricted")
	}
	if callableScopeAllowed(map[string]string{"skill.tool": "skill"}, "skill.tool") {
		t.Error("untrusted scope must be denied")
	}
}

func TestCheckBatchPermission_InteractionPrimitivesBypassScope(t *testing.T) {
	// turn.assess / goal.submit / plan.submit are agent-owned interaction
	// primitives intercepted internally by the turn engine, not card-provided
	// security tools. Even when allowCallableScope denies everything, they must
	// bypass the card-scope gate so goal/plan completion is never blocked.
	e := &turnEngine{
		allowCallableScope: func(callableID string) bool { return false },
	}
	for _, callableID := range []string{"turn_assess", "goal_submit", "goal_card_submit", "plan_submit"} {
		decision, reason := e.checkBatchPermission(testutil.AnonCtx(testutil.GenActorID()), toolExecutionBatch{calls: []pendingToolCall{{CallableID: callableID, EffectKind: domain.EffectNone}}})
		if decision == "deny" {
			t.Fatalf("interaction primitive %q must bypass card-scope gate, got deny (%q)", callableID, reason)
		}
	}
}

func TestCheckBatchPermission_RegistrationAlwaysConfirms(t *testing.T) {
	// 注册类 callable 扩大宿主信任边界：无论 permission mode、EffectKind、
	// 项目级预授权还是 AutoAllowTools 白名单，都必须挂起 turn 交互确认。
	registrationCallables := []string{
		"appmanager.register",
		"appmanager.register_project",
		"appmanager.install_local",
		"cloudaccount.content_install",
	}
	modes := []struct {
		name            string
		defaultBehavior string
		liveMode        string
	}{
		{name: "permission-default"},
		{name: "allow-default", defaultBehavior: "allow"},
		{name: "bypass-default", defaultBehavior: "bypass"},
		{name: "yolo", liveMode: "yolo"},
		{name: "allow-all", liveMode: "allow-all"},
		{name: "auto", liveMode: "auto"},
	}
	for _, mode := range modes {
		for _, callableID := range registrationCallables {
			for _, effect := range []domain.EffectKind{domain.EffectNone, domain.EffectIrreversible} {
				e := &turnEngine{
					roots: []string{"/workspace"},
					startReq: domain.TurnStartReq{
						CompiledContext: domain.CompiledContext{
							PermissionPolicy: &domain.PermissionPolicy{
								DefaultBehavior: mode.defaultBehavior,
								AutoAllowTools:  []string{callableID},
							},
						},
					},
					hasProjectApproval: func(projectID, callableID string) bool { return true },
					projectID:          "proj-1",
				}
				if mode.liveMode != "" {
					e.livePermissionMode = func() string { return mode.liveMode }
				}
				ctx := testutil.HumanCtx(testutil.GenActorID())
				batch := toolExecutionBatch{calls: []pendingToolCall{{ID: "1", CallableID: callableID, EffectKind: effect}}}
				decision, reason := e.checkBatchPermission(ctx, batch)
				if decision != "confirm" {
					t.Errorf("mode=%q callable=%q effect=%q: expected confirm, got %q (%q)", mode.name, callableID, effect, decision, reason)
				}
			}
		}
	}
}

func TestCheckBatchPermission_OutsideRootsRequiresConfirm(t *testing.T) {
	e := &turnEngine{
		roots: []string{"/workspace"},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "filesystem.write",
				Input:      `{"Path":"/etc/passwd","Content":"x"}`,
				EffectKind: domain.EffectReversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "confirm" {
		t.Errorf("expected confirm, got %q (reason: %q)", decision, reason)
	}
}

func TestPartitionToolCalls_MultipleForksInOneBatch(t *testing.T) {
	calls := []pendingToolCall{
		{ID: "fork-1", CallableID: "workspace.agent_spawn_by_type", LLMName: "fork_general", EffectKind: domain.EffectNone},
		{ID: "fork-2", CallableID: "workspace.agent_spawn_by_type", LLMName: "fork_general", EffectKind: domain.EffectNone},
		{ID: "fork-3", CallableID: "workspace.agent_spawn_by_type", LLMName: "fork_general", EffectKind: domain.EffectNone},
	}
	batches := partitionToolCalls(calls)
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch for multiple fork calls, got %d", len(batches))
	}
	if len(batches[0].calls) != 3 {
		t.Fatalf("expected batch with 3 fork calls, got %d", len(batches[0].calls))
	}
}

func TestCheckBatchPermission_OutsideRootsYoloAllows(t *testing.T) {
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "allow",
				},
			},
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "filesystem.write",
				Input:      `{"Path":"/etc/passwd","Content":"x"}`,
				EffectKind: domain.EffectReversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "allow" {
		t.Errorf("expected allow in yolo mode even for outside-root path, got %q (reason: %q)", decision, reason)
	}
}

func TestCheckBatchPermission_LiveModeOverridesSnapshot(t *testing.T) {
	// Turn was compiled with "permission" (confirm), but user switched to
	// "yolo" mid-turn. The live mode must take precedence so git_push
	// auto-allows instead of popping an approval card.
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "confirm",
				},
			},
		},
		livePermissionMode: func() string { return "yolo" },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "project.git_push",
				Input:      `{"Remote":"origin"}`,
				EffectKind: domain.EffectIrreversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "allow" {
		t.Errorf("expected allow when live mode is yolo, got %q (reason: %q)", decision, reason)
	}
}

func TestCheckBatchPermission_AllowAllModeAllows(t *testing.T) {
	// "allow-all" (formerly "auto") mirrors the legacy "yolo" tool-approval
	// behavior: every mutating tool is auto-allowed, even outside the
	// configured roots. Unlike "yolo" it does not auto-approve
	// plan/goal_submit or block ask_user — those are handled before
	// checkBatchPermission, so here we only assert the tool allow decision.
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "confirm",
				},
			},
		},
		livePermissionMode: func() string { return "allow-all" },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "project.git_push",
				Input:      `{"Remote":"origin"}`,
				EffectKind: domain.EffectIrreversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "allow" {
		t.Errorf("expected allow when live mode is auto, got %q (reason: %q)", decision, reason)
	}
}

func TestCheckBatchPermission_AutoModeRoutineMutationsAllowed(t *testing.T) {
	// auto（bypass 行为）的守卫目标是危险/攻击行为，不是全部可变操作：
	// roots 范围内的常规文件编辑直接放行，不产生 fast model 往返。
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "confirm",
				},
			},
		},
		livePermissionMode: func() string { return "auto" },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "project.edit",
				Input:      `{"Path":"pkg/main.go"}`,
				EffectKind: domain.EffectIrreversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "allow" || reason != "bypass-routine" {
		t.Errorf("expected allow/bypass-routine for in-roots routine mutation, got %q/%q", decision, reason)
	}
}

func TestCheckBatchPermission_AutoModeRiskyCallableReviewed(t *testing.T) {
	// 风险类 callable（命令执行、删除、网络推送、凭据）在 auto 模式下
	// 仍必须交 fast model 逐案裁决。
	for _, id := range []string{"project.git_push", "project.shell_exec", "project.rm", "authenticator-verify"} {
		e := &turnEngine{
			roots: []string{"/workspace"},
			startReq: domain.TurnStartReq{
				CompiledContext: domain.CompiledContext{
					PermissionPolicy: &domain.PermissionPolicy{DefaultBehavior: "confirm"},
				},
			},
			livePermissionMode: func() string { return "auto" },
		}
		ctx := testutil.HumanCtx(testutil.GenActorID())
		batch := toolExecutionBatch{
			calls: []pendingToolCall{
				{ID: "1", CallableID: id, Input: `{"Remote":"origin"}`, EffectKind: domain.EffectIrreversible},
			},
		}
		decision, reason := e.checkBatchPermission(ctx, batch)
		if decision != "bypass" {
			t.Errorf("callable %q: expected bypass (fast model review), got %q/%q", id, decision, reason)
		}
	}
}

func TestCheckBatchPermission_AutoModeOutsideRootsReviewed(t *testing.T) {
	// 常规 callable 但目标路径在 roots 之外：必须交 fast model 裁决，
	// 不得因 "routine" 直接放行。用 OS 临时目录构造跨平台绝对路径。
	outside := filepath.Join(os.TempDir(), "spore-outside-target.txt")
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{DefaultBehavior: "confirm"},
			},
		},
		livePermissionMode: func() string { return "auto" },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "project.edit",
				Input:      fmt.Sprintf(`{"Path":%q}`, outside),
				EffectKind: domain.EffectIrreversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "bypass" {
		t.Errorf("expected bypass for out-of-roots mutation in auto mode, got %q/%q", decision, reason)
	}
}

func TestBypassBatchNeedsReview_Patterns(t *testing.T) {
	e := &turnEngine{roots: []string{"/workspace"}}
	risky := []string{
		"project.shell_exec", "sshmanager.exec", "project.rm", "project.wiki_delete_card",
		"project.git_push", "project.git_reset", "authenticator-verify",
		"workspace.agent_terminate", "computeruse.interact",
	}
	for _, id := range risky {
		if !bypassBatchNeedsReview(e, toolExecutionBatch{calls: []pendingToolCall{{CallableID: id}}}) {
			t.Errorf("expected %q to require fast model review", id)
		}
	}
	routine := []string{
		"project.write", "project.edit", "project.read", "project.grep",
		"project.wiki_create_card", "project.wiki_edit_card", "workspace.agent_spawn_assign",
		"task_create", "task_update", "project.git_commit", "project.git_add",
		"project.graph_save",
	}
	for _, id := range routine {
		if bypassBatchNeedsReview(e, toolExecutionBatch{calls: []pendingToolCall{{CallableID: id}}}) {
			t.Errorf("expected %q to be routine (no review)", id)
		}
	}
}

func TestRebuildToolsForModel_YoloDropsAskUser(t *testing.T) {
	// YOLO 模式：上下文编译时不得注入 ask_user 工具，agent 必须自主决策、
	// 不得向用户提问。这是主防线；executeBlockAskUser 仅作纵深防御。
	e := &turnEngine{
		livePermissionMode: func() string { return "yolo" },
	}
	e.rebuildToolsForModel("any-model")
	for _, tool := range e.tools {
		if tool.Name == "ask_user" {
			t.Fatalf("ask_user must not be injected in YOLO mode, tools=%+v", e.tools)
		}
	}
}

func TestRebuildToolsForModel_AutopilotDropsAskUser(t *testing.T) {
	// autopilot 模式：同 yolo，ask_user 不得注入，agent 必须自主决策。
	e := &turnEngine{
		livePermissionMode: func() string { return "autopilot" },
	}
	e.rebuildToolsForModel("any-model")
	for _, tool := range e.tools {
		if tool.Name == "ask_user" {
			t.Fatalf("ask_user must not be injected in autopilot mode, tools=%+v", e.tools)
		}
	}
}

func TestRebuildToolsForModel_NonYoloInjectsAskUser(t *testing.T) {
	// 非自主模式（auto/permission/bypass/未设置）：注入 ask_user。
	e := &turnEngine{}
	e.rebuildToolsForModel("any-model")
	var sawAskUser bool
	for _, tool := range e.tools {
		if tool.Name == "ask_user" {
			sawAskUser = true
			break
		}
	}
	if !sawAskUser {
		t.Fatalf("ask_user must be injected in non-autonomous mode, tools=%+v", e.tools)
	}
}

func TestCheckBatchPermission_InsideRootsAllows(t *testing.T) {
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "allow",
				},
			},
		},
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	cases := []string{
		`{"Path":"/workspace/file.txt","Content":"x"}`,
		`{"Path":"file.txt","Content":"x"}`,
	}
	for _, input := range cases {
		batch := toolExecutionBatch{
			calls: []pendingToolCall{
				{
					ID:         "1",
					CallableID: "filesystem.write",
					Input:      input,
					EffectKind: domain.EffectReversible,
				},
			},
		}
		decision, reason := e.checkBatchPermission(ctx, batch)
		if decision != "allow" {
			t.Errorf("input %s: expected allow, got %q (reason: %q)", input, decision, reason)
		}
	}
}

func TestCheckBatchPermission_FileRmYoloAllowsOutsideRoots(t *testing.T) {
	// project.rm is EffectIrreversible. In yolo mode it must be auto-allowed
	// even when the target path is outside the configured roots, matching the
	// behavior of other mutating tools.
	e := &turnEngine{
		roots: []string{"/workspace"},
		startReq: domain.TurnStartReq{
			CompiledContext: domain.CompiledContext{
				PermissionPolicy: &domain.PermissionPolicy{
					DefaultBehavior: "confirm",
				},
			},
		},
		livePermissionMode: func() string { return "yolo" },
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	batch := toolExecutionBatch{
		calls: []pendingToolCall{
			{
				ID:         "1",
				CallableID: "project.rm",
				Input:      `{"Path":"/etc/passwd"}`,
				EffectKind: domain.EffectIrreversible,
			},
		},
	}
	decision, reason := e.checkBatchPermission(ctx, batch)
	if decision != "allow" {
		t.Errorf("expected allow for project.rm in yolo mode, got %q (reason: %q)", decision, reason)
	}
}

func TestResolveToolPath(t *testing.T) {
	cases := []struct {
		callableID string
		input      string
		wantPath   string
		wantOK     bool
	}{
		{"filesystem.write", `{"Path":"/tmp/x","Content":"y"}`, "/tmp/x", true},
		{"filesystem.edit", `{"Path":"/tmp/x","Old_string":"a","New_string":"b"}`, "/tmp/x", true},
		{"filesystem.rm", `{"Path":"/tmp/x"}`, "/tmp/x", true},
		{"project.write", `{"Path":"/tmp/x","Content":"y"}`, "/tmp/x", true},
		{"project.edit", `{"Path":"/tmp/x","Old_string":"a","New_string":"b"}`, "/tmp/x", true},
		{"project.rm", `{"Path":"/tmp/x"}`, "/tmp/x", true},
		{"agent_status", `{}`, "", false},
	}
	for _, tc := range cases {
		gotPath, gotOK := resolveToolPath(pendingToolCall{CallableID: tc.callableID, Input: tc.input})
		wantPath := util.NormalizePath(tc.wantPath)
		if gotOK != tc.wantOK || gotPath != wantPath {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", tc.callableID, gotPath, gotOK, wantPath, tc.wantOK)
		}
	}
}

func TestToolEffectIndex_CaseInsensitiveKeys(t *testing.T) {
	tools := []domain.ToolSpec{
		{Name: "FileRead", CallableID: "project.read", EffectKind: string(domain.EffectReversible)},
	}
	out := toolEffectIndex(tools)
	for _, key := range []string{"fileread", "read"} {
		if got, ok := out[key]; !ok || got != domain.EffectReversible {
			t.Fatalf("key %q: got (%q, %v), want (reversible, true)", key, got, ok)
		}
	}
}

func TestInitialStepSeqForTurn(t *testing.T) {
	steps := []domain.Step{
		{ID: "turn-1-llm-001", TurnID: "turn-1"},
		{ID: "turn-1-reasoning-002", TurnID: "turn-1"},
		{ID: "turn-1-tool-003", TurnID: "turn-1"},
		{ID: "turn-2-llm-001", TurnID: "turn-2"},
		{ID: "other-step", TurnID: "turn-3"},
	}
	got := initialStepSeqForTurn(steps, "turn-1")
	if got != 3 {
		t.Errorf("initialStepSeqForTurn(turn-1) = %d, want 3", got)
	}

	got = initialStepSeqForTurn(steps, "turn-2")
	if got != 1 {
		t.Errorf("initialStepSeqForTurn(turn-2) = %d, want 1", got)
	}

	got = initialStepSeqForTurn(steps, "turn-4")
	if got != 0 {
		t.Errorf("initialStepSeqForTurn(turn-4) = %d, want 0 (no existing steps)", got)
	}

	got = initialStepSeqForTurn(nil, "turn-1")
	if got != 0 {
		t.Errorf("initialStepSeqForTurn(nil) = %d, want 0", got)
	}
}

// TestInitialStepSeqForTurn_PreventsIDCollision verifies that a resumed turn
// with initialStepSeqForTurn produces step IDs that don't collide with the
// existing steps from the crashed turn.
func TestInitialStepSeqForTurn_PreventsIDCollision(t *testing.T) {
	turnID := "turn-abc"
	steps := []domain.Step{
		{ID: "turn-abc-llm-001", TurnID: turnID},
		{ID: "turn-abc-reasoning-002", TurnID: turnID},
	}
	base := initialStepSeqForTurn(steps, turnID)

	seq := base
	s1 := newStep(&seq, turnID, domain.TurnActionLLMCall, "llm")
	s2 := newStep(&seq, turnID, domain.TurnActionReasoning, "reasoning")

	existing := map[string]bool{"turn-abc-llm-001": true, "turn-abc-reasoning-002": true}
	if existing[s1.ID] {
		t.Errorf("new step ID %s collides with existing step", s1.ID)
	}
	if existing[s2.ID] {
		t.Errorf("new step ID %s collides with existing step", s2.ID)
	}
}

func TestPreviewReqFromToolInputRegisterInlineManifest(t *testing.T) {
	input := `{"Manifest":{"id":"app.x","name":"X","runtime":"spore","protocolVersion":1,"namespace":"ns","permissions":["shell.exec"]}}`
	req := previewReqFromToolInput("appmanager.register", input)
	if req.Manifest == nil || req.Manifest.ID != "app.x" {
		t.Fatalf("inline manifest not decoded: %+v", req.Manifest)
	}
	if req.Slug != "" || req.Path != "" || req.ProjectID != "" {
		t.Fatalf("non-inline sources should be empty: %+v", req)
	}
}

func TestPreviewReqFromToolInputRegisterProjectStripsForeignSources(t *testing.T) {
	// An LLM should not steer register_project's preview at a local path or
	// cloud slug by injecting those fields; only ProjectId (and the engine-
	// supplied CallerAgentId) is authoritative.
	input := `{"ProjectId":"proj-1","Path":"/evil","Slug":"evil-slug","AppDir":"sub"}`
	req := previewReqFromToolInput("appmanager.register_project", input)
	if req.ProjectID != "proj-1" {
		t.Fatalf("ProjectId = %q", req.ProjectID)
	}
	if req.AppDir != "sub" {
		t.Fatalf("AppDir = %q", req.AppDir)
	}
	if req.Slug != "" || req.Path != "" {
		t.Fatalf("foreign sources not stripped: %+v", req)
	}
}

func TestPreviewReqFromToolInputInstallLocalKeepsPathStripsSlug(t *testing.T) {
	input := `{"Path":"/pkg","Slug":"evil-slug"}`
	req := previewReqFromToolInput("appmanager.install_local", input)
	if req.Path != "/pkg" {
		t.Fatalf("Path = %q", req.Path)
	}
	if req.Slug != "" {
		t.Fatalf("Slug not stripped: %+v", req)
	}
}

func TestPreviewReqFromToolInputContentInstallKeepsSlugStripsPath(t *testing.T) {
	input := `{"Slug":"cloud-app","Path":"/evil"}`
	req := previewReqFromToolInput("cloudaccount.content_install", input)
	if req.Slug != "cloud-app" {
		t.Fatalf("Slug = %q", req.Slug)
	}
	if req.Path != "" {
		t.Fatalf("Path not stripped: %+v", req)
	}
}

func TestPreviewReqFromToolInputMalformedJSON(t *testing.T) {
	req := previewReqFromToolInput("appmanager.register_project", "{not json")
	if req != (gen.AppManagerRegistrationPreviewReq{}) {
		t.Fatalf("malformed input should yield zero req: %+v", req)
	}
}

func TestEnrichRegistrationSummariesNoRegistration(t *testing.T) {
	// Non-registration summaries are passed through untouched and no preview
	// call is made (no appmanager lookup needed).
	summaries := []domain.ToolCallSummary{{ID: "c1", CallableID: "shell.exec"}}
	(&turnEngine{}).enrichRegistrationSummaries(nil, summaries)
	if summaries[0].AppID != "" || summaries[0].PermissionNote != "" {
		t.Fatalf("non-registration summary mutated: %+v", summaries[0])
	}
}
