package agentkit

import (
	"encoding/json"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestCoderDefaultSkills(t *testing.T) {
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == domain.AgentKindCoder {
			if len(cfg.SkillIDs) != 1 || cfg.SkillIDs[0] != "plan-module" {
				t.Fatalf("coder SkillIDs = %v, want [plan-module]", cfg.SkillIDs)
			}
			return
		}
	}
	t.Fatal("coder kind missing")
}

func TestCoderDefaultBundles_NoWorkspaceOrOmnibox(t *testing.T) {
	var coder *domain.AgentKindConfig
	for i, cfg := range BaseKindConfigs() {
		if cfg.Kind == domain.AgentKindCoder {
			coder = &BaseKindConfigs()[i]
			break
		}
	}
	if coder == nil {
		t.Fatal("coder kind not found in BaseKindConfigs")
	}
	forbidden := map[string]bool{
		"builtin:bundle:workspace-tools":    true,
		"builtin:bundle:interface-controls": true,
	}
	for _, id := range coder.DefaultBundleIDs {
		if forbidden[id] {
			t.Errorf("coder kind must not mount default bundle %q", id)
		}
		if id == "builtin:bundle:fork-dream" {
			t.Errorf("coder kind must not mount fork-dream; it belongs to memory mode")
		}
	}
}

// TestCoderDefaultBundles_NoGoalBundle verifies that the coder kind does not
// mount builtin:bundle:goal by default. Goal tools (goal_submit, goal_card_submit)
// are only available when builtin:mode:goal is active.
func TestCoderDefaultBundles_NoGoalBundle(t *testing.T) {
	var coder *domain.AgentKindConfig
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == domain.AgentKindCoder {
			coder = &cfg
			break
		}
	}
	if coder == nil {
		t.Fatal("coder kind not found in BaseKindConfigs")
	}
	for _, id := range coder.DefaultBundleIDs {
		if id == "builtin:bundle:goal" {
			t.Errorf("coder kind must not mount builtin:bundle:goal by default; goal tools are only available in goal mode")
		}
	}
}

func TestAppBuilderKindAndProfile(t *testing.T) {
	found := false
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == "app-builder" {
			found = true
			if cfg.RolePromptRef.Key != "project.app-builder" {
				t.Fatalf("prompt key = %q", cfg.RolePromptRef.Key)
			}
			if len(cfg.DefaultBundleIDs) == 0 {
				t.Fatal("app-builder has no default bundle IDs")
			}
		}
	}
	if !found {
		t.Fatal("app-builder kind missing")
	}
}

func TestBaseKindConfigs_NoDuplicateKinds(t *testing.T) {
	seen := make(map[string]int)
	for i, cfg := range BaseKindConfigs() {
		if prev, ok := seen[cfg.Kind]; ok {
			t.Fatalf("duplicate kind %q at indices %d and %d", cfg.Kind, prev, i)
		}
		seen[cfg.Kind] = i
	}
}

func TestScoutKindConfig(t *testing.T) {
	var scout *domain.AgentKindConfig
	for i, cfg := range BaseKindConfigs() {
		if cfg.Kind == string(domain.AgentKindScout) {
			scout = &BaseKindConfigs()[i]
			break
		}
	}
	if scout == nil {
		t.Fatal("scout kind not found in BaseKindConfigs")
	}
	if scout.RolePromptRef.Key != "project.scout" {
		t.Errorf("scout RolePromptRef.Key = %q, want project.scout", scout.RolePromptRef.Key)
	}
	if scout.RolePromptRef.Kind != "profile" {
		t.Errorf("scout RolePromptRef.Kind = %q, want profile", scout.RolePromptRef.Kind)
	}
	expectedBundles := map[string]bool{
		"builtin:bundle:project-wiki":  true,
		"builtin:bundle:file-tools":    true,
		"builtin:bundle:git-tools":     true,
		"builtin:bundle:web-search":    true,
		"builtin:bundle:browser-crawl": true,
		"builtin:bundle:fork-explore":  true,
	}
	for _, id := range scout.DefaultBundleIDs {
		delete(expectedBundles, id)
	}
	if len(expectedBundles) > 0 {
		missing := make([]string, 0, len(expectedBundles))
		for id := range expectedBundles {
			missing = append(missing, id)
		}
		t.Errorf("scout DefaultBundleIDs missing: %v", missing)
	}
	if scout.MaxTurns != 100 {
		t.Errorf("scout MaxTurns = %d, want 100", scout.MaxTurns)
	}
}

func TestSkillUseToolSchemaMatchesGeneratedType(t *testing.T) {
	spec := SkillUseTool()
	if spec.InputSchema == "" {
		t.Fatal("SkillUseTool InputSchema is empty")
	}
	payload := map[string]interface{}{
		"SkillId": "grill-me",
		"Args":    "some args",
		"Context": "inline",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var req domain.AgentSkillUseReq
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal with generated type: %v", err)
	}
	if req.SkillID != "grill-me" {
		t.Fatalf("SkillID not bound: got %q, want grill-me. Schema property names likely mismatch generated JSON tags.", req.SkillID)
	}
}

func TestMediaToolSpecsFromBundlesIncludesVideoGeneration(t *testing.T) {
	for _, spec := range MediaToolSpecsFromBundles([]string{"builtin:bundle:video-gen"}) {
		if spec.Name != "generate_video" {
			continue
		}
		if spec.CallableID != "video_generate" || spec.ServiceName != "agent" {
			t.Fatalf("generate_video route = %s/%s, want agent/video_generate", spec.ServiceName, spec.CallableID)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal([]byte(spec.InputSchema), &schema); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"Prompt", "Model", "Duration", "AspectRatio", "ReferenceImages", "ReferenceVideos", "ReferenceAudios"} {
			if _, ok := schema.Properties[field]; !ok {
				t.Errorf("generate_video schema missing %s", field)
			}
		}
		return
	}
	t.Fatal("generate_video tool missing")
}

func TestToolSchemasAreValidJSON(t *testing.T) {
	cfg := domain.AgentKindConfig{
		Kind: domain.AgentKindCoder,
	}
	var all []domain.ToolSpec
	all = append(all, CoderAutonomousTools()...)
	all = append(all, PlanningTools()...)
	all = append(all, DebugToolSpecsFromBundles([]string{"builtin:bundle:debug"})...)
	all = append(all, SkillUseTool())
	all = append(all, OpenGlobalBrowserTool())
	all = append(all, AutonomousToolsForKind(cfg)...)

	seen := make(map[string]struct{})
	for _, spec := range all {
		if spec.InputSchema == "" {
			t.Errorf("%s: InputSchema is empty", spec.Name)
			continue
		}
		if !json.Valid([]byte(spec.InputSchema)) {
			t.Errorf("%s: InputSchema is not valid JSON: %s", spec.Name, spec.InputSchema)
		}
		if _, ok := seen[spec.Name]; ok {
			continue
		}
		seen[spec.Name] = struct{}{}
	}
}

func TestBaseKindConfigs_AllHaveCardSystemBundle(t *testing.T) {
	// Workspace-global agents (Coordinator) have no project context, so
	// they must NOT mount project-scoped bundles like project-wiki (whose
	// project.wiki.* callables resolve via LookupService("project"), which fails
	// for global agents). They use workspace-level card resolution internally.
	globalKinds := map[string]bool{
		"dreamer":                         true,
		string(domain.AgentKindCoordinator): true,
		// Plugin agents are workspace-global like the Coordinator: their tools
		// come from bound app-bundle cards, never project-scoped bundles.
		domain.AgentKindPlugin:            true,
	}
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind == "dreamer" {
			if len(cfg.AutoAllowTools) != 0 {
				t.Errorf("dreamer kind: expected no AutoAllowTools, got %v", cfg.AutoAllowTools)
			}
			continue
		}
		// Workspace-global agents skip the project-wiki requirement.
		if globalKinds[cfg.Kind] {
			continue
		}
		hasWiki := false
		for _, id := range cfg.DefaultBundleIDs {
			if id == "builtin:bundle:project-wiki" {
				hasWiki = true
				break
			}
		}
		if !hasWiki {
			t.Errorf("kind %q: missing card-system capability bundle builtin:bundle:project-wiki in DefaultBundleIDs", cfg.Kind)
		}
	}
}

// TestGlobalAgents_NoProjectScopedBundles is a regression test: workspace-global
// agents (Coordinator) must not mount project-scoped bundles (project-wiki,
// file-tools) whose callables resolve via LookupService("project"). Global agents
// cannot reach the project service (ExposeToChildren is scoped to project
// descendants), so those tools would fail at runtime with
// "service \"project\" not available".
func TestGlobalAgents_NoProjectScopedBundles(t *testing.T) {
	projectScopedBundles := map[string]bool{
		"builtin:bundle:project-wiki": true,
		"builtin:bundle:file-tools":   true,
	}
	for _, cfg := range BaseKindConfigs() {
		if cfg.Kind != domain.AgentKindCoordinator {
			continue
		}
		for _, id := range cfg.DefaultBundleIDs {
			if projectScopedBundles[id] {
				t.Errorf("global agent kind %q mounts project-scoped bundle %q; it has no project context (LookupService(\"project\") fails)", cfg.Kind, id)
			}
		}
	}
}

func TestCoordinatorKindConfig(t *testing.T) {
	var coordinator *domain.AgentKindConfig
	for i, cfg := range BaseKindConfigs() {
		if cfg.Kind == domain.AgentKindCoordinator {
			coordinator = &BaseKindConfigs()[i]
			break
		}
	}
	if coordinator == nil {
		t.Fatal("coordinator kind not found in BaseKindConfigs")
	}
	if coordinator.RolePromptRef.Key != "workspace.coordinator" {
		t.Errorf("coordinator RolePromptRef.Key = %q, want workspace.coordinator", coordinator.RolePromptRef.Key)
	}
	if len(coordinator.DefaultBundleIDs) == 0 {
		t.Error("coordinator has no DefaultBundleIDs")
	}
}

func TestDreamerKindConfig(t *testing.T) {
	var dreamer *domain.AgentKindConfig
	for i, cfg := range BaseKindConfigs() {
		if cfg.Kind == "dreamer" {
			dreamer = &BaseKindConfigs()[i]
			break
		}
	}
	if dreamer == nil {
		t.Fatal("dreamer kind not found in BaseKindConfigs")
	}
	if dreamer.UserCreatable {
		t.Error("dreamer should not be user-creatable")
	}
	if !dreamer.SystemManaged {
		t.Error("dreamer should be system-managed")
	}
	if dreamer.RolePromptRef.Key != "" || dreamer.RolePromptRef.Kind != "" {
		t.Error("dreamer should have no role prompt ref")
	}
	if len(dreamer.AutoAllowTools) != 0 {
		t.Errorf("dreamer AutoAllowTools = %v, want empty", dreamer.AutoAllowTools)
	}
	if len(dreamer.SkillIDs) != 0 {
		t.Errorf("dreamer SkillIDs = %v, want empty", dreamer.SkillIDs)
	}
	if tools := AutonomousToolsForKind(*dreamer); len(tools) != 0 {
		t.Errorf("dreamer runtime tools = %v, want empty", tools)
	}
	if len(dreamer.DefaultBundleIDs) != 0 {
		t.Errorf("dreamer DefaultBundleIDs = %v, want empty", dreamer.DefaultBundleIDs)
	}
}
