import type { ReactNode } from 'react'
import { useViewportSlot } from '../hooks/useViewportAware'

interface ViewportSlotProps {
  id: string
  children: ReactNode
  alwaysVisible?: boolean
}

export function ViewportSlot({ id, children, alwaysVisible }: ViewportSlotProps) {
  const { ref, isVisible, cachedHeight } = useViewportSlot(id, alwaysVisible)

  return (
    <div ref={ref} data-vp-id={id}>
      {isVisible ? children : <div style={{ height: cachedHeight ?? 200 }} />}
    </div>
  )
}
