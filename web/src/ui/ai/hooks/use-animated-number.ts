import { useEffect, useRef, useState } from 'react'

// Damped lag toward `target` — display always tracks current target with constant
// time-constant lag, rather than a fresh fixed-duration tween on every update.
export function useAnimatedNumber(target: number, opts?: { tauSec?: number; sync?: boolean }): number {
  const [display, setDisplay] = useState(0)
  const internalRef = useRef(0)
  const targetRef = useRef(target)
  const rafRef = useRef(0)
  const lastTsRef = useRef(0)

  targetRef.current = target

  useEffect(() => {
    if (opts?.sync) {
      internalRef.current = targetRef.current
      setDisplay(Math.round(targetRef.current))
      return
    }

    const tau = opts?.tauSec ?? 0.2
    lastTsRef.current = performance.now()

    const tick = (now: number) => {
      const dt = (now - lastTsRef.current) / 1000
      lastTsRef.current = now
      const t = targetRef.current
      const d = internalRef.current

      if (d !== t) {
        const alpha = 1 - Math.exp(-dt / tau)
        let next = d + (t - d) * alpha
        if (Math.abs(t - next) < 0.5) next = t
        internalRef.current = next
        const rounded = Math.round(next)
        setDisplay(prev => (prev !== rounded ? rounded : prev))
      }

      if (internalRef.current === t) return
      rafRef.current = requestAnimationFrame(tick)
    }
    rafRef.current = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(rafRef.current)
  }, [opts?.tauSec, opts?.sync, target])

  return display
}
