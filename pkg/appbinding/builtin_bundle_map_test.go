package appbinding

import (
	"sort"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/agentkit"
)

// builtinBundleNotPluginReachable lists every callID that appears in a builtin
// bundle card's data.tools but is intentionally NOT plugin-reachable (no
// host-call registration in this package, no alias, no prefix family). The
// gate below fails if a bundle-card callID is neither registered nor listed
// here — so new bundle tools must either gain a RegisterHostCall entry or an
// explicit, individually-reasoned exemption (no wildcards).
//
// Values carry the reason and the source bundle card for failure triage.
var builtinBundleNotPluginReachable = map[string]string{
	// ── 管理面（app-tools / plugin-dev bundle）──
	"appmanager.list":             "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.get":              "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.invoke":           "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.component_list":   "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.component_get":    "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.register":         "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.reload":           "管理面（app-tools bundle），不经插件宿主 host call",
	"appmanager.register_project": "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.reload_project":   "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.app_export":       "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.install_local":    "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.unregister":       "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.retry_cleanup":    "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.plugin_load":      "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.plugin_unload":    "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.dev_generate":     "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.dev_gate":         "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.sdk_vendor":       "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.callable_info":    "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.dev_guide":        "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.host_protocol":    "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.icon_names":       "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.open_view":        "管理面（plugin-dev bundle），不经插件宿主 host call",
	"appmanager.panel_topology":   "管理面（plugin-dev bundle），不经插件宿主 host call",
	"pluginhost.list_plugins":     "管理面（plugin-dev bundle），不经插件宿主 host call",
	"pluginhost.plugin_logs":      "管理面（plugin-dev bundle），不经插件宿主 host call",
	"pluginhost.plugin_dom":       "管理面（plugin-dev bundle），不经插件宿主 host call",

	// ── agent 编排面（workspace-tools / workflow-tools / bundle-use / fork-* / planning bundle）──
	"workspace.list_agents":        "agent 编排面（workspace-tools bundle），不经插件宿主 host call",
	"workspace.create":             "agent 编排面（interface-controls bundle），不经插件宿主 host call",
	"workspace.create_agent":       "agent 编排面（interface-controls bundle），不经插件宿主 host call",
	"workspace.agent_spawn_assign": "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.agent_review":       "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.agent_terminate":    "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.agent_send_message": "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.agent_read_message": "agent 编排面（workflow-tools bundle）；SDK 侧经 agent.read_messages 别名（callID 名不同，按名判定豁免）",
	"workspace.agent_pause":        "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.agent_resume":       "agent 编排面（workflow-tools bundle），不经插件宿主 host call",
	"workspace.list_project":       "agent 编排面（workspace-tools bundle）；SDK 侧经 workspace.list_projects 别名（callID 名不同，按名判定豁免）",
	"workspace.account":            "agent 编排面（workspace-tools bundle），不经插件宿主 host call",
	"crawl.cancel":                 "宿主侧 AdminOnly 取消操作（browser-crawl bundle），不对插件开放",
	"component_list":               "组件编排面（bundle-use bundle），不经插件宿主 host call",
	"component_snapshot":           "组件编排面（bundle-use bundle），不经插件宿主 host call",
	"component_mount":              "组件编排面（bundle-use bundle），不经插件宿主 host call",
	"component_unmount":            "组件编排面（bundle-use bundle），不经插件宿主 host call",
	"component_set_enabled":        "组件编排面（bundle-use bundle），不经插件宿主 host call",
	"project.component_list":       "组件编排面（bundle-use bundle）；SDK 无同名 host call",
	"project.component_get":        "组件编排面（bundle-use bundle）；SDK 无同名 host call",
	"fork_agent":                   "agent 编排面（fork-* bundle），不经插件宿主 host call",
	"plan_submit":                  "agent 编排面（planning bundle），不经插件宿主 host call",
	"task_create":                  "agent 编排面（planning bundle），不经插件宿主 host call",
	"task_update":                  "agent 编排面（planning bundle），不经插件宿主 host call",

	// ── file-tools / git-tools / shell-tools / worktree bundle：SDK 侧同名 host call 名字不同或未开放 ──
	"project.read":                    "file-tools bundle；SDK 侧为 project.read_file 等别名（callID 名不同，按名判定豁免）",
	"project.write":                   "file-tools bundle；SDK 侧为 project.write_file 等别名（callID 名不同，按名判定豁免）",
	"project.edit":                    "file-tools bundle；SDK 无同名 host call（SDK 侧整文件读写）",
	"project.rm":                      "file-tools bundle；SDK 无同名 host call（SDK 侧整文件读写）",
	"project.list":                    "file-tools bundle；SDK 无同名 host call",
	"project.glob":                    "file-tools bundle；SDK 无同名 host call",
	"project.grep":                    "file-tools bundle；SDK 无同名 host call",
	"project.git_status":              "git-tools bundle；SDK 无同名 host call",
	"project.git_log":                 "git-tools bundle；SDK 无同名 host call",
	"project.git_diff":                "git-tools bundle；SDK 无同名 host call",
	"project.git_add":                 "git-tools bundle；SDK 无同名 host call",
	"project.git_commit":              "git-tools bundle；SDK 无同名 host call",
	"project.git_push":                "git-tools bundle；SDK 无同名 host call",
	"project.git_pull":                "git-tools bundle；SDK 无同名 host call",
	"project.git_branch":              "git-tools bundle；SDK 无同名 host call",
	"project.git_checkout":            "git-tools bundle；SDK 无同名 host call",
	"project.shell_exec":              "shell-tools bundle；SDK 侧为 shell.exec 前缀族（callID 名不同，按名判定豁免）",
	"project.worktree_enter":          "worktree bundle；SDK 无同名 host call",
	"project.worktree_list":           "worktree bundle；SDK 无同名 host call",
	"project.worktree_get":            "worktree bundle；SDK 无同名 host call",
	"project.worktree_agent_bindings": "worktree bundle；SDK 无同名 host call",
	"project.review_changeset":        "workflow-tools bundle；SDK 无同名 host call",
	"project.review_file_content":     "workflow-tools bundle；SDK 无同名 host call",

	// ── project-wiki 写面（project-wiki / workflow-tools bundle）：SDK 只开放读面 ──
	"project.wiki_create_card":           "wiki 写面（project-wiki bundle），SDK 未开放",
	"project.wiki_edit_card":             "wiki 写面（project-wiki bundle），SDK 未开放",
	"project.wiki_delete_card":           "wiki 写面（project-wiki bundle），SDK 未开放",
	"project.wiki_validate_card":         "wiki 写面（project-wiki bundle），SDK 未开放",
	"project.wiki_create_map":            "wiki 写面（workflow-tools bundle），SDK 未开放",
	"project.wiki_create_task_card":      "wiki 写面（workflow-tools bundle），SDK 未开放",
	"project.wiki_set_task_dependencies": "wiki 写面（workflow-tools bundle），SDK 未开放",
	"project.wiki_frontier":              "wiki 写面（workflow-tools bundle），SDK 未开放",
	"project.wiki_set_status":            "wiki 写面（workflow-tools bundle），SDK 未开放",
	"project.wiki_set_map_owner":         "wiki 写面（workflow-tools bundle），SDK 未开放",

	// ── 调度面（scheduler bundle）：SDK 未开放，不经插件宿主 host call ──
	"project.wiki_list_timers":        "调度面（scheduler bundle），SDK 未开放",
	"project.wiki_toggle_timer":       "调度面（scheduler bundle），SDK 未开放",
	"project.wiki_trigger_timer_card": "调度面（scheduler bundle），SDK 未开放",

	// ── 桌面/浏览器/生成面：不经插件宿主 host call ──
	"computeruse.screenshot":              "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.capabilities":            "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.list_windows":            "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.list_displays":           "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.list_processes":          "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.get_window_info":         "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.cursor_position":         "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.list_elements":           "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.ocr":                     "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.setup_ocr":               "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.interact":                "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.stream":                  "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.clipboard_get":           "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.clipboard_set":           "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.clipboard_get_rich":      "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"computeruse.clipboard_set_rich":      "桌面控制面（computeruse-tools bundle），不经插件宿主 host call",
	"interfacemanager.control":            "界面控制面（interface-controls bundle），不经插件宿主 host call",
	"interfacemanager.query_interactions": "界面控制面（interface-controls bundle），不经插件宿主 host call",
	"workbench.attention_report":          "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.snapshot":                  "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.promote":                   "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.set_pinned":                "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.set_hidden":                "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.upsert_card":               "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.set_frozen":                "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"workbench.set_maximized":             "工作台注意力面（workbench-attention bundle），不经插件宿主 host call",
	"browsermanager.use":                  "浏览器面（browser-use bundle），不经插件宿主 host call",
	"open_global_browser":                 "浏览器面（browser-tools bundle），不经插件宿主 host call",
	"mcp.add_server":                      "MCP 管理面（bundle-use bundle），不经插件宿主 host call",
	"mcp.list_servers":                    "MCP 管理面（bundle-use bundle），不经插件宿主 host call",
	"coordinator_wearable_call":           "穿戴协调面（coordinator-wearable bundle），不经插件宿主 host call",
	"coordinator_wearable_notify":         "穿戴协调面（coordinator-wearable bundle），不经插件宿主 host call",
	"image_generate":                      "媒体生成（image-gen bundle）；SDK 侧为 image.generate（callID 名不同，按名判定豁免）",
	"image_recognize":                     "媒体识别（image-recognition bundle），不经插件宿主 host call",
	"video_generate":                      "媒体生成（video-gen bundle）；SDK 侧为 video.generate（callID 名不同，按名判定豁免）",
}

// TestBuiltinBundleToolsPluginReachable is the bundle-map gate: every callID
// declared by a builtin bundle card (pkg/agentkit/builtin/cards/bundle/**/*.md,
// including tool-usage/) must be plugin-reachable — HostCallCapability resolves
// it to a capability via exact registration, alias, or prefix family — or be
// individually listed in builtinBundleNotPluginReachable with a reason.
//
// HostCallCapability (not the raw registeredHostCalls keys) is the source of
// truth: it covers exact registrations, HostCallAliases, and prefix families
// such as sshmanager.* → ssh.invoke.
func TestBuiltinBundleToolsPluginReachable(t *testing.T) {
	assets, err := agentkit.LoadCardAssets()
	if err != nil {
		t.Fatalf("load card assets: %v", err)
	}

	// callID → bundle card IDs declaring it.
	declared := map[string][]string{}
	bundleCards := 0
	for _, a := range assets {
		if !strings.HasPrefix(a.Path, "builtin/cards/bundle/") {
			continue
		}
		bundleCards++
		for _, tool := range a.Tools {
			declared[tool] = append(declared[tool], a.Title)
		}
	}
	if bundleCards == 0 {
		t.Fatal("no builtin bundle cards found under builtin/cards/bundle/")
	}
	if len(declared) < 100 {
		t.Fatalf("only %d unique tool callIDs across %d bundle cards — walk looks wrong", len(declared), bundleCards)
	}

	var missing []string
	for callID := range declared {
		if HostCallCapability(callID) != "" {
			continue
		}
		if _, exempt := builtinBundleNotPluginReachable[callID]; exempt {
			continue
		}
		missing = append(missing, callID)
	}
	sort.Strings(missing)
	for _, callID := range missing {
		t.Errorf("bundle tool %q (declared by %v) is neither plugin-reachable nor exempted; "+
			"add a RegisterHostCall entry or an explicit exemption", callID, declared[callID])
	}

	// Drift guard the other way: an exemption entry that no bundle card
	// declares anymore is stale and must be pruned.
	var stale []string
	for callID := range builtinBundleNotPluginReachable {
		if _, ok := declared[callID]; !ok {
			stale = append(stale, callID)
		}
	}
	sort.Strings(stale)
	for _, callID := range stale {
		t.Errorf("exemption for %q is stale: no builtin bundle card declares it", callID)
	}

	var reachable []string
	for callID := range declared {
		if HostCallCapability(callID) != "" {
			reachable = append(reachable, callID)
		}
	}
	sort.Strings(reachable)
	t.Logf("%d bundle cards, %d unique tool callIDs: %d plugin-reachable, %d exempted",
		bundleCards, len(declared), len(reachable), len(declared)-len(reachable))
}
