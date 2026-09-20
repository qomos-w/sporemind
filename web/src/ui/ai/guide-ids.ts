/**
 * Centralized guide-id constants for tour targeting.
 * Annotate key UI elements with data-guide-id={GuideIds.xxx}.
 */
export const GuideIds = {
  // Topbar
  topbar_settings: 'topbar.settings',
  topbar_omnibox_trigger: 'topbar.omnibox-trigger',
  topbar_sidebar_toggle: 'topbar.sidebar-toggle',

  // Sidebar
  sidebar_new_agent: 'sidebar.new-agent',
  sidebar_agent_list: 'sidebar.agent-list',
  sidebar_new_chat: 'sidebar.new-chat',

  // Composer
  composer_input: 'composer.input',
  composer_send: 'composer.send',
  composer_permission_mode: 'composer.permission-mode',

  // Settings
  settings_sidebar: 'settings.sidebar',
  settings_category_general: 'settings.category.general',
  settings_category_provider: 'settings.category.model-providers',
  settings_category_prompt: 'settings.category.prompts',
  settings_category_skill: 'settings.category.skills',
  settings_category_developer: 'settings.category.developer',

  // Right panel tabs
  right_panel_tabs: 'right-panel.tabs',

  // Mode switch cluster
  mode_cluster: 'mode.cluster',
  mode_workflow: 'mode.workflow',

  // Workflow canvas — annotated on the workflow-graph container in WorkflowGraph.tsx.
  workflow_canvas: 'workflow.canvas',

  // Quick actions (ProjectNewChatPage)
  quick_action_create_project: 'quick-action.create-project',

  // Launcher (app panel) — anchors are annotated by the launcher components.
  launcher_mode_switch: 'launcher.mode-switch',
  launcher_app_grid: 'launcher.app-grid',
} as const

export type GuideId = typeof GuideIds[keyof typeof GuideIds]

/**
 * Tooltip registry for DelegatedTooltip.
 *
 * Elements annotated with `data-guide-id` can automatically show a tooltip on
 * hover/focus when their guide id is mapped here. The value is the i18n key
 * resolved via `useI18n().t`. Elements can also declare `data-tooltip-key`
 * directly, which takes precedence over this registry.
 */
export const tooltipRegistry: Partial<Record<GuideId, string>> = {
  [GuideIds.composer_send]: 'tooltip.composer.send',
}
