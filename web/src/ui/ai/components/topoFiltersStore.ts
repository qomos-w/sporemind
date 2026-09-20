import { useSyncExternalStore, useCallback } from 'react'

export type FilterField = 'title' | 'status' | 'tag' | 'type' | 'source' | 'subtree' | 'lineage'

export type FilterOperator = 'and' | 'or' | 'xor'

export interface CustomFilter {
  id: string
  field: FilterField
  value: string
  operator?: FilterOperator
}

let state: CustomFilter[] = []
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

function makeId(): string {
  return `cf-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}`
}

export const topoFiltersStore = {
  getState: () => state,
  subscribe(cb: () => void) {
    listeners.add(cb)
    return () => {
      listeners.delete(cb)
    }
  },
  add(field: FilterField, value: string, operator?: FilterOperator): string {
    const id = makeId()
    state = [...state, { id, field, value, operator }]
    emit()
    return id
  },
  update(id: string, field: FilterField, value: string, operator?: FilterOperator) {
    state = state.map(f => (f.id === id ? { id, field, value, operator } : f))
    emit()
  },
  remove(id: string) {
    state = state.filter(f => f.id !== id)
    emit()
  },
  setOperator(id: string, operator: FilterOperator) {
    state = state.map(f => (f.id === id ? { ...f, operator } : f))
    emit()
  },
}

export function useCustomFilters(): CustomFilter[] {
  const subscribe = useCallback((cb: () => void) => topoFiltersStore.subscribe(cb), [])
  const getSnapshot = useCallback(() => topoFiltersStore.getState(), [])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
