package agentkit

import (
	"github.com/qomos-w/sporemind/pkg/buildinfo"
	"github.com/qomos-w/sporemind/pkg/domain"
)

// DevOnlyBundleIDs lists builtin bundles that exist only in dev builds
// (BuildType=dev): outside dev they are excluded from kind defaults, cannot
// be mounted (agent_component mount guard), and are hidden from every
// frontend picker via the card metadata flag `data.devOnly`.
var DevOnlyBundleIDs = []string{
	"builtin:bundle:coordinator-wearable",
}

// IsDevOnlyBundle reports whether a builtin bundle card is dev-build-only.
func IsDevOnlyBundle(cardID string) bool {
	for _, id := range DevOnlyBundleIDs {
		if id == cardID {
			return true
		}
	}
	return false
}

// ReadOnlyAgentKinds lists kinds whose tool surface must stay read-only.
// These are the fork-child / system kinds (explorer, scout, dreamer,
// reviewer): their turns run with DefaultBehavior "allow", so any mutating
// callable that reaches their tool surface executes without user
// confirmation. Spawn-time extra bundles (plugin-dev for dev-app project
// agents) are therefore NOT seeded on these kinds — the bundle mounts
// dev_generate / register_project / plugin_load and other mutating appmanager
// callables. Only agents whose kind is absent from this set (coder, worker,
// general, ...) receive extra bundles.
var ReadOnlyAgentKinds = map[string]struct{}{
	"explorer":               {},
	domain.AgentKindScout:    {},
	domain.AgentKindDreamer:  {},
	domain.AgentKindReviewer: {},
}

// IsReadOnlyAgentKind reports whether the kind's tool surface must stay
// read-only (see ReadOnlyAgentKinds).
func IsReadOnlyAgentKind(kind string) bool {
	_, ok := ReadOnlyAgentKinds[kind]
	return ok
}

// BaseKindConfigs returns the static agent kind configurations.
// Each config includes prompt refs, default bundle IDs (tool capability),
// and auto-allow tools, but NOT EnvironmentContext or RandomName — callers
// (workspace) add those at runtime.
//
// Tool availability is declared entirely by bundle cards: each kind lists
// the bundles it should mount by default, and the bundle cards' data.tools
// frontmatter determines which callables become available.
func BaseKindConfigs() []domain.AgentKindConfig {
	profile := func(key string) domain.PromptRef {
		return domain.PromptRef{Kind: "profile", Key: key}
	}

	coder := domain.AgentKindConfig{Kind: domain.AgentKindCoder}
	coder.RolePromptRef = profile("project.coder")
	coder.SkillIDs = []string{"plan-module"}
	coder.DefaultBundleIDs = []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:shell-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:planning",
		"builtin:bundle:fork-explore",
		"builtin:bundle:fork-review",
		"builtin:bundle:fork-general",
		"builtin:bundle:swarm",
	}
	coder.AutoAllowTools = []string{
		"project.write", "project.edit",
		"project.shell_exec",
		"project.git_add", "project.git_commit",
		"project.git_pull", "project.git_branch",
		"task_create", "task_update",
		"fork_explore", "fork_review", "fork_general",
	}

	reviewer := domain.AgentKindConfig{Kind: domain.AgentKindReviewer}
	reviewer.RolePromptRef = profile("project.reviewer")
	reviewer.DefaultBundleIDs = []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:workspace-tools",
	}

	appBuilder := domain.AgentKindConfig{Kind: domain.AgentKindAppBuilder}
	appBuilder.RolePromptRef = profile("project.app-builder")
	appBuilder.DefaultBundleIDs = []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:shell-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:worktree",
		"builtin:bundle:workspace-tools",
		"builtin:bundle:app-tools",
		"builtin:bundle:planning",
		"builtin:bundle:image-gen",
		"builtin:bundle:video-gen",
	}
	appBuilder.AutoAllowTools = []string{"project.write", "project.edit", "project.shell_exec"}

	explorer := domain.AgentKindConfig{Kind: "explorer"}
	explorer.RolePromptRef = profile("project.explorer")
	explorer.DefaultBundleIDs = []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:workspace-tools",
	}

	general := domain.AgentKindConfig{Kind: "general"}
	general.RolePromptRef = profile("project.general")
	general.DefaultBundleIDs = []string{
		"builtin:bundle:project-wiki",
		"builtin:bundle:file-tools",
		"builtin:bundle:shell-tools",
		"builtin:bundle:git-tools",
		"builtin:bundle:worktree",
		"builtin:bundle:workspace-tools",
		"builtin:bundle:planning",
		"builtin:bundle:web-search",
		"builtin:bundle:swarm",
	}
	general.AutoAllowTools = []string{
		"project.write", "project.edit",
		"project.shell_exec",
		"project.git_add", "project.git_commit",
		"project.git_pull", "project.git_branch",
	}

	// dreamer is a system-managed child used only for memory consolidation.
	// It receives its complete prompt from the parent and exposes no tools.
	dreamer := domain.AgentKindConfig{Kind: domain.AgentKindDreamer}

	coordinator := domain.AgentKindConfig{Kind: domain.AgentKindCoordinator}
	coordinator.RolePromptRef = profile("workspace.coordinator")
	// Coordinator is a workspace-global dialogue/butler agent (no project
	// context), so it mounts no project-scoped bundles (project-wiki, file-tools)
	// and no work-performing bundles (fork-explore, planning): it routes and
	// converses, it does not investigate code, plan tasks, or edit files. It
	// keeps workspace orientation, UI navigation (interface controls), shared-browser opening,
	// desktop assistance (computeruse), its signature wearable capability, and
	// workbench attention control (the board's global awareness + card
	// operations — the coordinator is the workbench controller).
	coordinator.DefaultBundleIDs = []string{
		"builtin:bundle:workspace-tools",
		"builtin:bundle:interface-controls",
		"builtin:bundle:browser-tools",
		"builtin:bundle:computeruse-tools",
		"builtin:bundle:web-search",
		"builtin:bundle:workbench-attention",
	}
	if buildinfo.IsDev() {
		coordinator.DefaultBundleIDs = append(coordinator.DefaultBundleIDs, "builtin:bundle:coordinator-wearable")
	}

	// pluginAgent is the per-(app, slot) dedicated agent provisioned at
	// registration for each plugin_agent block an app declared. Like the
	// Coordinator it is workspace-global; unlike it, one instance exists per
	// binding slot and the app-specific tools come from bound app-bundle
	// cards (mounted at bind time), so the kind default stays minimal:
	// workspace orientation only.
	pluginAgent := domain.AgentKindConfig{Kind: domain.AgentKindPlugin}
	pluginAgent.RolePromptRef = profile("workspace.plugin-agent")
	pluginAgent.DefaultBundleIDs = []string{
		"builtin:bundle:workspace-tools",
	}

	return []domain.AgentKindConfig{
		{
			Kind:               coder.Kind,
			DisplayName:        "Coder",
			UserCreatable:      true,
			RolePromptRef:      coder.RolePromptRef,
			SystemFragmentRefs: coder.SystemFragmentRefs,
			SkillIDs:           coder.SkillIDs,
			DefaultBundleIDs:   coder.DefaultBundleIDs,
			AutoAllowTools:     coder.AutoAllowTools,
		},
		{
			Kind:             reviewer.Kind,
			DisplayName:      "Reviewer",
			UserCreatable:    false,
			SystemManaged:    true,
			RolePromptRef:    reviewer.RolePromptRef,
			DefaultBundleIDs: reviewer.DefaultBundleIDs,
		},
		{
			Kind:             pluginAgent.Kind,
			DisplayName:      "Plugin",
			UserCreatable:    false,
			SystemManaged:    true,
			RolePromptRef:    pluginAgent.RolePromptRef,
			DefaultBundleIDs: pluginAgent.DefaultBundleIDs,
		},
		{
			Kind:             appBuilder.Kind,
			DisplayName:      "App Builder",
			UserCreatable:    false,
			SystemManaged:    true,
			RolePromptRef:    appBuilder.RolePromptRef,
			DefaultBundleIDs: appBuilder.DefaultBundleIDs,
			AutoAllowTools:   appBuilder.AutoAllowTools,
		},
		{
			Kind:             explorer.Kind,
			DisplayName:      "Explorer",
			UserCreatable:    false,
			SystemManaged:    true,
			RolePromptRef:    explorer.RolePromptRef,
			DefaultBundleIDs: explorer.DefaultBundleIDs,
		},
		{
			Kind:             general.Kind,
			DisplayName:      "General",
			UserCreatable:    false,
			SystemManaged:    true,
			RolePromptRef:    general.RolePromptRef,
			DefaultBundleIDs: general.DefaultBundleIDs,
			AutoAllowTools:   general.AutoAllowTools,
		},
		{
			Kind:             dreamer.Kind,
			DisplayName:      "Dreamer",
			UserCreatable:    false,
			SystemManaged:    true,
			DefaultBundleIDs: dreamer.DefaultBundleIDs,
		},
		{
			Kind:             coordinator.Kind,
			DisplayName:      "Coordinator",
			UserCreatable:    true,
			SystemManaged:    true,
			RolePromptRef:    coordinator.RolePromptRef,
			DefaultBundleIDs: coordinator.DefaultBundleIDs,
		},
		{
			Kind:          string(domain.AgentKindWorker),
			DisplayName:   "Worker",
			UserCreatable: false,
			SystemManaged: true,
			RolePromptRef: profile("project.worker"),
			DefaultBundleIDs: []string{
				"builtin:bundle:file-tools",
				"builtin:bundle:project-wiki",
				"builtin:bundle:shell-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:fork-explore",
				"builtin:bundle:web-search",
				"builtin:bundle:swarm",
			},
			MaxTurns: 200,
		},
		{
			Kind:          string(domain.AgentKindScout),
			DisplayName:   "Scout",
			UserCreatable: false,
			SystemManaged: true,
			RolePromptRef: profile("project.scout"),
			DefaultBundleIDs: []string{
				"builtin:bundle:project-wiki",
				"builtin:bundle:file-tools",
				"builtin:bundle:git-tools",
				"builtin:bundle:web-search",
				"builtin:bundle:browser-crawl",
				"builtin:bundle:fork-explore",
			},
			MaxTurns: 100,
		},
	}
}
