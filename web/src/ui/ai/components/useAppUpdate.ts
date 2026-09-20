import { useCallback, useEffect, useRef, useState } from 'react'
import { isWails } from '../../../application/runtime'
import { onWailsEvent } from '../../../application/wails-runtime'
import * as desktopApp from '../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app'
import type { UpdateStatus } from '../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/models'

export type { UpdateStatus }

const UPDATE_EVENT = 'sporemind:update'

/**
 * Wails-mode auto-update state. Listens for the backend's "sporemind:update"
 * event (emitted on every phase transition) and exposes the current snapshot
 * plus the action methods. In non-Wails runtimes every action is a no-op.
 */
export function useAppUpdate() {
  const [status, setStatus] = useState<UpdateStatus | null>(null)
  const [featureEnabled, setFeatureEnabled] = useState(false)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    if (!isWails()) return
    let cancelled = false
    void desktopApp.GetFeatureFlags()
      .then((flags) => {
        if (!cancelled) setFeatureEnabled(Boolean(flags?.flags?.autoUpdate))
      })
      .catch(() => { if (!cancelled) setFeatureEnabled(false) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => {
    if (!isWails()) return
    const cancel = onWailsEvent(UPDATE_EVENT, (...args: unknown[]) => {
      const snap = args?.[0] as UpdateStatus | undefined
      if (mounted.current && snap) setStatus(snap)
    })
    // Pull once so the first render reflects any check that already ran
    // before this component mounted.
    void desktopApp.GetUpdateStatus()
      .then((snap) => { if (mounted.current && snap) setStatus(snap) })
      .catch(() => {})
    return cancel ?? undefined
  }, [])

  const check = useCallback(async () => {
    if (!isWails()) return
    try { await desktopApp.CheckUpdate() } catch { /* status event carries error */ }
  }, [])

  const download = useCallback(async () => {
    if (!isWails()) return
    try { await desktopApp.DownloadUpdate() } catch { /* status event carries error */ }
  }, [])

  const install = useCallback(async () => {
    if (!isWails()) return
    try { await desktopApp.InstallUpdate() } catch { /* keep UI in ready state */ }
  }, [])

  const dismiss = useCallback(async () => {
    if (!isWails()) return
    try { await desktopApp.DismissUpdate() } catch { /* no-op */ }
  }, [])

  const reveal = useCallback(async () => {
    if (!isWails()) return
    try { await desktopApp.RevealUpdateInFolder() } catch { /* no-op */ }
  }, [])

  return {
    status,
    featureEnabled,
    isWails: isWails(),
    check,
    download,
    install,
    dismiss,
    reveal,
  }
}
