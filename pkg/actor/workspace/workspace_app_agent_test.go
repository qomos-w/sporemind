package workspace

import (
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestCreateAppAgent_CreateAndIdempotentReconcile(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	resp, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID:         "app.demo",
		Slot:          "default",
		DisplayName:   "Demo Assistant",
		SystemPrompt:  "Run demo.",
		BundleCardIds: []string{"app-bundle:app.demo:main", "builtin:bundle:web-search"},
	})
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	if !resp.Created {
		t.Fatalf("create #1: Created = false, want true")
	}
	found := false
	for _, ag := range a.agentSnapshot() {
		if ag.ID == resp.AgentID {
			found = true
			if ag.AgentKind != domain.AgentKindPlugin {
				t.Errorf("agent kind = %q, want plugin", ag.AgentKind)
			}
			if ag.ProjectID != "" {
				t.Errorf("project = %q, want global (coordinator-position) agent", ag.ProjectID)
			}
			if ag.BoundAppID != "app.demo" {
				t.Errorf("bound app = %q, want app.demo", ag.BoundAppID)
			}
			if ag.BoundAppSlot != "default" {
				t.Errorf("bound slot = %q, want default", ag.BoundAppSlot)
			}
			if ag.DisplayName != "Demo Assistant" {
				t.Errorf("display name = %q", ag.DisplayName)
			}
		}
	}
	if !found {
		t.Fatal("bound agent not present in registry after create")
	}

	// Second create reconciles the same agent instead of creating a second one.
	resp2, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID:       "app.demo",
		Slot:        "default",
		DisplayName: "Demo Assistant v2",
	})
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	if resp2.Created {
		t.Fatal("create #2: Created = true, want false (idempotent)")
	}
	if resp2.AgentID != resp.AgentID {
		t.Fatalf("create #2 rebound to a different agent: %q vs %q", resp2.AgentID, resp.AgentID)
	}
	count := 0
	for _, ag := range a.agentSnapshot() {
		if ag.BoundAppID == "app.demo" && ag.BoundAppSlot == "default" {
			count++
			if ag.DisplayName != "Demo Assistant v2" {
				t.Errorf("display name after reconcile = %q", ag.DisplayName)
			}
		}
	}
	if count != 1 {
		t.Fatalf("bound agent count = %d, want exactly 1", count)
	}
}

func TestCreateAppAgent_MultipleSlots(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	def, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo Assistant",
	})
	if err != nil {
		t.Fatalf("create default: %v", err)
	}
	rev, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "reviewer", DisplayName: "Demo Reviewer",
	})
	if err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	if def.AgentID == rev.AgentID {
		t.Fatalf("slots share one agent id %q", def.AgentID)
	}

	// A reconcile of one slot must not touch the other slot's agent.
	rev2, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "reviewer", DisplayName: "Demo Reviewer v2",
	})
	if err != nil {
		t.Fatalf("reconcile reviewer: %v", err)
	}
	if rev2.Created || rev2.AgentID != rev.AgentID {
		t.Fatalf("reviewer reconcile = %+v, want same agent", rev2)
	}
	slots := map[string]string{}
	for _, ag := range a.agentSnapshot() {
		if ag.BoundAppID == "app.demo" {
			slots[ag.BoundAppSlot] = ag.DisplayName
		}
	}
	if len(slots) != 2 || slots["default"] != "Demo Assistant" || slots["reviewer"] != "Demo Reviewer v2" {
		t.Fatalf("slots = %v", slots)
	}
}

func TestCreateAppAgent_RoleGate(t *testing.T) {
	a, _ := freshActor(t)
	for _, role := range []string{"agent", "developer", ""} {
		ctx := roleCtx(t, a, role)
		_, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{AppID: "app.demo"})
		if err == nil || !strings.Contains(err.Error(), "system or admin") {
			t.Fatalf("role %q: expected system-or-admin denial, got %v", role, err)
		}
	}
	ctx := roleCtx(t, a, "admin")
	if _, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{AppID: "app.demo"}); err != nil {
		t.Fatalf("admin create: %v", err)
	}
}

func TestRemoveAppAgent_RemovesBoundAgentsOnly(t *testing.T) {
	a, adminCtx := freshActor(t)
	ctx := roleCtx(t, a, "system")

	resp, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{AppID: "app.demo", Slot: "default", DisplayName: "Demo"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp2, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{AppID: "app.demo", Slot: "reviewer", DisplayName: "Demo Reviewer"})
	if err != nil {
		t.Fatalf("create reviewer: %v", err)
	}
	// An unrelated agent must survive the removal.
	other, err := a.handleCreateAgent(adminCtx, domain.WorkspaceCreateAgentReq{AgentKind: domain.AgentKindCoordinator})
	if err != nil {
		t.Fatalf("seed coordinator: %v", err)
	}

	if err := a.handleRemoveAppAgent(ctx, domain.WorkspaceRemoveAppAgentReq{AppID: "app.demo"}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	statuses := map[string]string{}
	for _, ag := range a.agentSnapshot() {
		if ag.ID == resp.AgentID || ag.ID == resp2.AgentID {
			statuses[ag.ID] = ag.DeletionStatus
		}
		if ag.ID == other.ID {
			statuses["other"] = ag.DeletionStatus
		}
	}
	if statuses[resp.AgentID] != "deleting" {
		t.Fatalf("default-slot agent DeletionStatus = %q, want deleting", statuses[resp.AgentID])
	}
	if statuses[resp2.AgentID] != "deleting" {
		t.Fatalf("reviewer-slot agent DeletionStatus = %q, want deleting", statuses[resp2.AgentID])
	}
	if statuses["other"] == "deleting" {
		t.Fatal("unrelated agent was marked for deletion")
	}

	// Removing again (app already gone) is a no-op, not an error.
	if err := a.handleRemoveAppAgent(ctx, domain.WorkspaceRemoveAppAgentReq{AppID: "app.demo"}); err != nil {
		t.Fatalf("idempotent remove: %v", err)
	}
}

// pinnedSlotFirstUnit returns the unit pinned by a hard-pinned slot, or "" if
// the agent's Primary is empty (host default). Tests use it to assert the
// "Provider|Model" string flows all the way into the agent's primary slot.
func pinnedSlotFirstUnit(t *testing.T, ag domain.AgentRef) string {
	t.Helper()
	if ag.Primary == nil || len(ag.Primary.Candidates) == 0 {
		return ""
	}
	u := ag.Primary.Candidates[0].Unit
	if u == nil {
		return ""
	}
	return u.Provider + "|" + u.Model
}

func TestCreateAppAgent_ModelBinding_PinsOnCreate(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	// Declared model: the agent's Primary is the hard-pinned unit slot.
	resp, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo",
		Model: "openai|gpt-5",
	})
	if err != nil {
		t.Fatalf("create with model: %v", err)
	}
	for _, ag := range a.agentSnapshot() {
		if ag.ID != resp.AgentID {
			continue
		}
		if got := pinnedSlotFirstUnit(t, ag); got != "openai|gpt-5" {
			t.Errorf("Primary unit = %q, want openai|gpt-5 (full slot = %+v)", got, ag.Primary)
		}
		if ag.Primary == nil || ag.Primary.Candidates[0].Kind != "unit" {
			t.Errorf("Primary is not a hard-pinned [unit] slot: %+v", ag.Primary)
		}
	}
}

func TestCreateAppAgent_ModelBinding_EmptyKeepsHostDefault(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	// No declared model: Primary stays empty ([auto]) for the plugin kind,
	// matching the pre-binding behaviour.
	resp, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo",
	})
	if err != nil {
		t.Fatalf("create without model: %v", err)
	}
	for _, ag := range a.agentSnapshot() {
		if ag.ID == resp.AgentID && ag.Primary != nil {
			t.Errorf("Primary should be empty for the host default, got %+v", ag.Primary)
		}
	}
}

func TestCreateAppAgent_ModelBinding_ReconcileUpdatesPrimary(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	// First register with one model.
	first, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo",
		Model: "openai|gpt-5",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Reload with a different model: the existing binding reconciles in place
	// (Created=false) and the agent's Primary flips to the new unit.
	second, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo",
		Model: "anthropic|claude-sonnet-4-6",
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if second.Created || second.AgentID != first.AgentID {
		t.Fatalf("reconcile Created=%v AgentID=%q, want idempotent on same agent", second.Created, second.AgentID)
	}
	for _, ag := range a.agentSnapshot() {
		if ag.ID != first.AgentID {
			continue
		}
		if got := pinnedSlotFirstUnit(t, ag); got != "anthropic|claude-sonnet-4-6" {
			t.Errorf("after reconcile Primary unit = %q, want anthropic|claude-sonnet-4-6", got)
		}
	}

	// Drop the model on a subsequent reload: the agent's Primary is cleared
	// back to the host default ([auto], empty slot).
	third, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
		AppID: "app.demo", Slot: "default", DisplayName: "Demo",
		Model: "",
	})
	if err != nil {
		t.Fatalf("reconcile no-model: %v", err)
	}
	if third.AgentID != first.AgentID {
		t.Fatalf("reconcile rebound to a different agent")
	}
	for _, ag := range a.agentSnapshot() {
		if ag.ID == first.AgentID && ag.Primary != nil {
			t.Errorf("clearing model should leave Primary empty, got %+v", ag.Primary)
		}
	}
}

func TestCreateAppAgent_ModelBinding_MalformedIgnored(t *testing.T) {
	a, _ := freshActor(t)
	ctx := roleCtx(t, a, "system")

	// Malformed strings are silently ignored — workspace does not re-validate
	// the encoding shape (appdef does), but the defensive parser must not
	// blow up on bad input. Behaviour matches an empty Model: host default.
	for _, bad := range []string{"no-pipe", "|missing-provider", "missing-model|", "openai|gpt|5"} {
		resp, err := a.handleCreateAppAgent(ctx, domain.WorkspaceCreateAppAgentReq{
			AppID: "app.bad", Slot: "default", DisplayName: "Bad",
			Model: bad,
		})
		if err != nil {
			t.Fatalf("create with bad model %q: %v", bad, err)
		}
		for _, ag := range a.agentSnapshot() {
			if ag.ID == resp.AgentID && ag.Primary != nil {
				t.Errorf("malformed model %q should leave Primary empty, got %+v", bad, ag.Primary)
			}
		}
	}
}

// Sanity: the helper parses the canonical "Provider|Model" form and produces
// a hard-pinned [unit] slot. Empty / malformed inputs return nil so callers
// fall through to the host default.
func TestPinnedUnitSlotFromUnitString(t *testing.T) {
	if pinnedUnitSlotFromUnitString("") != nil {
		t.Error("empty input must return nil")
	}
	if pinnedUnitSlotFromUnitString("no-pipe") != nil {
		t.Error("missing pipe must return nil")
	}
	if pinnedUnitSlotFromUnitString("|MiniMax-M3") != nil {
		t.Error("empty provider must return nil")
	}
	if pinnedUnitSlotFromUnitString("minimax|") != nil {
		t.Error("empty model must return nil")
	}
	if pinnedUnitSlotFromUnitString("openai|gpt|5") != nil {
		t.Error("extra pipe in model must return nil")
	}
	slot := pinnedUnitSlotFromUnitString("openai|gpt-5")
	if slot == nil || len(slot.Candidates) != 1 {
		t.Fatalf("valid input must produce a single-candidate slot, got %+v", slot)
	}
	c := slot.Candidates[0]
	if c.Kind != "unit" || c.Unit == nil || c.Unit.Provider != "openai" || c.Unit.Model != "gpt-5" {
		t.Errorf("pinned slot = %+v, want [unit openai|gpt-5]", c)
	}
}

// Sanity: pinnedUnitSlotFromUnitString uses the gen types end-to-end so a
// future schema-driven change cannot silently regress the slot shape.
func TestPinnedUnitSlotFromUnitString_SchemaShape(t *testing.T) {
	slot := pinnedUnitSlotFromUnitString("minimax|MiniMax-M3")
	if slot == nil {
		t.Fatal("slot is nil for valid input")
	}
	var _ *gen.ModelSlot = slot
}
