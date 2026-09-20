package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	agentactor "github.com/qomos-w/sporemind/pkg/actor/agent"
	"github.com/qomos-w/sporemind/pkg/agentkit"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

var (
	// branchSuffixRe matches the uuid-derived 8-hex-character suffix that
	// every worktreeBranchName result ends with.
	branchSuffixRe = regexp.MustCompile(`-[0-9a-f]{8}$`)
	// wfFallbackRe matches the exact fallback format for empty/whitespace-only
	// or forbidden-only inputs: "wf-" + 8 hex chars.
	wfFallbackRe = regexp.MustCompile(`^wf-[0-9a-f]{8}$`)
)

// fakeClaimTaskCard emulates project.wiki_claim_task_card over a fake card
// status store: a claim succeeds only when the current status is in
// ExpectedStatuses, then flips the stored status to "doing" and returns the
// raw with the rewritten status line.
func fakeClaimTaskCard(cardStatus map[string]string, bodyByID map[string]string) func(payload any) any {
	return func(payload any) any {
		req, ok := payload.(gen.WikiClaimTaskCardReq)
		if !ok {
			return nil
		}
		cur := cardStatus[req.ID]
		match := false
		for _, exp := range req.ExpectedStatuses {
			if cur == exp {
				match = true
				break
			}
		}
		if !match {
			return fmt.Errorf("project.wiki.claim_task_card: expected status one of %v, got %q", req.ExpectedStatuses, cur)
		}
		cardStatus[req.ID] = req.Status
		return gen.WikiClaimTaskCardResp{
			PreviousStatus: cur,
			Raw:            "---\ntype: task\nstatus: " + req.Status + "\n---\n" + bodyByID[req.ID],
		}
	}
}

// fakeGetTaskCard emulates project.wiki_get_card over a fake card store:
// returns the raw frontmatter (no status rewrite) for known ids, and a
// stable "not found" error otherwise. Used by tests that go through
// handleAgentSpawnAssign / handleAgentSpawnByType, which now read the
// bound card to determine data.exec.kind before the claim.
func fakeGetTaskCard(cardStatus map[string]string, bodyByID map[string]string) func(payload any) any {
	return func(payload any) any {
		req, ok := payload.(domain.WikiGetCardReq)
		if !ok {
			return nil
		}
		body, ok := bodyByID[req.ID]
		if !ok {
			return fmt.Errorf("project.wiki.get_card: card %q not found", req.ID)
		}
		// Preserve the stored status (the dispatcher reads the card
		// BEFORE the claim CAS-flip; it must not see "doing" yet).
		status := cardStatus[req.ID]
		if status == "" {
			status = "backlog"
		}
		return domain.WikiGetCardResp{
			ID:  req.ID,
			Raw: "---\ntype: task\nstatus: " + status + "\n---\n" + body,
		}
	}
}

// fakeGetTaskCardWithCategory is like fakeGetTaskCard but injects a
// data.category field into the frontmatter for the requested card.
// Cards without a category entry get no data block.
func fakeGetTaskCardWithCategory(cardStatus map[string]string, bodyByID map[string]string, categoryByID map[string]string) func(payload any) any {
	return func(payload any) any {
		req, ok := payload.(domain.WikiGetCardReq)
		if !ok {
			return nil
		}
		body, ok := bodyByID[req.ID]
		if !ok {
			return fmt.Errorf("project.wiki.get_card: card %q not found", req.ID)
		}
		status := cardStatus[req.ID]
		if status == "" {
			status = "backlog"
		}
		category := categoryByID[req.ID]
		dataBlock := ""
		if category != "" {
			dataBlock = "data:\n  category: " + category + "\n"
		}
		return domain.WikiGetCardResp{
			ID:  req.ID,
			Raw: "---\ntype: task\n" + dataBlock + "status: " + status + "\n---\n" + body,
		}
	}
}

// fakeClaimTaskCardWithCategory is like fakeClaimTaskCard but preserves
// a data.category field in the returned raw, matching the production
// project actor's behavior (the saved Raw includes the data block, not
// just type+status). Tests that exercise the post-claim CardRaw (e.g.
// the coding-card gate in executor_worker_task.go, which reads
// req.CardRaw after the dispatcher overwrites it with the claim
// response raw) need this fidelity; otherwise the category is silently
// dropped between fetch and claim and the gate sees an empty category.
func fakeClaimTaskCardWithCategory(cardStatus map[string]string, bodyByID map[string]string, categoryByID map[string]string) func(payload any) any {
	return func(payload any) any {
		req, ok := payload.(gen.WikiClaimTaskCardReq)
		if !ok {
			return nil
		}
		cur := cardStatus[req.ID]
		match := false
		for _, exp := range req.ExpectedStatuses {
			if cur == exp {
				match = true
				break
			}
		}
		if !match {
			return fmt.Errorf("project.wiki.claim_task_card: expected status one of %v, got %q", req.ExpectedStatuses, cur)
		}
		cardStatus[req.ID] = req.Status
		category := categoryByID[req.ID]
		dataBlock := ""
		if category != "" {
			dataBlock = "data:\n  category: " + category + "\n"
		}
		return gen.WikiClaimTaskCardResp{
			PreviousStatus: cur,
			Raw:            "---\ntype: task\n" + dataBlock + "status: " + req.Status + "\n---\n" + bodyByID[req.ID],
		}
	}
}

// TestWorktreeBranchName verifies worktreeBranchName: whitespace and
// git ref-forbidden characters are sanitized, leading/trailing separators
// are trimmed, a short uuid suffix is always appended, and inputs that
// cannot contribute any name characters fall back to "wf-" + 8 hex chars.
func TestWorktreeBranchName(t *testing.T) {
	cases := []struct {
		in   string
		want string // prefix expectation
	}{
		{in: "Calm Fox", want: "Calm-Fox-"},
		{in: "T3 重构 developer 面板", want: "T3-重构-developer-面板-"},
		{in: "bad ~^:?*[]\\ name", want: "bad-name-"},
		{in: "  ", want: "wf-"},
		{in: "-lead.", want: "lead-"},
		{in: "", want: "wf-"},             // empty display name
		{in: "~^:?*[]\\", want: "wf-"},    // only git-forbidden chars
		{in: "\u3000\u3000", want: "wf-"}, // full-width ideographic spaces
		{in: "\ta\nb", want: "a-b-"},      // tab/newline collapse to a single '-'
		{in: "a  b--c", want: "a-b-c-"},   // runs of spaces/hyphens collapse
		{in: "a -- b", want: "a-b-"},      // mixed space+hyphen runs collapse
		{in: "..name..", want: "name-"},   // dots trimmed at both ends
		{in: ". - .", want: "wf-"},        // separators only → fallback
	}
	for _, c := range cases {
		got := worktreeBranchName(c.in)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("worktreeBranchName(%q) = %q, want prefix %q", c.in, got, c.want)
		}
		if strings.ContainsAny(got, " ~^:?*[]\\") {
			t.Errorf("worktreeBranchName(%q) = %q contains forbidden characters", c.in, got)
		}
		if !branchSuffixRe.MatchString(got) {
			t.Errorf("worktreeBranchName(%q) = %q: want a '-' followed by 8 hex uuid chars", c.in, got)
		}
	}
	// Inputs that cannot contribute any name character must fall back to
	// exactly "wf-xxxxxxxx" (wf- + 8 hex chars).
	for _, in := range []string{"", "  ", "\t\n", "~^:?*[]\\", "\u3000"} {
		if got := worktreeBranchName(in); !wfFallbackRe.MatchString(got) {
			t.Errorf("worktreeBranchName(%q) = %q, want fallback matching %s", in, got, wfFallbackRe)
		}
	}
}

// TestWorktreeBranchNameUniqueness verifies the uuid suffix makes branch
// names unique even for identical display names (agents with the same
// display name spawn many times).
func TestWorktreeBranchNameUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		got := worktreeBranchName("Calm Fox")
		if !branchSuffixRe.MatchString(got) {
			t.Fatalf("worktreeBranchName(Calm Fox) = %q lacks uuid suffix", got)
		}
		seen[got] = struct{}{}
	}
	if len(seen) != 100 {
		t.Fatalf("expected 100 distinct branch names, got %d (uuid suffix collision?)", len(seen))
	}
}

// TestCollapseHyphens verifies collapseHyphens reduces any run of two or
// more hyphens down to a single hyphen.
func TestCollapseHyphens(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"a", "a"},
		{"a-b", "a-b"},
		{"a--b", "a-b"},
		{"a---b", "a-b"},
		{"a----b", "a-b"},
		{"-", "-"},
		{"--", "-"},
		{"a---", "a-"},
	}
	for _, c := range cases {
		if got := collapseHyphens(c.in); got != c.want {
			t.Errorf("collapseHyphens(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHandleAgentSpawnAssign_Success(t *testing.T) {
	a, ctx := freshActor(t)
	beforeAgents := len(a.Agents)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}
	a.accountPrefs.Preferences = map[string]string{"permissionMode": "auto"}

	var spawnReq domain.ProjectSpawnAgentReq
	var stampOwnerReq gen.WikiSetMapOwnerReq
	stampOwnerCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_map_owner":
				stampOwnerCalled = true
				if req, ok := payload.(gen.WikiSetMapOwnerReq); ok {
					stampOwnerReq = req
				}
				return gen.WikiSetMapOwnerResp{}
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	if resp.AgentActorID != agentActorID {
		t.Errorf("expected AgentActorID %q, got %q", agentActorID, resp.AgentActorID)
	}
	if resp.DisplayName == "" || resp.DisplayName == "Worker One" {
		t.Errorf("expected a generated DisplayName distinct from To, got %q", resp.DisplayName)
	}
	if resp.Goal.Condition != "Implement the widget" {
		t.Errorf("expected Goal.Condition from task card body, got %q", resp.Goal.Condition)
	}
	if resp.Goal.MaxTurns != 5 {
		t.Errorf("expected Goal.MaxTurns 5, got %d", resp.Goal.MaxTurns)
	}
	if resp.Goal.BoundTaskCardID != "card-1" {
		t.Errorf("expected Goal.BoundTaskCardId 'card-1', got %q", resp.Goal.BoundTaskCardID)
	}

	if len(a.Agents) != beforeAgents+1 {
		t.Fatalf("expected %d stored agents, got %d", beforeAgents+1, len(a.Agents))
	}
	ag := a.Agents[len(a.Agents)-1]
	if ag.ActorID != agentActorID {
		t.Errorf("expected stored ActorID %q, got %q", agentActorID, ag.ActorID)
	}
	if ag.DisplayName == "" || ag.DisplayName == "Worker One" {
		t.Errorf("expected stored generated DisplayName distinct from To, got %q", ag.DisplayName)
	}
	if ag.Title != "card-1" {
		t.Errorf("expected stored Title from bound task card, got %q", ag.Title)
	}
	if ag.AgentKind != domain.AgentKindWorker {
		t.Errorf("expected stored AgentKind %q, got %q", domain.AgentKindWorker, ag.AgentKind)
	}
	if ag.ProjectID != projectID {
		t.Errorf("expected stored ProjectID %q, got %q", projectID, ag.ProjectID)
	}
	if ag.Mode == nil || ag.Mode.BoundTaskCardID != "card-1" {
		t.Errorf("expected stored goal mode for card-1, got %+v", ag.Mode)
	}

	if spawnReq.AgentKind != domain.AgentKindWorker {
		t.Errorf("expected spawn AgentKind %q, got %q", domain.AgentKindWorker, spawnReq.AgentKind)
	}
	if spawnReq.DisplayName == "" || spawnReq.DisplayName == "Worker One" {
		t.Errorf("expected spawned generated DisplayName distinct from To, got %q", spawnReq.DisplayName)
	}
	if spawnReq.ProjectID != projectID {
		t.Errorf("expected spawn ProjectID %q, got %q", projectID, spawnReq.ProjectID)
	}
	if spawnReq.PermissionMode != "auto" {
		t.Errorf("expected spawn PermissionMode %q (from account global preference), got %q", "auto", spawnReq.PermissionMode)
	}

	// Goal is threaded through the spawn request, not a separate invoke.
	if spawnReq.GoalCondition != "Implement the widget" {
		t.Errorf("expected spawn GoalCondition from task card body, got %q", spawnReq.GoalCondition)
	}
	if spawnReq.InterpretedGoal != "Worker One" {
		t.Errorf("expected spawn InterpretedGoal from To, got %q", spawnReq.InterpretedGoal)
	}
	if spawnReq.GoalMaxTurns != 5 {
		t.Errorf("expected spawn GoalMaxTurns 5, got %d", spawnReq.GoalMaxTurns)
	}
	if spawnReq.BoundTaskCardID != "card-1" {
		t.Errorf("expected spawn BoundTaskCardID 'card-1', got %q", spawnReq.BoundTaskCardID)
	}

	// The worker's actor id must be stamped onto the task card's
	// data.ownerAgentId so card-side consumers can resolve the binding.
	if !stampOwnerCalled {
		t.Error("expected project.wiki_set_map_owner to be invoked after spawn")
	}
	if stampOwnerReq.MapID != "card-1" || stampOwnerReq.OwnerActorID != agentActorID {
		t.Errorf("stamp owner req = {MapID: %q, OwnerActorID: %q}, want {card-1, %s}",
			stampOwnerReq.MapID, stampOwnerReq.OwnerActorID, agentActorID)
	}
}

// TestHandleAgentSpawnAssign_InheritsOwnerPluginDevBundle verifies the
// spawn_assign bundle-inheritance wiring: when the workflow owner agent's
// component mounts include an enabled plugin-dev bundle mount, the
// worker's ProjectSpawnAgentReq.ExtraBundleIDs must carry that bundle so the
// worker inherits the dev callables without a manual component.mount. Owners
// without the mount must not leak the bundle into their workers, and a
// dev-app project whose owner already has the bundle still yields exactly one
// entry (idempotent dedup via extraBundlesForAppKind).
func TestHandleAgentSpawnAssign_InheritsOwnerPluginDevBundle(t *testing.T) {
	run := func(t *testing.T, appKind string, ownerMounts []domain.AgentComponentMount) (domain.ProjectSpawnAgentReq, error) {
		a, ctx := freshActor(t)
		var ts uint64
		g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
		projectID := g.Next().String()
		agentActorID := g.Next().String()
		callerAgentID := g.Next().String()
		a.Mounts = []domain.ProjectRef{
			{Name: "p1", Path: t.TempDir(), ActorID: projectID, AppKind: appKind},
		}
		a.accountPrefs.Preferences = map[string]string{"permissionMode": "auto"}

		var spawnReq domain.ProjectSpawnAgentReq
		ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
			aidStr := aid.String()
			if aidStr == callerAgentID {
				return testutil.NewFakeRef(aid, func(callID string, _ any) any {
					switch callID {
					case "agent_status":
						return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
					case "resolve_child_slot":
						return agentactor.ResolveChildSlotResp{Slot: testForkSlot()}
					case "component_list":
						return domain.AgentComponentListResp{Items: ownerMounts}
					}
					return nil
				}), true
			}
			cardStatus := map[string]string{"card-1": "backlog"}
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "project.spawn_agent":
					if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
						spawnReq = req
					}
					return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
				case "project.wiki_claim_task_card":
					return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
				case "project.wiki_get_card":
					return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
				case "project.wiki_set_map_owner":
					return gen.WikiSetMapOwnerResp{}
				}
				return nil
			}), true
		}

		_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
			To:              "Worker One",
			AgentKind:       domain.AgentKindWorker,
			InterpretedGoal: "interpreted goal",
			BoundTaskCardID: "card-1",
			MaxTurns:        5,
			ProjectID:       "p1",
			CallerAgentID:   callerAgentID,
		})
		return spawnReq, err
	}

	mounted := []domain.AgentComponentMount{
		{CardID: agentkit.PluginDevBundleID, Kind: "bundle", Enabled: true},
	}

	t.Run("owner with mounted bundle inherits it on a regular project", func(t *testing.T) {
		spawn, err := run(t, "", mounted)
		if err != nil {
			t.Fatalf("handleAgentSpawnAssign: %v", err)
		}
		if len(spawn.ExtraBundleIDs) != 1 || spawn.ExtraBundleIDs[0] != agentkit.PluginDevBundleID {
			t.Fatalf("expected ExtraBundleIDs [%s], got %v", agentkit.PluginDevBundleID, spawn.ExtraBundleIDs)
		}
	})

	t.Run("owner with mounted bundle on a dev-app project yields exactly one entry", func(t *testing.T) {
		spawn, err := run(t, devAppDirName, mounted)
		if err != nil {
			t.Fatalf("handleAgentSpawnAssign: %v", err)
		}
		if len(spawn.ExtraBundleIDs) != 1 || spawn.ExtraBundleIDs[0] != agentkit.PluginDevBundleID {
			t.Fatalf("expected exactly one ExtraBundleIDs entry [%s], got %v", agentkit.PluginDevBundleID, spawn.ExtraBundleIDs)
		}
	})

	t.Run("owner without the bundle spawns without ExtraBundleIDs", func(t *testing.T) {
		spawn, err := run(t, "", []domain.AgentComponentMount{
			{CardID: "builtin:bundle:file-tools", Kind: "bundle", Enabled: true},
		})
		if err != nil {
			t.Fatalf("handleAgentSpawnAssign: %v", err)
		}
		if len(spawn.ExtraBundleIDs) != 0 {
			t.Fatalf("expected no ExtraBundleIDs, got %v", spawn.ExtraBundleIDs)
		}
	})
}

// TestHandleAgentSpawnAssign_ValidationErrors verifies each required field is
// validated before any cross-actor call is made.
func TestHandleAgentSpawnAssign_GeneratesConfiguredRandomName(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	cfg, ok := a.findAgentKindConfig(domain.AgentKindWorker)
	if !ok {
		t.Fatal("missing worker config")
	}
	cfg.RandomName = &domain.RandomNameConfig{Enabled: true, Prefixes: []string{"Swift"}, Suffixes: []string{"Fox"}}
	cfg.CompactionPolicy = &gen.CompactionPolicy{Enabled: true, BudgetMode: "percentage", TokenBudget: 75}
	for i := range a.AgentKindConfigs {
		if a.AgentKindConfigs[i].Kind == domain.AgentKindWorker {
			a.AgentKindConfigs[i] = cfg
			break
		}
	}

	workerSlot := domain.ModelSlot{Candidates: []domain.ModelRef{{Kind: "unit", Unit: &gen.ModelUnit{Model: "worker-default", Provider: "provider"}}}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				case "resolve_child_slot":
					return agentactor.ResolveChildSlotResp{Slot: workerSlot}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Task"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Task"})(payload)
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind: domain.AgentKindWorker, BoundTaskCardID: "card-1", ProjectID: projectID, CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.DisplayName != "Swift Fox" {
		t.Fatalf("generated display name = %q, want %q", resp.DisplayName, "Swift Fox")
	}
	spawned := a.Agents[len(a.Agents)-1]
	if spawned.Primary == nil || spawned.Primary.Candidates[0].Unit == nil || spawned.Primary.Candidates[0].Unit.Model != "worker-default" {
		t.Fatalf("configured default primary slot = %+v", spawned.Primary)
	}
	if spawned.CompactionPolicy == nil || !spawned.CompactionPolicy.Enabled || spawned.CompactionPolicy.TokenBudget != 75 {
		t.Fatalf("configured default compaction policy = %+v", spawned.CompactionPolicy)
	}
}

// TestHandleAgentSpawnAssign_CategoryRoutesToAgentKind verifies that
// data.category on the bound task card selects the AgentKind according
// to the dispatcher routing table, that an explicit non-default caller
// choice wins, and that research cards are rejected as owner-only.
func TestHandleAgentSpawnAssign_CategoryRoutesToAgentKind(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	a.accountPrefs.Preferences = map[string]string{"permissionMode": "auto"}

	cases := []struct {
		name        string
		cardID      string
		category    string
		reqKind     string
		wantKind    string
		wantErr     bool
		errContains string
	}{
		{"explore maps to scout", "card-explore", "explore", domain.AgentKindWorker, domain.AgentKindScout, false, ""},
		{"execute maps to worker", "card-execute", "execute", domain.AgentKindWorker, domain.AgentKindWorker, false, ""},
		{"code maps to worker", "card-code", "code", domain.AgentKindWorker, domain.AgentKindWorker, false, ""},
		{"review maps to reviewer", "card-review", "review", domain.AgentKindWorker, domain.AgentKindReviewer, false, ""},
		{"no category keeps worker", "card-none", "", domain.AgentKindWorker, domain.AgentKindWorker, false, ""},
		{"caller explicit wins", "card-explore", "explore", domain.AgentKindReviewer, domain.AgentKindReviewer, false, ""},
		{"research errors", "card-research", "research", domain.AgentKindWorker, "", true, "fork_explore"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cardStatus := map[string]string{tc.cardID: "backlog"}
			bodyByID := map[string]string{tc.cardID: "Task body"}
			categoryByID := map[string]string{tc.cardID: tc.category}
			var spawnReq domain.ProjectSpawnAgentReq
			var spawnCalled bool

			ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
				if aid.String() == callerAgentID {
					return testutil.NewFakeRef(aid, func(callID string, _ any) any {
						switch callID {
						case "agent_status":
							return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
						case "resolve_child_slot":
							return agentactor.ResolveChildSlotResp{}
						}
						return nil
					}), true
				}
				return testutil.NewFakeRef(aid, func(callID string, payload any) any {
					switch callID {
					case "project.spawn_agent":
						if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
							spawnReq = req
							spawnCalled = true
						}
						return domain.ProjectSpawnAgentResp{ActorID: g.Next().String()}
					case "project.wiki_claim_task_card":
						return fakeClaimTaskCard(cardStatus, bodyByID)(payload)
					case "project.wiki_get_card":
						return fakeGetTaskCardWithCategory(cardStatus, bodyByID, categoryByID)(payload)
					case "project.wiki_set_map_owner":
						return gen.WikiSetMapOwnerResp{}
					}
					return nil
				}), true
			}

			_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
				To:              "Task",
				AgentKind:       tc.reqKind,
				BoundTaskCardID: tc.cardID,
				ProjectID:       projectID,
				CallerAgentID:   callerAgentID,
			})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("handleAgentSpawnAssign: %v", err)
			}
			if !spawnCalled {
				t.Fatal("project.spawn_agent was not invoked")
			}
			if spawnReq.AgentKind != tc.wantKind {
				t.Errorf("spawn AgentKind = %q, want %q", spawnReq.AgentKind, tc.wantKind)
			}
		})
	}
}

func TestHandleAgentSpawnAssign_ValidationErrors(t *testing.T) {
	a, ctx := freshActor(t)

	base := domain.WorkspaceAgentSpawnAssignReq{
		To:        "Worker One",
		AgentKind: domain.AgentKindWorker,
	}
	cases := []struct {
		name string
		req  domain.WorkspaceAgentSpawnAssignReq
	}{
		{"missing AgentKind", func() domain.WorkspaceAgentSpawnAssignReq {
			r := base
			r.AgentKind = ""
			return r
		}()},
		{"invalid AgentKind", func() domain.WorkspaceAgentSpawnAssignReq {
			r := base
			r.AgentKind = "bogus-kind"
			return r
		}()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.handleAgentSpawnAssign(ctx, tc.req); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestHandleAgentSpawnAssign_ClaimFailure verifies that when BoundTaskCardID
// is set but the card cannot be claimed (CAS fails), the handler returns an
// error and does not spawn an agent.
func TestHandleAgentSpawnAssign_RejectsBoundTaskWithoutWorkflowMode(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	calledProject := false
	claimCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{}
				}
				return nil
			}), true
		}
		calledProject = true
		// Allow wiki_get_card to succeed so the dispatcher's prefetch
		// returns a body (this is a read-only call, not a claim). The
		// workflow check inside worker_task Preflight is what must
		// fail. The claim must not run.
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "project.wiki_get_card" {
				return domain.WikiGetCardResp{ID: "card-1", Raw: "---\ntype: task\nstatus: backlog\n---\nTask body"}
			}
			if callID == "project.wiki_claim_task_card" {
				claimCalled = true
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To: "Worker One", AgentKind: domain.AgentKindWorker,
		BoundTaskCardID: "card-1", ProjectID: projectID,
		CallerAgentID: callerAgentID,
	})
	if err == nil || !strings.Contains(err.Error(), "requires an active workflow") {
		t.Fatalf("expected workflow mode rejection, got %v", err)
	}
	// The dispatcher prefetches the card body (read-only) before the
	// workflow check, so the project is contacted — but the claim
	// itself must not run.
	if !calledProject {
		t.Fatal("dispatcher must prefetch the bound task card before claim")
	}
	if claimCalled {
		t.Fatal("task claim must not run before workflow mode authorization")
	}
	if len(a.Agents) != 0 {
		t.Fatalf("expected no spawned agents, got %d", len(a.Agents))
	}
}

func TestResolveWorkflowProjectID(t *testing.T) {
	a := &Actor{
		Mounts: []domain.ProjectRef{
			{Name: "workspace", ActorID: "system-id", System: true},
			{Name: "sporemind", ActorID: "project-id"},
		},
		Agents: []domain.AgentRef{{ID: "caller", ActorID: "caller-id", ProjectID: "project-id"}},
	}

	for _, tc := range []struct {
		name          string
		projectID     string
		callerAgentID string
		want          string
	}{
		{name: "project name", projectID: "sporemind", want: "project-id"},
		{name: "project actor ID", projectID: "project-id", want: "project-id"},
		{name: "caller project", callerAgentID: "caller-id", want: "project-id"},
		{name: "first non-system project", want: "project-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.resolveWorkflowProjectID(tc.projectID, tc.callerAgentID)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("resolveWorkflowProjectID() = %q, want %q", got, tc.want)
			}
		})
	}

	if _, err := a.resolveWorkflowProjectID("missing", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected project-not-found error, got %v", err)
	}
}

func TestIsDependencyBlockError(t *testing.T) {
	// Exact phrasing emitted by project.wiki_set_status claim guard.
	blockErr := fmt.Errorf("project.wiki.set_status: cannot claim task card %q: dependencies not done: %s", "t", "dep-1")
	if !isDependencyBlockError(blockErr) {
		t.Errorf("expected dependency block to be detected: %v", blockErr)
	}
	// A CAS status mismatch is NOT a dependency block.
	if isDependencyBlockError(fmt.Errorf("project.wiki.set_status: expected status %q, got %q", "backlog", "doing")) {
		t.Error("CAS mismatch should not be treated as a dependency block")
	}
	if isDependencyBlockError(nil) {
		t.Error("nil error should not be a dependency block")
	}
}

func TestHandleAgentSpawnAssign_UnknownKindDoesNotClaim(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	claimCalled := false
	workflowChecked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					workflowChecked = true
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "project.wiki_claim_task_card" {
				claimCalled = true
			}
			if callID == "project.wiki_get_card" {
				return domain.WikiGetCardResp{ID: "card-1", Raw: "---\ntype: task\nstatus: backlog\n---\nTask body"}
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind:       "unconfigured_kind",
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
		t.Fatalf("expected unknown-kind error, got %v", err)
	}
	if workflowChecked {
		t.Fatal("invalid kind must fail before checking the caller workflow")
	}
	if claimCalled {
		t.Fatal("invalid kind must fail before claiming the task card")
	}
}

func TestHandleAgentSpawnAssign_ClaimFailure(t *testing.T) {
	a, ctx := freshActor(t)
	beforeAgents := len(a.Agents)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	// A ref with a nil invokeFn returns a nil Call from Invoke, so
	// claimTaskCard sees call == nil and reports the claim as failed.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		// wiki_get_card answers cleanly so the dispatcher's prefetch
		// reaches the claim phase; claimTaskCard's nil-Call path is
		// what produces the transport failure under test.
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			if callID == "project.wiki_get_card" {
				return domain.WikiGetCardResp{ID: "card-1", Raw: "---\ntype: task\nstatus: backlog\n---\nTask body"}
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err == nil {
		t.Fatal("expected claim error, got nil")
	}
	// A nil-Call invoke is a transport failure, not a CAS miss: it must
	// surface as-is instead of being misreported as "already claimed".
	if !strings.Contains(err.Error(), "claim task card") {
		t.Errorf("expected transport error surfaced, got %v", err)
	}
	if len(a.Agents) != beforeAgents {
		t.Errorf("expected no agents stored, got %d", len(a.Agents))
	}
}

// TestHandleAgentSpawnAssign_NoDoubleBookingOnOrphanedCard reproduces the
// double-booking bug: when a worker dies leaving its card stuck at "doing"
// with no live binding, a reclaiming spawn_assign must keep the card claimed
// (doing) so a second concurrent spawn is rejected — not reset it to backlog
// (which let the next spawn succeed and bind a second agent to the same card).
func TestHandleAgentSpawnAssign_NoDoubleBookingOnOrphanedCard(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	// Card starts "doing" — an orphaned claim (worker died, card stuck).
	cardStatus := map[string]string{"card-1": "doing"}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_set_status":
				req := payload.(gen.WikiSetStatusReq)
				cur := cardStatus[req.ID]
				if req.ExpectedStatus != "" && cur != req.ExpectedStatus {
					return fmt.Errorf("project.wiki.set_status: expected %q, got %q", req.ExpectedStatus, cur)
				}
				cardStatus[req.ID] = req.Status
				return gen.WikiSetStatusResp{PreviousStatus: cur}
			}
			return nil
		}), true
	}

	// First spawn reclaims the orphaned card.
	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("first spawn should reclaim orphaned card: %v", err)
	}
	if resp.AgentActorID != agentActorID {
		t.Errorf("expected spawned agent %q, got %q", agentActorID, resp.AgentActorID)
	}
	if cardStatus["card-1"] != "doing" {
		t.Fatalf("orphan reclaim left card at %q, want doing (must stay claimed)", cardStatus["card-1"])
	}
	if len(a.Agents) != 1 {
		t.Fatalf("expected 1 agent after first spawn, got %d", len(a.Agents))
	}

	// Second spawn to the same (now in-use) card must be rejected.
	_, err = a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker Two",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
	})
	if err == nil {
		t.Fatal("second spawn to an in-use card should be rejected")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("expected 'already claimed' rejection, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected still 1 agent (no double-booking), got %d", len(a.Agents))
	}
}

// TestHandleAgentReview_Approve verifies approve tears down the agent (async)
// and removes it from a.Agents.
func TestHandleAgentReview_Approve(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
	if resp.CardStatus != "done" {
		t.Errorf("expected CardStatus 'done', got %q", resp.CardStatus)
	}
	// Cascade delete marks the agent "deleting" (tombstone) rather than
	// removing it synchronously. It stays in a.Agents until async teardown
	// converges, but is excluded from the projection.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting, got %d agents, status %q", len(a.Agents), func() string {
			if len(a.Agents) > 0 {
				return a.Agents[0].DeletionStatus
			}
			return ""
		}())
	}
	// Projection must exclude the deleting agent.
	state := a.buildAgentListState(true)
	if len(state.Items) != 0 {
		t.Errorf("expected 0 items in projection, got %d", len(state.Items))
	}
}

// TestHandleAgentReview_ApproveAbortsWhenCardFlipFails pins the checked card
// flip: when project.wiki_set_status fails, approve must abort BEFORE any
// mutation — the worker stays registered (no cascadeDelete) and the response
// carries an error. The old fire-and-forget flip tore the worker down while
// the card stayed pending_review, wedging the map with no agent to re-review
// (the T6 stall shape).
func TestHandleAgentReview_ApproveAbortsWhenCardFlipFails(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"}}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, _ any) any {
			switch callID {
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			case "project.wiki_set_status":
				return fmt.Errorf("project.wiki.set_status: verify after save: disk full")
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err == nil {
		t.Fatalf("expected approve to abort on card-flip failure, got %+v", resp)
	}
	if resp.Approved {
		t.Error("Approved must be false when the flip fails")
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus == "deleting" {
		t.Errorf("worker must stay registered and unmarked (no cascadeDelete), got %+v", a.Agents)
	}
}

// TestHandleAgentReview_ApproveCASMissOnResolvedCard pins the CAS guard: when
// the bound card is already resolved (done — e.g. a prior approve's teardown
// aborted and the owner re-approves), approve loses cleanly with no mutation,
// and the error points at agent_terminate as the orphan disposal path.
func TestHandleAgentReview_ApproveCASMissOnResolvedCard(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	cardStatus := map[string]string{"card-1": "done"}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"}}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			case "project.wiki_set_status":
				req := payload.(gen.WikiSetStatusReq)
				cur := cardStatus[req.ID]
				if req.ExpectedStatus != "" && cur != req.ExpectedStatus {
					return fmt.Errorf("project.wiki.set_status: expected status %q, got %q", req.ExpectedStatus, cur)
				}
				cardStatus[req.ID] = req.Status
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err == nil {
		t.Fatalf("expected approve to abort on CAS miss, got %+v", resp)
	}
	if !strings.Contains(err.Error(), "agent_terminate") {
		t.Errorf("error should point at agent_terminate for the orphan:\n%v", err)
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus == "deleting" {
		t.Errorf("worker must stay registered and unmarked, got %+v", a.Agents)
	}
	if cardStatus["card-1"] != "done" {
		t.Errorf("card status must be untouched, got %q", cardStatus["card-1"])
	}
}

// TestHandleAgentReview_ApproveNoGitWorktreeSkipsMerge pins the no-git
// workflow approve path: a workflow worker spawned in no-git mode carries an
// empty Mode.ActiveWorkflowWorktreeID (no child worktree — it runs directly on
// the project root). reviewApprove must skip the entire merge/verify block,
// mark the bound task card done, and cascade-delete the agent — the worktree
// merge callables must never be invoked.
func TestHandleAgentReview_ApproveNoGitWorktreeSkipsMerge(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Worker#0001",
			ActorID:       agentActorID,
			ProjectID:     projectID,
			AgentKind:     domain.AgentKindWorker,
			DisplayName:   "Worker One",
			ParentAgentID: parentAgentID, // workflow worker under the map owner
			// No-git activation: no child worktree is ever created, so the
			// worker's Mode carries an empty ActiveWorkflowWorktreeID.
			Mode:      &gen.AgentModeState{ActiveWorkflowWorktreeID: ""},
			LoadState: "loaded",
		},
	}

	mergeCalls, verifyCalls := 0, 0
	var setStatusReq gen.WikiSetStatusReq
	statusCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{
						Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"},
					}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_merge_to_parent":
				mergeCalls++
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.worktree_verify_merged_to_parent":
				verifyCalls++
				return gen.ProjectWorktreeVerifyMergedResp{Status: "merged"}
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReq = req
					statusCalled = true
				}
				return gen.WikiSetStatusResp{PreviousStatus: "pending_review"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_task_outputs":
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Fatalf("expected approve/done, got Approved=%v CardStatus=%q", resp.Approved, resp.CardStatus)
	}
	// No child worktree → neither merge nor verify may be invoked.
	if mergeCalls != 0 {
		t.Errorf("approve must not merge a no-git worker (no child worktree), got %d merge calls", mergeCalls)
	}
	if verifyCalls != 0 {
		t.Errorf("approve must not verify a no-git worker (no child worktree), got %d verify calls", verifyCalls)
	}
	// The bound task card must still reach done, and the worker is torn down.
	if !statusCalled {
		t.Fatal("expected project.wiki.set_status to mark the task card done")
	}
	if setStatusReq.ID != "card-1" || setStatusReq.Status != "done" {
		t.Errorf("expected set_status {ID: card-1, Status: done}, got {ID: %q, Status: %q}", setStatusReq.ID, setStatusReq.Status)
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting after approve, got %d agents", len(a.Agents))
	}
}

// TestHandleAgentReview_ResolvesFromAuthoritativeCardOnCacheMiss pins the
// desync-recovery path: when the in-memory a.Agents cache has dropped a worker
// (a persist write failed, or a restart reload interleaved with a mutation)
// but the authoritative .ragents card still carries it, agent_review must
// resolve the agent from the card, heal the cache, and proceed to approve —
// instead of dead-ending on "not found" and leaving the worktree
// un-releasable.
func TestHandleAgentReview_ResolvesFromAuthoritativeCardOnCacheMiss(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	projectID := genID()
	parentAgentID := genID()
	worker := domain.AgentRef{
		ID:            "Worker#0001",
		ActorID:       agentActorID,
		ProjectID:     projectID,
		AgentKind:     domain.AgentKindWorker,
		DisplayName:   "Worker One",
		ParentAgentID: parentAgentID,
		Mode:          &gen.AgentModeState{ActiveWorkflowWorktreeID: ""},
		LoadState:     "loaded",
	}
	// Simulate the cache desync: the worker is authoritatively present in the
	// .ragents card but absent from the in-memory a.Agents cache.
	if err := a.store.Save(a.agentRegistryName(), []domain.AgentRef{worker}); err != nil {
		t.Fatalf("seed ragents card: %v", err)
	}
	if len(a.Agents) != 0 {
		t.Fatalf("precondition: a.Agents must be empty to exercise cache miss, got %d", len(a.Agents))
	}

	mergeCalls, verifyCalls := 0, 0
	var setStatusReq gen.WikiSetStatusReq
	statusCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"}}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.worktree_merge_to_parent":
				mergeCalls++
				return gen.ProjectWorktreeMergeToParentResp{Status: "merged"}
			case "project.worktree_verify_merged_to_parent":
				verifyCalls++
				return gen.ProjectWorktreeVerifyMergedResp{Status: "merged"}
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReq = req
					statusCalled = true
				}
				return gen.WikiSetStatusResp{PreviousStatus: "pending_review"}
			case "project.review_changeset_clear":
				return nil
			case "project.wiki_set_task_outputs":
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Fatalf("expected approve/done, got Approved=%v CardStatus=%q", resp.Approved, resp.CardStatus)
	}
	if !statusCalled || setStatusReq.ID != "card-1" || setStatusReq.Status != "done" {
		t.Errorf("expected set_status {card-1, done}, got called=%v {%q, %q}", statusCalled, setStatusReq.ID, setStatusReq.Status)
	}
	if mergeCalls != 0 || verifyCalls != 0 {
		t.Errorf("no-git worker must not merge/verify, got merge=%d verify=%d", mergeCalls, verifyCalls)
	}
	// The cache-miss path healed the worker into a.Agents, then cascade-delete
	// marked it deleting (tombstone). The worker is now reachable, not orphaned.
	if len(a.Agents) != 1 || a.Agents[0].ID != "Worker#0001" || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected healed+deleting Worker#0001, got %d agents: %+v", len(a.Agents), a.Agents)
	}
}

// TestHandleAgentReview_Reject verifies reject resumes the agent with feedback
// and keeps it in a.Agents.
func TestHandleAgentReview_Reject(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
		},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			if callID == "internal_resume_from_review" {
				if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
					resumeReq = req
				}
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		Feedback:     "Fix the failing tests",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if resp.Approved {
		t.Error("expected Approved=false")
	}
	if resp.CardStatus != "doing" {
		t.Errorf("expected CardStatus 'doing', got %q", resp.CardStatus)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent kept, got %d agents", len(a.Agents))
	}
	if resumeReq.Feedback != "Fix the failing tests" {
		t.Errorf("expected feedback passthrough, got %q", resumeReq.Feedback)
	}
}

// TestHandleAgentReview_RejectCardDisposedCASMiss verifies the rollback race
// fix: when the task card is no longer pending_review at reject time (e.g. a
// concurrent approve already set done — human UI and map owner share this
// callable), the reject must abort instead of resuming the worker, whose next
// ready_for_review completion would roll the approved card back to
// pending_review.
func TestHandleAgentReview_RejectCardDisposedCASMiss(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
		},
	}

	resumeCalled := false
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "project.wiki_set_status":
				return fmt.Errorf(`project.wiki.set_status: expected status "pending_review", got "done"`)
			case "internal_resume_from_review":
				resumeCalled = true
			}
			return nil
		}), true
	}

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "reject",
		TaskCardID:   "card-raced",
	})
	if err == nil {
		t.Fatal("expected error when the card is no longer pending_review")
	}
	if !strings.Contains(err.Error(), "no longer pending_review") {
		t.Errorf("unexpected error: %v", err)
	}
	if resumeCalled {
		t.Error("worker must not be resumed after the card CAS miss")
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus == "deleting" {
		t.Errorf("agent must be left untouched on aborted reject, got %+v", a.Agents)
	}
}

// TestHandleAgentReview_NotFound verifies an unknown AgentActorID returns an
// error.
func TestHandleAgentReview_NotFound(t *testing.T) {
	a, ctx := freshActor(t)

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: testutil.GenActorID().String(),
		Decision:     "approve",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

// TestHandleAgentReview_IdempotentReapproveAfterTeardown covers the retry
// path that produced "agent not found in registry or authoritative card" in
// production: a prior approve tore the worker down (row removed everywhere)
// and flipped the card to done, but the response was lost (or a stale nudge
// fired) and the owner retried. The retry must succeed idempotently instead
// of dead-ending.
func TestHandleAgentReview_IdempotentReapproveAfterTeardown(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	// The worker is gone from registry AND card — nothing in a.Agents.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.wiki_get_card" {
					if req, ok := payload.(domain.WikiGetCardReq); ok && req.ID == "card-1" {
						return domain.WikiGetCardResp{ID: "card-1", Raw: "---\nid: card-1\ntype: task\nstatus: done\n---\n\nBody"}
					}
				}
				return nil
			}), true
		}
		return nil, false
	}

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: genID(),
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("idempotent re-approve must succeed, got %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Errorf("expected Approved=true CardStatus=done, got %+v", resp)
	}
	if !strings.Contains(resp.ReviewNote, "idempotent") {
		t.Errorf("expected idempotency note, got %q", resp.ReviewNote)
	}
}

// TestHandleAgentReview_NotFoundOrphanCardGuidance covers the genuinely
// orphaned case: the agent is gone but its card never reached done (the
// pre-CAS-fix swallowed-flip failure, or a terminate during pending_review).
// The error must say not-found AND tell the operator how to dispose the
// stuck card.
func TestHandleAgentReview_NotFoundOrphanCardGuidance(t *testing.T) {
	a, ctx := freshActor(t)
	projectID := genID()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == projectID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.wiki_get_card" {
					if req, ok := payload.(domain.WikiGetCardReq); ok && req.ID == "card-1" {
						return domain.WikiGetCardResp{ID: "card-1", Raw: "---\nid: card-1\ntype: task\nstatus: pending_review\n---\n\nBody"}
					}
				}
				return nil
			}), true
		}
		return nil, false
	}

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: genID(),
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err == nil {
		t.Fatal("expected error for orphaned pending_review card, got nil")
	}
	for _, want := range []string{"not found", "pending_review", "wiki_set_status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must mention %q, got: %v", want, err)
		}
	}
}

// TestHandleAgentReview_InvalidDecision verifies an unknown decision value is
// rejected.
func TestHandleAgentReview_InvalidDecision(t *testing.T) {
	a, ctx := freshActor(t)

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: testutil.GenActorID().String(),
		Decision:     "foo",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Decision must be") {
		t.Errorf("expected 'Decision must be' error, got %v", err)
	}
}

func TestHandleAgentReview_RejectsUndefinedActorID(t *testing.T) {
	a, ctx := freshActor(t)
	a.Agents = []domain.AgentRef{{ID: "Worker#0001", ActorID: "undefined", AgentKind: domain.AgentKindWorker}}

	_, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: "Worker#0001",
		Decision:     "reject",
	})
	if err == nil {
		t.Fatal("expected error for undefined actor ID")
	}
	if !strings.Contains(err.Error(), "invalid agent actor id") {
		t.Errorf("expected invalid agent actor ID error, got %v", err)
	}
}

// TestHandleAgentReview_ApproveFallsBackToBoundCard verifies that when the
// caller omits TaskCardId, approve recovers the bound card from the agent's
// live goal binding and still advances it to done. Without this fallback the
// task card would be stuck in pending_review and the root map would never
// auto-complete.
func TestHandleAgentReview_ApproveFallsBackToBoundCard(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
			LoadState:   "loaded",
		},
	}

	var setStatusReq gen.WikiSetStatusReq
	statusCalled := false
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "agent_status":
				return gen.AgentStatusResp{
					Goal: &gen.GoalSummary{BoundTaskCardID: "card-fallback", Status: "ready_for_review"},
				}
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReq = req
					statusCalled = true
				}
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved {
		t.Error("expected Approved=true")
	}
	if resp.CardStatus != "done" {
		t.Errorf("expected CardStatus 'done', got %q", resp.CardStatus)
	}
	if !statusCalled {
		t.Fatal("expected project.wiki.set_status to be called for the bound card")
	}
	if setStatusReq.ID != "card-fallback" {
		t.Errorf("expected set_status on bound card 'card-fallback', got %q", setStatusReq.ID)
	}
	if setStatusReq.Status != "done" {
		t.Errorf("expected set_status 'done', got %q", setStatusReq.Status)
	}
}

// TestHandleAgentReview_ApproveNoOutputsContract verifies that a task card with
// no data.outputs contract passes the review gate and approves normally.
func TestHandleAgentReview_ApproveNoOutputsContract(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "agent_status":
				return gen.AgentStatusResp{Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"}}
			case "project.task_validate_outputs":
				// No contract declared → always valid.
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Fatalf("expected approve/done, got Approved=%v CardStatus=%q note=%q", resp.Approved, resp.CardStatus, resp.ReviewNote)
	}
	if resp.ReviewNote != "" {
		t.Errorf("expected empty ReviewNote for clean approve, got %q", resp.ReviewNote)
	}
}

// TestHandleAgentReview_ApproveValidOutputs verifies that when a task card
// declares an outputs contract and the worker produced conforming outputs, the
// gate passes and the approve proceeds.
func TestHandleAgentReview_ApproveValidOutputs(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "agent_status":
				return gen.AgentStatusResp{Goal: &gen.GoalSummary{
					BoundTaskCardID: "card-1",
					Status:          "ready_for_review",
					Outputs:         map[string]any{"score": float64(9)},
				}}
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{Valid: true}
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Fatalf("expected approve/done for valid outputs, got Approved=%v CardStatus=%q note=%q", resp.Approved, resp.CardStatus, resp.ReviewNote)
	}
}

// TestHandleAgentReview_AutoRejectInvalidOutputs verifies that when the worker's
// outputs fail the data.outputs contract, an approve is converted to an
// auto-reject: the card returns to "doing" and the worker is resumed with the
// validation errors as feedback.
func TestHandleAgentReview_AutoRejectInvalidOutputs(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}

	var resumeReq domain.AgentInternalResumeFromReviewReq
	var setStatusReq gen.WikiSetStatusReq
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "agent_status":
				// Worker produced a string where an int was declared.
				return gen.AgentStatusResp{Goal: &gen.GoalSummary{
					BoundTaskCardID: "card-1",
					Status:          "ready_for_review",
					Outputs:         map[string]any{"score": "not-a-number"},
				}}
			case "project.task_validate_outputs":
				return gen.ProjectTaskValidateOutputsResp{
					Valid: false,
					Errors: []gen.CardValidationError{{
						Code: "outputs_type_mismatch", Field: "outputs.score",
						Message: `outputs.score: expected int, got string`,
					}},
				}
			case "project.wiki_set_status":
				if req, ok := payload.(gen.WikiSetStatusReq); ok {
					setStatusReq = req
				}
				return nil
			case "internal_resume_from_review":
				if req, ok := payload.(domain.AgentInternalResumeFromReviewReq); ok {
					resumeReq = req
				}
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	// Auto-reject: not approved, card back to doing.
	if resp.Approved {
		t.Error("expected Approved=false (auto-reject)")
	}
	if resp.CardStatus != "doing" {
		t.Errorf("expected CardStatus 'doing' (auto-reject), got %q", resp.CardStatus)
	}
	if resp.ReviewNote == "" || !strings.Contains(resp.ReviewNote, "auto-reject") {
		t.Errorf("expected ReviewNote explaining auto-reject, got %q", resp.ReviewNote)
	}
	// Card status must have been set back to doing.
	if setStatusReq.ID != "card-1" || setStatusReq.Status != "doing" {
		t.Errorf("expected set_status card-1→doing, got ID=%q Status=%q", setStatusReq.ID, setStatusReq.Status)
	}
	// Worker must be resumed with feedback containing the validation error.
	if !strings.Contains(resumeReq.Feedback, "outputs.score") || !strings.Contains(resumeReq.Feedback, "data.outputs contract") {
		t.Errorf("expected resume feedback to carry validation errors, got %q", resumeReq.Feedback)
	}
	// The agent is kept (not torn down) on auto-reject.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus == "deleting" {
		t.Errorf("expected agent kept (not deleting) on auto-reject, got %+v", a.Agents)
	}
}

// TestHandleAgentReview_GateFailsOpenWhenProjectUnreachable verifies that when
// the project actor cannot validate (infrastructure outage), the approve still
// proceeds rather than wedging the review loop. Most cards declare no contract.
func TestHandleAgentReview_GateFailsOpenWhenProjectUnreachable(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	projectID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{ID: "Worker#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindWorker, LoadState: "loaded"},
	}
	ctx.LookupIDFn = func(_ id.ActorID) (ref.Ref, bool) {
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
			switch callID {
			case "agent_status":
				return gen.AgentStatusResp{Goal: &gen.GoalSummary{BoundTaskCardID: "card-1", Status: "ready_for_review"}}
			case "project.task_validate_outputs":
				// Simulate project unable to respond: return a type the workspace
				// cannot assert as the resp → triggers the fail-open path.
				return nil
			}
			return nil
		}), true
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentReview(ctx, domain.WorkspaceAgentReviewReq{
		AgentActorID: agentActorID,
		Decision:     "approve",
		TaskCardID:   "card-1",
	})
	if err != nil {
		t.Fatalf("handleAgentReview: %v", err)
	}
	if !resp.Approved || resp.CardStatus != "done" {
		t.Fatalf("expected fail-open approve/done, got Approved=%v CardStatus=%q", resp.Approved, resp.CardStatus)
	}
}

// TestHandleAgentTerminate_RejectsTopLevel verifies that top-level agents
// (no parent: user-created coder, scheduler-managed agents, etc.) are
// refused; only child agents may be terminated here.
func TestHandleAgentTerminate_RejectsTopLevel(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Coder#0001",
			ActorID:     agentActorID,
			AgentKind:   domain.AgentKindCoder,
			DisplayName: "Coder",
		},
	}

	_, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{AgentActorID: agentActorID})
	if err == nil {
		t.Fatal("expected error terminating a top-level agent")
	}
	if !strings.Contains(err.Error(), "only child agents") {
		t.Errorf("expected 'only child agents' error, got %v", err)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected coder agent preserved, got %d agents", len(a.Agents))
	}
}

// TestHandleAgentTerminate_NonWorkerChild verifies a persistent child agent
// (non-worker, non-fork — e.g. a coder spawned by another agent) can be
// terminated without task-card release, with self/parent authorization for
// non-human callers.
func TestHandleAgentTerminate_NonWorkerChild(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	parentAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:            "Coder#0001",
			ActorID:       agentActorID,
			AgentKind:     domain.AgentKindCoder,
			DisplayName:   "Child Coder",
			ParentAgentID: parentAgentID,
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	// Human caller bypasses authorization (UI-driven termination).
	resp, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
	})
	if err != nil {
		t.Fatalf("handleAgentTerminate for child coder (human): %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting, got %d agents", len(a.Agents))
	}

	// Reset and verify non-human callers: self, parent, then unauthorized.
	a.Agents[0].DeletionStatus = ""
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	anonCtx.DestroyFn = func(ref.Ref) error { return nil }
	anonCtx.LookupIDFn = ctx.LookupIDFn

	if _, err := a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: parentAgentID,
	}); err != nil {
		t.Fatalf("parent-termination should succeed: %v", err)
	}

	a.Agents[0].DeletionStatus = ""
	_, err = a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: genID(),
	})
	if err == nil || !strings.Contains(err.Error(), "only the child agent itself or its direct parent") {
		t.Errorf("expected authorization error, got %v", err)
	}
}

// TestHandleAgentTerminate_Worker verifies a worker agent is removed and torn
// down.
func TestHandleAgentTerminate_Worker(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := testutil.GenActorID().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Worker#0001",
			ActorID:     agentActorID,
			AgentKind:   domain.AgentKindWorker,
			DisplayName: "Worker One",
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	resp, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
		Reason:       "failed",
	})
	if err != nil {
		t.Fatalf("handleAgentTerminate: %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
	// Cascade delete marks the agent "deleting" rather than removing it
	// synchronously from a.Agents.
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting, got %d agents", len(a.Agents))
	}
	// Projection must exclude the deleting agent.
	state := a.buildAgentListState(true)
	if len(state.Items) != 0 {
		t.Errorf("expected 0 items in projection, got %d", len(state.Items))
	}
}

// TestHandleAgentTerminate_ForkChild verifies a fork child agent
// (LifecycleScope="fork") can be terminated without parent authorization
// or task-card release, since fork children self-terminate.
func TestHandleAgentTerminate_ForkChild(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	parentAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:             "explore-1-toolu-1",
			ActorID:        agentActorID,
			AgentKind:      "explorer",
			DisplayName:    "Scout Ant",
			ParentAgentID:  parentAgentID,
			LifecycleScope: "fork",
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	// Human caller bypasses authorization (UI-driven termination).
	resp, err := a.handleAgentTerminate(ctx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
	})
	if err != nil {
		t.Fatalf("handleAgentTerminate for fork child (human): %v", err)
	}
	if !resp.Deleted {
		t.Error("expected Deleted=true")
	}
	if len(a.Agents) != 1 || a.Agents[0].DeletionStatus != "deleting" {
		t.Errorf("expected agent marked deleting, got %d agents", len(a.Agents))
	}
	state := a.buildAgentListState(true)
	if len(state.Items) != 0 {
		t.Errorf("expected 0 items in projection, got %d", len(state.Items))
	}
}

// TestHandleAgentTerminate_ForkChild_Authorization verifies that non-human
// callers must be the fork child itself or its direct parent.
func TestHandleAgentTerminate_ForkChild_Authorization(t *testing.T) {
	a, ctx := freshActor(t)
	agentActorID := genID()
	parentAgentID := genID()
	a.Agents = []domain.AgentRef{
		{
			ID:             "explore-1-toolu-1",
			ActorID:        agentActorID,
			AgentKind:      "explorer",
			DisplayName:    "Scout Ant",
			ParentAgentID:  parentAgentID,
			LifecycleScope: "fork",
		},
	}
	ctx.DestroyFn = func(ref.Ref) error { return nil }

	// Use an anonymous (non-human) context so the identity check does not bypass.
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	anonCtx.DestroyFn = func(ref.Ref) error { return nil }
	anonCtx.LookupIDFn = ctx.LookupIDFn

	// Self-termination: caller == target agent.
	_, err := a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: agentActorID,
	})
	if err != nil {
		t.Fatalf("self-termination should succeed: %v", err)
	}

	// Reset agent state for next subtest.
	a.Agents[0].DeletionStatus = ""

	// Parent-terminated: caller == ParentAgentID.
	_, err = a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: parentAgentID,
	})
	if err != nil {
		t.Fatalf("parent-termination should succeed: %v", err)
	}

	// Reset agent state for next subtest.
	a.Agents[0].DeletionStatus = ""

	// Unauthorized caller: a random agent ID.
	otherAgentID := genID()
	_, err = a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID:  agentActorID,
		CallerAgentID: otherAgentID,
	})
	if err == nil {
		t.Fatal("expected error for unauthorized caller")
	}
	if !strings.Contains(err.Error(), "only the child agent itself or its direct parent") {
		t.Errorf("expected authorization error, got %v", err)
	}

	// Empty caller from non-human: rejected.
	_, err = a.handleAgentTerminate(anonCtx, domain.WorkspaceAgentTerminateReq{
		AgentActorID: agentActorID,
	})
	if err == nil {
		t.Fatal("expected error for empty caller with non-human identity")
	}
	if !strings.Contains(err.Error(), "requires caller identity") {
		t.Errorf("expected caller identity error, got %v", err)
	}
}

// TestSeedAgentKindConfigs_PruneRetired verifies that configs and agent
// references for retired builtin kinds (grapher) are pruned during seed, so
// they do not linger as un-creatable, un-deletable ghost entries.
func TestSeedAgentKindConfigs_PruneRetired(t *testing.T) {
	a, ctx := freshActor(t)
	a.AgentKindConfigs = append(a.AgentKindConfigs, domain.AgentKindConfig{
		Kind:          "grapher",
		DisplayName:   "Grapher",
		UserCreatable: true,
		SystemManaged: true,
	})
	a.Agents = append(a.Agents, domain.AgentRef{
		ID:          "ghost-grapher",
		AgentKind:   "grapher",
		DisplayName: "Ghost Grapher",
	})

	a.seedAgentKindConfigs(ctx)

	for _, cfg := range a.AgentKindConfigs {
		if cfg.Kind == "grapher" {
			t.Errorf("expected grapher config pruned, found %+v", cfg)
		}
	}
	for _, ag := range a.Agents {
		if ag.AgentKind == "grapher" {
			t.Errorf("expected grapher agent pruned, found %+v", ag)
		}
	}
}

// ── workspace.agent_assign (existing agent) ────────────────────────────────

// TestHandleAgentAssign_Success verifies the happy path: the card is
// CAS-claimed (backlog → doing), the goal is handed to the existing agent via
// internal_assign_goal, and the generated response carries the goal summary.
func TestHandleAgentAssign_Success(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Coder#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindCoder,
			DisplayName: "Coder",
		},
	}

	var assignReq gen.AgentInternalAssignGoalReq
	var claimReq gen.WikiClaimTaskCardReq
	cardStatus := map[string]string{"card-1": "backlog"}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case callerAgentID:
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		case projectID:
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				switch callID {
				case "project.wiki_claim_task_card":
					if req, ok := payload.(gen.WikiClaimTaskCardReq); ok {
						claimReq = req
					}
					return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
				case "project.wiki_get_card":
					return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
				}
				return nil
			}), true
		case agentActorID:
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				if callID == "internal_assign_goal" {
					if req, ok := payload.(gen.AgentInternalAssignGoalReq); ok {
						assignReq = req
					}
				}
				return nil
			}), true
		}
		return nil, false
	}

	resp, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
		AgentActorID:    agentActorID,
		InterpretedGoal: "interpreted goal",
		BoundTaskCardID: "card-1",
		MaxTurns:        5,
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentAssign: %v", err)
	}
	if resp.Goal.Condition != "Implement the widget" {
		t.Errorf("expected Goal.Condition from task card body, got %q", resp.Goal.Condition)
	}
	if resp.Goal.Status != "active" {
		t.Errorf("expected Goal.Status 'active', got %q", resp.Goal.Status)
	}
	if !resp.Goal.Confirmed {
		t.Error("expected Goal.Confirmed=true")
	}
	if resp.Goal.BoundTaskCardID != "card-1" {
		t.Errorf("expected Goal.BoundTaskCardId 'card-1', got %q", resp.Goal.BoundTaskCardID)
	}
	if resp.Goal.MaxTurns != 5 {
		t.Errorf("expected Goal.MaxTurns 5, got %d", resp.Goal.MaxTurns)
	}

	if claimReq.ID != "card-1" || claimReq.Status != "doing" || len(claimReq.ExpectedStatuses) == 0 {
		t.Errorf("expected fused claim card-1 ->doing with expected-status guard, got %+v", claimReq)
	}
	if assignReq.Condition != "Implement the widget" {
		t.Errorf("expected internal_assign_goal Condition from task card body, got %q", assignReq.Condition)
	}
	if assignReq.InterpretedGoal != "interpreted goal" {
		t.Errorf("expected internal_assign_goal InterpretedGoal passthrough, got %q", assignReq.InterpretedGoal)
	}
	if assignReq.BoundTaskCardID != "card-1" {
		t.Errorf("expected internal_assign_goal BoundTaskCardId, got %q", assignReq.BoundTaskCardID)
	}
	if assignReq.MaxTurns != 5 {
		t.Errorf("expected internal_assign_goal MaxTurns 5, got %d", assignReq.MaxTurns)
	}
	if !strings.Contains(assignReq.PromptPrelude, "card-1") {
		t.Errorf("expected prompt prelude to reference the bound card, got %q", assignReq.PromptPrelude)
	}
	if len(a.Agents) != 1 {
		t.Errorf("expected agent list unchanged, got %d agents", len(a.Agents))
	}
}

// TestHandleAgentAssign_ValidationErrors verifies each required field is
// validated before any cross-actor call is made.
func TestHandleAgentAssign_ValidationErrors(t *testing.T) {
	a, ctx := freshActor(t)
	base := gen.WorkspaceAgentAssignReq{
		AgentActorID:    testutil.GenActorID().String(),
		BoundTaskCardID: "card-1",
	}
	cases := []struct {
		name string
		req  gen.WorkspaceAgentAssignReq
	}{
		{"missing AgentActorId", func() gen.WorkspaceAgentAssignReq {
			r := base
			r.AgentActorID = ""
			return r
		}()},
		{"missing BoundTaskCardId", func() gen.WorkspaceAgentAssignReq {
			r := base
			r.BoundTaskCardID = ""
			return r
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := a.handleAgentAssign(ctx, tc.req); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

// TestHandleAgentAssign_NotFound verifies an unknown AgentActorID returns an
// error.
func TestHandleAgentAssign_NotFound(t *testing.T) {
	a, ctx := freshActor(t)
	_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
		AgentActorID:    testutil.GenActorID().String(),
		BoundTaskCardID: "card-1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

// TestHandleAgentAssign_ProjectMembership verifies the agent must belong to
// the project that owns the task card: a mismatched project or a project-less
// agent is rejected before any card state is touched.
func TestHandleAgentAssign_ProjectMembership(t *testing.T) {
	t.Run("agent bound to a different project", func(t *testing.T) {
		a, ctx := freshActor(t)
		a.Agents = []domain.AgentRef{{
			ID: "Coder#0001", ActorID: testutil.GenActorID().String(),
			ProjectID: "project-B", AgentKind: domain.AgentKindCoder,
		}}
		_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
			AgentActorID: a.Agents[0].ActorID, BoundTaskCardID: "card-1",
			ProjectID: "project-A",
		})
		if err == nil || !strings.Contains(err.Error(), "does not belong to project") {
			t.Fatalf("expected membership rejection, got %v", err)
		}
	})
	t.Run("agent with no project", func(t *testing.T) {
		a, ctx := freshActor(t)
		a.Agents = []domain.AgentRef{{
			ID: "Coder#0001", ActorID: testutil.GenActorID().String(),
			AgentKind: domain.AgentKindCoder,
		}}
		_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
			AgentActorID: a.Agents[0].ActorID, BoundTaskCardID: "card-1",
		})
		if err == nil || !strings.Contains(err.Error(), "not bound to a project") {
			t.Fatalf("expected project-less rejection, got %v", err)
		}
	})
}

// TestHandleAgentAssign_RejectsWithoutWorkflowMode verifies the card claim
// requires builtin:mode:workflow and never runs before that authorization.
func TestHandleAgentAssign_RejectsWithoutWorkflowMode(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Agents = []domain.AgentRef{{
		ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder,
	}}
	calledLookup := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{}
				}
				return nil
			}), true
		}
		calledLookup = true
		return testutil.NewFakeRef(aid, nil), true
	}
	_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
		AgentActorID: agentActorID, BoundTaskCardID: "card-1",
		CallerAgentID: callerAgentID,
	})
	if err == nil || !strings.Contains(err.Error(), "requires an active workflow") {
		t.Fatalf("expected workflow mode rejection, got %v", err)
	}
	if calledLookup {
		t.Fatal("task claim must not run before workflow mode authorization")
	}
}

// TestHandleAgentAssign_ClaimFailure verifies that when the card cannot be
// claimed (CAS fails), the handler returns an error and never looks up the
// agent.
func TestHandleAgentAssign_ClaimFailure(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Agents = []domain.AgentRef{{
		ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder,
	}}
	agentLookedUp := false
	// A ref with a nil invokeFn returns a nil Call from Invoke, so
	// claimTaskCard sees call == nil and reports the claim as failed.
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		if aidStr == agentActorID {
			agentLookedUp = true
		}
		return testutil.NewFakeRef(aid, nil), true
	}
	_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
		AgentActorID: agentActorID, BoundTaskCardID: "card-1",
		CallerAgentID: callerAgentID,
	})
	if err == nil {
		t.Fatal("expected claim error, got nil")
	}
	if !strings.Contains(err.Error(), "claim task card") {
		t.Errorf("expected transport error surfaced, got %v", err)
	}
	if agentLookedUp {
		t.Fatal("agent must not be looked up when the card claim fails")
	}
}

// TestHandleAgentAssign_RollbackOnInvokeFailure verifies that a failed
// internal_assign_goal invoke rolls the card status back to the pre-claim
// state (backlog).
func TestHandleAgentAssign_RollbackOnInvokeFailure(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Agents = []domain.AgentRef{{
		ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder,
	}}
	var statusReqs []gen.WikiSetStatusReq
	cardStatus := map[string]string{"card-1": "backlog"}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch aid.String() {
		case callerAgentID:
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		case projectID:
			return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any {
				switch callID {
				case "project.wiki_claim_task_card":
					return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "body"})(payload)
				case "project.wiki_get_card":
					return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "body"})(payload)
				case "project.wiki_set_status":
					if req, ok := payload.(gen.WikiSetStatusReq); ok {
						statusReqs = append(statusReqs, req)
					}
				}
				return nil
			}), true
		case agentActorID:
			// nil invokeFn -> internal_assign_goal returns a nil Call, which
			// the handler treats as an invoke failure and rolls back.
			return testutil.NewFakeRef(testutil.GenActorID(), nil), true
		}
		return nil, false
	}

	_, err := a.handleAgentAssign(ctx, gen.WorkspaceAgentAssignReq{
		AgentActorID: agentActorID, BoundTaskCardID: "card-1",
		CallerAgentID: callerAgentID,
	})
	if err == nil {
		t.Fatal("expected invoke error, got nil")
	}
	if !strings.Contains(err.Error(), "assign invoke returned nil") {
		t.Errorf("expected invoke error, got %v", err)
	}
	if len(statusReqs) != 1 {
		t.Fatalf("expected rollback status call, got %d status calls: %+v", len(statusReqs), statusReqs)
	}
	if statusReqs[0].ID != "card-1" || statusReqs[0].Status != "backlog" || statusReqs[0].ExpectedStatus != "" {
		t.Errorf("expected rollback of card-1 to backlog, got %+v", statusReqs[0])
	}
	if got := cardStatus["card-1"]; got != "doing" {
		t.Errorf("fake card status after claim: got %q, want doing", got)
	}
}

// TestHandleAgentSpawnAssign_InheritsParentSlot verifies that the workflow
// worker's model slot is resolved from the parent (caller) agent's runtime
// slots via the resolve_child_slot internal callable — the same inheritance
// path fork children use. The resolved slot is passed as the child's Primary;
// all other slots stay nil.
func TestHandleAgentSpawnAssign_InheritsParentSlot(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	execSlot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: "unit", Unit: &gen.ModelUnit{Model: "exec-model", Provider: "openai"}},
	}}

	var spawnReq domain.ProjectSpawnAgentReq
	var slotReq agentactor.ResolveChildSlotReq
	cardStatus := map[string]string{"card-1": "backlog"}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				case "resolve_child_slot":
					if req, ok := payload.(agentactor.ResolveChildSlotReq); ok {
						slotReq = req
					}
					return agentactor.ResolveChildSlotResp{Slot: execSlot}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	// The parent must have been asked for a slot with the worker kind.
	if slotReq.ChildKind != domain.AgentKindWorker {
		t.Errorf("expected resolve_child_slot ChildKind %q, got %q", domain.AgentKindWorker, slotReq.ChildKind)
	}

	// Primary slot must carry the parent-resolved slot.
	if spawnReq.Primary == nil || len(spawnReq.Primary.Candidates) == 0 {
		t.Fatalf("expected non-nil Primary slot with candidates, got %+v", spawnReq.Primary)
	}
	gotModel := ""
	if c := spawnReq.Primary.Candidates[0]; c.Unit != nil {
		gotModel = c.Unit.Model
	}
	if gotModel != "exec-model" {
		t.Errorf("expected Primary slot to use exec-model (inherited from parent), got %q", gotModel)
	}

	// All other slots must be nil so the agent's model selection is unambiguous.
	if spawnReq.Execution != nil {
		t.Errorf("expected Execution slot to be nil, got %+v", spawnReq.Execution)
	}
	if spawnReq.Fast != nil {
		t.Errorf("expected Fast slot to be nil, got %+v", spawnReq.Fast)
	}
	if spawnReq.Review != nil {
		t.Errorf("expected Review slot to be nil, got %+v", spawnReq.Review)
	}
	if spawnReq.Summary != nil {
		t.Errorf("expected Summary slot to be nil, got %+v", spawnReq.Summary)
	}

	// The stored AgentRef must also reflect the inherited slot in its Primary.
	ag := a.Agents[len(a.Agents)-1]
	if ag.Primary == nil || len(ag.Primary.Candidates) == 0 {
		t.Fatalf("expected stored agent Primary slot with candidates, got %+v", ag.Primary)
	}
	storedModel := ""
	if c := ag.Primary.Candidates[0]; c.Unit != nil {
		storedModel = c.Unit.Model
	}
	if storedModel != "exec-model" {
		t.Errorf("expected stored agent Primary to use exec-model, got %q", storedModel)
	}
	if ag.Execution != nil {
		t.Errorf("expected stored agent Execution slot to be nil, got %+v", ag.Execution)
	}
}

// TestHandleAgentSpawnAssign_PassesUnitOverride verifies that an explicit
// ModelUnit on the spawn_assign request is threaded through to the parent
// agent's resolve_child_slot callable, matching spawn_by_type behavior.
func TestHandleAgentSpawnAssign_PassesUnitOverride(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{
		{Name: "p1", Path: t.TempDir(), ActorID: projectID},
	}

	unitSlot := domain.ModelSlot{Candidates: []domain.ModelRef{
		{Kind: "unit", Unit: &gen.ModelUnit{Model: "override-model", Provider: "openai"}},
	}}

	var spawnReq domain.ProjectSpawnAgentReq
	var slotReq agentactor.ResolveChildSlotReq
	cardStatus := map[string]string{"card-1": "backlog"}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		aidStr := aid.String()
		if aidStr == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "agent_status":
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				case "resolve_child_slot":
					if req, ok := payload.(agentactor.ResolveChildSlotReq); ok {
						slotReq = req
					}
					return agentactor.ResolveChildSlotResp{Slot: unitSlot}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker One",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       "p1",
		CallerAgentID:   callerAgentID,
		Unit:            &domain.ModelUnit{Model: "override-model", Provider: "openai"},
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	if slotReq.Unit == nil || slotReq.Unit.Model != "override-model" {
		t.Errorf("expected unit override forwarded to resolve_child_slot, got %+v", slotReq.Unit)
	}
	if spawnReq.Primary == nil || len(spawnReq.Primary.Candidates) == 0 {
		t.Fatalf("expected non-nil Primary slot, got %+v", spawnReq.Primary)
	}
	gotModel := ""
	if c := spawnReq.Primary.Candidates[0]; c.Unit != nil {
		gotModel = c.Unit.Model
	}
	if gotModel != "override-model" {
		t.Errorf("expected Primary slot to use override-model, got %q", gotModel)
	}
}

// ── task inputs injection (frontier → goal context) ──

// fakeClaimTaskCardWithInputs emulates project.wiki_claim_task_card and
// returns the supplied inputs map as part of the response. This is the
// "upstream has produced outputs and the graph has resolved bindings" path.
func fakeClaimTaskCardWithInputs(cardStatus map[string]string, bodyByID map[string]string, inputs map[string]any) func(payload any) any {
	return func(payload any) any {
		req, ok := payload.(gen.WikiClaimTaskCardReq)
		if !ok {
			return nil
		}
		cur := cardStatus[req.ID]
		match := false
		for _, exp := range req.ExpectedStatuses {
			if cur == exp {
				match = true
				break
			}
		}
		if !match {
			return fmt.Errorf("project.wiki.claim_task_card: expected status one of %v, got %q", req.ExpectedStatuses, cur)
		}
		cardStatus[req.ID] = req.Status
		return gen.WikiClaimTaskCardResp{
			PreviousStatus: cur,
			Raw:            "---\ntype: task\nstatus: " + req.Status + "\n---\n" + bodyByID[req.ID],
			Inputs:         inputs,
		}
	}
}

// TestInjectTaskInputs_EmptyInputsReturnsUnchanged verifies that an empty
// inputs map is a no-op — the worker just gets the bare condition (no
// spurious "Task Inputs" section).
func TestInjectTaskInputs_EmptyInputsReturnsUnchanged(t *testing.T) {
	cond := "Original goal body."
	if got := injectTaskInputs(cond, nil); got != cond {
		t.Errorf("nil inputs: got %q, want %q", got, cond)
	}
	if got := injectTaskInputs(cond, map[string]any{}); got != cond {
		t.Errorf("empty inputs: got %q, want %q", got, cond)
	}
}

// TestInjectTaskInputs_AppendsYAMLSection verifies that populated inputs are
// appended as a fenced YAML section under "## Task Inputs (resolved from
// upstream task outputs)". The JSON form is round-tripped as YAML so the
// worker can parse it deterministically.
func TestInjectTaskInputs_AppendsYAMLSection(t *testing.T) {
	cond := "Implement the widget."
	inputs := map[string]any{
		"report": "audit text",
		"score":  0.91,
	}
	got := injectTaskInputs(cond, inputs)
	if !strings.HasPrefix(got, cond) {
		t.Errorf("injected condition should preserve original prefix; got %q", got)
	}
	if !strings.Contains(got, "## Task Inputs (resolved from upstream task outputs)") {
		t.Errorf("injected condition should contain the Task Inputs header; got %q", got)
	}
	if !strings.Contains(got, "```yaml") {
		t.Errorf("injected condition should be fenced as YAML; got %q", got)
	}
	// The values must be present (JSON form, not YAML structural encoding).
	if !strings.Contains(got, `"report"`) || !strings.Contains(got, `"audit text"`) {
		t.Errorf("injected condition should carry inputs verbatim; got %q", got)
	}
}

// TestHandleAgentSpawnAssign_PropagatesTaskInputsIntoGoal verifies the
// end-to-end frontier-injection path: project.wiki_claim_task_card returns
// resolved Inputs (from upstream task_outputs via data bindings), the
// workspace handler threads them into the goal condition, and the worker's
// goal carries the "## Task Inputs" section alongside the original body.
func TestHandleAgentSpawnAssign_PropagatesTaskInputsIntoGoal(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	upstreamOutputs := map[string]any{
		"report": "audit summary",
		"score":  0.95,
	}
	var spawnReq domain.ProjectSpawnAgentReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				if req, ok := payload.(domain.ProjectSpawnAgentReq); ok {
					spawnReq = req
				}
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_claim_task_card":
				return fakeClaimTaskCardWithInputs(cardStatus, map[string]string{"card-1": "Implement the widget"}, upstreamOutputs)(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}

	// Resp carries the merged condition.
	if !strings.HasPrefix(resp.Goal.Condition, "Implement the widget") {
		t.Errorf("goal condition should start with task body; got %q", resp.Goal.Condition)
	}
	if !strings.Contains(resp.Goal.Condition, "## Task Inputs (resolved from upstream task outputs)") {
		t.Errorf("goal condition should carry Task Inputs header; got %q", resp.Goal.Condition)
	}
	if !strings.Contains(resp.Goal.Condition, `"report"`) || !strings.Contains(resp.Goal.Condition, `"audit summary"`) {
		t.Errorf("goal condition should carry upstream output values; got %q", resp.Goal.Condition)
	}

	// Spawn request is threaded through with the same merged condition.
	if !strings.HasPrefix(spawnReq.GoalCondition, "Implement the widget") {
		t.Errorf("spawn GoalCondition should start with task body; got %q", spawnReq.GoalCondition)
	}
	if !strings.Contains(spawnReq.GoalCondition, "## Task Inputs (resolved from upstream task outputs)") {
		t.Errorf("spawn GoalCondition should carry Task Inputs header; got %q", spawnReq.GoalCondition)
	}
}

// TestHandleAgentSpawnAssign_NoInputsKeepsPlainCondition verifies the
// negative path: when the project returns nil Inputs (task has no declared
// bindings, or upstreams produced no outputs), the goal condition is the bare
// task body with no injected section.
func TestHandleAgentSpawnAssign_NoInputsKeepsPlainCondition(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		cardStatus := map[string]string{"card-1": "backlog"}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: testutil.GenActorID().String()}
			case "project.wiki_claim_task_card":
				// No Inputs in the response.
				return fakeClaimTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			case "project.wiki_get_card":
				return fakeGetTaskCard(cardStatus, map[string]string{"card-1": "Implement the widget"})(payload)
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind: domain.AgentKindWorker, BoundTaskCardID: "card-1",
		ProjectID: projectID, CallerAgentID: callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}
	if resp.Goal.Condition != "Implement the widget" {
		t.Errorf("goal condition should be bare body when no inputs; got %q", resp.Goal.Condition)
	}
	if strings.Contains(resp.Goal.Condition, "## Task Inputs") {
		t.Errorf("no inputs → no Task Inputs header; got %q", resp.Goal.Condition)
	}
}

// TestHandleAgentSpawnAssign_UnknownExecKind verifies the new
// execKind dispatcher path: when a task card declares an exec kind
// that is not registered in the workspace registry, the orchestrator
// returns a stable ErrUnknownExecKind before any claim happens. The
// card's data.exec.kind is read by the dispatcher's preflight, so the
// rejection surfaces with no state change to the project.
func TestHandleAgentSpawnAssign_UnknownExecKind(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	claimCalled := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.wiki_get_card":
				// Card declares an exec kind that is not registered.
				return domain.WikiGetCardResp{
					ID: "card-1",
					Raw: "---\nid: card-1\ntype: task\nstatus: backlog\n" +
						"data:\n  exec:\n    kind: not_registered_kind\n---\n\nBody.",
				}
			case "project.wiki_claim_task_card":
				claimCalled = true
				return nil
			}
			return nil
		}), true
	}

	_, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err == nil {
		t.Fatal("expected ErrUnknownExecKind, got nil")
	}
	if !IsUnknownExecKind(err) {
		t.Fatalf("expected ErrUnknownExecKind, got %v", err)
	}
	if !strings.Contains(err.Error(), "not_registered_kind") {
		t.Errorf("error must mention the unknown kind; got %v", err)
	}
	if claimCalled {
		t.Fatal("unknown execKind must fail before claiming the task card")
	}
}

// TestHandleAgentSpawnAssign_ExplicitWorkerTaskExecKind verifies the
// positive side of the dispatch path: a card that explicitly declares
// data.exec.kind = worker_task still routes to the worker_task
// executor and produces a spawned agent. This locks in that the new
// dispatch path is symmetric — both implicit (no data.exec) and
// explicit (worker_task) declarations take the same executor.
//
// Both wiki_get_card (read by the dispatcher to resolve data.exec.kind)
// and wiki_claim_task_card (whose Raw drives Body extraction) carry
// data.exec.kind = worker_task inside the frontmatter block, so the
// dispatcher resolves to ExecKindWorkerTask and the worker_task
// executor is dispatched.
func TestHandleAgentSpawnAssign_ExplicitWorkerTaskExecKind(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	callerAgentID := g.Next().String()
	a.Mounts = []domain.ProjectRef{{Name: "p1", Path: t.TempDir(), ActorID: projectID}}

	// Single source of truth for the card raw — used by both
	// wiki_get_card (preflight read) and wiki_claim_task_card (post-
	// CAS Raw). Both must surface data.exec.kind inside the
	// frontmatter block so the dispatcher resolves the kind.
	const cardFrontmatter = "---\nid: card-1\ntype: task\nstatus: backlog\n" +
		"data:\n  exec:\n    kind: worker_task\n---\n\nExplicit kind body."
	const cardFrontmatterDoing = "---\nid: card-1\ntype: task\nstatus: doing\n" +
		"data:\n  exec:\n    kind: worker_task\n---\n\nExplicit kind body."

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == callerAgentID {
			return testutil.NewFakeRef(aid, func(callID string, _ any) any {
				if callID == "agent_status" {
					return gen.AgentStatusResp{ActiveWorkflowMapCardID: "map-1"}
				}
				return nil
			}), true
		}
		return testutil.NewFakeRef(aid, func(callID string, payload any) any {
			switch callID {
			case "project.spawn_agent":
				return domain.ProjectSpawnAgentResp{ActorID: agentActorID}
			case "project.wiki_get_card":
				return domain.WikiGetCardResp{ID: "card-1", Raw: cardFrontmatter}
			case "project.wiki_claim_task_card":
				// project.wiki_claim_task_card CAS-flips status to
				// doing; the Raw in the response carries the post-
				// flip frontmatter so stripCardFrontmatter sees the
				// same structure as the read.
				if req, ok := payload.(gen.WikiClaimTaskCardReq); ok && req.ID == "card-1" {
					return gen.WikiClaimTaskCardResp{
						PreviousStatus: "backlog",
						Raw:            cardFrontmatterDoing,
					}
				}
				return fmt.Errorf("project.wiki.claim_task_card: unknown card")
			}
			return nil
		}), true
	}

	resp, err := a.handleAgentSpawnAssign(ctx, domain.WorkspaceAgentSpawnAssignReq{
		To:              "Worker",
		AgentKind:       domain.AgentKindWorker,
		BoundTaskCardID: "card-1",
		ProjectID:       projectID,
		CallerAgentID:   callerAgentID,
	})
	if err != nil {
		t.Fatalf("handleAgentSpawnAssign: %v", err)
	}
	if resp.AgentActorID != agentActorID {
		t.Errorf("expected spawned agent %q, got %q", agentActorID, resp.AgentActorID)
	}
	if resp.Goal.Condition != "Explicit kind body." {
		t.Errorf("goal condition should come from the explicit-kind card body; got %q", resp.Goal.Condition)
	}
}

// ── workspace.workflow_start ───────────────────────────────────────────────

// TestHandleWorkflowStart_FromTemplate verifies the one-step template start
// path: when TemplateMapId is provided, workspace.workflow_start first calls
// project.wiki_template_instantiate, then activates the resulting instance map.
func TestHandleWorkflowStart_FromTemplate(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder, LoadState: "loaded"},
	}

	var instantiateReq gen.WikiTemplateInstantiateReq
	var activateReq gen.AgentWorkflowStartReq
	var kickoffReq gen.AgentChatSubmitReq

	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		switch {
		case aid.String() == projectID:
			// Mock project actor: handles template instantiation.
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				if callID == "project.wiki_template_instantiate" {
					req, ok := payload.(gen.WikiTemplateInstantiateReq)
					if !ok {
						if p, ok2 := payload.(*gen.WikiTemplateInstantiateReq); ok2 {
							req = *p
						}
					}
					instantiateReq = req
					return gen.WikiTemplateInstantiateResp{InstanceMap: gen.MonoCardListItem{ID: req.InstanceMapID}}
				}
				return nil
			}), true
		case aid.String() == agentActorID:
			// Mock agent actor.
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "workflow_start":
					if req, ok := payload.(gen.AgentWorkflowStartReq); ok {
						activateReq = req
					}
					return gen.AgentWorkflowStartResp{MapCardID: "map-from-template"}
				case "chat_submit":
					if req, ok := payload.(gen.AgentChatSubmitReq); ok {
						kickoffReq = req
					}
					return gen.AgentChatSubmitResp{TurnActorID: "turn-1"}
				}
				return nil
			}), true
		default:
			return nil, false
		}
	}

	resp, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{
		AgentActorID:  agentActorID,
		TemplateMapID: "tpl::workflow",
	})
	if err != nil {
		t.Fatalf("handleWorkflowStart with TemplateMapId: %v", err)
	}
	if resp.MapCardID == "" {
		t.Errorf("expected non-empty MapCardID in response")
	}
	if activateReq.MapCardID != resp.MapCardID {
		t.Errorf("agent workflow_start MapCardID = %q, want %q", activateReq.MapCardID, resp.MapCardID)
	}
	if instantiateReq.TemplateMapID != "tpl::workflow" {
		t.Errorf("template_instantiate TemplateMapID = %q, want tpl::workflow", instantiateReq.TemplateMapID)
	}
	if instantiateReq.Source != "workspace" {
		t.Errorf("template_instantiate Source = %q, want workspace", instantiateReq.Source)
	}
	if !strings.Contains(kickoffReq.Text, resp.MapCardID) {
		t.Errorf("expected kickoff chat to reference the instance map, got %q", kickoffReq.Text)
	}
}

// TestHandleWorkflowStart_Success verifies the happy path: the agent's
// workflow_start activates the map and a kickoff chat message starts the
// first orchestration turn.
func TestHandleWorkflowStart_Success(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{
			ID:          "Coder#0001",
			ActorID:     agentActorID,
			ProjectID:   projectID,
			AgentKind:   domain.AgentKindCoder,
			DisplayName: "Coder",
			LoadState:   "loaded",
		},
	}

	var activateReq gen.AgentWorkflowStartReq
	var kickoffReq gen.AgentChatSubmitReq
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "workflow_start":
					if req, ok := payload.(gen.AgentWorkflowStartReq); ok {
						activateReq = req
					}
					return gen.AgentWorkflowStartResp{MapCardID: "map-1"}
				case "chat_submit":
					if req, ok := payload.(gen.AgentChatSubmitReq); ok {
						kickoffReq = req
					}
					return gen.AgentChatSubmitResp{TurnActorID: "turn-1"}
				}
				return nil
			}), true
		}
		return nil, false
	}

	resp, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{
		AgentActorID: agentActorID,
		MapCardID:    "map-1",
	})
	if err != nil {
		t.Fatalf("handleWorkflowStart: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Errorf("expected MapCardId 'map-1', got %q", resp.MapCardID)
	}
	if activateReq.MapCardID != "map-1" {
		t.Errorf("expected agent workflow_start invoked with map-1, got %q", activateReq.MapCardID)
	}
	if !strings.Contains(kickoffReq.Text, "map-1") {
		t.Errorf("expected kickoff chat to reference the map, got %q", kickoffReq.Text)
	}
}

// TestHandleWorkflowStart_ValidationErrors verifies required fields and the
// unknown-agent / wrong-project rejections happen before any invoke.
func TestHandleWorkflowStart_ValidationErrors(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder, LoadState: "loaded"},
	}
	invoked := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		invoked = true
		return nil, false
	}

	if _, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{MapCardID: "map-1"}); err == nil {
		t.Fatal("expected error for empty AgentActorId")
	}
	if _, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{AgentActorID: agentActorID}); err == nil {
		t.Fatal("expected error for empty MapCardId")
	}
	if _, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{AgentActorID: "nobody", MapCardID: "map-1"}); err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if _, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{AgentActorID: agentActorID, MapCardID: "map-1", ProjectID: "other-project"}); err == nil {
		t.Fatal("expected error for project mismatch")
	}
	if invoked {
		t.Fatal("no agent actor should be touched on validation failure")
	}
}

// TestHandleWorkflowStart_ActivateErrorSurfaces verifies an activation
// failure (e.g. map owned by another agent) surfaces and no kickoff chat is
// submitted.
func TestHandleWorkflowStart_ActivateErrorSurfaces(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder, LoadState: "loaded"},
	}

	chatSubmitted := false
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return testutil.NewFakeRef(aid, func(callID string, payload any) any {
				switch callID {
				case "workflow_start":
					return fmt.Errorf("map is owned by another agent")
				case "chat_submit":
					chatSubmitted = true
					return gen.AgentChatSubmitResp{}
				}
				return nil
			}), true
		}
		return nil, false
	}

	if _, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{AgentActorID: agentActorID, MapCardID: "map-1"}); err == nil {
		t.Fatal("expected activation error to surface")
	}
	if chatSubmitted {
		t.Fatal("kickoff chat must not be submitted when activation fails")
	}
}

// ── fire-and-forget kickoff ────────────────────────────────────────────────

// blockingChatStream blocks in Recv until Close is called, simulating a hung
// agent chat_submit handler. Used to prove handleWorkflowStart never waits on
// the kickoff chat's Final.
type blockingChatStream struct {
	done chan struct{}
	once sync.Once
}

func newBlockingChatStream() *blockingChatStream {
	return &blockingChatStream{done: make(chan struct{})}
}

func (s *blockingChatStream) Recv() (any, error) {
	<-s.done
	return nil, errors.New("stream cancelled")
}
func (s *blockingChatStream) RecvRaw() ([]byte, error) {
	<-s.done
	return nil, errors.New("stream cancelled")
}
func (s *blockingChatStream) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// blockingChatRef is a ref.Ref whose chat_submit Invoke returns a call that
// blocks forever (simulating a turn that synchronously queries workspace).
// workflow_start succeeds normally.
type blockingChatRef struct {
	actorID id.ActorID
}

func (b blockingChatRef) ID() id.ActorID          { return b.actorID }
func (blockingChatRef) Service() (string, bool)    { return "", false }
func (b blockingChatRef) Invoke(_ context.Context, callID string, _ any, _ ...map[string]string) *invoke.Call {
	if callID == "workflow_start" {
		return invoke.NewCall(invoke.CallModeUnary, &oneShotValueStream{value: gen.AgentWorkflowStartResp{MapCardID: "map-1"}})
	}
	// chat_submit: block forever — the kickoff must not wait on it.
	return invoke.NewCall(invoke.CallModeUnary, newBlockingChatStream())
}

// oneShotValueStream yields one value then io.EOF — simulates a successful
// unary call.
type oneShotValueStream struct {
	value    any
	consumed bool
}

func (s *oneShotValueStream) Recv() (any, error) {
	if s.consumed {
		return nil, io.EOF
	}
	s.consumed = true
	return s.value, nil
}
func (s *oneShotValueStream) RecvRaw() ([]byte, error) { return nil, io.EOF }
func (s *oneShotValueStream) Close() error             { return nil }

var _ ref.Ref = blockingChatRef{}

// TestHandleWorkflowStart_KickoffFireAndForget verifies the kickoff chat is
// fire-and-forget: handleWorkflowStart returns promptly even when the agent's
// chat_submit handler blocks (as it does when the turn synchronously queries
// workspace stateful callables). The activation must still be in place and
// the response returned.
func TestHandleWorkflowStart_KickoffFireAndForget(t *testing.T) {
	a, ctx := freshActor(t)
	var ts uint64
	g := id.NewCanonical(99, 0, func() uint64 { ts++; return ts })
	projectID := g.Next().String()
	agentActorID := g.Next().String()
	a.Agents = []domain.AgentRef{
		{ID: "Coder#0001", ActorID: agentActorID, ProjectID: projectID, AgentKind: domain.AgentKindCoder, LoadState: "loaded"},
	}
	ctx.LookupIDFn = func(aid id.ActorID) (ref.Ref, bool) {
		if aid.String() == agentActorID {
			return blockingChatRef{actorID: aid}, true
		}
		return nil, false
	}

	start := time.Now()
	resp, err := a.handleWorkflowStart(ctx, gen.WorkspaceWorkflowStartReq{AgentActorID: agentActorID, MapCardID: "map-1"})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("handleWorkflowStart: %v", err)
	}
	if resp.MapCardID != "map-1" {
		t.Errorf("expected MapCardId 'map-1', got %q", resp.MapCardID)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("handleWorkflowStart waited for the kickoff chat handler: %v", elapsed)
	}
}

// TestRegistrationSurface_WorkflowOrchestrationLoops pins the loop wiring of
// the workflow orchestration surface after the stateless conversion:
//
//   - agent_spawn_scheduler, agent_spawn_by_type, agent_assign,
//     agent_spawn_assign, workflow_start, agent_terminate and agent_review
//     are PureContext handlers and must NOT declare a WithLoop — a named loop
//     (e.g. the former "lifecycle" lane) would enqueue the stateless handler
//     onto a stateful lane and re-serialize it; without one they default to
//     the pure loop (forked goroutine, no ownerLoop occupancy).
func TestRegistrationSurface_WorkflowOrchestrationLoops(t *testing.T) {
	a := &Actor{}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.RegOpts = map[string][]actor.RegisterOption{}

	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{
		"workspace.agent_spawn_scheduler",
		"workspace.agent_spawn_by_type",
		"workspace.agent_assign",
		"workspace.agent_spawn_assign",
		"workspace.workflow_start",
		"workspace.agent_terminate",
		"workspace.agent_review",
	} {
		opts, ok := ctx.RegOpts[id]
		if !ok {
			t.Errorf("%s not registered", id)
			continue
		}
		if got := actor.ResolveLoop(opts...); got != "" {
			t.Errorf("%s declares loop %q; stateless handlers must not pin a loop", id, got)
		}
		if got := actor.ResolveLoopOrDefault(actor.ModeStateless, opts...); got != actor.DefaultLoopPure {
			t.Errorf("%s stateless loop = %q, want %q", id, got, actor.DefaultLoopPure)
		}
	}
}
