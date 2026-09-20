package agentkit

import (
	"sort"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestMediaToolSpecsFromBundlesGatesByBundle(t *testing.T) {
	if got := MediaToolSpecsFromBundles(nil); len(got) != 0 {
		t.Fatalf("unmounted media bundles exposed %d tools", len(got))
	}
	got := MediaToolSpecsFromBundles([]string{"builtin:bundle:image-gen", "builtin:bundle:image-recognition", "builtin:bundle:video-gen"})
	if len(got) != 3 {
		t.Fatalf("mounted media bundles exposed %d tools, want 3", len(got))
	}
	allowed := map[string]bool{"image_generate": true, "image_recognize": true, "video_generate": true}
	for _, spec := range got {
		if spec.ServiceName != "agent" || !allowed[spec.CallableID] {
			t.Errorf("unexpected media route: %+v", spec)
		}
	}
}

func TestMediaBundlesRegistered(t *testing.T) {
	for _, want := range []string{"builtin:bundle:image-gen", "builtin:bundle:image-recognition", "builtin:bundle:video-gen"} {
		found := false
		for _, card := range BuiltinCards {
			if card.Title == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s missing from BuiltinCards registry", want)
		}
		if ids := CallableIDsForBundles([]string{want}); len(ids) != 1 {
			t.Fatalf("%s resolves %v, want one callable", want, ids)
		}
	}
}

// TestImageRecognitionBundleCallable locks the recognition bundle contract:
// mounting builtin:bundle:image-recognition exposes exactly the
// image_recognize callable.
func TestImageRecognitionBundleCallable(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:image-recognition"})
	if len(ids) != 1 || ids[0] != "image_recognize" {
		t.Fatalf("image-recognition bundle resolves %v, want [image_recognize]", ids)
	}
}

// TestCallableIDsForBundles_WorktreeBundle is the acceptance check for the
// worktree bundle: mounting builtin:bundle:worktree must expose the general
// worktree management callables (enter/list/get/agent_bindings). The exit
// callable has moved to builtin:mode:worktree because it only makes sense
// while the agent is inside a worktree.
func TestCallableIDsForBundles_WorktreeBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:worktree"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:worktree resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{
		"project.worktree_enter",
		"project.worktree_list",
		"project.worktree_get",
		"project.worktree_agent_bindings",
	} {
		if !got[want] {
			t.Errorf("worktree bundle missing callable %q (got %v)", want, ids)
		}
	}
	if got["project.worktree_exit"] {
		t.Errorf("project.worktree.exit must not come from the bundle; it belongs to builtin:mode:worktree")
	}
}

// TestCallableIDsForBundles_WorktreeModeCard verifies that the worktree mode
// card contributes the exit callable used to leave a worktree.
func TestCallableIDsForBundles_WorktreeModeCard(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:mode:worktree"})
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["project.worktree_exit"] {
		t.Errorf("worktree mode card missing callable project.worktree.exit (got %v)", ids)
	}
}

// TestCallableIDsForBundles_WorktreeMountedOnWorkerKinds verifies the bundle is
// mounted on the kinds that need it. This guards against accidental removal.
func TestCallableIDsForBundles_WorktreeMountedOnWorkerKinds(t *testing.T) {
	wantKinds := []string{"app-builder", "general"}
	cfgs := BaseKindConfigs()
	got := map[string]bool{}
	for _, c := range cfgs {
		for _, b := range c.DefaultBundleIDs {
			if b == "builtin:bundle:worktree" {
				got[c.Kind] = true
			}
		}
	}
	sort.Strings(wantKinds)
	for _, k := range wantKinds {
		if !got[k] {
			t.Errorf("kind %q does not mount builtin:bundle:worktree", k)
		}
	}
}

// TestForkToolSpecsFromBundles verifies that fork tool specs are generated
// from the data.fork declarations on bundle cards, with the correct tool names,
// the shared workspace.agent_spawn_by_type callable, and per-tool schema
// differences (fork_review carries ReviewText/PlanEvidence; others carry
// Description/Prompt).
func TestForkToolSpecsFromBundles(t *testing.T) {
	specs := ForkToolSpecsFromBundles([]string{
		"builtin:bundle:fork-explore",
		"builtin:bundle:fork-review",
		"builtin:bundle:fork-general",
	})
	if len(specs) != 3 {
		t.Fatalf("expected 3 fork tool specs, got %d", len(specs))
	}
	byName := map[string]domain.ToolSpec{}
	for _, s := range specs {
		byName[s.Name] = s
	}
	for _, want := range []string{"fork_explore", "fork_review", "fork_general"} {
		s, ok := byName[want]
		if !ok {
			t.Errorf("missing fork tool spec %q", want)
			continue
		}
		if s.CallableID != "workspace.agent_spawn_by_type" {
			t.Errorf("%s: CallableID = %q, want workspace.agent_spawn_by_type", want, s.CallableID)
		}
		if s.Description == "" {
			t.Errorf("%s: Description is empty", want)
		}
		if s.InputSchema == "" {
			t.Errorf("%s: InputSchema is empty", want)
		}
	}
	// fork_review schema carries ReviewText; the explore/general schema does not.
	review := byName["fork_review"]
	if !strings.Contains(review.InputSchema, "ReviewText") {
		t.Errorf("fork_review schema missing ReviewText: %s", review.InputSchema)
	}
	if strings.Contains(byName["fork_explore"].InputSchema, "ReviewText") {
		t.Errorf("fork_explore schema should not carry ReviewText")
	}
}

// TestForkToolSpecsFromBundles_FiltersInternalForkDream verifies that the
// system-internal fork_dream declaration is not exposed as a tool spec even
// when its bundle is present, so it never reaches the LLM tool surface.
func TestForkToolSpecsFromBundles_FiltersInternalForkDream(t *testing.T) {
	specs := ForkToolSpecsFromBundles([]string{
		"builtin:bundle:fork-explore",
		"builtin:bundle:fork-dream",
	})
	for _, s := range specs {
		if s.Name == "fork_dream" {
			t.Fatalf("fork_dream should not be exposed as a tool spec, got %+v", s)
		}
	}
	for _, want := range []string{"fork_explore"} {
		found := false
		for _, s := range specs {
			if s.Name == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing fork tool spec %q", want)
		}
	}
}

// TestForkKindMapFromBundles_IncludesDreamerRoute verifies that the internal
// fork_dream bundle still contributes the fork_dream -> dreamer kind route to the
// turn-engine mapping, even though it is not exposed as an LLM tool.
func TestForkKindMapFromBundles_IncludesDreamerRoute(t *testing.T) {
	m := ForkKindMapFromBundles([]string{"builtin:bundle:fork-dream"})
	if got, ok := m["fork_dream"]; !ok || got != domain.AgentKindDreamer {
		t.Fatalf("fork_dream route = %q (ok=%v), want %q", got, ok, domain.AgentKindDreamer)
	}
}

// from bundle data.fork declarations for each kind's mounted fork bundles.
// fork tools are sourced from bundle data.fork declarations; the bundle is the
// single source of truth for the child-kind mapping.
func TestForkKindMapFromBundles(t *testing.T) {
	cases := []struct {
		kind string
		want map[string]string
	}{
		{domain.AgentKindCoder, map[string]string{"fork_explore": "explorer", "fork_review": "reviewer", "fork_general": "general"}},
	}
	cfgs := BaseKindConfigs()
	for _, tc := range cases {
		var bundles []string
		for _, c := range cfgs {
			if c.Kind == tc.kind {
				bundles = c.DefaultBundleIDs
				break
			}
		}
		got := ForkKindMapFromBundles(bundles)
		if len(got) != len(tc.want) {
			t.Errorf("kind %q: got %d fork mappings, want %d (%v)", tc.kind, len(got), len(tc.want), got)
			continue
		}
		for name, wantKind := range tc.want {
			if got[name] != wantKind {
				t.Errorf("kind %q: %s -> %q, want %q", tc.kind, name, got[name], wantKind)
			}
		}
	}
}

// TestCallableIDsForBundles_InterfaceControlsBundle is the acceptance check
// for the interface-controls bundle: mounting
// builtin:bundle:interface-controls must expose the single UI control
// callable plus the two creation callables reused from workspace.
func TestCallableIDsForBundles_InterfaceControlsBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:interface-controls"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:interface-controls resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{
		"interfacemanager.control",
		"interfacemanager.query_interactions",
		"workspace.create",
		"workspace.create_agent",
	} {
		if !got[want] {
			t.Errorf("interface-controls bundle missing callable %q (got %v)", want, ids)
		}
	}
}

// TestCallableIDsForBundles_InterfaceControlsMountedOnKinds guards the
// coordinator-only default mount for the interface-controls bundle.
func TestCallableIDsForBundles_InterfaceControlsMountedOnKinds(t *testing.T) {
	wantKinds := []string{"coordinator"}
	cfgs := BaseKindConfigs()
	got := map[string]bool{}
	for _, c := range cfgs {
		for _, b := range c.DefaultBundleIDs {
			if b == "builtin:bundle:interface-controls" {
				got[c.Kind] = true
			}
		}
	}
	sort.Strings(wantKinds)
	for _, k := range wantKinds {
		if !got[k] {
			t.Errorf("kind %q does not mount builtin:bundle:interface-controls", k)
		}
	}
	for k := range got {
		if k != "coordinator" {
			t.Errorf("kind %q must not mount builtin:bundle:interface-controls", k)
		}
	}
}

// TestCallableIDsForBundles_PluginDevBundle is the acceptance check for the
// plugin-dev bundle: mounting builtin:bundle:plugin-dev must expose the
// declared callables covering the dev lifecycle, the packaged
// export/install_local distribution loop, and the
// plugin_load/plugin_unload stop-resume pair. The runtime app-manager surface
// (list/get/invoke/component_list/component_get) is NOT declared here — it is
// inherited through the required builtin:bundle:app-tools dependency (see
// TestPluginDevBundleRequiresAppToolsBundle), the same layering workflow
// mode uses onto workflow-tools.
func TestCallableIDsForBundles_PluginDevBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:plugin-dev"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:plugin-dev resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	want := []string{
		"appmanager.register_project",
		"appmanager.app_export",
		"appmanager.install_local",
		"appmanager.reload_project",
		"appmanager.unregister",
		"appmanager.retry_cleanup",
		"appmanager.plugin_load",
		"appmanager.plugin_unload",
		"appmanager.dev_generate",
		"appmanager.dev_gate",
		"appmanager.sdk_vendor",
		"pluginhost.list_plugins",
		"pluginhost.plugin_logs",
		"pluginhost.plugin_dom",
		"appmanager.callable_info",
		"appmanager.dev_guide",
		"appmanager.host_protocol",
		"appmanager.icon_names",
		"appmanager.open_view",
		"appmanager.panel_topology",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("plugin-dev bundle missing callable %q (got %v)", w, ids)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("plugin-dev bundle resolves %v, want exactly %v", ids, want)
	}
}

// TestPluginDevBundleRequiresAppToolsBundle verifies the layered mount
// pattern: mounting builtin:bundle:plugin-dev must pull in the appmanager
// runtime tool surface by requiring builtin:bundle:app-tools, mirroring how
// builtin:mode:workflow requires builtin:bundle:workflow-tools.
func TestPluginDevBundleRequiresAppToolsBundle(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:bundle:plugin-dev" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:bundle:plugin-dev not registered")
	}
	found := false
	for _, dep := range asset.Dependencies {
		if dep == "builtin:bundle:app-tools" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("builtin:bundle:plugin-dev requires builtin:bundle:app-tools, got dependencies %v", asset.Dependencies)
	}
}

// TestTutorBundleRequiresInterfaceControlsBundle verifies the layered mount
// pattern: mounting builtin:bundle:tutor must pull in the UI control surface
// by requiring builtin:bundle:interface-controls; the tutor card declares no
// tools of its own.
func TestTutorBundleRequiresInterfaceControlsBundle(t *testing.T) {
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	var asset *CardAsset
	for i := range assets {
		if assets[i].Title == "builtin:bundle:tutor" {
			asset = &assets[i]
			break
		}
	}
	if asset == nil {
		t.Fatal("builtin:bundle:tutor not registered")
	}
	found := false
	for _, dep := range asset.Dependencies {
		if dep == "builtin:bundle:interface-controls" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("builtin:bundle:tutor requires builtin:bundle:interface-controls, got dependencies %v", asset.Dependencies)
	}
	if len(asset.Tools) != 0 {
		t.Fatalf("builtin:bundle:tutor must not re-declare tools inherited via requires, got %v", asset.Tools)
	}
}

// TestCallableIDsForBundles_AppToolsBundle is the acceptance check for the
// app-tools bundle: mounting builtin:bundle:app-tools must expose the core
// appmanager observability callables, including the bundle mount surface
// (component_list / component_get) alongside list/get/invoke.
func TestCallableIDsForBundles_AppToolsBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:app-tools"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:app-tools resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{
		"appmanager.list",
		"appmanager.get",
		"appmanager.invoke",
		"appmanager.component_list",
		"appmanager.component_get",
		"appmanager.register",
		"appmanager.reload",
	} {
		if !got[want] {
			t.Errorf("app-tools bundle missing callable %q (got %v)", want, ids)
		}
	}
}

// TestPluginDevBundleRegistered verifies the bundle card is present in the
// BuiltinCards registry and loads as a bundle-type asset.
func TestPluginDevBundleRegistered(t *testing.T) {
	found := false
	for _, card := range BuiltinCards {
		if card.Title == "builtin:bundle:plugin-dev" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("builtin:bundle:plugin-dev missing from BuiltinCards registry")
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:plugin-dev" {
			if asset.Type != "bundle" {
				t.Errorf("plugin-dev asset Type = %q, want bundle", asset.Type)
			}
			return
		}
	}
	t.Fatal("builtin:bundle:plugin-dev not found in builtin assets")
}

// TestCallableIDsForBundles_BrowserUseBundle is the acceptance check for the
// browser-use bundle: mounting builtin:bundle:browser-use must expose
// browsermanager.use (structured-DOM automation) and open_global_browser
// (open/navigate the shared global browser tab).
func TestCallableIDsForBundles_BrowserUseBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:browser-use"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:browser-use resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["browsermanager.use"] {
		t.Errorf("browser-use bundle missing callable browsermanager.use (got %v)", ids)
	}
	if !got["open_global_browser"] {
		t.Errorf("browser-use bundle missing callable open_global_browser (got %v)", ids)
	}
	if len(ids) != 2 {
		t.Errorf("browser-use bundle resolves %v, want exactly [browsermanager.use, open_global_browser]", ids)
	}
}

// TestBrowserUseBundleRegistered verifies the bundle card is present in the
// BuiltinCards registry so it is seeded for all agents and discoverable.
func TestBrowserUseBundleRegistered(t *testing.T) {
	found := false
	for _, card := range BuiltinCards {
		if card.Title == "builtin:bundle:browser-use" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("builtin:bundle:browser-use missing from BuiltinCards registry")
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:browser-use" {
			if asset.Type != "bundle" {
				t.Errorf("browser-use asset Type = %q, want bundle", asset.Type)
			}
			return
		}
	}
	t.Fatal("builtin:bundle:browser-use not found in builtin assets")
}

// TestCallableIDsForBundles_BrowserCrawlBundle is the acceptance check for the
// browser-crawl bundle: mounting builtin:bundle:browser-crawl must expose the
// five crawl callables (the full public surface the crawl actor registers).
func TestCallableIDsForBundles_BrowserCrawlBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:browser-crawl"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:browser-crawl resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	want := []string{
		"crawl.start",
		"crawl.status",
		"crawl.results",
		"crawl.cancel",
		"crawl.handoff",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("browser-crawl bundle missing callable %q (got %v)", w, ids)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("browser-crawl bundle resolves %v, want exactly %v", ids, want)
	}
}

// TestBrowserCrawlBundleRegistered verifies the bundle card is present in the
// BuiltinCards registry so it is seeded for all agents and discoverable.
func TestBrowserCrawlBundleRegistered(t *testing.T) {
	found := false
	for _, card := range BuiltinCards {
		if card.Title == "builtin:bundle:browser-crawl" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("builtin:bundle:browser-crawl missing from BuiltinCards registry")
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:browser-crawl" {
			if asset.Type != "bundle" {
				t.Errorf("browser-crawl asset Type = %q, want bundle", asset.Type)
			}
			return
		}
	}
	t.Fatal("builtin:bundle:browser-crawl not found in builtin assets")
}

// TestCallableIDsForBundles_GoalBundleHasNoGoalTools verifies that the
// guidance bundle is not itself a capability grant.
func TestCallableIDsForBundles_GoalBundleHasNoGoalTools(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:goal"})
	for _, id := range ids {
		if id == "goal_submit" || id == "goal_card_submit" || id == "turn_assess" {
			t.Errorf("goal bundle must not expose %q; Goal Mode owns the capability", id)
		}
	}
}

// TestCallableIDsForBundles_GoalModeCard verifies that the goal mode card
// contributes goal_submit, goal_card_submit, and turn_assess. The goal bundle's
// required dependency is mounted automatically when the mode is activated, so
// both sources contribute the same tools. The mode card is the entry point for
// the /goal lifecycle.
func TestCallableIDsForBundles_GoalModeCard(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:mode:goal"})
	if len(ids) == 0 {
		t.Fatal("builtin:mode:goal resolved to no callables; mode card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	for _, want := range []string{
		"turn_assess",
		"goal_submit",
		"goal_card_submit",
	} {
		if !got[want] {
			t.Errorf("goal mode card missing callable %q (got %v)", want, ids)
		}
	}
}

// TestGoalTools_AvailableOnlyInGoalMode verifies that goal tools are NOT
// exposed by default bundles. The coder kind no longer mounts builtin:bundle:goal,
// so a coder without goal mode active will not see goal_submit or goal_card_submit
// in the tool schema.
func TestGoalTools_AvailableOnlyInGoalMode(t *testing.T) {
	// Simulate a coder with only non-goal default bundles mounted.
	coderNonGoalBundles := []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:shell-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:planning",
	}
	ids := CallableIDsForBundles(coderNonGoalBundles)
	for _, id := range ids {
		if id == "goal_submit" || id == "goal_card_submit" {
			t.Errorf("goal tools must not appear in non-goal bundles (got %v)", ids)
		}
	}
}

// TestGoalTools_AvailableInGoalMode verifies that goal tools ARE exposed when
// builtin:mode:goal is mounted (the mode's required bundle is auto-mounted).
func TestGoalTools_AvailableInGoalMode(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:mode:goal"})
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["goal_submit"] {
		t.Errorf("goal_submit must be available in goal mode (got %v)", ids)
	}
	if !got["goal_card_submit"] {
		t.Errorf("goal_card_submit must be available in goal mode (got %v)", ids)
	}
	if !got["turn_assess"] {
		t.Errorf("turn_assess must be available in goal mode (got %v)", ids)
	}
}

// TestCallableIDsForBundles_SshToolsBundle is the acceptance check for the
// ssh-tools bundle: mounting builtin:bundle:ssh-tools must expose exactly the
// SSH callables — host discovery, read-only host status (single + fleet),
// interactive session management (list, open), interactive session command
// execution, and composite file transfer (download, upload).
// sshmanager.exec is intentionally excluded so every remote command runs
// through a visible interactive session.
func TestCallableIDsForBundles_SshToolsBundle(t *testing.T) {
	ids := CallableIDsForBundles([]string{"builtin:bundle:ssh-tools"})
	if len(ids) == 0 {
		t.Fatal("builtin:bundle:ssh-tools resolved to no callables; bundle card missing data.tools?")
	}
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	want := []string{"sshmanager.host_list", "sshmanager.status_get", "sshmanager.status_list", "sshmanager.session_list", "sshmanager.shell_open", "sshmanager.shell_run", "sshmanager.download", "sshmanager.upload"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("ssh-tools bundle missing callable %q (got %v)", w, ids)
		}
	}
	if len(ids) != len(want) {
		t.Errorf("ssh-tools bundle resolves %v, want exactly %v", ids, want)
	}
	if got["sshmanager.exec"] {
		t.Errorf("ssh-tools bundle must NOT expose sshmanager.exec (got %v)", ids)
	}
}

// TestSshToolsBundleRegistered verifies the bundle card is present in the
// BuiltinCards registry and loads as a bundle-typed asset, so it is seeded for
// all agents and discoverable.
func TestSshToolsBundleRegistered(t *testing.T) {
	found := false
	for _, card := range BuiltinCards {
		if card.Title == "builtin:bundle:ssh-tools" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("builtin:bundle:ssh-tools missing from BuiltinCards registry")
	}
	assets, err := LoadBuiltinAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		if asset.Title == "builtin:bundle:ssh-tools" {
			if asset.Type != "bundle" {
				t.Errorf("ssh-tools asset Type = %q, want bundle", asset.Type)
			}
			return
		}
	}
	t.Fatal("builtin:bundle:ssh-tools not found in builtin assets")
}
