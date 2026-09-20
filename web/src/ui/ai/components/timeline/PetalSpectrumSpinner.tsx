import React, { useEffect, useRef } from 'react'

interface PetalSpectrumSpinnerProps {
  /** 0..1 — drives rotation/sweep period and elastic petal extension */
  speed?: number
}

const PETAL_COUNT = 6
const PETAL_ANGLES = Array.from({ length: PETAL_COUNT }, (_, i) => (i * 360) / PETAL_COUNT)

const PERIOD_SLOW_MS = 4500
const PERIOD_FAST_MS = 60

// Stroboscope flicker — engages near the 6-petal symmetry threshold (period ≈ 100ms at 60fps),
// where wagon-wheel reversal becomes visible.
const STROBE_PERIOD_THRESHOLD_MS = 140
const STROBE_PERIOD_FULL_MS = 60
const STROBE_FREQ_HZ = 11
const STROBE_OPACITY_MIN = 0.45

// Flywheel — angular velocity tracks a pre-filtered target with first-order lag.
// Two-stage cascade (pre-filter + flywheel) produces an S-curve response: no instant onset of rate change.
// Asymmetric flywheel tau: revs up moderately, coasts down slowly.
const FLYWHEEL_PRE_TAU_S = 0.35
const FLYWHEEL_ACCEL_TAU_S = 0.8
const FLYWHEEL_DECEL_TAU_S = 2.2

const PETAL_DIST_IDLE = 4.0
const PETAL_DIST_PEAK = 7.0
const PETAL_RX_IDLE = 2.5
const PETAL_RX_PEAK = 1.4
const PETAL_RY_IDLE = 0.85
const PETAL_RY_PEAK = 1.5

const SPRING_STIFFNESS = 90
const SPRING_DAMPING = 12

function clamp(v: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, v))
}

function lerp(a: number, b: number, t: number) {
  return a + (b - a) * t
}

// Exponential interpolation — human-perceptual speed is closer to log scale,
// and this compresses the slow band so the wagon-wheel threshold is crossed only near peak.
function periodForSpeed(speed: number): number {
  const t = clamp(speed, 0, 1)
  return PERIOD_SLOW_MS * Math.pow(PERIOD_FAST_MS / PERIOD_SLOW_MS, t)
}

interface PetalGeom {
  cx: number
  rx: number
  ry: number
}

function targetGeom(speed: number): PetalGeom {
  const t = clamp(speed, 0, 1)
  return {
    cx: 8 + lerp(PETAL_DIST_IDLE, PETAL_DIST_PEAK, t),
    rx: lerp(PETAL_RX_IDLE, PETAL_RX_PEAK, t),
    ry: lerp(PETAL_RY_IDLE, PETAL_RY_PEAK, t),
  }
}

export const PetalSpectrumSpinner = React.memo<PetalSpectrumSpinnerProps>(({ speed = 0 }) => {
  const rawId = React.useId()
  const uid = rawId.replace(/:/g, '')
  const gradientId = `petal-spectrum-${uid}`
  const maskId = `petal-mask-${uid}`

  const maskGroupRef = useRef<SVGGElement | null>(null)
  const gradientRef = useRef<SVGLinearGradientElement | null>(null)
  const ellipseRefs = useRef<(SVGEllipseElement | null)[]>([])
  const speedRef = useRef(clamp(speed, 0, 1))
  const targetRef = useRef<PetalGeom>(targetGeom(speed))
  const currentRef = useRef<PetalGeom>(targetGeom(speed))
  const velocityRef = useRef<PetalGeom>({ cx: 0, rx: 0, ry: 0 })
  const omegaRef = useRef(0)
  const omegaTargetSmoothedRef = useRef(0)
  const lastWrittenRef = useRef({ cx: '', rx: '', ry: '', angle: '', opacity: '', offset: '' })

  useEffect(() => {
    speedRef.current = clamp(speed, 0, 1)
    targetRef.current = targetGeom(speed)
  }, [speed])

  useEffect(() => {
    let phase = 0
    let lastTs = performance.now()
    let rafId = 0

    const tick = (ts: number) => {
      const dtMs = Math.min(ts - lastTs, 100)
      const dt = dtMs / 1000
      lastTs = ts

      const periodTargetMs = periodForSpeed(speedRef.current)
      const omegaTargetRaw = 1000 / periodTargetMs

      // Stage 1: low-pass the target so it doesn't jolt the flywheel with step changes
      const preAlpha = 1 - Math.exp(-dt / FLYWHEEL_PRE_TAU_S)
      omegaTargetSmoothedRef.current += (omegaTargetRaw - omegaTargetSmoothedRef.current) * preAlpha

      // Stage 2: flywheel chases the filtered target with asymmetric (accel/decel) tau
      const omegaTarget = omegaTargetSmoothedRef.current
      const tau = omegaTarget > omegaRef.current ? FLYWHEEL_ACCEL_TAU_S : FLYWHEEL_DECEL_TAU_S
      const alpha = 1 - Math.exp(-dt / tau)
      omegaRef.current += (omegaTarget - omegaRef.current) * alpha

      phase = (phase + omegaRef.current * dt) % 1
      if (phase < 0) phase += 1

      // Strobe keys off ACTUAL rotation (post-flywheel), not the input target,
      // so flicker engages with the visible wagon-wheel rather than ahead of it.
      const periodMs = omegaRef.current > 1e-6 ? 1000 / omegaRef.current : Infinity
      const strobeBlend = clamp(
        (STROBE_PERIOD_THRESHOLD_MS - periodMs) / (STROBE_PERIOD_THRESHOLD_MS - STROBE_PERIOD_FULL_MS),
        0,
        1,
      )
      let groupOpacity = 1
      if (strobeBlend > 0) {
        const strobePhase = ((ts / 1000) * STROBE_FREQ_HZ) % 1
        const wave = 0.5 - 0.5 * Math.cos(strobePhase * 2 * Math.PI)
        const flickerOpacity = lerp(1, STROBE_OPACITY_MIN, wave)
        groupOpacity = lerp(1, flickerOpacity, strobeBlend)
      }

      const last = lastWrittenRef.current

      if (maskGroupRef.current) {
        const angle = (phase * 360).toFixed(2)
        if (angle !== last.angle) {
          maskGroupRef.current.setAttribute('transform', `rotate(${angle} 8 8)`)
          last.angle = angle
        }
        const opacityStr = groupOpacity.toFixed(3)
        if (opacityStr !== last.opacity) {
          maskGroupRef.current.setAttribute('opacity', opacityStr)
          last.opacity = opacityStr
        }
      }
      if (gradientRef.current) {
        const offset = (-phase * 16).toFixed(2)
        if (offset !== last.offset) {
          gradientRef.current.setAttribute('gradientTransform', `translate(${offset} 0)`)
          last.offset = offset
        }
      }

      // Spring-damped tween of current geometry toward target — gives elastic overshoot
      const tg = targetRef.current
      const cur = currentRef.current
      const vel = velocityRef.current
      ;(['cx', 'rx', 'ry'] as const).forEach(k => {
        const force = -SPRING_STIFFNESS * (cur[k] - tg[k]) - SPRING_DAMPING * vel[k]
        vel[k] += force * dt
        cur[k] += vel[k] * dt
      })

      const cxStr = cur.cx.toFixed(2)
      const rxStr = cur.rx.toFixed(2)
      const ryStr = cur.ry.toFixed(2)
      if (cxStr !== last.cx) {
        last.cx = cxStr
        for (const el of ellipseRefs.current) {
          if (el) el.setAttribute('cx', cxStr)
        }
      }
      if (rxStr !== last.rx) {
        last.rx = rxStr
        for (const el of ellipseRefs.current) {
          if (el) el.setAttribute('rx', rxStr)
        }
      }
      if (ryStr !== last.ry) {
        last.ry = ryStr
        for (const el of ellipseRefs.current) {
          if (el) el.setAttribute('ry', ryStr)
        }
      }

      rafId = requestAnimationFrame(tick)
    }

    rafId = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(rafId)
  }, [])

  // Initial geometry for first paint (before rAF kicks in)
  const initial = currentRef.current

  return (
    <svg
      viewBox="0 0 16 16"
      width="16"
      height="16"
      xmlns="http://www.w3.org/2000/svg"
      aria-hidden="true"
      data-spinner-speed={clamp(speed, 0, 1).toFixed(2)}
    >
      <defs>
        <linearGradient
          ref={gradientRef}
          id={gradientId}
          x1="0"
          y1="0"
          x2="32"
          y2="0"
          gradientUnits="userSpaceOnUse"
        >
          <stop offset="0" stopColor="#a06bff" />
          <stop offset="0.1" stopColor="#4a8eff" />
          <stop offset="0.2" stopColor="#2bc466" />
          <stop offset="0.3" stopColor="#ffd84a" />
          <stop offset="0.4" stopColor="#ff7a3a" />
          <stop offset="0.5" stopColor="#a06bff" />
          <stop offset="0.6" stopColor="#4a8eff" />
          <stop offset="0.7" stopColor="#2bc466" />
          <stop offset="0.8" stopColor="#ffd84a" />
          <stop offset="0.9" stopColor="#ff7a3a" />
          <stop offset="1" stopColor="#a06bff" />
        </linearGradient>
        <mask id={maskId} maskUnits="userSpaceOnUse" x="0" y="0" width="16" height="16">
          <g ref={maskGroupRef}>
            {PETAL_ANGLES.map((angle, i) => (
              <g key={i} transform={`rotate(${angle} 8 8)`}>
                <ellipse
                  ref={el => { ellipseRefs.current[i] = el }}
                  fill="white"
                  cx={initial.cx}
                  cy={8}
                  rx={initial.rx}
                  ry={initial.ry}
                />
              </g>
            ))}
          </g>
        </mask>
      </defs>
      <rect
        x="0"
        y="0"
        width="16"
        height="16"
        fill={`url(#${gradientId})`}
        mask={`url(#${maskId})`}
      />
    </svg>
  )
})
PetalSpectrumSpinner.displayName = 'PetalSpectrumSpinner'
