interface MushroomIconProps {
  size?: number
  strokeWidth?: number
  /** Scale factor for the artwork about its center; 1 renders uncropped. */
  artScale?: number
  /** Y coordinate where the stems end; 548 short, 578 full length. */
  stemEnd?: number
}

// Brand mushroom mark as an inline icon. The viewBox is cropped to the artwork
// bounds (square 660 at center 512,339) so the mark fills the icon box.
// strokeWidth is in viewBox units; 80 renders ~1.7px at size 14.
export function MushroomIcon({ size = 14, strokeWidth = 80, artScale = 1, stemEnd = 548 }: MushroomIconProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="182 9 660 660"
      fill="none"
      stroke="currentColor"
      strokeWidth={strokeWidth}
      strokeLinecap="round"
      aria-hidden="true"
    >
      <g transform={artScale === 1 ? undefined : `translate(512 339) scale(${artScale}) translate(-512 -339)`}>
        <path d="M 241.5 307.5 C 315.6 31.1 708.4 31.1 782.5 307.5 C 802 380 778 490 652 490 L 372 490 C 246 490 222 380 241.5 307.5 Z" />
        <path d="M 430 235 L 430 345" />
        <path d="M 594 235 L 594 345" />
        <path d={`M 400 498 L 400 ${stemEnd}`} />
        <path d={`M 624 498 L 624 ${stemEnd}`} />
      </g>
    </svg>
  )
}
