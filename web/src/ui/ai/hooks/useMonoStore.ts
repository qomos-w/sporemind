import { useSyncExternalStore, useCallback, useRef } from 'react'
import { monoStore, type MonoStoreState } from '../../panels/mono-store'

export function useMonoStore(): MonoStoreState
export function useMonoStore<T>(selector: (state: MonoStoreState) => T): T
export function useMonoStore<T>(selector?: (state: MonoStoreState) => T) {
  const subscribe = useCallback((cb: () => void) => monoStore.subscribe(cb), [])
  const selectorRef = useRef(selector)
  selectorRef.current = selector
  const getSnapshot = useCallback(() => {
    const state = monoStore.getState()
    return selectorRef.current ? selectorRef.current(state) : state
  }, [])
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}
