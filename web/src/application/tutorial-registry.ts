/**
 * Tutorial category registry — defines the multi-category tutorial system
 * that replaces the former single linear onboarding tour.
 *
 * Each category is a self-contained tutorial covering one functional area
 * of the app. The `basics` category auto-triggers on first run; the rest
 * are replayable from Settings → About.
 */
import { useSyncExternalStore } from 'react'
import type { I18nKey } from '../i18n'
import type { GuideStep, TutorialSpec } from '../gen-types/interfacemanager'
import type { TFunction } from './onboarding-guide'
import { buildBasicsTutorial, buildWorkflowTutorial, buildToolsTutorial, buildSettingsTutorial } from './onboarding-guide'

export interface TutorialContext {
  hasProjects: boolean
  hasAgents: boolean
  /** True when at least one installed app surfaces a launcher tile. */
  hasApps: boolean
}

export interface TutorialCategory {
  /** Unique id used for persistence and lookup. */
  id: string
  /** i18n key for the category display label. */
  labelKey: I18nKey
  /** i18n key for the category description. */
  descKey: I18nKey
  /** Build the guide steps for this tutorial. */
  build: (t: TFunction, ctx: TutorialContext) => GuideStep[]
  /** Only the `basics` category auto-triggers on first run. */
  autoTrigger?: boolean
  /** Whether this tutorial is available given the current context. */
  available?: (ctx: TutorialContext) => boolean
}

export const TUTORIAL_CATEGORIES: TutorialCategory[] = [
  {
    id: 'basics',
    labelKey: 'onboarding.tutorial.category.basics.label',
    descKey: 'onboarding.tutorial.category.basics.desc',
    build: (t, ctx) => buildBasicsTutorial(t, ctx),
    autoTrigger: true,
  },
  {
    id: 'workflow',
    labelKey: 'onboarding.tutorial.category.workflow.label',
    descKey: 'onboarding.tutorial.category.workflow.desc',
    build: (t, ctx) => buildWorkflowTutorial(t, ctx),
    available: (ctx) => ctx.hasProjects,
  },
  {
    id: 'tools',
    labelKey: 'onboarding.tutorial.category.tools.label',
    descKey: 'onboarding.tutorial.category.tools.desc',
    build: (t) => buildToolsTutorial(t),
  },
  {
    id: 'settings',
    labelKey: 'onboarding.tutorial.category.settings.label',
    descKey: 'onboarding.tutorial.category.settings.desc',
    build: (t) => buildSettingsTutorial(t),
  },
]

/** The category that auto-triggers on first run. */
export const AUTO_TRIGGER_CATEGORY = TUTORIAL_CATEGORIES.find((c) => c.autoTrigger) ?? TUTORIAL_CATEGORIES[0]!

/** Look up a category by id. */
export function getTutorialCategory(id: string): TutorialCategory | undefined {
  return TUTORIAL_CATEGORIES.find((c) => c.id === id)
}

// ── Dynamic tutorials (agent-created via interfacemanager.create_tutorial) ──
//
// The store is a pure in-memory projection of the actor-owned backend state
// (persisted by interfacemanager). AIShellLayout wires it up at startup:
// one `control(tutorial_catalog)` load plus create/delete event handling.
// No localStorage/sessionStorage — durable state lives in the actor.

export type DynamicTutorialListener = () => void

/** Defensive normalize: tolerate both PascalCase (wire) and camelCase shapes. */
export function normalizeTutorialSpec(value: any): TutorialSpec {
  return {
    TutorialId: String(value?.TutorialId ?? value?.tutorialId ?? ''),
    Title: String(value?.Title ?? value?.title ?? ''),
    Description: value?.Description ?? value?.description,
    Steps: Array.isArray(value?.Steps ?? value?.steps) ? (value.Steps ?? value.steps) : [],
    CreatedAt: String(value?.CreatedAt ?? value?.createdAt ?? ''),
  }
}

class DynamicTutorialStore {
  private tutorials = new Map<string, TutorialSpec>()
  private listeners = new Set<DynamicTutorialListener>()
  private allSnapshot: TutorialSpec[] | null = null

  subscribe(listener: DynamicTutorialListener): () => void {
    this.listeners.add(listener)
    return () => { this.listeners.delete(listener) }
  }

  private notify(): void {
    this.allSnapshot = null
    for (const listener of this.listeners) listener()
  }

  /** Insert or overwrite by TutorialId. Ignores entries without an id. */
  upsert(spec: TutorialSpec): void {
    const tutorial = normalizeTutorialSpec(spec)
    if (!tutorial.TutorialId) return
    const previous = this.tutorials.get(tutorial.TutorialId)
    if (previous && JSON.stringify(previous) === JSON.stringify(tutorial)) return
    this.tutorials.set(tutorial.TutorialId, tutorial)
    this.notify()
  }

  remove(id: string): void {
    if (this.tutorials.delete(id)) this.notify()
  }

  /** Drop every dynamic tutorial (startup re-sync / test isolation). */
  clear(): void {
    if (!this.tutorials.size) return
    this.tutorials.clear()
    this.notify()
  }

  get(id: string): TutorialSpec | undefined {
    return this.tutorials.get(id)
  }

  getAll(): TutorialSpec[] {
    if (this.allSnapshot === null) this.allSnapshot = Array.from(this.tutorials.values())
    return this.allSnapshot
  }
}

export const dynamicTutorialStore = new DynamicTutorialStore()

/** Unified tutorial entry shared by the tutorial library and help menu. */
export interface TutorialListItem {
  id: string
  title: string
  description?: string
  steps: GuideStep[]
  /** True for agent-created dynamic tutorials; false for the static categories. */
  isDynamic: boolean
  /** Dynamic tutorials only: creation timestamp (UTC, wire format). */
  createdAt?: string
}

/**
 * Merge static categories and dynamic tutorials into one normalized list.
 * Static entries are built through i18n (`build(t, ctx)`); dynamic entries
 * carry literal copy generated in the user's language at creation time.
 */
export function listTutorials(t: TFunction, ctx: TutorialContext): TutorialListItem[] {
  const staticItems: TutorialListItem[] = TUTORIAL_CATEGORIES.map((cat) => ({
    id: cat.id,
    title: t(cat.labelKey),
    description: t(cat.descKey),
    steps: cat.build(t, ctx),
    isDynamic: false,
  }))
  const dynamicItems: TutorialListItem[] = dynamicTutorialStore.getAll().map((spec) => ({
    id: spec.TutorialId,
    title: spec.Title,
    description: spec.Description,
    steps: spec.Steps,
    isDynamic: true,
    createdAt: spec.CreatedAt,
  }))
  return [...staticItems, ...dynamicItems]
}

// ── React subscription ──────────────────────────────────────────────────────

/**
 * Subscribe to the dynamic tutorial list. Re-renders on upsert/remove/clear;
 * returns a stable empty array while no dynamic tutorials exist.
 */
export function useDynamicTutorials(): TutorialSpec[] {
  return useSyncExternalStore(
    (listener) => dynamicTutorialStore.subscribe(listener),
    () => dynamicTutorialStore.getAll(),
  )
}