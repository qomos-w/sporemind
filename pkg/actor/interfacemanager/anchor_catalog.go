package interfacemanager

import "github.com/qomos-w/sporemind/pkg/domain"

// GuideAnchorCatalog returns the full catalog of addressable UI anchors the
// agent may target when composing show_guide steps. It mirrors
// web/src/ui/ai/guide-ids.ts (kept in sync by the drift test in
// anchor_catalog_test.go) and is the whitelist source for show_guide
// validation: a step whose TargetGuideId is not in this catalog is rejected
// with a hint to call the anchor_catalog action.
func GuideAnchorCatalog() []domain.GuideAnchor {
	return []domain.GuideAnchor{
		{
			GuideID:        "topbar.settings",
			Area:           "topbar",
			Summary:        "Topbar settings gear; opens the settings panel.",
			VisibleWhen:    "always",
			SuggestedGates: "click:topbar.settings",
		},
		{
			GuideID:        "topbar.omnibox-trigger",
			Area:           "topbar",
			Summary:        "Topbar omnibox command trigger; opens the command palette.",
			VisibleWhen:    "always",
			SuggestedGates: "click:topbar.omnibox-trigger",
		},
		{
			GuideID:        "topbar.sidebar-toggle",
			Area:           "topbar",
			Summary:        "Topbar button toggling the left sidebar visibility.",
			VisibleWhen:    "always",
			SuggestedGates: "click:topbar.sidebar-toggle",
		},
		{
			GuideID:        "sidebar.new-chat",
			Area:           "sidebar",
			Summary:        "Sidebar button starting a new chat in the active project.",
			VisibleWhen:    "only when the sidebar is expanded and a project is open",
			SuggestedGates: "click:sidebar.new-chat",
		},
		{
			GuideID:        "sidebar.new-agent",
			Area:           "sidebar",
			Summary:        "Sidebar button creating a new agent.",
			VisibleWhen:    "only when the sidebar is expanded",
			SuggestedGates: "click:sidebar.new-agent",
		},
		{
			GuideID:        "sidebar.agent-list",
			Area:           "sidebar",
			Summary:        "Sidebar list of existing agents; click an entry to focus it.",
			VisibleWhen:    "only when the sidebar is expanded",
			SuggestedGates: "click:sidebar.agent-list",
		},
		{
			GuideID:        "composer.input",
			Area:           "composer",
			Summary:        "Composer text input where the user types a message.",
			VisibleWhen:    "always",
			SuggestedGates: "text:<message substring>,submit",
		},
		{
			GuideID:        "composer.send",
			Area:           "composer",
			Summary:        "Composer send button; submits the current message.",
			VisibleWhen:    "always",
			SuggestedGates: "click:composer.send,submit",
		},
		{
			GuideID:        "composer.permission-mode",
			Area:           "composer",
			Summary:        "Composer permission-mode selector cycling permission policies.",
			VisibleWhen:    "always",
			SuggestedGates: "click:composer.permission-mode",
		},
		{
			GuideID:        "settings.sidebar",
			Area:           "settings",
			Summary:        "Settings panel left sidebar listing the setting categories.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.sidebar",
		},
		{
			GuideID:        "settings.category.general",
			Area:           "settings",
			Summary:        "Settings category: general preferences.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.category.general",
		},
		{
			GuideID:        "settings.category.model-providers",
			Area:           "settings",
			Summary:        "Settings category: model providers.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.category.model-providers",
		},
		{
			GuideID:        "settings.category.prompts",
			Area:           "settings",
			Summary:        "Settings category: prompts.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.category.prompts",
		},
		{
			GuideID:        "settings.category.skills",
			Area:           "settings",
			Summary:        "Settings category: skills.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.category.skills",
		},
		{
			GuideID:        "settings.category.developer",
			Area:           "settings",
			Summary:        "Settings category: developer options.",
			VisibleWhen:    "only when the settings panel is open",
			SuggestedGates: "click:settings.category.developer",
		},
		{
			GuideID:        "right-panel.tabs",
			Area:           "right-panel",
			Summary:        "Right panel tab strip switching between right-panel views.",
			VisibleWhen:    "always",
			SuggestedGates: "click:right-panel.tabs",
		},
		{
			GuideID:        "mode.cluster",
			Area:           "mode",
			Summary:        "Mode switch cluster in the topbar.",
			VisibleWhen:    "always",
			SuggestedGates: "click:mode.cluster",
		},
		{
			GuideID:        "mode.workflow",
			Area:           "mode",
			Summary:        "Workflow mode entry inside the mode dropdown.",
			VisibleWhen:    "only while the mode dropdown is expanded",
			SuggestedGates: "click:mode.workflow",
		},
		{
			GuideID:        "workflow.canvas",
			Area:           "canvas",
			Summary:        "Workflow graph canvas container.",
			VisibleWhen:    "only in the workflow view",
			SuggestedGates: "click:workflow.canvas",
		},
		{
			GuideID:        "quick-action.create-project",
			Area:           "quick-action",
			Summary:        "Quick-action card on the empty project page creating a new project.",
			VisibleWhen:    "only when no project is open",
			SuggestedGates: "click:quick-action.create-project",
		},
		{
			GuideID:        "launcher.mode-switch",
			Area:           "launcher",
			Summary:        "Launcher 构建/应用 mode switcher; parked in the desktop topbar and the mobile sidebar/launcher header.",
			VisibleWhen:    "always",
			SuggestedGates: "click:launcher.mode-switch",
		},
		{
			GuideID:        "launcher.app-grid",
			Area:           "launcher",
			Summary:        "Launcher app tile grid; clicking any tile launches the app.",
			VisibleWhen:    "only when the launcher is in app mode (应用)",
			SuggestedGates: "click:launcher.app-grid",
		},
	}
}

// guideAnchorSet returns the set of anchor ids in the catalog, for whitelist
// validation. Built from GuideAnchorCatalog so the catalog stays the single
// source of truth.
func guideAnchorSet() map[string]struct{} {
	catalog := GuideAnchorCatalog()
	set := make(map[string]struct{}, len(catalog))
	for _, a := range catalog {
		set[a.GuideID] = struct{}{}
	}
	return set
}
