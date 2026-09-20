import { isWails } from './runtime'
import * as desktop from '../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'

/** Structural mirror of the generated StorageSettings binding model.
 * Declared locally so importing this module does not pull
 * @wailsio/runtime (which probes the dev server at import time and breaks
 * node test environments). */
export interface StorageSettingsForm {
  /** Raw backend_aistats value: "" (default goleveldb) / "fs" / "goleveldb". */
  backendAistats: string
  /** Raw backend_logs value: "" (default goleveldb) / "fs" / "goleveldb". */
  backendLogs: string
  /** Effective backend-log retention window in days. */
  logsRetentionDays: number
  /** Effective raw aistats record retention window in days. */
  aistatsRawDays: number
  /** Effective daily-rollup aistats retention window in days. */
  aistatsDailyDays: number
}

/** Default storage settings shown when no backend writes them (non-wails
 * environments, or a yaml that never set the keys). Mirrors the Go-side
 * defaults: both scopes default to goleveldb, retention 7/90/550 days. */
export function defaultStorageSettings(): StorageSettingsForm {
  return {
    backendAistats: '',
    backendLogs: '',
    logsRetentionDays: 7,
    aistatsRawDays: 90,
    aistatsDailyDays: 550,
  }
}

let cached: StorageSettingsForm | null = null

/** Whether the storage settings can be persisted from this environment —
 * only the Wails desktop app exposes the config bindings. */
export function storageSettingsEditable(): boolean {
  return isWails()
}

/** Load the storage settings from the backend. Non-wails environments get
 * the defaults immediately (read-only view). */
export async function loadStorageSettings(): Promise<StorageSettingsForm> {
  if (!isWails()) {
    cached = defaultStorageSettings()
    return cached
  }
  try {
    cached = await desktop.GetStorageSettings()
  } catch (err) {
    console.warn('[storage-config] failed to load storage settings, using defaults', err)
    cached = defaultStorageSettings()
  }
  return cached
}

/** Return the cached storage settings, or null before the first load. */
export function getStorageSettings(): StorageSettingsForm | null {
  return cached
}

/** Apply the form to the in-memory config and persist sporemind.yaml.
 * Backend and retention changes take effect after a restart. */
export async function saveStorageSettings(settings: StorageSettingsForm): Promise<void> {
  await desktop.SetStorageSettings(settings)
  await desktop.SaveConfig()
  cached = { ...settings }
}

/** Test-only: override the cached settings. */
export function setStorageSettingsForTest(settings: StorageSettingsForm | null): void {
  cached = settings
}
