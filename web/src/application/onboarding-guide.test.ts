import { describe, it, expect } from 'vitest'
import { buildBasicsTutorial, buildWorkflowTutorial, buildToolsTutorial, buildSettingsTutorial, buildOnboardingSteps, type TFunction } from './onboarding-guide'
import { GuideIds } from '../ui/ai/guide-ids'

/**
 * A TFunction stub that returns the raw i18n key, so the test can assert
 * exactly which keys the tour uses without depending on any locale catalog.
 */
const tEcho: TFunction = (key) => key

describe('buildBasicsTutorial', () => {
  describe('with no projects and no agents', () => {
    const steps = buildBasicsTutorial(tEcho, { hasProjects: false, hasAgents: false, hasApps: false })

    it('produces a 8-step tour: sidebarToggle → permission → viewModes → sidebar → createProject → conversation → openSettings → finish', () => {
      expect(steps.length).toBe(8)
    })

    it('every step has a non-empty title, body and target anchor', () => {
      for (const step of steps) {
        expect(step.Title).toBeTruthy()
        expect(step.Body).toBeTruthy()
        expect(step.TargetGuideId).toBeTruthy()
      }
    })

    it('every target anchor is a registered GuideId', () => {
      const validAnchors = new Set<string>(Object.values(GuideIds))
      for (const step of steps) {
        expect(validAnchors.has(step.TargetGuideId)).toBe(true)
      }
    })

    it('every step declares an explicit placement', () => {
      for (const step of steps) {
        expect(step.Placement).toMatch(/^(top|bottom|left|right|auto)$/)
      }
    })

    it('orders: sidebarToggle → permission → viewModes → sidebar → createProject → conversation → openSettings → finish', () => {
      expect(steps[0]!.TargetGuideId).toBe(GuideIds.topbar_sidebar_toggle)
      expect(steps[1]!.TargetGuideId).toBe(GuideIds.composer_permission_mode)
      expect(steps[2]!.TargetGuideId).toBe(GuideIds.mode_cluster)
      expect(steps[3]!.TargetGuideId).toBe(GuideIds.sidebar_new_chat)
      expect(steps[4]!.TargetGuideId).toBe(GuideIds.quick_action_create_project)
      expect(steps[5]!.TargetGuideId).toBe(GuideIds.composer_input)
      expect(steps[6]!.TargetGuideId).toBe(GuideIds.topbar_settings)
      expect(steps[7]!.TargetGuideId).toBe(GuideIds.settings_sidebar)
    })

    it('uses action gates on all steps except sidebarToggle, viewModes and finish', () => {
      expect(steps[0]!.ExpectedInteraction).toBeUndefined()
      expect(steps[1]!.ExpectedInteraction).toBe(`click:${GuideIds.composer_permission_mode}`)
      expect(steps[2]!.ExpectedInteraction).toBeUndefined()
      expect(steps[3]!.ExpectedInteraction).toBe(`click:${GuideIds.sidebar_new_chat}`)
      expect(steps[4]!.ExpectedInteraction).toBe(`click:${GuideIds.quick_action_create_project}`)
      expect(steps[5]!.ExpectedInteraction).toBe('submit')
      expect(steps[6]!.ExpectedInteraction).toBe(`click:${GuideIds.topbar_settings}`)
      expect(steps[7]!.ExpectedInteraction).toBeUndefined()
    })

    it('reports semantic capability domains', () => {
      expect(steps.map(s => s.CapabilityKind)).toEqual([
        'navigation',
        'permission',
        'navigation',
        'navigation',
        'project',
        'conversation',
        'settings',
        'tools',
      ])
    })

    it('uses the expected i18n keys', () => {
      const used = new Set([...steps.map(s => s.Title), ...steps.map(s => s.Body)])
      const expectedKeys = [
        'onboarding.guide.sidebarToggle.title',
        'onboarding.guide.sidebarToggle.body',
        'onboarding.guide.permissionMode.title',
        'onboarding.guide.permissionMode.body',
        'onboarding.guide.viewModes.title',
        'onboarding.guide.viewModes.body',
        'onboarding.guide.sidebar.title',
        'onboarding.guide.sidebar.body',
        'onboarding.guide.createProject.title',
        'onboarding.guide.createProject.body',
        'onboarding.guide.firstConversation.title',
        'onboarding.guide.firstConversation.body',
        'onboarding.guide.openSettings.title',
        'onboarding.guide.openSettings.body',
        'onboarding.guide.finish.title',
        'onboarding.guide.finish.body',
      ]
      for (const key of expectedKeys) {
        expect(used, `missing i18n key usage: ${key}`).toContain(key)
      }
    })
  })

  describe('with projects and no agents', () => {
    const steps = buildBasicsTutorial(tEcho, { hasProjects: true, hasAgents: false, hasApps: false })

    it('produces a 8-step tour: sidebarToggle → permission → viewModes → sidebar → createAgent → conversation → openSettings → finish', () => {
      expect(steps.length).toBe(8)
    })

    it('includes createAgent step, not createProject', () => {
      expect(steps[4]!.TargetGuideId).toBe(GuideIds.sidebar_new_agent)
      expect(steps[4]!.CapabilityKind).toBe('agent')
    })
  })

  describe('with projects and agents', () => {
    const steps = buildBasicsTutorial(tEcho, { hasProjects: true, hasAgents: true, hasApps: false })

    it('produces a 7-step tour: sidebarToggle → permission → viewModes → sidebar → conversation → openSettings → finish', () => {
      expect(steps.length).toBe(7)
    })

    it('does not include createProject or createAgent', () => {
      const anchors = steps.map(s => s.TargetGuideId)
      expect(anchors).not.toContain(GuideIds.quick_action_create_project)
      expect(anchors).not.toContain(GuideIds.sidebar_new_agent)
    })
  })

  describe('with apps in the launcher', () => {
    const steps = buildBasicsTutorial(tEcho, { hasProjects: true, hasAgents: true, hasApps: true })

    it('produces a 9-step tour (7 base steps plus the two launcher steps)', () => {
      expect(steps.length).toBe(9)
    })

    it('inserts appPanel and openApp right after sidebar.new-chat, before the conversation step', () => {
      expect(steps[3]!.TargetGuideId).toBe(GuideIds.sidebar_new_chat)
      expect(steps[4]!.TargetGuideId).toBe(GuideIds.launcher_mode_switch)
      expect(steps[5]!.TargetGuideId).toBe(GuideIds.launcher_app_grid)
      expect(steps[6]!.TargetGuideId).toBe(GuideIds.composer_input)
    })

    it('gates both launcher steps on their anchor clicks, with navigation capability', () => {
      expect(steps[4]!.ExpectedInteraction).toBe(`click:${GuideIds.launcher_mode_switch}`)
      expect(steps[4]!.Placement).toBe('bottom')
      expect(steps[4]!.CapabilityKind).toBe('navigation')
      expect(steps[5]!.ExpectedInteraction).toBe(`click:${GuideIds.launcher_app_grid}`)
      expect(steps[5]!.Placement).toBe('right')
      expect(steps[5]!.CapabilityKind).toBe('navigation')
    })

    it('uses the appPanel and openApp i18n keys', () => {
      expect(steps[4]!.Title).toBe('onboarding.guide.appPanel.title')
      expect(steps[4]!.Body).toBe('onboarding.guide.appPanel.body')
      expect(steps[5]!.Title).toBe('onboarding.guide.openApp.title')
      expect(steps[5]!.Body).toBe('onboarding.guide.openApp.body')
    })
  })

  describe('without apps in the launcher', () => {
    const steps = buildBasicsTutorial(tEcho, { hasProjects: true, hasAgents: true, hasApps: false })

    it('omits the launcher steps entirely', () => {
      const anchors = steps.map(s => s.TargetGuideId)
      expect(anchors).not.toContain(GuideIds.launcher_mode_switch)
      expect(anchors).not.toContain(GuideIds.launcher_app_grid)
      expect(steps.length).toBe(7)
    })
  })
})

describe('buildWorkflowTutorial', () => {
  const steps = buildWorkflowTutorial(tEcho)

  it('produces a 4-step tutorial', () => {
    expect(steps.length).toBe(4)
  })

  it('orders: type /workflow → switch view → canvas → summary (typing BEFORE the view switch)', () => {
    expect(steps[0]!.TargetGuideId).toBe(GuideIds.composer_input)
    expect(steps[1]!.TargetGuideId).toBe(GuideIds.mode_cluster)
    expect(steps[2]!.TargetGuideId).toBe(GuideIds.workflow_canvas)
    expect(steps[3]!.TargetGuideId).toBe(GuideIds.workflow_canvas)
  })

  it('gates: text on the command step, mode-item click on the switch step, submit on the canvas step', () => {
    expect(steps[0]!.ExpectedInteraction).toBe('text:/workflow')
    expect(steps[1]!.ExpectedInteraction).toBe(`click:${GuideIds.mode_workflow}`)
    expect(steps[2]!.ExpectedInteraction).toBe('submit')
  })

  it('uses the workflow.view i18n keys on the view-switch step', () => {
    expect(steps[1]!.Title).toBe('onboarding.guide.workflow.view.title')
    expect(steps[1]!.Body).toBe('onboarding.guide.workflow.view.body')
  })

  it('all steps have workflow capability kind', () => {
    for (const step of steps) {
      expect(step.CapabilityKind).toBe('workflow')
    }
  })
})

describe('buildToolsTutorial', () => {
  const steps = buildToolsTutorial(tEcho)

  it('produces a 9-step tutorial', () => {
    expect(steps.length).toBe(9)
  })

  it('orders: overview → / → ! → @ → # → $ → modes → bundles → omnibox summary', () => {
    expect(steps.slice(0, 8).map(s => s.TargetGuideId)).toEqual(Array(8).fill(GuideIds.composer_input))
    expect(steps[8]!.TargetGuideId).toBe(GuideIds.topbar_omnibox_trigger)
  })

  it('is introduction-only: no step forces an interaction (Next always available)', () => {
    for (const step of steps) {
      expect(step.ExpectedInteraction).toBeUndefined()
    }
  })

  it('covers every composer prefix plus modes and bundles', () => {
    const bodies = steps.map(s => s.Body)
    expect(bodies).toContain('onboarding.guide.tools.body')
    for (const key of ['slash', 'bang', 'at', 'hash', 'dollar', 'modes', 'bundles']) {
      expect(bodies).toContain(`onboarding.guide.tools.${key}.body`)
    }
    expect(bodies).toContain('onboarding.guide.tools.summary.body')
  })
})

describe('buildSettingsTutorial', () => {
  const steps = buildSettingsTutorial(tEcho)

  it('produces a 6-step tutorial', () => {
    expect(steps.length).toBe(6)
  })

  it('starts with settings entry and ends with about', () => {
    expect(steps[0]!.TargetGuideId).toBe(GuideIds.topbar_settings)
    expect(steps[5]!.TargetGuideId).toBe(GuideIds.topbar_settings)
  })

  it('walks through settings categories', () => {
    expect(steps[1]!.TargetGuideId).toBe(GuideIds.settings_category_provider)
    expect(steps[2]!.TargetGuideId).toBe(GuideIds.settings_category_prompt)
    expect(steps[3]!.TargetGuideId).toBe(GuideIds.settings_category_skill)
    expect(steps[4]!.TargetGuideId).toBe(GuideIds.settings_category_developer)
  })
})

describe('buildOnboardingSteps (backward compatibility)', () => {
  const steps = buildOnboardingSteps(tEcho, { hasProjects: true })

  it('delegates to buildBasicsTutorial with default context', () => {
    const direct = buildBasicsTutorial(tEcho, { hasProjects: true, hasAgents: true, hasApps: true })
    expect(steps.length).toBe(direct.length)
    expect(steps.map(s => s.TargetGuideId)).toEqual(direct.map(s => s.TargetGuideId))
  })
})