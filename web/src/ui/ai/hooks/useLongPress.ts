import { useCallback, useEffect, useRef } from 'react'

interface LongPressOptions {
  onLongPress: (e: React.PointerEvent) => void
  onRelease?: () => void
  threshold?: number
  shouldStart?: (e: React.PointerEvent) => boolean
}

export function useLongPress({ onLongPress, onRelease, threshold = 420, shouldStart }: LongPressOptions) {
  const timerRef = useRef<number | null>(null)
  const firedAtRef = useRef(0)
  const firedRef = useRef(false)
  const releasedRef = useRef(false)
  const startCoordsRef = useRef<{ x: number; y: number } | null>(null)

  const cancel = useCallback(() => {
    if (timerRef.current !== null) {
      window.clearTimeout(timerRef.current)
      timerRef.current = null
    }
  }, [])

  const release = useCallback(() => {
    if (!firedRef.current || releasedRef.current) return
    releasedRef.current = true
    firedRef.current = false
    onRelease?.()
  }, [onRelease])

  const wasLongPress = useCallback(() => {
    return performance.now() - firedAtRef.current < 600
  }, [])

  const onPointerDown = useCallback((e: React.PointerEvent) => {
    if (e.pointerType === 'mouse') return
    if (shouldStart && !shouldStart(e)) return
    startCoordsRef.current = { x: e.clientX, y: e.clientY }
    firedRef.current = false
    releasedRef.current = false
    cancel()
    timerRef.current = window.setTimeout(() => {
      timerRef.current = null
      firedRef.current = true
      firedAtRef.current = performance.now()
      onLongPress(e)
    }, threshold)
  }, [onLongPress, threshold, cancel, shouldStart])

  const onPointerMove = useCallback((e: React.PointerEvent) => {
    if (timerRef.current === null) return
    const start = startCoordsRef.current
    if (!start) return
    const dx = e.clientX - start.x
    const dy = e.clientY - start.y
    if (Math.hypot(dx, dy) > 10) cancel()
  }, [cancel])

  const onPointerUp = useCallback(() => {
    release()
    cancel()
  }, [release, cancel])

  const onPointerLeave = useCallback(() => {
    if (!firedRef.current) cancel()
  }, [cancel])

  const onPointerCancel = useCallback(() => {
    if (!firedRef.current) cancel()
  }, [cancel])

  useEffect(() => {
    const handleRelease = () => {
      release()
      cancel()
    }
    window.addEventListener('pointerup', handleRelease, true)
    window.addEventListener('touchend', handleRelease, true)
    return () => {
      window.removeEventListener('pointerup', handleRelease, true)
      window.removeEventListener('touchend', handleRelease, true)
      cancel()
    }
  }, [release, cancel])

  return {
    onPointerDown,
    onPointerMove,
    onPointerUp,
    onPointerLeave,
    onPointerCancel,
    wasLongPress,
  }
}
