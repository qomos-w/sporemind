import { useEffect, useState } from 'react'

export type ViewportMode = 'mobile' | 'desktop'

const MOBILE_MAX = 768

function widthToMode(w: number): ViewportMode {
  return w < MOBILE_MAX ? 'mobile' : 'desktop'
}

export function useViewportMode(): ViewportMode {
  const [mode, setMode] = useState<ViewportMode>(() => {
    if (typeof window === 'undefined') return 'desktop'
    return widthToMode(window.innerWidth)
  })

  useEffect(() => {
    const onResize = () => setMode(widthToMode(window.innerWidth))
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])

  return mode
}
