// StorageAdapter — abstracts the persistence backend for layout/panels/dock-sizes.
//
// sporemind provides an implementation backed by actor-owned backend state.
// Frontend localStorage/sessionStorage persistence is forbidden in this repo.
// Keys used by the shell:
//   - "sporemind-layout-v1"
//   - "sporemind-panels-v1"
//   - "sporemind-dock-sizes-v1"

export interface StorageAdapter {
  get(key: string): Promise<string | null>
  set(key: string, value: string): Promise<void>
}

/**
 * Wraps any StorageAdapter so that all writes are silently dropped
 * until `unlock()` is called. Reads always pass through (init needs them).
 *
 * Prevents stores from overwriting persisted state with defaults
 * before their `init()` has finished loading.
 */
export class DeferredStorageAdapter implements StorageAdapter {
  private inner: StorageAdapter
  private ready = false

  constructor(inner: StorageAdapter) {
    this.inner = inner
  }

  get(key: string) {
    return this.inner.get(key)
  }

  async set(_key: string, _value: string) {
    if (!this.ready) return
    return this.inner.set(_key, _value)
  }

  /** Allow writes through to the underlying adapter. */
  unlock() {
    this.ready = true
  }
}
