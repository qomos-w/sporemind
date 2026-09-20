/**
 * Onboarding guide steps — multi-category tutorial builders.
 *
 * After the global coordinator is created, OnboardingFlow calls
 *   guideManager.showGuide(buildBasicsTutorial(t, ctx), true)
 * to walk the user through the basics. Additional tutorials (workflow, tools,
 * settings) are replayable from Settings → About via the tutorial registry.
 *
 * Each step targets a `data-guide-id` anchor (see guide-ids.ts) and pulls its
 * copy from i18n keys under `onboarding.guide.*`.
 *
 * Steps 1 (project) and 2 (coordinator) are handled by OnboardingFlow's
 * modals themselves; this module owns only the post-creation tours.
 */
import type { I18nKey } from '../i18n'
import type { GuideStep } from '../gen-types/interfacemanager'
import { GuideIds } from '../ui/ai/guide-ids'

/**
 * Translation function compatible with `useI18n().t`. Defined here so the
 * guide builder has a stable, importable signature without depending on React.
 */
export type TFunction = (key: I18nKey, params?: Record<string, string | number>) => string

/** Context passed to tutorial builders to enable conditional steps. */
export interface TutorialContext {
  hasProjects: boolean
  hasAgents: boolean
  /** True when at least one installed app surfaces a launcher tile. */
  hasApps: boolean
}

// ── Basics Tutorial ────────────────────────────────────────────────────────

/**
 * The auto-triggered first-run tutorial. Steps are ordered to walk the user
 * through the app before they enter a conversation:
 *
 * 1. Sidebar toggle  — show/hide the left sidebar.
 * 2. Permission mode — click the mode selector.
 * 3. View modes      — switch the main-area content mode.
 * 4. Sidebar nav     — click "New Chat" to learn the sidebar.
 * 5. App panel       — only when the launcher has apps; click the
 *                      build/apps mode switch, then open an app tile.
 * 6. Create project  — only when no projects exist.
 * 7. Create agent    — only when projects exist but no agents.
 * 8. First conversation — send any message.
 * 9. Open settings   — gated: click the settings entry.
 * 10. Finish          — inside the settings panel, replay pointer.
 *
 * The app-panel steps run here (before the first conversation) because
 * launching an app opens a right-panel tab — the main-area composer stays
 * reachable, so the later conversation step is unaffected.
 */
export function buildBasicsTutorial(t: TFunction, ctx: TutorialContext): GuideStep[] {
  const steps: GuideStep[] = [
    // B0 — Sidebar Toggle
    {
      TargetGuideId: GuideIds.topbar_sidebar_toggle,
      Title: t('onboarding.guide.sidebarToggle.title'),
      Body: t('onboarding.guide.sidebarToggle.body'),
      Placement: 'bottom',
      CapabilityKind: 'navigation',
    },

    // B1 — Permission Mode
    {
      TargetGuideId: GuideIds.composer_permission_mode,
      Title: t('onboarding.guide.permissionMode.title'),
      Body: t('onboarding.guide.permissionMode.body'),
      Placement: 'auto',
      ExpectedInteraction: `click:${GuideIds.composer_permission_mode}`,
      CapabilityKind: 'permission',
    },

    // B1.5 — View Modes
    {
      TargetGuideId: GuideIds.mode_cluster,
      Title: t('onboarding.guide.viewModes.title'),
      Body: t('onboarding.guide.viewModes.body'),
      Placement: 'bottom',
      CapabilityKind: 'navigation',
    },

    // B2 — Sidebar Navigation
    {
      TargetGuideId: GuideIds.sidebar_new_chat,
      Title: t('onboarding.guide.sidebar.title'),
      Body: t('onboarding.guide.sidebar.body'),
      Placement: 'right',
      ExpectedInteraction: `click:${GuideIds.sidebar_new_chat}`,
      CapabilityKind: 'navigation',
    },
  ]

  // B2.5 — App Panel (only when the launcher has tiles to show). Gated on the
  // mode switch: clicking it flips the sidebar into the Apps panel, which
  // reveals the app grid targeted by the next step.
  if (ctx.hasApps) {
    steps.push(
      {
        TargetGuideId: GuideIds.launcher_mode_switch,
        Title: t('onboarding.guide.appPanel.title'),
        Body: t('onboarding.guide.appPanel.body'),
        Placement: 'bottom',
        ExpectedInteraction: `click:${GuideIds.launcher_mode_switch}`,
        CapabilityKind: 'navigation',
      },
      // B2.6 — Open App: the app runs in a right-panel tab, so the main-area
      // composer stays reachable for the later first-conversation step.
      {
        TargetGuideId: GuideIds.launcher_app_grid,
        Title: t('onboarding.guide.openApp.title'),
        Body: t('onboarding.guide.openApp.body'),
        Placement: 'right',
        ExpectedInteraction: `click:${GuideIds.launcher_app_grid}`,
        CapabilityKind: 'navigation',
      },
    )
  }

  // B3 — Create Project (only when no projects exist)
  if (!ctx.hasProjects) {
    steps.push({
      TargetGuideId: GuideIds.quick_action_create_project,
      Title: t('onboarding.guide.createProject.title'),
      Body: t('onboarding.guide.createProject.body'),
      Placement: 'auto',
      ExpectedInteraction: `click:${GuideIds.quick_action_create_project}`,
      CapabilityKind: 'project',
    })
  }

  // B4 — Create Agent (only when projects exist but no agents)
  if (ctx.hasProjects && !ctx.hasAgents) {
    steps.push({
      TargetGuideId: GuideIds.sidebar_new_agent,
      Title: t('onboarding.guide.createAgent.title'),
      Body: t('onboarding.guide.createAgent.body'),
      Placement: 'right',
      ExpectedInteraction: `click:${GuideIds.sidebar_new_agent}`,
      CapabilityKind: 'agent',
    })
  }

  // B5 — First Conversation
  steps.push({
    TargetGuideId: GuideIds.composer_input,
    Title: t('onboarding.guide.firstConversation.title'),
    Body: t('onboarding.guide.firstConversation.body'),
    Placement: 'auto',
    ExpectedInteraction: 'submit',
    CapabilityKind: 'conversation',
  })

  // B6 — Open Settings (gated: actually open the panel)
  steps.push({
    TargetGuideId: GuideIds.topbar_settings,
    Title: t('onboarding.guide.openSettings.title'),
    Body: t('onboarding.guide.openSettings.body'),
    Placement: 'right',
    ExpectedInteraction: `click:${GuideIds.topbar_settings}`,
    CapabilityKind: 'settings',
  })

  // B7 — Finish (inside the now-open settings panel)
  steps.push({
    TargetGuideId: GuideIds.settings_sidebar,
    Title: t('onboarding.guide.finish.title'),
    Body: t('onboarding.guide.finish.body'),
    Placement: 'right',
    CapabilityKind: 'tools',
  })

  return steps
}

// ── Workflow Tutorial ──────────────────────────────────────────────────────

/**
 * Tutorial for the /workflow core capability. Requires at least one project.
 *
 * Step order matters by anchor visibility, not narrative convenience:
 * the composer exists in a conversation under ANY view, but on the
 * coordinator home it only exists inside the conversation pane — so the
 * typing step must run BEFORE the view switch hides that pane. Sending
 * /workflow does NOT switch views, so an explicit view-switch step
 * (mode.cluster gated on the menu item) must precede the canvas steps.
 *
 * 1. Type /workflow in the composer (gate: text).
 * 2. Switch to the workflow view (gate: click the menu item).
 * 3. Workflow canvas (gate: submit — the graph exists once the command ran).
 * 4. Core loop — intent → decompose → execute → review → summarize.
 */
export function buildWorkflowTutorial(t: TFunction, _ctx?: TutorialContext): GuideStep[] {
  return [
    {
      TargetGuideId: GuideIds.composer_input,
      Title: t('onboarding.guide.workflow.title'),
      Body: t('onboarding.guide.workflow.body'),
      Placement: 'auto',
      ExpectedInteraction: `text:/workflow`,
      CapabilityKind: 'workflow',
    },
    {
      TargetGuideId: GuideIds.mode_cluster,
      Title: t('onboarding.guide.workflow.view.title'),
      Body: t('onboarding.guide.workflow.view.body'),
      Placement: 'bottom',
      ExpectedInteraction: `click:${GuideIds.mode_workflow}`,
      CapabilityKind: 'workflow',
    },
    {
      TargetGuideId: GuideIds.workflow_canvas,
      Title: t('onboarding.guide.workflow.canvas.title'),
      Body: t('onboarding.guide.workflow.canvas.body'),
      Placement: 'auto',
      ExpectedInteraction: 'submit',
      CapabilityKind: 'workflow',
    },
    {
      TargetGuideId: GuideIds.workflow_canvas,
      Title: t('onboarding.guide.workflow.summary.title'),
      Body: t('onboarding.guide.workflow.summary.body'),
      Placement: 'auto',
      CapabilityKind: 'workflow',
    },
  ]
}

// ── Tools Tutorial ─────────────────────────────────────────────────────────

/**
 * Tutorial for composer prefixes, modes and bundles — introduction only.
 *
 * Every step is ungated (no ExpectedInteraction): this tour explains what
 * / ! @ # $ and the mode/bundle system do; the user reads and hits Next.
 * Gating would force typing during an overview, which is exactly what we
 * do not want. Teaches by description, not by drill.
 *
 * 1. Overview of the five prefixes.
 * 2. `/` — commands, skills and mode entries.
 * 3. `!` — quick note to Backlog.
 * 4. `@` — mention agents and files.
 * 5. `#` — reference project cards.
 * 6. `$` — reference files by path.
 * 7. Modes — /workflow /goal /worktree /memory /scheduler.
 * 8. Bundles — mountable capability packs.
 * 9. Omnibox wrap-up.
 */
export function buildToolsTutorial(t: TFunction): GuideStep[] {
  const composer = (key: 'slash' | 'bang' | 'at' | 'hash' | 'dollar' | 'modes' | 'bundles'): GuideStep => ({
    TargetGuideId: GuideIds.composer_input,
    Title: t(`onboarding.guide.tools.${key}.title`),
    Body: t(`onboarding.guide.tools.${key}.body`),
    Placement: 'auto',
    CapabilityKind: 'tools',
  })
  return [
    {
      TargetGuideId: GuideIds.composer_input,
      Title: t('onboarding.guide.tools.title'),
      Body: t('onboarding.guide.tools.body'),
      Placement: 'auto',
      CapabilityKind: 'tools',
    },
    composer('slash'),
    composer('bang'),
    composer('at'),
    composer('hash'),
    composer('dollar'),
    composer('modes'),
    composer('bundles'),
    {
      TargetGuideId: GuideIds.topbar_omnibox_trigger,
      Title: t('onboarding.guide.tools.summary.title'),
      Body: t('onboarding.guide.tools.summary.body'),
      Placement: 'bottom',
      CapabilityKind: 'tools',
    },
  ]
}

// ── Settings Tutorial ──────────────────────────────────────────────────────

/**
 * Tutorial for the settings panel — walks through each major category.
 *
 * 1. Settings entry — open settings.
 * 2. Provider settings — configure model providers.
 * 3. Prompt settings — manage prompts.
 * 4. Skill settings — manage skills.
 * 5. Keymap settings — customize shortcuts.
 * 6. About — tutorials replay & info.
 */
export function buildSettingsTutorial(t: TFunction): GuideStep[] {
  return [
    {
      TargetGuideId: GuideIds.topbar_settings,
      Title: t('onboarding.guide.settings.entry.title'),
      Body: t('onboarding.guide.settings.entry.body'),
      Placement: 'bottom',
      ExpectedInteraction: `click:${GuideIds.topbar_settings}`,
      CapabilityKind: 'settings',
    },
    {
      TargetGuideId: GuideIds.settings_category_provider,
      Title: t('onboarding.guide.settings.provider.title'),
      Body: t('onboarding.guide.settings.provider.body'),
      Placement: 'right',
      CapabilityKind: 'settings',
    },
    {
      TargetGuideId: GuideIds.settings_category_prompt,
      Title: t('onboarding.guide.settings.prompt.title'),
      Body: t('onboarding.guide.settings.prompt.body'),
      Placement: 'right',
      CapabilityKind: 'settings',
    },
    {
      TargetGuideId: GuideIds.settings_category_skill,
      Title: t('onboarding.guide.settings.skill.title'),
      Body: t('onboarding.guide.settings.skill.body'),
      Placement: 'right',
      CapabilityKind: 'settings',
    },
    {
      TargetGuideId: GuideIds.settings_category_developer,
      Title: t('onboarding.guide.settings.developer.title'),
      Body: t('onboarding.guide.settings.developer.body'),
      Placement: 'right',
      CapabilityKind: 'settings',
    },
    {
      TargetGuideId: GuideIds.topbar_settings,
      Title: t('onboarding.guide.settings.about.title'),
      Body: t('onboarding.guide.settings.about.body'),
      Placement: 'bottom',
      CapabilityKind: 'settings',
    },
  ]
}

// ── Backward compatibility ─────────────────────────────────────────────────

/**
 * @deprecated Use `buildBasicsTutorial(t, ctx)` via the tutorial registry.
 * Kept for backward compatibility with existing callers/tests.
 */
export function buildOnboardingSteps(t: TFunction, opts?: { hasProjects?: boolean }): GuideStep[] {
  return buildBasicsTutorial(t, {
    hasProjects: opts?.hasProjects ?? true,
    hasAgents: true,
    hasApps: true,
  })
}