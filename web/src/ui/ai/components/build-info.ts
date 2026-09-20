import { useEffect, useState } from 'react'
import type { Info as BuildInfo } from '../../../bindings/github.com/qomos-w/sporemind/pkg/buildinfo/models'

/** Compute app name same as cmd/sporemind-desktop/instance.go appName() */
export function computeAppName(type: string): string {
  switch (type) {
    case 'release': return 'sporemind'
    case 'beta':    return 'sporemind-beta'
    default:        return 'sporemind-dev'
  }
}

/**
 * Runtime build info (commit, build_time, dirty) via the desktop binding.
 * Falls back silently to null in non-desktop contexts; no fetch happens while
 * `enabled` is false so always-mounted overlays avoid the dynamic import.
 */
export function useBuildInfo(enabled = true): BuildInfo | null {
  const [buildInfo, setBuildInfo] = useState<BuildInfo | null>(null)

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    ;(async () => {
      try {
        const { GetBuildInfo } = await import('../../../bindings/github.com/qomos-w/sporemind/pkg/desktop/app')
        const info = await GetBuildInfo()
        if (!cancelled) {
          setBuildInfo(info)
        }
      } catch {
        // Runtime binding unavailable — use build-time values only
        if (!cancelled) setBuildInfo(null)
      }
    })()
    return () => { cancelled = true }
  }, [enabled])

  return buildInfo
}
