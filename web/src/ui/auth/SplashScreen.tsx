import { AuthTitleBar } from './AuthTitleBar'
import './SplashScreen.css'

// App icon mark (assets/icon.svg), inlined so the bolt can be drawn
// progressively: the mask stroke grows waypoint by waypoint (1 → 2 → 3 → 4 → 5)
// until the whole polyline is extended, then a light band sweeps across the
// metallic stroke every 5 seconds (game-style gleam).
function SplashBolt() {
  return (
    <svg
      className="splash-screen-icon splash-bolt"
      viewBox="0 0 1024 1024"
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="splashBoltGrad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stopColor="#c3ea5c" />
          <stop offset="1" stopColor="#7ba422" />
        </linearGradient>
        <linearGradient id="splashVeilGrad" gradientUnits="userSpaceOnUse" x1="405.1" y1="668.6" x2="433.0" y2="727.9">
          <stop offset="0" stopColor="#0c0e08" stopOpacity="0.85" />
          <stop offset="1" stopColor="#0c0e08" stopOpacity="0" />
        </linearGradient>
        <linearGradient id="splashSheenGrad" x1="0" y1="0" x2="1" y2="0">
          <stop offset="0" stopColor="#ffffff" stopOpacity="0" />
          <stop offset="0.35" stopColor="#ffffff" stopOpacity="0.55" />
          <stop offset="0.5" stopColor="#ffffff" stopOpacity="0.9" />
          <stop offset="1" stopColor="#ffffff" stopOpacity="0" />
        </linearGradient>
        <mask maskUnits="userSpaceOnUse" x="0" y="0" width="1024" height="1024" id="splashBoltMask">
          <path
            className="splash-bolt-draw"
            d="M680 226.2 L344 384.2 L616 512 L344 639.8 L680 797.8"
            pathLength="4"
            fill="none"
            stroke="#fff"
            strokeWidth="104"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </mask>
      </defs>
      <rect className="splash-bolt-bg" width="1024" height="1024" rx="230" fill="#1b1d17" />
      <g mask="url(#splashBoltMask)">
        <path d="M680 226.2 L344 384.2 L616 512 L344 639.8 L680 797.8" fill="none" stroke="url(#splashBoltGrad)" strokeWidth="104" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M466.2 639.9 L344.0 697.4 L421.0 733.5 L543.2 676.1 Z" fill="url(#splashVeilGrad)" />
        {/* Slanted light band (parallelogram: top edge x 200..540, bottom edge
            shifted +373 = tan(20°)·1024). It lives inside the mask so the gleam
            only shows on the metallic stroke; CSS sweeps it left → right. */}
        <path className="splash-bolt-sheen" d="M200 0 L540 0 L913 1024 L573 1024 Z" fill="url(#splashSheenGrad)" />
      </g>
    </svg>
  )
}

export function SplashScreen() {
  return (
    <div className="splash-screen">
      <AuthTitleBar />
      <div className="splash-screen-body">
        <SplashBolt />
        <span className="splash-screen-title">sporemind</span>
      </div>
    </div>
  )
}
