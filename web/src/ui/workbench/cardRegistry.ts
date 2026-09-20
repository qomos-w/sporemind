import { useSyncExternalStore } from 'react'
import type { WorkbenchCardDescriptor } from './cardTypes'

/**
 * Workbench card registry (spec §2).
 *
 * A tiny reactive store of {@link WorkbenchCardDescriptor}s. Builtin cards and
 * plugin cards register the same descriptor shape here; the board reads the
 * snapshot and the layout engine places them. Registration is keyed by
 * descriptor id, so re-registering (e.g. after a plugin reload) replaces the
 * prior entry in place.
 */
export class WorkbenchCardRegistry {
  private cards = new Map<string, WorkbenchCardDescriptor>()
  private listeners = new Set<() => void>()
  private snapshot: WorkbenchCardDescriptor[] | null = null

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  /** Current cards, newest snapshot. Stable identity until the set changes. */
  getSnapshot = (): WorkbenchCardDescriptor[] => {
    if (this.snapshot === null) this.snapshot = Array.from(this.cards.values())
    return this.snapshot
  }

  register(card: WorkbenchCardDescriptor): void {
    if (!card.id) return
    this.cards.set(card.id, card)
    this.emit()
  }

  unregister(id: string): void {
    if (this.cards.delete(id)) this.emit()
  }

  get(id: string): WorkbenchCardDescriptor | undefined {
    return this.cards.get(id)
  }

  list(): WorkbenchCardDescriptor[] {
    return this.getSnapshot()
  }

  clear(): void {
    if (this.cards.size === 0) return
    this.cards.clear()
    this.emit()
  }

  private emit(): void {
    this.snapshot = null
    for (const listener of this.listeners) listener()
  }
}

export function createWorkbenchCardRegistry(): WorkbenchCardRegistry {
  return new WorkbenchCardRegistry()
}

/** Reactive read of a registry's cards for React consumers. */
export function useWorkbenchCardRegistry(registry: WorkbenchCardRegistry): WorkbenchCardDescriptor[] {
  return useSyncExternalStore(registry.subscribe, registry.getSnapshot, registry.getSnapshot)
}
