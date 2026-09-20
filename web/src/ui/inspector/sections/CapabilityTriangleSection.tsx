import type { InspectSection } from '../../../gen-clients/system/types'

const TRI_SIZE = 160
const TRI_PAD = 20
const TRI_H = TRI_SIZE - TRI_PAD * 2

const V = {
  performance: { x: TRI_H / 2, y: 0 },
  cost:        { x: 0,       y: TRI_H * Math.sqrt(3) / 2 },
  speed:       { x: TRI_H,   y: TRI_H * Math.sqrt(3) / 2 },
}

function toCartesian(w: { Performance: number; Cost: number; Speed: number }) {
  const ox = TRI_PAD, oy = TRI_PAD
  return {
    x: ox + w.Performance * V.performance.x + w.Cost * V.cost.x + w.Speed * V.speed.x,
    y: oy + w.Performance * V.performance.y + w.Cost * V.cost.y + w.Speed * V.speed.y,
  }
}

const TRI_COLORS = {
  performance: '#a78bfa',
  cost:        '#34d399',
  speed:       '#fb923c',
}

function CapabilityTriangle({ weights }: { weights: { Performance: number; Cost: number; Speed: number } }) {
  const pt = toCartesian(weights)
  const triPath = `M ${TRI_PAD + V.performance.x} ${TRI_PAD + V.performance.y} L ${TRI_PAD + V.cost.x} ${TRI_PAD + V.cost.y} L ${TRI_PAD + V.speed.x} ${TRI_PAD + V.speed.y} Z`
  const gradId = (k: string) => `insp-grad-${k}`

  return (
    <svg width={TRI_SIZE} height={TRI_SIZE} viewBox={`0 0 ${TRI_SIZE} ${TRI_SIZE}`} className="inspector-tri-svg">
      <defs>
        {(['performance', 'cost', 'speed'] as const).map(k => {
          const oppMap: Record<string, { x: number; y: number }> = {
            performance: { x: (V.cost.x + V.speed.x) / 2, y: (V.cost.y + V.speed.y) / 2 },
            cost:        { x: (V.performance.x + V.speed.x) / 2, y: (V.performance.y + V.speed.y) / 2 },
            speed:       { x: (V.performance.x + V.cost.x) / 2, y: (V.performance.y + V.cost.y) / 2 },
          }
          const opp = oppMap[k]!
          return (
            <linearGradient
              key={k}
              id={gradId(k)}
              x1={TRI_PAD + V[k].x}
              y1={TRI_PAD + V[k].y}
              x2={TRI_PAD + opp.x}
              y2={TRI_PAD + opp.y}
              gradientUnits="userSpaceOnUse"
            >
              <stop offset="0%" stopColor={TRI_COLORS[k]} stopOpacity={Math.min(0.9, weights[k as keyof typeof weights] * 2.2)} />
              <stop offset="100%" stopColor={TRI_COLORS[k]} stopOpacity="0" />
            </linearGradient>
          )
        })}
        <clipPath id="insp-tri-clip">
          <path d={triPath} />
        </clipPath>
      </defs>
      <path d={triPath} fill="var(--bg-elevated)" />
      {(['performance', 'cost', 'speed'] as const).map(k => (
        <path
          key={k}
          d={triPath}
          fill={`url(#${gradId(k)})`}
          opacity={weights[k as keyof typeof weights]}
          style={{ mixBlendMode: 'screen' }}
          clipPath="url(#insp-tri-clip)"
        />
      ))}
      <path d={triPath} fill="none" stroke="var(--border-default)" strokeWidth="1" />
      <text x={TRI_PAD + V.performance.x} y={TRI_PAD + V.performance.y - 6} className="inspector-tri-label" textAnchor="middle">Perf</text>
      <text x={TRI_PAD + V.cost.x - 4} y={TRI_PAD + V.cost.y + 12} className="inspector-tri-label" textAnchor="middle">Cost</text>
      <text x={TRI_PAD + V.speed.x + 4} y={TRI_PAD + V.speed.y + 12} className="inspector-tri-label" textAnchor="middle">Speed</text>
      <text x={TRI_PAD + V.performance.x} y={TRI_PAD + V.performance.y + 8} className="inspector-tri-val" textAnchor="middle" fill={TRI_COLORS.performance}>
        {(weights.Performance * 100).toFixed(0)}%
      </text>
      <text x={TRI_PAD + V.cost.x - 4} y={TRI_PAD + V.cost.y - 2} className="inspector-tri-val" textAnchor="middle" fill={TRI_COLORS.cost}>
        {(weights.Cost * 100).toFixed(0)}%
      </text>
      <text x={TRI_PAD + V.speed.x + 4} y={TRI_PAD + V.speed.y - 2} className="inspector-tri-val" textAnchor="middle" fill={TRI_COLORS.speed}>
        {(weights.Speed * 100).toFixed(0)}%
      </text>
      <circle cx={pt.x} cy={pt.y} r="6" fill="var(--accent-primary-dim)" stroke="var(--accent-primary)" strokeWidth="1.5" />
      <circle cx={pt.x} cy={pt.y} r="2" fill="var(--accent-primary)" />
    </svg>
  )
}

export function CapabilityTriangleSection({ section }: { section: InspectSection }) {
  return (
    <div className="inspector-section">
      <h4 className="inspector-section-title">{section.Title}</h4>
      <div className="inspector-tri-wrap">
        <CapabilityTriangle weights={section.Capability!} />
      </div>
    </div>
  )
}
